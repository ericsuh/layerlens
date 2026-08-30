package cachestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/domain"
)

func TestOpenAndRoundTrip(t *testing.T) {
	s := openStore(t, t.TempDir(), 1<<30, newClock())
	idx := makeIndex("base", 50)

	rec := putImage(t, s, "demo:v1", false, idx)

	got, err := s.LayerIndex(context.Background(), idx.DiffID)
	require.NoError(t, err)
	assert.Equal(t, idx.DiffID, got.DiffID)
	assert.Equal(t, idx.ChangesetDigest, got.ChangesetDigest)
	require.Len(t, got.Entries, 50)
	assert.Equal(t, idx.Entries[7], got.Entries[7])

	summary, ok := s.LayerSummary(idx.DiffID)
	require.True(t, ok)
	assert.Equal(t, idx.ContentBytes, summary.ContentBytes)
	assert.Equal(t, 50, summary.EntryCount)

	stored, err := s.Image(context.Background(), rec.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"demo:v1"}, stored.RefNames)
	assert.Positive(t, s.UsedBytes())
}

// TestIdenticalLayersStoredOnce is the property the whole content-addressed
// layout exists for: two images that share a layer pay for it once.
func TestIdenticalLayersStoredOnce(t *testing.T) {
	s := openStore(t, t.TempDir(), 1<<30, newClock())
	shared := makeIndex("shared-base", 40)

	putImage(t, s, "a:1", false, shared, makeIndex("a-top", 10))
	afterFirst := s.UsedBytes()
	putImage(t, s, "b:1", false, shared, makeIndex("b-top", 10))

	entries, err := os.ReadDir(s.layersRoot())
	require.NoError(t, err)
	assert.Len(t, entries, 3, "the shared layer must not be stored twice")

	sharedDir, err := layerDirFor(s.Root(), shared.DiffID)
	require.NoError(t, err)
	sharedBytes, err := dirBytes(sharedDir)
	require.NoError(t, err)
	// The second image only added its own top layer and its record: its
	// whole cost is less than one more copy of the shared base would have
	// been.
	require.Positive(t, sharedBytes)
	assert.Less(t, s.UsedBytes()-afterFirst, sharedBytes)
}

// TestAtomicCommitOrderSurvivesKill simulates a crash in the one window the
// commit order leaves open — the index is renamed in, the process dies before
// layer.json — and asserts the next start sweeps it rather than serving a
// layer whose sidecar never existed.
func TestAtomicCommitOrderSurvivesKill(t *testing.T) {
	root := t.TempDir()
	c := newClock()
	s := openStore(t, root, 1<<30, c)

	keep := makeIndex("keep", 20)
	putImage(t, s, "keep:1", false, keep)

	halfWritten := makeIndex("half", 20)
	txn, err := s.Begin()
	require.NoError(t, err)
	require.NoError(t, txn.PutLayer(halfWritten))
	require.NoError(t, txn.Abort())

	// Kill simulation: the sidecar is what marks a layer directory
	// committed, so removing it reproduces exactly the state a crash
	// between the two renames leaves behind.
	halfDir, err := layerDirFor(root, halfWritten.DiffID)
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(halfDir, layerFileName)))

	// And a staging directory from an ingest that never finished.
	orphanStaging := filepath.Join(root, schemaDir, stagingDir, "abandoned")
	require.NoError(t, os.MkdirAll(orphanStaging, dirPerm))
	require.NoError(t, os.WriteFile(filepath.Join(orphanStaging, "x.tmp"), []byte("junk"), 0o600))

	require.NoError(t, s.Close())
	reopened := openStore(t, root, 1<<30, c)

	assert.NoDirExists(t, halfDir, "a layer directory without its sidecar must be swept")
	assert.NoDirExists(t, orphanStaging, "abandoned staging must be swept")
	assert.False(t, reopened.HasLayer(halfWritten.DiffID))

	// The complete layer and its image survived untouched.
	assert.True(t, reopened.HasLayer(keep.DiffID))
	_, err = reopened.Image(context.Background(), dig("image:keep:1"))
	assert.NoError(t, err)
}

