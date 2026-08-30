// Package ingest turns an image from some source into cached analysis: one
// streaming pass per layer through analyze.IndexLayer, committed layer by
// layer into the cachestore, then one image record.
//
// This phase implements the local source only — an OCI image layout on disk,
// which is how the vendored demo fixtures are loaded at startup with no
// network and no Docker. Registry and daemon sources plug into the same
// pipeline in a later phase; they differ only in where the v1.Image comes
// from.
//
// It is the only package besides imgref that may import go-containerregistry,
// and none of its types escape: everything this package returns is
// domain-typed (ARCHITECTURE §2).
package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"

	"github.com/ericsuh/layerlens/internal/analyze"
	"github.com/ericsuh/layerlens/internal/cachestore"
	"github.com/ericsuh/layerlens/internal/domain"
)

// Platform is the only platform layerlens analyzes. Multi-platform indexes are
// filtered down to it (DECISIONS risk 4).
const (
	PlatformOS   = "linux"
	PlatformArch = "amd64"
)

// PlatformString is the display form written into every image record.
const PlatformString = PlatformOS + "/" + PlatformArch

// ErrLayerCountMismatch reports an image whose config and manifest disagree
// about how many layers it has. Such an image cannot be analyzed coherently:
// the DiffID at position i would not describe the blob at position i.
var ErrLayerCountMismatch = errors.New("ingest: manifest layers and config diff_ids disagree")

// MaxLayers caps how many layers one image may have (ARCHITECTURE §7.2). Real
// images run to a few dozen; the cap exists so a hostile-but-allowlisted
// registry cannot turn one manifest into unbounded work.
const MaxLayers = 512

// ErrTooManyLayers reports an image past MaxLayers.
var ErrTooManyLayers = errors.New("ingest: image has too many layers")

// Meta is the provenance an image cannot tell us about itself.
type Meta struct {
	// RefNames are the display references, e.g. ["example:v1"].
	RefNames []string
	// Source is one of domain.SourceFixture, SourceRegistry, SourceDocker.
	Source string
	// Pinned marks an image that must never be LRU-evicted.
	Pinned bool
	// ManifestDigest is the platform manifest digest, when known.
	ManifestDigest domain.Digest
	// Progress, when non-nil, receives byte-accurate progress for this
	// ingest. It is the pull manager's window into a 25 GiB stream.
	Progress Reporter
}

// reporter returns the meta's reporter, or a discarding one.
func (m Meta) reporter() Reporter {
	if m.Progress == nil {
		return &NopReporter{}
	}
	return m.Progress
}

// Ingester runs the analysis pipeline against a cache store.
type Ingester struct {
	store *cachestore.Store
	log   *slog.Logger
	now   func() time.Time
}

// Options configures an Ingester.
type Options struct {
	Logger *slog.Logger
	// Now defaults to time.Now; tests pin it for deterministic records.
	Now func() time.Time
}

// New builds an Ingester writing into store.
func New(store *cachestore.Store, opts Options) *Ingester {
	i := &Ingester{store: store, log: opts.Logger, now: opts.Now}
	if i.log == nil {
		i.log = slog.Default()
	}
	if i.now == nil {
		i.now = time.Now
	}
	return i
}

// Result reports what one Ingest call did. The skipped count is the interesting
// number: it is the layers that were already indexed under another image and
// therefore cost nothing at all — no download, no decompression, no hashing.
type Result struct {
	Record        *domain.ImageRecord
	LayersIndexed int
	LayersSkipped int
	// AlreadyPresent is true when the image record already existed and no
	// work was done.
	AlreadyPresent bool
}

