package gen

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/ericsuh/layerlens/internal/domain"
)

// Whiteout markers from the OCI image-spec v1.1.1 layer.md. They are spelled
// out here rather than imported from internal/analyze so that the generator
// and the analyzer are independent implementations of the convention: a typo
// on either side shows up as a failing property test rather than cancelling
// out.
const (
	whiteoutPrefix    = ".wh."
	whiteoutOpaqueDir = ".wh..wh..opq"
)

// blobResult is one compressed layer blob plus the identities derived from it.
type blobResult struct {
	// Blob is the gzip-compressed layer tar, i.e. the bytes stored under
	// blobs/sha256/ and described by the manifest.
	Blob []byte
	// Digest is sha256 of Blob (the manifest layer digest).
	Digest domain.Digest
	// DiffID is sha256 of the *uncompressed* tar — what the image config
	// declares and what all trunk/ChainID logic keys on.
	DiffID domain.Digest
	// Uncompressed is the length of the tar before compression.
	Uncompressed int64
}

// buildLayer renders one layer spec into a deterministic gzip-compressed tar.
//
// Three properties make the output stable across runs and machines:
//
//   - members are emitted in sorted member-name order, so the ordering of the
//     Go literal never leaks into the bytes;
//   - every header field is explicit (mode, uid/gid, uname/gname, mtime), so
//     nothing is inherited from the host filesystem or the clock;
//   - the gzip header is zeroed apart from the conventional OS byte, so the
//     compressor does not stamp the current time into the blob.
func buildLayer(l *LayerSpec) (blobResult, error) {
	members := make([]tarMember, 0, len(l.Entries))
	for i := range l.Entries {
		m, err := memberOf(&l.Entries[i], l)
		if err != nil {
			return blobResult{}, err
		}
		members = append(members, m)
	}
	sort.Slice(members, func(i, j int) bool { return members[i].hdr.Name < members[j].hdr.Name })
	for i := 1; i < len(members); i++ {
		if members[i].hdr.Name == members[i-1].hdr.Name {
			// Duplicate members are legal in a tar (last wins), but in
			// a hand-written fixture they are always a mistake, and a
			// silent one: the layer would still build and the property
			// tests would assert against the wrong entry.
			return blobResult{}, fmt.Errorf("gen: duplicate tar member %q", members[i].hdr.Name)
		}
	}

	var out bytes.Buffer
	zw, err := gzip.NewWriterLevel(&out, gzip.BestCompression)
	if err != nil {
		return blobResult{}, err
	}
	// Zero ModTime, no Name/Comment, OS 255 ("unknown"): the three places a
	// gzip writer would otherwise record the environment it ran in.
	zw.Header = gzip.Header{OS: 255}

	diff := sha256.New()
	counted := &countingWriter{w: io.MultiWriter(zw, diff)}
	tw := tar.NewWriter(counted)
	for i := range members {
		m := &members[i]
		if err := tw.WriteHeader(&m.hdr); err != nil {
			return blobResult{}, fmt.Errorf("gen: write header %q: %w", m.hdr.Name, err)
		}
		if m.hdr.Typeflag == tar.TypeReg && m.hdr.Size > 0 {
			if err := writeBody(tw, m.seed, m.hdr.Size); err != nil {
				return blobResult{}, fmt.Errorf("gen: write body %q: %w", m.hdr.Name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		return blobResult{}, err
	}
	if err := zw.Close(); err != nil {
		return blobResult{}, err
	}

	blob := out.Bytes()
	sum := sha256.Sum256(blob)
	return blobResult{
		Blob:         blob,
		Digest:       domain.DigestFromBytes(sum[:]),
		DiffID:       domain.DigestFromHash(diff),
		Uncompressed: counted.n,
	}, nil
}

// tarMember is a header plus the seed its body is generated from.
type tarMember struct {
	hdr  tar.Header
	seed string
}

// memberOf projects an entry spec onto a tar header.
func memberOf(e *EntrySpec, l *LayerSpec) (tarMember, error) {
	if !strings.HasPrefix(e.Path, "/") {
		return tarMember{}, fmt.Errorf("gen: entry path %q must be rooted at /", e.Path)
	}
	clean := path.Clean(e.Path)
	if clean == "/" {
		return tarMember{}, fmt.Errorf("gen: entry path %q names the archive root", e.Path)
	}

	mtime := e.Mtime
	if mtime.IsZero() {
		mtime = l.Mtime
	}
	hdr := tar.Header{
		Name:    strings.TrimPrefix(clean, "/"),
		Mode:    int64(defaultMode(e)),
		Uid:     e.UID,
		Gid:     e.GID,
		Uname:   e.Uname,
		Gname:   e.Gname,
		ModTime: mtime.UTC(),
	}

	switch e.Kind {
	case domain.KindFile:
		hdr.Typeflag = tar.TypeReg
		hdr.Size = e.Size
	case domain.KindDir:
		hdr.Typeflag = tar.TypeDir
		hdr.Name += "/"
	case domain.KindSymlink:
		hdr.Typeflag = tar.TypeSymlink
		hdr.Linkname = e.Link
	case domain.KindHardlink:
		hdr.Typeflag = tar.TypeLink
		// Tar hardlink targets are archive-relative, never "/"-rooted.
		hdr.Linkname = strings.TrimPrefix(e.Link, "/")
	case domain.KindDevice:
		hdr.Typeflag = tar.TypeBlock
		if e.Seed == charDeviceSeed {
			hdr.Typeflag = tar.TypeChar
		}
		hdr.Devmajor, hdr.Devminor = e.Devmajor, e.Devminor
	case domain.KindFifo:
		hdr.Typeflag = tar.TypeFifo
	case domain.KindWhiteout:
		// The marker is an ordinary empty file named ".wh.<target>"
		// beside the path it deletes.
		hdr.Typeflag = tar.TypeReg
		hdr.Name = strings.TrimPrefix(path.Join(path.Dir(clean), whiteoutPrefix+path.Base(clean)), "/")
	case domain.KindOpaque:
		// The marker lives *inside* the directory it empties.
		hdr.Typeflag = tar.TypeReg
		hdr.Name = strings.TrimPrefix(path.Join(clean, whiteoutOpaqueDir), "/")
	default:
		return tarMember{}, fmt.Errorf("gen: entry %q has unsupported kind %v", e.Path, e.Kind)
	}

	if len(e.Xattrs) > 0 {
		hdr.PAXRecords = make(map[string]string, len(e.Xattrs))
		for k, v := range e.Xattrs {
			hdr.PAXRecords["SCHILY.xattr."+k] = v
		}
		// archive/tar sorts PAX records before writing them, so an
		// explicit format is all the determinism this needs.
		hdr.Format = tar.FormatPAX
	}

	seed := e.Seed
	if seed == "" {
		seed = clean
	}
	return tarMember{hdr: hdr, seed: seed}, nil
}

// defaultMode supplies conventional permission bits when a spec leaves Mode
// unset, so that fixture literals stay about the interesting fields.
func defaultMode(e *EntrySpec) uint32 {
	if e.Mode != 0 {
		return e.Mode & domain.ModePermMask
	}
	switch e.Kind {
	case domain.KindDir:
		return 0o755
	case domain.KindSymlink:
		return 0o777
	case domain.KindDevice, domain.KindFifo:
		return 0o600
	default:
		return 0o644
	}
}

// countingWriter tracks how many uncompressed tar bytes were produced.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// zeroPad is the shared NUL buffer used to pad file bodies. Fixture bodies are
// deliberately mostly zeros: the fixtures are committed to the repository, and
// a 15 MiB "node binary" whose tail compresses a thousand to one costs the
// repo kilobytes while still giving the UI a realistic size to draw.
var zeroPad = make([]byte, 32<<10)

// writeBody emits exactly size bytes for a file: a short ASCII banner that
// makes the content a pure, unique function of the seed, then NUL padding.
//
// Uniqueness matters — the banner is what gives each fixture path a distinct
// ContentSHA, so "modified" really means modified — and so does compressibility,
// because these bytes live in git.
func writeBody(w io.Writer, seed string, size int64) error {
	banner := fmt.Sprintf("layerlens fixture %s %d\n", seed, size)
	if int64(len(banner)) > size {
		banner = banner[:size]
	}
	if _, err := io.WriteString(w, banner); err != nil {
		return err
	}
	remaining := size - int64(len(banner))
	for remaining > 0 {
		n := int64(len(zeroPad))
		if n > remaining {
			n = remaining
		}
		if _, err := w.Write(zeroPad[:n]); err != nil {
			return err
		}
		remaining -= n
	}
	return nil
}
