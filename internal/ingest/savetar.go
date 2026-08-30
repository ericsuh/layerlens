package ingest

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	v1 "github.com/google/go-containerregistry/pkg/v1"

	"github.com/ericsuh/layerlens/internal/analyze"
	"github.com/ericsuh/layerlens/internal/cachestore"
	"github.com/ericsuh/layerlens/internal/domain"
)

// Bounds on what a `docker save` stream may make us hold in memory.
//
// The stream is read exactly once, forward only, and never spooled to disk:
// at 25 GiB a spool would be both a copy of the image and a charge against the
// cache budget. What that costs is the inability to look ahead — the manifest
// and config land *after* the blobs in an Engine 29 save — so the parser
// buffers only members small enough to be metadata and indexes everything
// larger as it goes, reconciling at the end.
//
// They are variables rather than constants only so that a test can lower them
// and exercise the streaming path with a small fixture; nothing in production
// writes to them.
var (
	// maxSmallMember is the largest member the parser will hold in memory.
	// Configs run to a few kilobytes; anything above this is treated as a
	// layer and streamed.
	maxSmallMember int64 = 8 << 20
	// maxSmallTotal bounds the sum of buffered members.
	maxSmallTotal int64 = 64 << 20
	// maxSaveMembers bounds the member count of one save stream.
	maxSaveMembers = 8192
)

var (
	// ErrSaveStreamInvalid reports a save stream layerlens cannot make
	// sense of.
	ErrSaveStreamInvalid = errors.New("ingest: docker save stream is not a readable image")
	// ErrSaveStreamTooLarge reports a save stream whose metadata members
	// exceed the in-memory bounds above.
	ErrSaveStreamTooLarge = errors.New("ingest: docker save stream has too much metadata")
	// ErrNoAmd64Manifest reports a save with no linux/amd64 image in it.
	ErrNoAmd64Manifest = errors.New("ingest: no linux/amd64 image in the docker save stream")
)

// legacyManifest is the `manifest.json` a `docker save` writes for backward
// compatibility, present alongside the OCI layout in Engine 29 saves.
type legacyManifest struct {
	Config   string   `json:"Config"`
	RepoTags []string `json:"RepoTags"`
	Layers   []string `json:"Layers"`
}

// saveParser accumulates the state of one pass over a save stream.
type saveParser struct {
	ingester *Ingester
	txn      *cachestore.Txn
	progress Reporter

	// small holds buffered metadata members, keyed by their name inside
	// the tar ("index.json", "blobs/sha256/<hex>", ...).
	small      map[string][]byte
	smallBytes int64

	// diffIDByBlob maps a blob's own (compressed) digest to the DiffID we
	// computed while indexing it.
	diffIDByBlob map[string]domain.Digest
	// indexed records every DiffID committed during this pass, so the
	// reconciliation can tell "already streamed" from "still needed".
	indexed map[domain.Digest]bool

	// streamed counts the large members indexed so far. It is what the
	// progress card labels "layer n": a save stream's member order is not
	// rootfs order, so no truer index exists until the reconciliation.
	streamed int

	// target is the resolved image, once enough metadata has been seen. It
	// is recomputed as members arrive precisely so that a save whose
	// manifest happens to precede its blobs can skip layers it already has
	// without hashing them (DECISIONS A2).
	target *saveTarget
}

// saveTarget is the resolved linux/amd64 image inside a save stream.
type saveTarget struct {
	config     []byte
	layers     []saveLayer
	legacyName []string
}

// saveLayer is one layer as the save's manifest describes it.
type saveLayer struct {
	// blob is the member name holding the bytes ("blobs/sha256/<hex>" or
	// "<hash>/layer.tar").
	blob string
	// digest is the compressed digest, when the save format states one.
	digest    domain.Digest
	mediaType string
	size      int64
}