// Ingest analyzes img and commits it to the cache.
//
// Layer commits happen one at a time and each is durable on its own, so a
// cancelled or failed ingest leaves valid indexes behind and a retry resumes at
// layer granularity (§4.1). The image record is written last, which is what
// makes a visible record imply all of its layers are present (§5).
func (i *Ingester) Ingest(ctx context.Context, img v1.Image, meta Meta) (*Result, error) {
	progress := meta.reporter()
	progress.Phase(PhaseResolving)
	cfgName, err := img.ConfigName()
	if err != nil {
		return nil, fmt.Errorf("ingest: read config digest: %w", err)
	}
	id, err := domain.ParseDigest(cfgName.String())
	if err != nil {
		return nil, fmt.Errorf("ingest: config digest: %w", err)
	}

	if _, err := i.store.Image(ctx, id); err == nil {
		// Already analyzed. Re-reading the blobs would produce exactly
		// the same indexes, so none of the analysis is redone.
		//
		// The provenance, though, is not a property of the blobs: the
		// same image can arrive by two routes, and the second route
		// knows things the first did not. A fixture that a registry
		// pull happened to fetch first is the case that matters — it
		// would otherwise stay unpinned, which is to say evictable,
		// which is exactly what pinning a vendored demo image is for.
		upgraded, err := i.store.UpgradeProvenance(ctx, id, cachestore.Provenance{
			RefNames: meta.RefNames,
			Source:   meta.Source,
			Pinned:   meta.Pinned,
		})
		if err != nil {
			return nil, fmt.Errorf("ingest: upgrade provenance of %s: %w", id, err)
		}
		// Reported as a complete pull rather than as no pull at all: the
		// UI is showing a progress card, and "every layer was already
		// analyzed" is the honest reading of what just happened.
		progress.Totals(0, len(upgraded.Layers), false)
		for n := range upgraded.Layers {
			progress.LayerFinished(n, true, 0)
		}
		return &Result{Record: upgraded, AlreadyPresent: true, LayersSkipped: len(upgraded.Layers)}, nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}

	cfg, err := img.ConfigFile()
	if err != nil {
		return nil, fmt.Errorf("ingest: read config: %w", err)
	}
	manifest, err := img.Manifest()
	if err != nil {
		return nil, fmt.Errorf("ingest: read manifest: %w", err)
	}
	blobs, err := img.Layers()
	if err != nil {
		return nil, fmt.Errorf("ingest: list layers: %w", err)
	}
	if len(cfg.RootFS.DiffIDs) != len(blobs) || len(manifest.Layers) != len(blobs) {
		return nil, fmt.Errorf("%w: %d diff_ids, %d manifest layers, %d blobs",
			ErrLayerCountMismatch, len(cfg.RootFS.DiffIDs), len(manifest.Layers), len(blobs))
	}
	if len(blobs) > MaxLayers {
		return nil, fmt.Errorf("%w: %d layers", ErrTooManyLayers, len(blobs))
	}

	// The denominator comes from the manifest, which is why registry
	// progress is exact rather than a guess (§6.3): every layer's
	// compressed size is declared before a single byte is fetched.
	var bytesTotal int64
	for _, l := range manifest.Layers {
		bytesTotal += l.Size
	}
	progress.Totals(bytesTotal, len(blobs), false)
	progress.Phase(PhaseDownloading)

	txn, err := i.store.Begin()
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			if err := txn.Abort(); err != nil {
				i.log.Warn("ingest: abort transaction", "err", err)
			}
		}
	}()

	res := &Result{}
	layers := make([]domain.Layer, 0, len(blobs))
	var totalBytes int64

	for n, blob := range blobs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		diffID, err := domain.ParseDigest(cfg.RootFS.DiffIDs[n].String())
		if err != nil {
			return nil, fmt.Errorf("ingest: layer %d diff_id: %w", n, err)
		}
		progress.LayerStarted(n, diffID, manifest.Layers[n].Size)
		summary, skipped, err := i.indexOne(ctx, txn, blob, diffID, n, progress)
		if err != nil {
			return nil, err
		}
		if skipped {
			res.LayersSkipped++
		} else {
			res.LayersIndexed++
		}
		progress.LayerFinished(n, skipped, manifest.Layers[n].Size)

		compressedDigest, _ := domain.ParseDigest(manifest.Layers[n].Digest.String())
		layers = append(layers, domain.Layer{
			Index:            n,
			DiffID:           diffID,
			CompressedDigest: compressedDigest,
			CompressedSize:   manifest.Layers[n].Size,
			ContentBytes:     summary.ContentBytes,
			EntryCount:       summary.EntryCount,
			ChangesetDigest:  summary.ChangesetDigest,
		})
		totalBytes += summary.ContentBytes
	}

	progress.Phase(PhaseFinalizing)
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

