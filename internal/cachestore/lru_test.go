package cachestore

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/domain"
)

// tighten pins the cap to whatever is currently stored, so that the very next
// write has to evict something. Compressed index sizes are not predictable
// enough to hardcode a cap, and a test that guessed one would fail for reasons
// having nothing to do with the eviction policy it is checking.
func tighten(s *Store) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.max = s.accounted
}

func TestLRUEvictsInLastUsedAtOrder(t *testing.T) {
	c := newClock()
	s := openStore(t, t.TempDir(), 1<<30, c)

	// The oldest image is also the largest, so a single eviction always
	// frees enough for the newcomer and the assertion below is about
	// ordering rather than about how many victims were needed.
	putImage(t, s, "oldest:1", false, makeIndex("oldest", 400))
	c.advance(time.Hour)
	putImage(t, s, "middle:1", false, makeIndex("middle", 5))
	c.advance(time.Hour)
	putImage(t, s, "newest:1", false, makeIndex("newest", 5))

	tighten(s)
	c.advance(time.Hour)
	putImage(t, s, "arrival:1", false, makeIndex("arrival", 5))

	ctx := context.Background()
	_, err := s.Image(ctx, dig("image:oldest:1"))
	assert.ErrorIs(t, err, domain.ErrNotFound, "the least recently used image must be evicted first")
	assert.False(t, s.HasLayer(dig("oldest")), "its layers go with it")

	for _, name := range []string{"middle:1", "newest:1", "arrival:1"} {
		_, err := s.Image(ctx, dig("image:"+name))
		assert.NoError(t, err, "%s should have survived", name)
	}
	assert.LessOrEqual(t, s.UsedBytes(), s.MaxBytes())
}

// TestTouchIsDebounced: every comparison request touches both of its images, so
// an undebounced Touch would rewrite two record files per tree expand.
func TestTouchIsDebounced(t *testing.T) {
	c := newClock()
	s := openStore(t, t.TempDir(), 1<<30, c)
	rec := putImage(t, s, "demo:1", false, makeIndex("demo", 5))
	ctx := context.Background()
	start := c.now()

	path, err := imagePathFor(s.Root(), rec.ID)
	require.NoError(t, err)
	before, err := os.ReadFile(path) //nolint:gosec // test-controlled path
	require.NoError(t, err)

	c.advance(30 * time.Second) // inside the 1 minute debounce
	require.NoError(t, s.Touch(ctx, rec.ID))
	after, err := os.ReadFile(path) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	assert.Equal(t, before, after, "a touch inside the debounce window must not rewrite the record")

	stored, err := s.Image(ctx, rec.ID)
	require.NoError(t, err)
	assert.True(t, stored.LastUsedAt.Equal(start))

	c.advance(2 * time.Minute)
	want := c.now()
	require.NoError(t, s.Touch(ctx, rec.ID))
	stored, err = s.Image(ctx, rec.ID)
	require.NoError(t, err)
	assert.True(t, stored.LastUsedAt.Equal(want), "a touch past the debounce window must bump the clock")

	// And the bump is durable, not just in memory.
	require.NoError(t, s.Close())
	reopened := openStore(t, s.Root(), 1<<30, c)
	stored, err = reopened.Image(ctx, rec.ID)
	require.NoError(t, err)
	assert.True(t, stored.LastUsedAt.Equal(want))

	assert.ErrorIs(t, s.Touch(ctx, dig("image:absent")), domain.ErrNotFound)
}