// IngestDockerSave analyzes one `docker save` stream.
//
// The stream is consumed in a single forward pass with bounded memory and no
// spool file, which is what makes the daemon path viable for a 25 GiB image on
// a server whose disk budget is the analysis cache, not a copy of the image.
func (i *Ingester) IngestDockerSave(ctx context.Context, stream io.Reader, meta Meta) (*Result, error) {
	progress := meta.reporter()
	progress.Phase(PhaseDownloading)

	txn, err := i.store.Begin()
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			if err := txn.Abort(); err != nil {
				i.log.Warn("ingest: abort docker save transaction", "err", err)
			}
		}
	}()

	p := &saveParser{
		ingester:     i,
		txn:          txn,
		progress:     progress,
		small:        map[string][]byte{},
		diffIDByBlob: map[string]domain.Digest{},
		indexed:      map[domain.Digest]bool{},
	}
	res := &Result{}
	if err := p.consume(ctx, stream, res); err != nil {
		return nil, err
	}

	progress.Phase(PhaseFinalizing)
	target := p.resolveTarget()
	if target == nil {
		return nil, ErrNoAmd64Manifest
	}
	cfg, err := v1.ParseConfigFile(bytes.NewReader(target.config))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSaveStreamInvalid, err)
	}
	if len(cfg.RootFS.DiffIDs) > MaxLayers {
		return nil, fmt.Errorf("%w: %d layers", ErrTooManyLayers, len(cfg.RootFS.DiffIDs))
	}

	layers, totalBytes, err := p.reconcile(ctx, cfg, target, res)
	if err != nil {
		return nil, err
	}

	id, err := configDigest(target.config)
	if err != nil {
		return nil, err
	}
	if meta.RefNames == nil {
		meta.RefNames = target.legacyName
	}
	rec, err := i.buildRecord(id, cfg, layers, totalBytes, meta)
	if err != nil {
		return nil, err
	}
	if err := txn.Commit(rec); err != nil {
		return nil, err
	}
	committed = true

	stored, err := i.store.Image(ctx, id)
	if err != nil {
		return nil, err
	}
	res.Record = stored
	return res, nil
}

// consume walks the save tar once.
func (p *saveParser) consume(ctx context.Context, stream io.Reader, res *Result) error {
	tr := tar.NewReader(stream)
	members := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: %w", ErrSaveStreamInvalid, err)
		}
		members++
		if members > maxSaveMembers {
			return fmt.Errorf("%w: more than %d members", ErrSaveStreamTooLarge, maxSaveMembers)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := strings.TrimPrefix(path.Clean("/"+hdr.Name), "/")
		if name == "" {
			continue
		}
		if hdr.Size <= maxSmallMember {
			if err := p.buffer(name, hdr.Size, tr); err != nil {
				return err
			}
			continue
		}
		if err := p.streamLayer(ctx, name, hdr.Size, tr, res); err != nil {
			return err
		}
	}
}

// buffer holds a metadata-sized member in memory.
func (p *saveParser) buffer(name string, size int64, r io.Reader) error {
	if p.smallBytes+size > maxSmallTotal {
		return fmt.Errorf("%w: metadata past %d bytes", ErrSaveStreamTooLarge, maxSmallTotal)
	}
	buf := make([]byte, 0, size)
	body := bytes.NewBuffer(buf)
	if _, err := io.Copy(body, io.LimitReader(r, size)); err != nil {
		return fmt.Errorf("%w: read %s: %w", ErrSaveStreamInvalid, name, err)
	}
	p.small[name] = body.Bytes()
	p.smallBytes += size
	// Re-resolving here is what lets a save whose manifest precedes its
	// blobs skip a layer it already has: the moment the config is known,
	// so is the compressed-digest → DiffID mapping.
	if p.target == nil {
		p.target = p.resolveTarget()
	}
	return nil
}

