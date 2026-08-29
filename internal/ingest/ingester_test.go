package ingest_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/analyze"
	"github.com/ericsuh/layerlens/internal/cachestore"
	"github.com/ericsuh/layerlens/internal/domain"
	"github.com/ericsuh/layerlens/internal/ingest"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fixturesDir is the committed fixtures/ tree. The tests read the artifact
// that ships, not a freshly generated one.
func fixturesDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "fixtures"))
	require.NoError(t, err)
	require.DirExists(t, dir, "run `mise run genfixtures` to create the vendored fixtures")
	return dir
}

func newStore(t *testing.T, root string, maxBytes int64) *cachestore.Store {
	t.Helper()
	s, err := cachestore.Open(cachestore.Options{Root: root, MaxBytes: maxBytes, Logger: discard()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func newIngester(s *cachestore.Store) *ingest.Ingester {
	return ingest.New(s, ingest.Options{Logger: discard()})
}

// TestFixtureStartupLoad is the acceptance path: with no network and no Docker,
// every vendored demo image ends up analyzed and pinned.
func TestFixtureStartupLoad(t *testing.T) {
	s := newStore(t, t.TempDir(), 1<<30)
	res, err := newIngester(s).LoadFixtures(context.Background(), fixturesDir(t))
	require.NoError(t, err)

	assert.Equal(t, 5, res.Layouts, "one OCI layout per fixture pair")
	assert.Equal(t, 10, res.Ingested)
	assert.Zero(t, res.AlreadyPresent)
	assert.Positive(t, res.LayersSkipped, "the pairs share trunk layers, which must be indexed once")

	images, err := s.Images(context.Background())
	require.NoError(t, err)
	require.Len(t, images, 10)

	refs := make([]string, 0, len(images))
	for i := range images {
		rec := &images[i]
		assert.True(t, rec.Pinned, "%v must be pinned", rec.RefNames)
		assert.Equal(t, domain.SourceFixture, rec.Source)
		assert.Equal(t, ingest.PlatformString, rec.Platform)
		assert.NotEmpty(t, rec.ManifestDigest)
		assert.NoError(t, rec.ID.Validate())
		require.NotEmpty(t, rec.Layers)
		refs = append(refs, rec.RefNames...)

		var total int64
		for n := range rec.Layers {
			l := &rec.Layers[n]
			assert.Equal(t, n, l.Index)
			assert.NoError(t, l.DiffID.Validate())
			assert.NoError(t, l.ChainID.Validate())
			assert.NoError(t, l.ChangesetDigest.Validate())
			total += l.ContentBytes
		}
		assert.Equal(t, total, rec.TotalBytes)
	}
	assert.ElementsMatch(t, []string{
		"disjoint:a", "disjoint:b",
		"edgecase:opaque", "edgecase:plain",
		"example:v1", "example:v2",
		"prefix:base", "prefix:extended",
		"wide:v1", "wide:v2",
	}, refs)
}

// TestFixtureLoadIsIdempotent covers the restart path: the cache is durable, so
// a second boot analyzes nothing and the store does not grow.
func TestFixtureLoadIsIdempotent(t *testing.T) {
	root := t.TempDir()
	dir := fixturesDir(t)

	first := newStore(t, root, 1<<30)
	_, err := newIngester(first).LoadFixtures(context.Background(), dir)
	require.NoError(t, err)
	usedAfterFirst := first.UsedBytes()
	require.NoError(t, first.Close())

	second := newStore(t, root, 1<<30)
	res, err := newIngester(second).LoadFixtures(context.Background(), dir)
	require.NoError(t, err)

	assert.Zero(t, res.Ingested, "a restart must not re-analyze anything")
	assert.Equal(t, 10, res.AlreadyPresent)
	assert.Equal(t, usedAfterFirst, second.UsedBytes(), "and must not grow the store")

	images, err := second.Images(context.Background())
	require.NoError(t, err)
	assert.Len(t, images, 10)
}

// TestAlreadyIndexedLayersAreSkippedWithoutStreaming is the §4.1 property that
// makes analyzing a second image built on the same base nearly free: the shared
// trunk is never read again, not merely re-hashed cheaply.
func TestAlreadyIndexedLayersAreSkippedWithoutStreaming(t *testing.T) {
	s := newStore(t, t.TempDir(), 1<<30)
	ing := newIngester(s)
	images := loadExamplePair(t)
	ctx := context.Background()

	var firstReads atomic.Int32
	_, err := ing.Ingest(ctx, countingImage{Image: images["example:v1"], reads: &firstReads},
		ingest.Meta{Source: domain.SourceFixture})
	require.NoError(t, err)
	assert.Equal(t, int32(8), firstReads.Load(), "a cold ingest streams every layer once")

	var secondReads atomic.Int32
	res, err := ing.Ingest(ctx, countingImage{Image: images["example:v2"], reads: &secondReads},
		ingest.Meta{Source: domain.SourceFixture})
	require.NoError(t, err)

	// Six, not five: the five trunk layers plus the apt/ffmpeg layer, whose
	// tar is byte-identical in both images (deb archives reproduce their
	// own timestamps) and therefore has the same DiffID even though it sits
	// past the fork. Dedupe is by DiffID, not by position.
	assert.Equal(t, 6, res.LayersSkipped)
	assert.Equal(t, 2, res.LayersIndexed, "only the COPY and npm layers are new content")
	assert.Equal(t, int32(2), secondReads.Load(),
		"a skipped layer must not be opened at all, let alone streamed")
}

// TestDiffIDMismatchAbortsImage: a layer whose bytes do not hash to the DiffID
// the config declares is a tampered or corrupt stream, and must fail the ingest
// rather than being cached as the layer it claims to be.
func TestDiffIDMismatchAbortsImage(t *testing.T) {
	s := newStore(t, t.TempDir(), 1<<30)
	ctx := context.Background()
	img := loadExamplePair(t)["example:v1"]

	configName, err := img.ConfigName()
	require.NoError(t, err)
	id, err := domain.ParseDigest(configName.String())
	require.NoError(t, err)

	// Layer 1 is served the bytes of layer 2: everything the manifest and
	// config say about it stays correct, only the content is wrong — which
	// is exactly what a tampered blob or a truncated transfer looks like.
	_, err = newIngester(s).Ingest(ctx, swappedLayerImage{Image: img, at: 1, from: 2},
		ingest.Meta{Source: domain.SourceFixture})
	require.Error(t, err)
	assert.ErrorIs(t, err, analyze.ErrDiffIDMismatch)

	_, err = s.Image(ctx, id)
	assert.ErrorIs(t, err, domain.ErrNotFound, "no record may be committed for a failed ingest")

	staging, err := os.ReadDir(filepath.Join(s.Root(), "v1", "staging"))
	require.NoError(t, err)
	assert.Empty(t, staging, "the aborted ingest must leave no staging behind")
}

// TestIngestRefusesWhenCacheIsFull threads the cachestore refusal through the
// ingest pipeline, which is where the API's cache_full comes from.
func TestIngestRefusesWhenCacheIsFull(t *testing.T) {
	s := newStore(t, t.TempDir(), 16<<10)
	ctx := context.Background()

	_, err := newIngester(s).Ingest(ctx, loadExamplePair(t)["example:v1"],
		ingest.Meta{Source: domain.SourceFixture})
	require.ErrorIs(t, err, cachestore.ErrCacheFull)

	images, err := s.Images(ctx)
	require.NoError(t, err)
	assert.Empty(t, images)
}

func TestDiscoverLayouts(t *testing.T) {
	root := fixturesDir(t)

	dirs, err := ingest.DiscoverLayouts(root)
	require.NoError(t, err)
	names := make([]string, 0, len(dirs))
	for _, d := range dirs {
		names = append(names, filepath.Base(d))
	}
	assert.Equal(t, []string{"disjoint", "edgecase", "example", "prefix", "wide"}, names)

	// A layout directory passed directly is itself a valid fixtures root.
	dirs, err = ingest.DiscoverLayouts(filepath.Join(root, "example"))
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(root, "example")}, dirs)

	// A directory with nothing in it is not an error, just empty: the
	// server warns and starts anyway.
	dirs, err = ingest.DiscoverLayouts(t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, dirs)

	_, err = ingest.DiscoverLayouts(filepath.Join(t.TempDir(), "absent"))
	assert.Error(t, err)
}

func TestLoadFixturesWithoutLayouts(t *testing.T) {
	s := newStore(t, t.TempDir(), 1<<30)
	_, err := newIngester(s).LoadFixtures(context.Background(), t.TempDir())
	assert.ErrorIs(t, err, ingest.ErrNoLayouts)
}

// TestOpenLayoutSkipsForeignManifests covers DECISIONS risk 4: BuildKit
// attestation manifests advertise themselves as unknown/unknown and would
// otherwise appear in the picker as phantom images.
func TestOpenLayoutSkipsForeignManifests(t *testing.T) {
	dir := copyTree(t, filepath.Join(fixturesDir(t), "example"))

	var index struct {
		SchemaVersion int               `json:"schemaVersion"`
		MediaType     string            `json:"mediaType"`
		Manifests     []json.RawMessage `json:"manifests"`
	}
	readJSON(t, filepath.Join(dir, "index.json"), &index)
	require.Len(t, index.Manifests, 2)

	var genuine map[string]any
	require.NoError(t, json.Unmarshal(index.Manifests[0], &genuine))
	for _, platform := range []map[string]any{
		{"os": "unknown", "architecture": "unknown"},
		{"os": "linux", "architecture": "arm64"},
	} {
		foreign := make(map[string]any, len(genuine))
		for k, v := range genuine {
			foreign[k] = v
		}
		foreign["platform"] = platform
		raw, err := json.Marshal(foreign)
		require.NoError(t, err)
		index.Manifests = append(index.Manifests, raw)
	}
	writeJSON(t, filepath.Join(dir, "index.json"), index)

	images, err := ingest.OpenLayout(dir)
	require.NoError(t, err)
	assert.Len(t, images, 2, "only the two linux/amd64 manifests are analyzable")
}

// ---------------------------------------------------------------- helpers

func loadExamplePair(t *testing.T) map[string]v1.Image {
	t.Helper()
	images, err := ingest.OpenLayout(filepath.Join(fixturesDir(t), "example"))
	require.NoError(t, err)
	out := make(map[string]v1.Image, len(images))
	for _, img := range images {
		out[img.Ref] = img.Image
	}
	require.Contains(t, out, "example:v1")
	require.Contains(t, out, "example:v2")
	return out
}

// countingImage counts how many layer blobs are actually opened, which is the
// only way to distinguish "skipped without streaming" from "streamed cheaply".
type countingImage struct {
	v1.Image
	reads *atomic.Int32
}

func (c countingImage) Layers() ([]v1.Layer, error) {
	layers, err := c.Image.Layers()
	if err != nil {
		return nil, err
	}
	out := make([]v1.Layer, 0, len(layers))
	for _, l := range layers {
		out = append(out, countingLayer{Layer: l, reads: c.reads})
	}
	return out, nil
}

type countingLayer struct {
	v1.Layer
	reads *atomic.Int32
}

func (c countingLayer) Compressed() (io.ReadCloser, error) {
	c.reads.Add(1)
	return c.Layer.Compressed()
}

// swappedLayerImage serves the blob of layer `from` at position `at`, leaving
// every declaration intact: a content/declaration mismatch and nothing else.
type swappedLayerImage struct {
	v1.Image
	at   int
	from int
}

func (s swappedLayerImage) Layers() ([]v1.Layer, error) {
	layers, err := s.Image.Layers()
	if err != nil {
		return nil, err
	}
	layers[s.at] = swappedLayer{Layer: layers[s.at], other: layers[s.from]}
	return layers, nil
}

type swappedLayer struct {
	v1.Layer
	other v1.Layer
}

func (s swappedLayer) Compressed() (io.ReadCloser, error) { return s.other.Compressed() }

func copyTree(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	require.NoError(t, filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		data, err := os.ReadFile(path) //nolint:gosec // test-controlled path
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	}))
	return dst
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, v))
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))
}
