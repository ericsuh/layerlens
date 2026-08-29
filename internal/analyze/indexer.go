package analyze

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/klauspost/compress/zstd"

	"github.com/ericsuh/layerlens/internal/domain"
)

// Layer blob media types layerlens understands. Anything else is refused
// rather than guessed at.
const (
	MediaTypeOCILayerTar     = "application/vnd.oci.image.layer.v1.tar"
	MediaTypeOCILayerGzip    = "application/vnd.oci.image.layer.v1.tar+gzip"
	MediaTypeOCILayerZstd    = "application/vnd.oci.image.layer.v1.tar+zstd"
	MediaTypeDockerLayerTar  = "application/vnd.docker.image.rootfs.diff.tar"
	MediaTypeDockerLayerGzip = "application/vnd.docker.image.rootfs.diff.tar.gzip"
)

// Whiteout markers from the OCI image-spec v1.1.1 layer.md.
const (
	// whiteoutPrefix marks the *basename* of a deleted sibling: an entry
	// named "dir/.wh.x" deletes "dir/x" from the lower layers.
	whiteoutPrefix = ".wh."
	// whiteoutOpaqueDir hides every lower-layer child of its directory.
	whiteoutOpaqueDir = ".wh..wh..opq"
)

// copyBufferSize is the scratch buffer the indexer reuses to drain every file
// body. io.Copy would allocate a fresh 32 KiB buffer per file, which on a
// 500k-file layer is gigabytes of pure garbage.
const copyBufferSize = 128 << 10

// maxWarnings bounds the warnings a single hostile or broken layer can make us
// retain; the surplus is summarized in one final warning. The indexer's memory
// must stay proportional to entry count, never to attacker-chosen input.
const maxWarnings = 64

// ErrDiffIDMismatch reports that the uncompressed layer stream did not hash to
// the DiffID the image config declared: the blob is corrupt or tampered with.
var ErrDiffIDMismatch = errors.New("layer diff_id mismatch")

// ErrUnknownMediaType reports a layer media type layerlens cannot decompress.
var ErrUnknownMediaType = errors.New("unknown layer media type")

// ByteCounter is a concurrency-safe counter of compressed bytes consumed. The
// ingest layer polls it to drive byte-accurate pull progress while IndexLayer
// runs.
type ByteCounter struct {
	n atomic.Int64
}

// Add records n more bytes read.
func (c *ByteCounter) Add(n int64) {
	if c != nil {
		c.n.Add(n)
	}
}

// Load returns the bytes read so far.
func (c *ByteCounter) Load() int64 {
	if c == nil {
		return 0
	}
	return c.n.Load()
}

// LayerSource describes one layer blob to index.
type LayerSource struct {
	// Reader yields the layer blob exactly as the registry or daemon
	// serves it, compressed per MediaType. It is read once, forward only,
	// and never buffered in full.
	Reader io.Reader
	// MediaType selects the decompressor. An empty value means the stream
	// is an uncompressed tar.
	MediaType string
	// DiffID is the digest the image config declares for the uncompressed
	// tar. When set, the computed digest is verified against it and a
	// mismatch fails the ingest. When empty, no declaration exists to check
	// against (fixture generation) and the computed digest is simply
	// recorded.
	DiffID domain.Digest
	// Progress, if non-nil, accumulates compressed bytes as they are read.
	Progress *ByteCounter
}

// IndexLayer streams a layer blob once and returns its complete changeset.
//
// This is the only place that reads layer bytes, and it is deliberately shaped
// so that supporting a 25 GiB image is a structural property rather than a
// tuning exercise (ARCHITECTURE §4.1):
//
//   - the blob is decompressed, tar-parsed, content-hashed and DiffID-hashed
//     in a single forward pass;
//   - file content is hashed *while draining* the tar reader and is never
//     buffered, written to disk, or retained;
//   - resident memory is O(entries in this one layer), not O(layer bytes).
//
// Entries whose names fail sanitization are skipped with a warning; the rest
// of the layer still indexes. Duplicate paths within one tar resolve
// last-in-tar-wins, matching how an extractor would apply them.
func IndexLayer(ctx context.Context, src LayerSource) (*domain.LayerIndex, error) {
	if src.Reader == nil {
		return nil, errors.New("analyze: nil layer reader")
	}
	if src.DiffID != "" {
		if err := src.DiffID.Validate(); err != nil {
			return nil, fmt.Errorf("analyze: declared diff_id: %w", err)
		}
	}

	counted := &countingReader{r: src.Reader, counter: src.Progress}
	uncompressed, closer, err := decompressor(src.MediaType, counted)
	if err != nil {
		return nil, err
	}
	defer closer()

	// The DiffID is the digest of the *whole* uncompressed tar, trailing
	// zero padding included, so everything the tar reader consumes has to
	// flow through this tee — and the remainder is drained into it below.
	diffHash := sha256.New()
	tee := io.TeeReader(uncompressed, diffHash)

	idx := indexState{
		entries: make(map[string]domain.Entry),
		buf:     make([]byte, copyBufferSize),
	}
	tr := tar.NewReader(tee)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("analyze: read layer tar: %w", err)
		}
		if err := idx.add(hdr, tr); err != nil {
			return nil, err
		}
	}

	// archive/tar stops at the end-of-archive marker; GNU tar pads the
	// stream out to its blocking factor and those bytes are part of the
	// DiffID. Drain them through the tee before verifying.
	if _, err := io.Copy(io.Discard, tee); err != nil {
		return nil, fmt.Errorf("analyze: drain layer tar: %w", err)
	}

	computed := domain.DigestFromHash(diffHash)
	if src.DiffID != "" && computed != src.DiffID {
		return nil, fmt.Errorf("%w: declared %s, computed %s", ErrDiffIDMismatch, src.DiffID, computed)
	}

	return idx.finish(computed), nil
}