// TestImageRecordOnlyAfterAllLayers pins the other half of the commit order: a
// record is never visible unless every layer it names is present.
func TestImageRecordOnlyAfterAllLayers(t *testing.T) {
	root := t.TempDir()
	c := newClock()
	s := openStore(t, root, 1<<30, c)

	present := makeIndex("present", 10)
	txn, err := s.Begin()
	require.NoError(t, err)
	require.NoError(t, txn.PutLayer(present))

	err = txn.Commit(&domain.ImageRecord{
		ID: dig("image:broken"),
		Layers: []domain.Layer{
			{Index: 0, DiffID: present.DiffID},
			{Index: 1, DiffID: dig("never-written")},
		},
	})
	require.Error(t, err, "committing a record naming an uncommitted layer must fail")
	assert.Contains(t, err.Error(), "never put or used")
	require.NoError(t, txn.Abort())

	// A corrupt record that nevertheless reached the disk is dropped at the
	// next start rather than being served with a hole in it.
	recPath, err := imagePathFor(root, dig("image:handwritten"))
	require.NoError(t, err)
	doc := imageDoc{V: RecordSchemaVersion, ImageRecord: domain.ImageRecord{
		ID:     dig("image:handwritten"),
		Layers: []domain.Layer{{DiffID: dig("missing-layer")}},
	}}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(recPath, raw, 0o600))

	require.NoError(t, s.Close())
	reopened := openStore(t, root, 1<<30, c)

	assert.NoFileExists(t, recPath)
	_, err = reopened.Image(context.Background(), dig("image:handwritten"))
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestSweepRejectsUnknownSchemaVersion(t *testing.T) {
	root := t.TempDir()
	c := newClock()
	s := openStore(t, root, 1<<30, c)
	idx := makeIndex("future", 5)
	putImage(t, s, "future:1", false, idx)

	dir, err := layerDirFor(root, idx.DiffID)
	require.NoError(t, err)
	raw, err := json.Marshal(layerDoc{V: RecordSchemaVersion + 1, DiffID: idx.DiffID})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, layerFileName), raw, 0o600))

	require.NoError(t, s.Close())
	reopened := openStore(t, root, 1<<30, c)

	assert.False(t, reopened.HasLayer(idx.DiffID))
	// The image referenced it, so the record goes too: a half-readable
	// cache is worse than an empty one, and both are re-derivable.
	_, err = reopened.Image(context.Background(), dig("image:future:1"))
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

// TestDigestPathTraversalRejected is the ARCHITECTURE §7.3 control: a hostile
// digest must fail validation before it can reach filepath.Join.
func TestDigestPathTraversalRejected(t *testing.T) {
	hostile := []string{
		"sha256:../../etc/passwd",
		"sha256:..",
		"../../../etc/passwd",
		"sha256:/absolute/path",
		"sha256:" + string(make([]byte, 64)),
		"sha256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"",
	}
	for _, raw := range hostile {
		t.Run(raw, func(t *testing.T) {
			d := domain.Digest(raw)

			_, err := pathComponent(d)
			require.ErrorIs(t, err, ErrInvalidDigest)

			_, err = layerDirFor("/data", d)
			require.ErrorIs(t, err, ErrInvalidDigest)

			_, err = imagePathFor("/data", d)
			require.ErrorIs(t, err, ErrInvalidDigest)
		})
	}

	// The same check guards the live API surface, not just the helpers.
	s := openStore(t, t.TempDir(), 1<<30, newClock())
	_, err := s.LayerIndex(context.Background(), domain.Digest("sha256:../../etc/passwd"))
	assert.ErrorIs(t, err, ErrInvalidDigest)

	txn, err := s.Begin()
	require.NoError(t, err)
	defer func() { _ = txn.Abort() }()
	err = txn.PutLayer(&domain.LayerIndex{DiffID: domain.Digest("sha256:../../x")})
	assert.ErrorIs(t, err, ErrInvalidDigest)
}

// TestSecondProcessFlockFailsFast covers ARCHITECTURE §1.3's "exactly one
// server process per cache root". flock is per open-file-description, so a
// second Open of the same directory conflicts even from this process.
func TestSecondProcessFlockFailsFast(t *testing.T) {
	root := t.TempDir()
	first := openStore(t, root, 1<<30, newClock())

	second, err := Open(Options{Root: root, MaxBytes: 1 << 30, Logger: discard()})
	require.Error(t, err)
	assert.Nil(t, second)
	assert.Contains(t, err.Error(), "locked by another layerlens process")

	// Releasing the lock makes the directory usable again immediately.
	require.NoError(t, first.Close())
	third, err := Open(Options{Root: root, MaxBytes: 1 << 30, Logger: discard()})
	require.NoError(t, err)
	require.NoError(t, third.Close())
}

func TestOpenRejectsInvalidOptions(t *testing.T) {
	_, err := Open(Options{Root: "", MaxBytes: 1})
	assert.ErrorContains(t, err, "Root")

	_, err = Open(Options{Root: t.TempDir(), MaxBytes: 0})
	assert.ErrorContains(t, err, "MaxBytes")
}