// indexOne stores the changeset for one layer, streaming the blob only if the
// DiffID is not already indexed.
//
// The skip is the reason two images that share a base cost one pass, not two:
// layer indexes are content-addressed by DiffID and shared across every image
// that uses them.
func (i *Ingester) indexOne(ctx context.Context, txn *cachestore.Txn, blob v1.Layer,
	diffID domain.Digest, n int, progress Reporter,
) (cachestore.LayerSummary, bool, error) {
	if summary, ok := txn.UseLayer(diffID); ok {
		return summary, true, nil
	}

	mediaType, err := blob.MediaType()
	if err != nil {
		return cachestore.LayerSummary{}, false, fmt.Errorf("ingest: layer %d media type: %w", n, err)
	}
	rc, err := blob.Compressed()
	if err != nil {
		return cachestore.LayerSummary{}, false, fmt.Errorf("ingest: open layer %d: %w", n, err)
	}
	idx, indexErr := analyze.IndexLayer(ctx, analyze.LayerSource{
		Reader:    rc,
		MediaType: string(mediaType),
		// Passing the declared DiffID is what makes a tampered or
		// truncated blob fail the ingest instead of being cached as if
		// it were the layer it claims to be.
		DiffID: diffID,
		// The same counter for every layer, so it reads as bytes-done
		// for the whole pull rather than for this layer alone.
		Progress: progress.Bytes(),
	})
	closeErr := rc.Close()
	if indexErr != nil {
		return cachestore.LayerSummary{}, false, fmt.Errorf("ingest: index layer %d (%s): %w", n, diffID, indexErr)
	}
	if closeErr != nil {
		return cachestore.LayerSummary{}, false, fmt.Errorf("ingest: close layer %d: %w", n, closeErr)
	}
	if err := txn.PutLayer(idx); err != nil {
		return cachestore.LayerSummary{}, false, err
	}
	summary, ok := txn.UseLayer(diffID)
	if !ok {
		return cachestore.LayerSummary{}, false, fmt.Errorf("ingest: layer %s vanished after commit", diffID)
	}
	return summary, false, nil
}

// buildRecord assembles the image record: the derived chain IDs, the history
// mapping, and the provenance the image cannot tell us about itself.
//
// Shared by every source. The registry and layout paths walk a v1.Image's
// layers in order; the docker-save path reconstructs the same list from a
// stream that arrived out of order. Both end here, so an image record means
// exactly the same thing whichever door it came through.
func (i *Ingester) buildRecord(id domain.Digest, cfg *v1.ConfigFile, layers []domain.Layer,
	totalBytes int64, meta Meta,
) (*domain.ImageRecord, error) {
	diffIDs := make([]domain.Digest, len(layers))
	for n := range layers {
		diffIDs[n] = layers[n].DiffID
	}
	chains, err := analyze.ChainIDs(diffIDs)
	if err != nil {
		return nil, fmt.Errorf("ingest: chain ids: %w", err)
	}
	for n := range layers {
		layers[n].ChainID = chains[n]
	}
	analyze.ApplyHistory(layers, historyOf(cfg))

	rec := &domain.ImageRecord{
		ID:             id,
		ManifestDigest: meta.ManifestDigest,
		RefNames:       meta.RefNames,
		Source:         meta.Source,
		Platform:       PlatformString,
		IngestedAt:     i.now(),
		Pinned:         meta.Pinned,
		Layers:         layers,
		TotalBytes:     totalBytes,
	}
	if cfg.Created.IsZero() {
		rec.CreatedAt = rec.IngestedAt
	} else {
		rec.CreatedAt = cfg.Created.UTC()
	}
	return rec, nil
}

// configDigest is the image ID: the digest of the config blob exactly as it
// was serialized. It is computed over the raw bytes rather than over a
// re-encoding, because a re-encoding is a different image ID.
func configDigest(raw []byte) (domain.Digest, error) {
	sum := sha256.Sum256(raw)
	return domain.ParseDigest("sha256:" + hex.EncodeToString(sum[:]))
}

// historyOf projects an OCI config's history onto the domain mirror that
// analyze.MapHistory consumes, keeping go-containerregistry's types out of the
// pure packages.
func historyOf(cfg *v1.ConfigFile) []domain.HistoryEntry {
	out := make([]domain.HistoryEntry, 0, len(cfg.History))
	for _, h := range cfg.History {
		out = append(out, domain.HistoryEntry{
			CreatedBy:  h.CreatedBy,
			Comment:    h.Comment,
			EmptyLayer: h.EmptyLayer,
		})
	}
	return out
}