// TestRefcountedLayerSurvivesUntilLastImageGone: the retention unit is the
// image, but the storage unit is the layer, so a layer must outlive the
// eviction of one of the several images that reference it.
func TestRefcountedLayerSurvivesUntilLastImageGone(t *testing.T) {
	c := newClock()
	s := openStore(t, t.TempDir(), 1<<30, c)
	shared := makeIndex("shared", 200)
	ctx := context.Background()

	putImage(t, s, "first:1", false, shared, makeIndex("first-top", 200))
	c.advance(time.Hour)
	putImage(t, s, "second:1", false, shared, makeIndex("second-top", 5))

	tighten(s)
	c.advance(time.Hour)
	putImage(t, s, "third:1", false, makeIndex("third", 5))

	_, err := s.Image(ctx, dig("image:first:1"))
	require.ErrorIs(t, err, domain.ErrNotFound)
	assert.False(t, s.HasLayer(dig("first-top")), "a layer only the evicted image used is reclaimed")
	assert.True(t, s.HasLayer(shared.DiffID), "a layer the surviving image still references must stay")

	// Now evict the last referencing image.
	tighten(s)
	c.advance(time.Hour)
	putImage(t, s, "fourth:1", false, makeIndex("fourth", 5))

	_, err = s.Image(ctx, dig("image:second:1"))
	require.ErrorIs(t, err, domain.ErrNotFound)
	assert.False(t, s.HasLayer(shared.DiffID), "with its last referencing image gone the layer is reclaimed")
}

// TestPinnedImagesAreNeverEvicted: the vendored fixtures are the offline demo,
// so a pull large enough to sweep the cache must not be able to delete them.
func TestPinnedImagesAreNeverEvicted(t *testing.T) {
	c := newClock()
	s := openStore(t, t.TempDir(), 1<<30, c)
	ctx := context.Background()

	pinned := makeIndex("fixture", 200)
	putImage(t, s, "example:v1", true, pinned)
	c.advance(time.Hour)
	putImage(t, s, "pulled:1", false, makeIndex("pulled", 200))

	tighten(s)
	c.advance(time.Hour)
	putImage(t, s, "newcomer:1", false, makeIndex("newcomer", 5))

	_, err := s.Image(ctx, dig("image:example:v1"))
	assert.NoError(t, err, "a pinned image survives eviction pressure")
	assert.True(t, s.HasLayer(pinned.DiffID))
	_, err = s.Image(ctx, dig("image:pulled:1"))
	assert.ErrorIs(t, err, domain.ErrNotFound, "the un-pinned image is what gets evicted instead")
}

// TestTinyCapRefusesWithCacheFull is RESEARCH Q7's named requirement: an image
// that cannot fit is refused with a clear error and nothing already cached is
// evicted on its behalf.
func TestTinyCapRefusesWithCacheFull(t *testing.T) {
	c := newClock()
	// Small enough that a single sizeable image cannot fit, large enough
	// for a one-entry index plus its record.
	s := openStore(t, t.TempDir(), 8<<10, c)
	ctx := context.Background()

	existing := makeIndex("existing", 1)
	putImage(t, s, "existing:1", false, existing)
	usedBefore := s.UsedBytes()
	require.Positive(t, usedBefore)

	_, err := tryPutImage(s, "toobig:1", false, makeIndex("toobig", 20000))
	require.ErrorIs(t, err, ErrCacheFull)
	assert.Contains(t, err.Error(), "cap")

	// Nothing was evicted to make room for something that could never fit.
	_, err = s.Image(ctx, dig("image:existing:1"))
	assert.NoError(t, err, "a refused ingest must leave pre-existing entries untouched")
	assert.True(t, s.HasLayer(existing.DiffID))
	assert.Equal(t, usedBefore, s.UsedBytes(), "the accounting must be exactly where it was")

	// The refused ingest left no staging behind either.
	staging, err := os.ReadDir(s.stagingRoot())
	require.NoError(t, err)
	assert.Empty(t, staging)

	// And the layer store holds only the surviving image's layer.
	layers, err := os.ReadDir(s.layersRoot())
	require.NoError(t, err)
	assert.Len(t, layers, 1)
}

// TestRefusalAccountsForPinnedImages: the budget an ingest is measured against
// is the cap minus what can never be evicted, so a cache full of pinned
// fixtures refuses an image that would have fit into an empty cache.
func TestRefusalAccountsForPinnedImages(t *testing.T) {
	c := newClock()
	s := openStore(t, t.TempDir(), 1<<30, c)

	pinned := makeIndex("pinned-bulk", 400)
	putImage(t, s, "fixture:v1", true, pinned)

	// Cap set so that the pinned image leaves only a sliver spare.
	s.mu.Lock()
	s.max = s.accounted + 128
	s.mu.Unlock()

	_, err := tryPutImage(s, "another:1", false, makeIndex("another", 400))
	require.ErrorIs(t, err, ErrCacheFull)
	assert.Contains(t, err.Error(), "pinned")

	assert.True(t, s.HasLayer(pinned.DiffID))
}