// TestConcurrentReadDuringEviction asserts the §5 promise that a reader racing
// an eviction sees the old index or nothing — never a truncated or mixed one.
func TestConcurrentReadDuringEviction(t *testing.T) {
	root := t.TempDir()
	c := newClock()
	s := openStore(t, root, 1<<30, c)

	victim := makeIndex("victim", 300)
	putImage(t, s, "victim:1", false, victim)
	c.advance(time.Hour)
	filler := makeIndex("filler", 300)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				idx, err := s.LayerIndex(context.Background(), victim.DiffID)
				if err != nil {
					// Gone is fine; torn is not, and a torn
					// index surfaces as a decode error.
					assert.True(t,
						errors.Is(err, domain.ErrNotIndexed),
						"unexpected read error: %v", err)
					continue
				}
				assert.Len(t, idx.Entries, 300)
				assert.Equal(t, victim.DiffID, idx.DiffID)
			}
		}()
	}

	// Shrink the budget to exactly what the second image needs, forcing the
	// first one out while the readers are hammering it.
	s.mu.Lock()
	s.max = s.accounted
	s.mu.Unlock()
	_, err := tryPutImage(s, "filler:1", false, filler)
	require.NoError(t, err)

	close(stop)
	wg.Wait()
	assert.False(t, s.HasLayer(victim.DiffID))
}

// diskBytes sums exactly what the accounting claims to track: every file under
// the layer store and the image store. Staging is excluded — a committed cache
// has none, and the sweep does not count it either.
func diskBytes(t *testing.T, s *Store) int64 {
	t.Helper()
	var total int64
	for _, root := range []string{s.layersRoot(), s.imagesRoot()} {
		err := filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() {
				total += info.Size()
			}
			return nil
		})
		require.NoError(t, err)
	}
	return total
}

// evictAll drives the LRU until nothing evictable is left, which is the state
// that makes accounting drift visible: whatever `accounted` still holds after
// every byte is gone from disk is drift, and it is permanent.
func evictAll(s *Store) {
	s.mu.Lock()
	s.max = 1
	s.mu.Unlock()
	if err := s.reserve(nil, 1); err == nil {
		s.release(nil, 1)
	}
}

// TestConcurrentPutLayerOfSameDiffIDAccountsOnce is ARCHITECTURE §5's "a mutex
// per layer digest", asserted as the property that mutex exists for.
//
// PutLayer is check-then-act. Without serialization two transactions that both
// miss the same DiffID both reserve its bytes and both install a table entry,
// one overwriting the other — so the cache is charged twice for one directory
// and refunded once when it is evicted. The drift is monotonic and invisible
// until a long-running server refuses `cache_full` on an empty disk, which is
// exactly what this asserts cannot happen: accounting matches the bytes on
// disk while the layer is live, and returns to zero when it is gone.
//
// Unreachable while fixtures ingest sequentially; normal the moment pulls run
// concurrently.
func TestConcurrentPutLayerOfSameDiffIDAccountsOnce(t *testing.T) {
	s := openStore(t, t.TempDir(), 1<<30, newClock())

	const putters = 16
	var wg sync.WaitGroup
	errs := make([]error, putters)
	start := make(chan struct{})
	for i := range putters {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each transaction builds its own copy of the same
			// layer, exactly as two concurrent pulls of two images
			// sharing a base would.
			idx := makeIndex("shared", 400)
			txn, err := s.Begin()
			if err != nil {
				errs[i] = err
				return
			}
			<-start
			if err := txn.PutLayer(idx); err != nil {
				errs[i] = err
				_ = txn.Abort()
				return
			}
			errs[i] = txn.Commit(&domain.ImageRecord{
				ID:       dig(fmt.Sprintf("image:concurrent-%d", i)),
				RefNames: []string{fmt.Sprintf("concurrent:%d", i)},
				Source:   domain.SourceRegistry,
				Platform: "linux/amd64",
				Layers: []domain.Layer{
					{Index: 0, DiffID: idx.DiffID, ChangesetDigest: idx.ChangesetDigest},
				},
			})
		}()
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		require.NoError(t, err, "putter %d", i)
	}

	// One layer directory on disk, however many transactions raced for it.
	layerDirs, err := os.ReadDir(s.layersRoot())
	require.NoError(t, err)
	assert.Len(t, layerDirs, 1, "one DiffID is one layer directory")

	assert.Equal(t, diskBytes(t, s), s.UsedBytes(),
		"the accounting must equal the bytes actually on disk, not the number of transactions that raced")

	evictAll(s)
	assert.Equal(t, int64(0), diskBytes(t, s), "everything is evictable, so everything is gone")
	assert.Equal(t, int64(0), s.UsedBytes(),
		"and the accounting returns to zero: any residue is permanent drift toward a false cache_full")
}