// streamLayer indexes one large member as a layer blob, or drains it if the
// layer it holds is already indexed.
//
// A member that turns out not to be a layer at all (an oversized attestation
// blob, say) is a warning, not a failure: the reconciliation below decides
// what the image actually needs.
func (p *saveParser) streamLayer(ctx context.Context, name string, size int64, r io.Reader, res *Result) error {
	if diffID, ok := p.knownLayer(name); ok {
		// Drained, not re-hashed: the bytes have to move because a tar
		// is sequential, but nothing is decompressed or digested
		// (DECISIONS A2).
		if _, err := io.Copy(io.Discard, r); err != nil {
			return fmt.Errorf("%w: drain %s: %w", ErrSaveStreamInvalid, name, err)
		}
		p.indexed[diffID] = true
		res.LayersSkipped++
		p.progress.LayerFinished(p.streamed, true, size)
		p.streamed++
		return nil
	}

	buffered := bufio.NewReaderSize(r, 4096)
	mediaType, err := sniffLayerMediaType(buffered)
	if err != nil {
		p.ingester.log.Warn("ingest: unreadable member in docker save stream", "member", name, "err", err)
		_, _ = io.Copy(io.Discard, buffered)
		return nil
	}

	p.progress.LayerStarted(p.streamed, "", size)
	idx, err := analyze.IndexLayer(ctx, analyze.LayerSource{
		Reader:    buffered,
		MediaType: mediaType,
		// No declared DiffID: in a save stream the config that would
		// declare it has not necessarily been seen yet, so the digest is
		// computed here and reconciled below.
		Progress:   p.progress.Bytes(),
		MaxEntries: p.ingester.maxLayerEntries,
	})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, analyze.ErrTooManyEntries) {
			// Not a "this member was not a layer after all" case:
			// the member *is* a layer and it is past the cap.
			// Draining on would let the very allocation the cap
			// exists to refuse be paid for by the next member.
			return err
		}
		p.ingester.log.Warn("ingest: member of docker save stream is not a layer", "member", name, "err", err)
		_, _ = io.Copy(io.Discard, buffered)
		return nil
	}
	if err := p.txn.PutLayer(idx); err != nil {
		return err
	}
	p.indexed[idx.DiffID] = true
	if hex, ok := blobHex(name); ok {
		p.diffIDByBlob[hex] = idx.DiffID
	}
	res.LayersIndexed++
	p.progress.LayerFinished(p.streamed, false, 0)
	p.streamed++
	return nil
}

// knownLayer reports whether this member's bytes are a layer the cache
// already holds, which is knowable only when the save put its manifest and
// config before its blobs.
func (p *saveParser) knownLayer(name string) (domain.Digest, bool) {
	if p.target == nil {
		return "", false
	}
	for _, l := range p.target.layers {
		if l.blob != name {
			continue
		}
		diffID, ok := p.diffIDForManifestLayer(l)
		if !ok {
			return "", false
		}
		if _, present := p.txn.UseLayer(diffID); present {
			return diffID, true
		}
		return "", false
	}
	return "", false
}

// diffIDForManifestLayer maps a manifest layer onto the DiffID the config
// declares for the same position.
func (p *saveParser) diffIDForManifestLayer(l saveLayer) (domain.Digest, bool) {
	if p.target == nil {
		return "", false
	}
	cfg, err := v1.ParseConfigFile(bytes.NewReader(p.target.config))
	if err != nil {
		return "", false
	}
	for n, candidate := range p.target.layers {
		if candidate.blob != l.blob || n >= len(cfg.RootFS.DiffIDs) {
			continue
		}
		diffID, err := domain.ParseDigest(cfg.RootFS.DiffIDs[n].String())
		if err != nil {
			return "", false
		}
		return diffID, true
	}
	return "", false
}

// reconcile turns the config's diff_ids into committed layer records, indexing
// from the buffered metadata anything the streaming pass did not cover (a
// layer small enough to have been buffered as metadata).
func (p *saveParser) reconcile(ctx context.Context, cfg *v1.ConfigFile, target *saveTarget, res *Result) ([]domain.Layer, int64, error) {
	layers := make([]domain.Layer, 0, len(cfg.RootFS.DiffIDs))
	var totalBytes int64

	for n, raw := range cfg.RootFS.DiffIDs {
		diffID, err := domain.ParseDigest(raw.String())
		if err != nil {
			return nil, 0, fmt.Errorf("ingest: layer %d diff_id: %w", n, err)
		}
		summary, present := p.txn.UseLayer(diffID)
		if !present {
			if n >= len(target.layers) {
				return nil, 0, fmt.Errorf("%w: config declares %d layers, the manifest has %d",
					ErrSaveStreamInvalid, len(cfg.RootFS.DiffIDs), len(target.layers))
			}
			var err error
			summary, err = p.indexBuffered(ctx, target.layers[n], diffID)
			if err != nil {
				return nil, 0, err
			}
			res.LayersIndexed++
		} else if !p.indexed[diffID] {
			// Present before this pass began: free.
			res.LayersSkipped++
		}

		layer := domain.Layer{
			Index:           n,
			DiffID:          diffID,
			ContentBytes:    summary.ContentBytes,
			EntryCount:      summary.EntryCount,
			ChangesetDigest: summary.ChangesetDigest,
		}
		if n < len(target.layers) {
			layer.CompressedDigest = target.layers[n].digest
			layer.CompressedSize = target.layers[n].size
		}
		layers = append(layers, layer)
		totalBytes += summary.ContentBytes
	}
	return layers, totalBytes, nil
}