// indexState accumulates one layer's entries. Only metadata lives here: file
// content is hashed as it streams past and then dropped.
type indexState struct {
	entries      map[string]domain.Entry
	buf          []byte
	warnings     []string
	suppressed   int
	contentBytes int64
}

func (s *indexState) warn(format string, args ...any) {
	if len(s.warnings) >= maxWarnings {
		s.suppressed++
		return
	}
	s.warnings = append(s.warnings, fmt.Sprintf(format, args...))
}

// put records an entry, letting a later entry for the same path replace an
// earlier one (last-in-tar wins).
func (s *indexState) put(e domain.Entry) {
	if prev, ok := s.entries[e.Path]; ok && prev.Kind == domain.KindFile {
		s.contentBytes -= prev.Size
	}
	if e.Kind == domain.KindFile {
		s.contentBytes += e.Size
	}
	s.entries[e.Path] = e
}

func (s *indexState) add(hdr *tar.Header, body io.Reader) error {
	switch hdr.Typeflag {
	case tar.TypeXGlobalHeader, tar.TypeXHeader:
		// PAX metadata members carry no filesystem state of their own;
		// archive/tar has already folded the useful records into the
		// headers that follow.
		return nil
	}
	if IsRootName(hdr.Name) {
		// The archive root always exists; a "./" member says nothing.
		return nil
	}

	p, ok := SanitizePath(hdr.Name)
	if !ok {
		s.warn("skipped entry with unsafe name %q", hdr.Name)
		return nil
	}

	base := path.Base(p)
	dir := path.Dir(p)
	switch {
	case base == whiteoutOpaqueDir:
		// The marker's own path is not a filesystem path: the payload is
		// the directory it makes opaque.
		s.put(domain.Entry{Path: dir, Kind: domain.KindOpaque})
		return nil
	case strings.HasPrefix(base, whiteoutPrefix):
		name := strings.TrimPrefix(base, whiteoutPrefix)
		if name == "" {
			s.warn("skipped malformed whiteout entry %q", hdr.Name)
			return nil
		}
		s.put(domain.Entry{Path: path.Join(dir, name), Kind: domain.KindWhiteout})
		return nil
	}

	kind, ok := kindOf(hdr.Typeflag)
	if !ok {
		s.warn("skipped entry %q with unsupported tar type %q", hdr.Name, string(rune(hdr.Typeflag)))
		return nil
	}

	e := entryFrom(hdr, p, kind)
	if kind == domain.KindFile {
		h := sha256.New()
		// The single most important line in the package: content is
		// hashed as it is drained and is never held. The anonymous
		// wrapper hides tar.Reader's WriteTo so that CopyBuffer really
		// uses our reusable buffer instead of allocating its own.
		n, err := io.CopyBuffer(h, struct{ io.Reader }{body}, s.buf)
		if err != nil {
			return fmt.Errorf("analyze: hash %q: %w", p, err)
		}
		e.Size = n
		e.ContentSHA = domain.DigestFromHash(h)
	}
	if kind == domain.KindHardlink {
		// The target's bytes are counted once, at the target.
		e.Size = 0
		if target, ok := SanitizePath(hdr.Linkname); ok {
			e.LinkTarget = target
		} else {
			s.warn("hardlink %q has unsafe target %q", hdr.Name, hdr.Linkname)
		}
	}
	s.put(e)
	return nil
}

