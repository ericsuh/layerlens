package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
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
