package server

import (
	"context"
	"fmt"
	"sync"

	"github.com/ericsuh/layerlens/internal/analyze"
	"github.com/ericsuh/layerlens/internal/domain"
)

// DefaultComparisonCacheSize is the number of assembled comparisons kept in
// memory. Two is the ARCHITECTURE §4.6 budget: a merged diff tree for a pair of
// 500k-file images is a few hundred megabytes, so this cap is the difference
// between a bounded process and an unbounded one.
const DefaultComparisonCacheSize = 2

// comparisonKey identifies one assembled comparison: a pair of images at a
// pair of layer points. Everything the tree endpoint serves is a projection of
// the value behind this key, which is why paging, filtering and drill-down all
// hit the cache instead of re-squashing.
type comparisonKey struct {
	left        domain.Digest
	right       domain.Digest
	leftLayers  int
	rightLayers int
}

func (k comparisonKey) String() string {
	return fmt.Sprintf("%s@%d|%s@%d", k.left, k.leftLayers, k.right, k.rightLayers)
}

// comparison is the unified diff tree for one key.
type comparison struct {
	key  comparisonKey
	root *domain.DiffNode
}

// comparisonCache is a tiny LRU with single-flight assembly.
//
// The single flight is what keeps a burst of expands cheap: opening a
// directory in the UI can fire several requests for the same (pair, selection)
// at once, and without it each would squash both sides independently — the
// most expensive thing the server does, duplicated N times for one unit of
// useful work.
type comparisonCache struct {
	mu       sync.Mutex
	capacity int
	// entries is most-recently-used first.
	entries  []*comparison
	inflight map[comparisonKey]*assembly
	// onAssembled counts real assemblies for tests.
	onAssembled func()
}

// assembly is one in-flight assemble call other requests can wait on.
type assembly struct {
	done chan struct{}
	cmp  *comparison
	err  error
}

func newComparisonCache(capacity int, onAssembled func()) *comparisonCache {
	return &comparisonCache{
		capacity:    capacity,
		inflight:    map[comparisonKey]*assembly{},
		onAssembled: onAssembled,
	}
}

// get returns the comparison for key, assembling it at most once even under
// concurrent callers.
func (c *comparisonCache) get(ctx context.Context, key comparisonKey,
	assemble func(context.Context) (*comparison, error),
) (*comparison, error) {
	c.mu.Lock()
	if cmp := c.lookupLocked(key); cmp != nil {
		c.mu.Unlock()
		return cmp, nil
	}
	if inflight, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		select {
		case <-inflight.done:
			return inflight.cmp, inflight.err
		case <-ctx.Done():
			// Only this caller gives up: the assembly it was
			// waiting on runs on a context of its own (see the
			// WithoutCancel below), so the other waiters are
			// unaffected.
			return nil, ctx.Err()
		}
	}
	leader := &assembly{done: make(chan struct{})}
	c.inflight[key] = leader
	c.mu.Unlock()

	if c.onAssembled != nil {
		c.onAssembled()
	}
	// Detached deliberately, and here rather than at the call site: one
	// assembly serves every waiter on the same key, so the client that
	// happened to arrive first must not be able to cancel work the others
	// are blocked on by navigating away. Leaving that to the caller made
	// the comment above true only by luck. The work is bounded (one
	// comparison) and its result is cached for whoever asks next; values
	// on the context — deadlines aside — still reach the assembler.
	cmp, err := assemble(context.WithoutCancel(ctx))

	c.mu.Lock()
	delete(c.inflight, key)
	if err == nil {
		c.insertLocked(cmp)
	}
	c.mu.Unlock()

	// Published before the close, so a waiter that observes done also
	// observes these fields.
	leader.cmp, leader.err = cmp, err
	close(leader.done)
	return cmp, err
}

// lookupLocked returns a cached comparison and promotes it to most-recent.
func (c *comparisonCache) lookupLocked(key comparisonKey) *comparison {
	for i, e := range c.entries {
		if e.key != key {
			continue
		}
		copy(c.entries[1:i+1], c.entries[:i])
		c.entries[0] = e
		return e
	}
	return nil
}

func (c *comparisonCache) insertLocked(cmp *comparison) {
	if cmp == nil {
		return
	}
	if existing := c.lookupLocked(cmp.key); existing != nil {
		return
	}
	c.entries = append([]*comparison{cmp}, c.entries...)
	if len(c.entries) > c.capacity {
		// Dropping the tail is what bounds resident memory; the
		// evicted tree becomes garbage as soon as no request holds it.
		c.entries = c.entries[:c.capacity]
	}
}

// assembleComparison squashes both sides to their selected layer points and
// diffs them.
//
// The two cumulative trees exist only inside this function: analyze.Diff
// copies every per-side value it needs into the merged tree, so both side
// trees are garbage the moment it returns. That is what keeps the §4.6 peak
// transient rather than resident.
func (s *Server) assembleComparison(ctx context.Context, left, right *domain.ImageRecord,
	leftLayers, rightLayers int,
) (*comparison, error) {
	leftTree, err := s.squash(ctx, left, leftLayers)
	if err != nil {
		return nil, err
	}
	rightTree, err := s.squash(ctx, right, rightLayers)
	if err != nil {
		return nil, err
	}
	root := analyze.Diff(leftTree, rightTree)
	return &comparison{
		key: comparisonKey{
			left: left.ID, right: right.ID,
			leftLayers: leftLayers, rightLayers: rightLayers,
		},
		root: root,
	}, nil
}

// squash folds an image's first n layers into a cumulative filesystem tree,
// applying each layer index and dropping it before the next is loaded.
//
// That is the difference between §4.6's budget and the sum over layers:
// analyze.Squash's slice parameter would hold all n indexes resident at once,
// so a 30-layer image peaked at 30 layer indexes plus the tree. Here the
// resident set is the cumulative tree plus exactly one index, whatever n is.
func (s *Server) squash(ctx context.Context, rec *domain.ImageRecord, n int) (*domain.Node, error) {
	sq := analyze.NewSquasher()
	for i := 0; i < n; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		idx, err := s.layers.LayerIndex(ctx, rec.Layers[i].DiffID)
		if err != nil {
			// Named with the image this layer belongs to: an
			// evicted-layer 404 has to tell the client WHICH of
			// the two images to refetch, and both sides run
			// through here.
			return nil, &imageError{
				id:  rec.ID,
				err: fmt.Errorf("load layer %d of %s: %w", i, rec.ID, err),
			}
		}
		// idx dies here: it is scoped to this iteration and the tree
		// copied everything it keeps, so the next load reuses this
		// one's memory instead of adding to it.
		sq.Apply(idx.Entries)
	}
	return sq.Tree(), nil
}

// imageError attributes a failure to one of the two images in a comparison, so
// the error envelope can name the image the client must refetch rather than
// always naming the left one.
type imageError struct {
	id  domain.Digest
	err error
}

func (e *imageError) Error() string { return e.err.Error() }
func (e *imageError) Unwrap() error { return e.err }