// indexBuffered indexes a layer whose bytes were small enough to be buffered
// as metadata.
func (p *saveParser) indexBuffered(ctx context.Context, l saveLayer, diffID domain.Digest) (cachestore.LayerSummary, error) {
	body, ok := p.small[l.blob]
	if !ok {
		return cachestore.LayerSummary{}, fmt.Errorf("%w: layer %s is not in the stream",
			ErrSaveStreamInvalid, diffID)
	}
	reader := bufio.NewReader(bytes.NewReader(body))
	mediaType := l.mediaType
	if sniffed, err := sniffLayerMediaType(reader); err == nil {
		mediaType = sniffed
	}
	idx, err := analyze.IndexLayer(ctx, analyze.LayerSource{
		Reader:     reader,
		MediaType:  mediaType,
		DiffID:     diffID,
		Progress:   p.progress.Bytes(),
		MaxEntries: p.ingester.maxLayerEntries,
	})
	if err != nil {
		return cachestore.LayerSummary{}, fmt.Errorf("ingest: index layer %s: %w", diffID, err)
	}
	if err := p.txn.PutLayer(idx); err != nil {
		return cachestore.LayerSummary{}, err
	}
	summary, ok := p.txn.UseLayer(diffID)
	if !ok {
		return cachestore.LayerSummary{}, fmt.Errorf("ingest: layer %s vanished after commit", diffID)
	}
	return summary, nil
}

// resolveTarget picks the linux/amd64 image out of the buffered metadata,
// preferring the OCI layout (Engine 29's real format) and falling back to the
// legacy manifest.json that every save still carries.
func (p *saveParser) resolveTarget() *saveTarget {
	if target := p.resolveOCITarget(); target != nil {
		return target
	}
	return p.resolveLegacyTarget()
}

func (p *saveParser) resolveOCITarget() *saveTarget {
	raw, ok := p.small["index.json"]
	if !ok {
		return nil
	}
	index, err := v1.ParseIndexManifest(bytes.NewReader(raw))
	if err != nil {
		return nil
	}
	desc := p.findAmd64Manifest(index, 0)
	if desc == nil {
		return nil
	}
	manifestBytes, ok := p.small[blobPath(desc.Digest.Hex)]
	if !ok {
		return nil
	}
	manifest, err := v1.ParseManifest(bytes.NewReader(manifestBytes))
	if err != nil {
		return nil
	}
	config, ok := p.small[blobPath(manifest.Config.Digest.Hex)]
	if !ok {
		return nil
	}
	target := &saveTarget{config: config}
	for _, l := range manifest.Layers {
		digest, _ := domain.ParseDigest(l.Digest.String())
		target.layers = append(target.layers, saveLayer{
			blob:      blobPath(l.Digest.Hex),
			digest:    digest,
			mediaType: string(l.MediaType),
			size:      l.Size,
		})
	}
	if name := desc.Annotations[RefNameAnnotation]; name != "" {
		target.legacyName = []string{name}
	}
	if name := desc.Annotations["io.containerd.image.name"]; name != "" {
		target.legacyName = []string{name}
	}
	return target
}