// TestAbortKeepsCommittedLayers: the layer is the durable checkpoint unit, so
// an aborted ingest leaves its finished layers behind for the retry to reuse —
// while they stay evictable, because no record references them.
func TestAbortKeepsCommittedLayers(t *testing.T) {
	c := newClock()
	s := openStore(t, t.TempDir(), 1<<30, c)

	idx := makeIndex("checkpoint", 20)
	txn, err := s.Begin()
	require.NoError(t, err)
	require.NoError(t, txn.PutLayer(idx))
	require.NoError(t, txn.Abort())

	assert.True(t, s.HasLayer(idx.DiffID), "a committed layer survives an aborted ingest")
	staging, err := os.ReadDir(s.stagingRoot())
	require.NoError(t, err)
	assert.Empty(t, staging, "but its staging area does not")

	// A retry reuses it without rewriting it.
	rec := putImage(t, s, "retry:1", false, idx)
	assert.Len(t, rec.Layers, 1)
}

// TestInFlightLayersAreNotEvicted: a layer committed by an open transaction is
// referenced by nothing yet, and must not be mistaken for garbage before the
// image record that names it lands.
func TestInFlightLayersAreNotEvicted(t *testing.T) {
	c := newClock()
	s := openStore(t, t.TempDir(), 1<<30, c)

	putImage(t, s, "victim:1", false, makeIndex("victim", 400))

	txn, err := s.Begin()
	require.NoError(t, err)
	first := makeIndex("in-flight-a", 5)
	require.NoError(t, txn.PutLayer(first))

	// Force eviction pressure while the transaction is still open.
	tighten(s)
	c.advance(time.Hour)
	require.NoError(t, txn.PutLayer(makeIndex("in-flight-b", 5)))

	assert.True(t, s.HasLayer(first.DiffID), "the in-flight layer must survive the eviction it triggered")
	require.NoError(t, txn.Abort())
}

// TestPeakDiskStaysWithinTheCap is M1. --cache-max-bytes bounded what the cache
// *retained*, not what it ever *touched*: the staged index was written in full
// and only then measured against the budget, so a 1 MiB cap was observed with
// 12,104,078 bytes on disk (11.5x) before ErrCacheFull came back. The staged
// write is now charged against the remaining budget as it streams.
func TestPeakDiskStaysWithinTheCap(t *testing.T) {
	const maxBytes = 1 << 20
	root := t.TempDir()
	c := newClock()
	s := openStore(t, root, maxBytes, c)

	// A sampler watches the whole data directory while the doomed put runs.
	// Peak, not final size, is the quantity that matters: the old code
	// deleted the oversized staging file on its way out, so the overshoot
	// was invisible to anything that only looked afterwards.
	var peak atomic.Int64
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if n := treeBytes(root); n > peak.Load() {
				peak.Store(n)
			}
			select {
			case <-stop:
				return
			default:
			}
		}
	}()

	// An index far too large for the cap: ~500k entries compressed.
	_, err := tryPutImage(s, "toobig:1", false, makeIndex("toobig", 500_000))
	close(stop)
	<-done

	require.ErrorIs(t, err, ErrCacheFull)
	t.Logf("cap %d bytes; peak on disk %d bytes (%.2fx)", maxBytes, peak.Load(), float64(peak.Load())/float64(maxBytes))
	assert.LessOrEqual(t, peak.Load(), int64(maxBytes+(64<<10)),
		"peak on-disk bytes (%d) must stay within a small constant of the %d byte cap", peak.Load(), maxBytes)
	assert.Zero(t, s.UsedBytes())

	// And the refusal left nothing behind.
	staging, err := os.ReadDir(s.stagingRoot())
	require.NoError(t, err)
	assert.Empty(t, staging)
}

// treeBytes is the on-disk size of everything under root, recursively.
func treeBytes(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // a file that vanished mid-walk is a rename, not a failure
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
