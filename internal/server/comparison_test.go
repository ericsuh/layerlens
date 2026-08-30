package server_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/domain"
	"github.com/ericsuh/layerlens/internal/server"
)

// gatedLayers blocks the first layer read until the test releases it, which is
// what lets the single-flight property be asserted as a fact about work done
// rather than as a guess about timing.
type gatedLayers struct {
	inner   domain.LayerIndexSource
	entered chan struct{}
	once    sync.Once
	release chan struct{}
}

func (g *gatedLayers) LayerIndex(ctx context.Context, diffID domain.Digest) (*domain.LayerIndex, error) {
	g.once.Do(func() { close(g.entered) })
	select {
	case <-g.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return g.inner.LayerIndex(ctx, diffID)
}

// TestComparisonAssemblyIsSingleFlighted: expanding a directory in the UI can
// fire several requests for the same pair and selection at once, and squashing
// both sides is the most expensive thing the server does. N simultaneous
// identical requests must therefore do exactly one unit of work.
func TestComparisonAssemblyIsSingleFlighted(t *testing.T) {
	const requests = 16
	cache := seeded(t)
	gate := &gatedLayers{
		inner:   cache.store,
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	var assemblies atomic.Int32
	srv := apiServer(t, func(o *server.Options) {
		o.Layers = gate
		server.WithAssemblyCounter(o, func() { assemblies.Add(1) })
	})

	var inFlight atomic.Int32
	target := treeURL(id(t, "example:v1"), id(t, "example:v2"), url.Values{"path": {"/app"}})

	var wg sync.WaitGroup
	statuses := make([]int, requests)
	for i := range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			inFlight.Add(1)
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
			statuses[i] = rec.Code
		}()
	}

	// The leader is inside assembly and every request has entered the
	// server; give the followers a moment to reach the cache before the
	// leader is allowed to finish and publish its result. If single-flight
	// were broken they would start their own assemblies here, which the
	// counter would then see.
	<-gate.entered
	require.Eventually(t, func() bool { return inFlight.Load() == requests },
		5*time.Second, time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	close(gate.release)
	wg.Wait()

	for _, status := range statuses {
		assert.Equal(t, http.StatusOK, status)
	}
	assert.Equal(t, int32(1), assemblies.Load(),
		"%d concurrent identical requests must assemble the comparison once", requests)
}

// TestComparisonCacheReusesAndEvicts: paging and drill-down inside one
// comparison must be free, and the cache must stay bounded at its cap — the
// §4.6 memory budget is enforced by that cap, not by accounting.
func TestComparisonCacheReusesAndEvicts(t *testing.T) {
	var assemblies atomic.Int32
	srv := apiServer(t, func(o *server.Options) {
		o.ComparisonCacheSize = 2
		server.WithAssemblyCounter(o, func() { assemblies.Add(1) })
	})

	example := [2]domain.Digest{id(t, "example:v1"), id(t, "example:v2")}
	prefix := [2]domain.Digest{id(t, "prefix:base"), id(t, "prefix:extended")}
	disjoint := [2]domain.Digest{id(t, "disjoint:a"), id(t, "disjoint:b")}

	fetch := func(pair [2]domain.Digest, values url.Values) {
		t.Helper()
		var page server.TreePage
		getJSON(t, srv, treeURL(pair[0], pair[1], values), &page)
	}

	fetch(example, nil)
	assert.Equal(t, int32(1), assemblies.Load())

	// Same pair and selection, different page/path/filter: all served from
	// the one assembled comparison.
	fetch(example, url.Values{"path": {"/app"}})
	fetch(example, url.Values{"path": {"/app"}, "filter": {"changed"}})
	fetch(example, url.Values{"limit": {"1"}})
	assert.Equal(t, int32(1), assemblies.Load(), "paging inside a comparison is free")

	// A different layer selection is a different comparison.
	fetch(example, url.Values{"leftLayers": {"5"}, "rightLayers": {"5"}})
	assert.Equal(t, int32(2), assemblies.Load())

	// A third key evicts the least recently used of the two.
	fetch(prefix, nil)
	assert.Equal(t, int32(3), assemblies.Load())
	fetch(disjoint, nil)
	assert.Equal(t, int32(4), assemblies.Load())

	fetch(example, nil)
	assert.Equal(t, int32(5), assemblies.Load(), "the evicted comparison is reassembled on demand")

	fetch(disjoint, nil)
	assert.Equal(t, int32(5), assemblies.Load(), "…and the two most recent are still resident")
}

// TestSquashPeakScalesWithOneLayerNotLayerCount is ARCHITECTURE §4.6's
// "per-layer index being loaded — transient per layer", asserted as a fact
// rather than a table row.
//
// The server used to load every layer index into a slice before squashing, so
// peak memory was Σ over layers: 30 layers × 50k entries measured at 512 MiB
// for a squashed tree holding only 50k paths, and both sides of a comparison
// put it near a gigabyte well below the 500k-file target. Applying and
// dropping each index makes the resident set the tree plus one index, whatever
// the layer count.
//
// The measurement is taken from inside the store, at the moment the LAST layer
// is requested: that is the point of maximum retention, and it is the only
// place a test can observe the squasher's working set. Two layer counts over
// the SAME set of paths isolate the variable — the tree is identical in both
// runs, so any difference in heap is the indexes.
func TestSquashPeakScalesWithOneLayerNotLayerCount(t *testing.T) {
	if raceEnabled {
		t.Skip("heap measurements are not meaningful under the race detector")
	}
	// Deliberately not parallel: the assertion reads a process-wide heap.

	const entriesPerLayer = 50_000
	const few, many = 5, 30

	fewHeap := heapAtLastLayer(t, few, entriesPerLayer)
	manyHeap := heapAtLastLayer(t, many, entriesPerLayer)

	// One index's worth of entries, measured the same way so the budget is
	// in the units the thing under test actually allocates.
	perIndex := heapOfOneIndex(t, entriesPerLayer)

	t.Logf("%d entries/layer: %d layers → %d MiB resident, %d layers → %d MiB resident, one index ≈ %d MiB",
		entriesPerLayer, few, fewHeap>>20, many, manyHeap>>20, perIndex>>20)

	// Six times the layers, at most one more index resident. Retaining
	// them all would cost (many-few) × perIndex ≈ 25 indexes more.
	assert.Less(t, manyHeap, fewHeap+perIndex,
		"squashing %d layers held %d bytes against %d for %d layers: peak is scaling with the layer count",
		many, manyHeap, fewHeap, few)
	assert.Less(t, manyHeap, uint64(perIndex)*4,
		"the resident set must be the tree plus about one layer index")
}

// heapAtLastLayer squashes an image of layerCount layers, each restating the
// same entriesPerLayer paths, and returns the live heap at the moment the last
// index is requested.
func heapAtLastLayer(t *testing.T, layerCount, entriesPerLayer int) uint64 {
	t.Helper()

	store := newSynthStore()
	builders := make([]func() []domain.Entry, 0, layerCount)
	for i := range layerCount {
		builders = append(builders, flatLayer(entriesPerLayer, fmt.Sprintf("v%d", i)))
	}
	left := store.addImage("stack:v1", builders...)
	// The right side is compared at layer point 0 — the legal empty
	// filesystem — so exactly one squash is measured.
	right := store.addImage("stack:empty", flatLayer(1, "empty"))

	var peak uint64
	store.onLoad = func(nth int) {
		if nth != layerCount-1 {
			return
		}
		// At this point layerCount-1 indexes have been applied. Under
		// the old implementation every one of them is still reachable
		// from the slice; under this one they are garbage, and the GC
		// is what turns "unreachable" into "not resident".
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		peak = m.HeapAlloc
	}

	srv := server.New(server.Options{
		Logger: discardLogger(), UI: emptyUI(), Images: store, Layers: store,
	})
	var page server.TreePage
	getJSON(t, srv, treeURL(left, right, url.Values{
		"path":        {"/data"},
		"rightLayers": {"0"},
		"limit":       {"1"},
	}), &page)
	require.Equal(t, entriesPerLayer, page.TotalRows, "the squashed tree really holds every path")
	require.NotZero(t, peak, "the measurement hook never fired")
	return peak
}

// heapOfOneIndex is the live heap cost of a single decoded layer index of the
// same shape, so the budget above is expressed in the units being measured.
func heapOfOneIndex(t *testing.T, entriesPerLayer int) uint64 {
	t.Helper()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	entries := flatLayer(entriesPerLayer, "measure")()
	runtime.GC()
	runtime.ReadMemStats(&after)
	require.Len(t, entries, entriesPerLayer+1)
	runtime.KeepAlive(entries)
	return after.HeapAlloc - before.HeapAlloc
}