// findAmd64Manifest walks an index (and any nested index) for the linux/amd64
// image manifest, skipping BuildKit's unknown/unknown attestation manifests.
func (p *saveParser) findAmd64Manifest(index *v1.IndexManifest, depth int) *v1.Descriptor {
	if index == nil || depth > 4 {
		return nil
	}
	for n := range index.Manifests {
		desc := index.Manifests[n]
		if desc.MediaType.IsIndex() {
			nested, ok := p.small[blobPath(desc.Digest.Hex)]
			if !ok {
				continue
			}
			parsed, err := v1.ParseIndexManifest(bytes.NewReader(nested))
			if err != nil {
				continue
			}
			if found := p.findAmd64Manifest(parsed, depth+1); found != nil {
				// Carry the outer descriptor's name annotation
				// down: the tag lives on the index, not on the
				// per-platform manifest.
				if found.Annotations == nil && desc.Annotations != nil {
					found.Annotations = desc.Annotations
				}
				return found
			}
			continue
		}
		if !desc.MediaType.IsImage() {
			continue
		}
		if desc.Platform != nil {
			if !wantedPlatform(desc.Platform) {
				continue
			}
			return &desc
		}
		// No platform on the descriptor: consult the config it points at.
		if p.manifestIsAmd64(desc) {
			return &desc
		}
	}
	return nil
}

// manifestIsAmd64 reads the config a manifest descriptor points at to decide
// its platform, for saves whose descriptors carry no platform block.
func (p *saveParser) manifestIsAmd64(desc v1.Descriptor) bool {
	manifestBytes, ok := p.small[blobPath(desc.Digest.Hex)]
	if !ok {
		return false
	}
	manifest, err := v1.ParseManifest(bytes.NewReader(manifestBytes))
	if err != nil {
		return false
	}
	configBytes, ok := p.small[blobPath(manifest.Config.Digest.Hex)]
	if !ok {
		return false
	}
	cfg, err := v1.ParseConfigFile(bytes.NewReader(configBytes))
	if err != nil {
		return false
	}
	return cfg.OS == PlatformOS && cfg.Architecture == PlatformArch
}

// resolveLegacyTarget reads the pre-OCI `manifest.json` layout, which a
// graphdriver-backed daemon still produces on its own.
func (p *saveParser) resolveLegacyTarget() *saveTarget {
	raw, ok := p.small["manifest.json"]
	if !ok {
		return nil
	}
	var entries []legacyManifest
	if err := json.Unmarshal(raw, &entries); err != nil || len(entries) == 0 {
		return nil
	}
	for _, entry := range entries {
		config, ok := p.small[entry.Config]
		if !ok {
			continue
		}
		cfg, err := v1.ParseConfigFile(bytes.NewReader(config))
		if err != nil {
			continue
		}
		if cfg.OS != "" && cfg.Architecture != "" && (cfg.OS != PlatformOS || cfg.Architecture != PlatformArch) {
			continue
		}
		target := &saveTarget{config: config, legacyName: entry.RepoTags}
		for _, layerPath := range entry.Layers {
			target.layers = append(target.layers, saveLayer{blob: layerPath})
		}
		return target
	}
	return nil
}

// blobPath is the member name of a blob inside a save tar.
func blobPath(hex string) string { return "blobs/sha256/" + hex }

// blobHex recovers a blob's digest from its member name.
func blobHex(name string) (string, bool) {
	const prefix = "blobs/sha256/"
	if !strings.HasPrefix(name, prefix) {
		return "", false
	}
	return strings.TrimPrefix(name, prefix), true
}

// Magic numbers of the compressions a layer blob may use. A save stream's
// blobs carry no media type of their own, so the format is sniffed rather than
// assumed — a legacy save's layer.tar is raw, an OCI-layout save's blob is
// whatever the registry stored.
var (
	gzipMagic = []byte{0x1f, 0x8b}
	zstdMagic = []byte{0x28, 0xb5, 0x2f, 0xfd}
)

// sniffLayerMediaType peeks at a blob's first bytes and returns the media type
// analyze.IndexLayer needs. An unrecognized prefix is reported as an
// uncompressed tar, which the tar reader then rejects if it is not one.
func sniffLayerMediaType(r *bufio.Reader) (string, error) {
	head, err := r.Peek(4)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	switch {
	case len(head) >= 2 && bytes.Equal(head[:2], gzipMagic):
		return analyze.MediaTypeOCILayerGzip, nil
	case len(head) >= 4 && bytes.Equal(head, zstdMagic):
		return analyze.MediaTypeOCILayerZstd, nil
	default:
		return analyze.MediaTypeOCILayerTar, nil
	}
}