// TestCommitRejectsLayerThisTransactionNeverDeclared: presence in the cache is
// not the property that matters. A layer this ingest never put or used is held
// against eviction by nothing, so the evictor is free to delete it between the
// check and the record rename — producing the very record-without-its-layers
// state the startup sweep exists to clean up.
func TestCommitRejectsLayerThisTransactionNeverDeclared(t *testing.T) {
	s := openStore(t, t.TempDir(), 1<<30, newClock())
	other := makeIndex("someone-elses", 20)
	putImage(t, s, "other:v1", false, other)
	require.True(t, s.HasLayer(other.DiffID), "the layer really is in the cache")

	mine := makeIndex("mine", 20)
	txn, err := s.Begin()
	require.NoError(t, err)
	require.NoError(t, txn.PutLayer(mine))

	err = txn.Commit(&domain.ImageRecord{
		ID: dig("image:borrowing"),
		Layers: []domain.Layer{
			{Index: 0, DiffID: mine.DiffID},
			{Index: 1, DiffID: other.DiffID},
		},
	})
	require.Error(t, err, "a present-but-undeclared layer must not be enough to commit")
	assert.Contains(t, err.Error(), "never put or used")
	require.NoError(t, txn.Abort())

	// Declaring it — which is what pins it against eviction — makes the
	// same commit legal.
	txn, err = s.Begin()
	require.NoError(t, err)
	require.NoError(t, txn.PutLayer(mine))
	_, ok := txn.UseLayer(other.DiffID)
	require.True(t, ok)
	require.NoError(t, txn.Commit(&domain.ImageRecord{
		ID: dig("image:borrowing"),
		Layers: []domain.Layer{
			{Index: 0, DiffID: mine.DiffID},
			{Index: 1, DiffID: other.DiffID},
		},
	}))
}

// TestUpgradeProvenanceMergesWithoutRewritingUnchangedRecords covers the half
// of m8 that lives in the store: pinning is monotonic, refNames merge, and an
// ingest that learns nothing new writes nothing at all — the fixture load runs
// on every startup and must not churn a record file per boot.
func TestUpgradeProvenanceMergesWithoutRewritingUnchangedRecords(t *testing.T) {
	root := t.TempDir()
	s := openStore(t, root, 1<<30, newClock())
	idx := makeIndex("base", 20)

	txn, err := s.Begin()
	require.NoError(t, err)
	require.NoError(t, txn.PutLayer(idx))
	require.NoError(t, txn.Commit(&domain.ImageRecord{
		ID:       dig("image:dual"),
		RefNames: []string{"registry.example/app:v1"},
		Source:   domain.SourceRegistry,
		Platform: "linux/amd64",
		Layers:   []domain.Layer{{Index: 0, DiffID: idx.DiffID}},
	}))

	ctx := context.Background()
	got, err := s.UpgradeProvenance(ctx, dig("image:dual"), Provenance{
		RefNames: []string{"registry.example/app:v1", "example:v1"},
		Source:   domain.SourceFixture,
		Pinned:   true,
	})
	require.NoError(t, err)
	assert.True(t, got.Pinned, "a pinned ingest of an image already present must pin it")
	assert.Equal(t, domain.SourceFixture, got.Source)
	assert.Equal(t, []string{"registry.example/app:v1", "example:v1"}, got.RefNames,
		"refNames merge, in order, without duplicates")

	reloaded, err := s.Image(ctx, dig("image:dual"))
	require.NoError(t, err)
	assert.True(t, reloaded.Pinned, "and it is durable, not just in memory")
	assert.Equal(t, diskBytes(t, s), s.UsedBytes(), "the rewrite is reaccounted")

	// Pinning never reverses, and a no-op upgrade does not touch the file.
	path, err := imagePathFor(root, dig("image:dual"))
	require.NoError(t, err)
	before, err := os.ReadFile(path) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	again, err := s.UpgradeProvenance(ctx, dig("image:dual"), Provenance{
		RefNames: []string{"example:v1"},
		Source:   domain.SourceFixture,
	})
	require.NoError(t, err)
	assert.True(t, again.Pinned, "an unpinned ingest must not unpin an image")
	after, err := os.ReadFile(path) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	assert.Equal(t, before, after, "an upgrade that changes nothing must not rewrite the record")

	_, err = s.UpgradeProvenance(ctx, dig("image:absent"), Provenance{Pinned: true})
	assert.ErrorIs(t, err, domain.ErrNotFound)
}