func (s *indexState) finish(diffID domain.Digest) *domain.LayerIndex {
	entries := make([]domain.Entry, 0, len(s.entries))
	for _, e := range s.entries {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })

	warnings := s.warnings
	if s.suppressed > 0 {
		warnings = append(warnings, fmt.Sprintf("%d further warnings suppressed", s.suppressed))
	}

	return &domain.LayerIndex{
		SchemaVersion:   domain.LayerIndexSchemaVersion,
		DiffID:          diffID,
		ChangesetDigest: ChangesetDigest(entries),
		ContentBytes:    s.contentBytes,
		Entries:         entries,
		Warnings:        warnings,
	}
}

// entryFrom captures every field the tarsum-v1 changeset digest needs (§3.1),
// plus MtimeUnix, which is stored for display only.
func entryFrom(hdr *tar.Header, p string, kind domain.EntryKind) domain.Entry {
	e := domain.Entry{
		Path:      p,
		Kind:      kind,
		Mode:      uint32(hdr.Mode) & domain.ModePermMask, //nolint:gosec // masked to 12 bits
		UID:       hdr.Uid,
		GID:       hdr.Gid,
		Uname:     hdr.Uname,
		Gname:     hdr.Gname,
		Xattrs:    FilterXattrs(hdr.PAXRecords),
		MtimeUnix: hdr.ModTime.Unix(),
	}
	if kind == domain.KindDevice {
		e.Devmajor = hdr.Devmajor
		e.Devminor = hdr.Devminor
	}
	if kind == domain.KindSymlink {
		// Symlink targets are stored verbatim and never dereferenced on
		// the server filesystem (§7.3).
		e.LinkTarget = hdr.Linkname
	}
	if hdr.ModTime.IsZero() {
		e.MtimeUnix = 0
	}
	return e
}

func kindOf(typeflag byte) (domain.EntryKind, bool) {
	switch typeflag {
	case tar.TypeReg:
		return domain.KindFile, true
	case tar.TypeDir:
		return domain.KindDir, true
	case tar.TypeSymlink:
		return domain.KindSymlink, true
	case tar.TypeLink:
		return domain.KindHardlink, true
	case tar.TypeChar, tar.TypeBlock:
		return domain.KindDevice, true
	case tar.TypeFifo:
		return domain.KindFifo, true
	default:
		return 0, false
	}
}

// countingReader feeds a ByteCounter as the compressed stream is consumed.
type countingReader struct {
	r       io.Reader
	counter *ByteCounter
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.counter.Add(int64(n))
	return n, err
}

// decompressor wraps r according to mediaType and returns a cleanup function
// that must be called: the zstd decoder owns a goroutine pool.
func decompressor(mediaType string, r io.Reader) (io.Reader, func(), error) {
	switch classifyMediaType(mediaType) {
	case compressionNone:
		return r, func() {}, nil
	case compressionGzip:
		zr, err := gzip.NewReader(r)
		if err != nil {
			return nil, nil, fmt.Errorf("analyze: open gzip layer: %w", err)
		}
		return zr, func() { _ = zr.Close() }, nil
	case compressionZstd:
		zr, err := zstd.NewReader(r)
		if err != nil {
			return nil, nil, fmt.Errorf("analyze: open zstd layer: %w", err)
		}
		return zr.IOReadCloser(), zr.Close, nil
	default:
		return nil, nil, fmt.Errorf("%w: %q", ErrUnknownMediaType, mediaType)
	}
}

type compression int

const (
	compressionUnknown compression = iota
	compressionNone
	compressionGzip
	compressionZstd
)

func classifyMediaType(mediaType string) compression {
	mt := strings.ToLower(strings.TrimSpace(mediaType))
	if i := strings.IndexByte(mt, ';'); i >= 0 {
		mt = strings.TrimSpace(mt[:i])
	}
	switch {
	case mt == "":
		return compressionNone
	case mt == MediaTypeOCILayerTar, mt == MediaTypeDockerLayerTar:
		return compressionNone
	case mt == MediaTypeOCILayerGzip, mt == MediaTypeDockerLayerGzip:
		return compressionGzip
	case mt == MediaTypeOCILayerZstd:
		return compressionZstd
	// Non-distributable and vendor-specific variants reuse the same
	// suffixes, so classify by suffix rather than enumerating them all.
	case strings.HasSuffix(mt, "+gzip"), strings.HasSuffix(mt, ".tar.gzip"), strings.HasSuffix(mt, ".tar.gz"):
		return compressionGzip
	case strings.HasSuffix(mt, "+zstd"):
		return compressionZstd
	case strings.HasSuffix(mt, ".tar"), strings.HasSuffix(mt, "+tar"):
		return compressionNone
	default:
		return compressionUnknown
	}
}
