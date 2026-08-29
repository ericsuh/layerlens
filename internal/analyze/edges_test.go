package analyze_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/analyze"
	"github.com/ericsuh/layerlens/internal/analyze/tartest"
	"github.com/ericsuh/layerlens/internal/domain"
)

// tarLayer runs a tar through the indexer and returns the Layer record ingest
// would have stored for it, so the edge tests reason about real digests
// instead of hand-written ones.
func tarLayer(t *testing.T, index int, b *tartest.Builder) domain.Layer {
	t.Helper()
	raw := b.Bytes()
	idx := indexTar(t, raw)
	return domain.Layer{
		Index:           index,
		DiffID:          idx.DiffID,
		ContentBytes:    idx.ContentBytes,
		EntryCount:      len(idx.Entries),
		ChangesetDigest: idx.ChangesetDigest,
	}
}

func TestCouldBeSharedEdges(t *testing.T) {
	t.Parallel()

	t.Run("equal_changeset_digests_post_fork_get_edge", func(t *testing.T) {
		t.Parallel()
		a := stack("base", "leftA", "common")
		b := stack("base", "rightA", "rightB", "commonRebuilt")
		// Give the two "common" layers equal changesets but different
		// DiffIDs, the usual outcome of two identical build steps.
		b[3].ChangesetDigest = a[2].ChangesetDigest

		edges := analyze.CouldBeSharedEdges(a, b, analyze.TrunkLCP(a, b))
		require.Len(t, edges, 1)
		assert.Equal(t, 2, edges[0].LeftIndex, "absolute layer index, not a branch offset")
		assert.Equal(t, 3, edges[0].RightIndex)
		assert.False(t, edges[0].DiffIDEqual)
	})

	t.Run("mtime_differing_layers_edge", func(t *testing.T) {
		t.Parallel()
		// The npm-install demo: the same files installed twice, minutes
		// apart. Different tar bytes (so different DiffIDs, so no cache
		// hit) but identical under Docker's build-cache rule.
		build := func(mtime time.Time) *tartest.Builder {
			return tartest.New().
				Dir("app/node_modules", tartest.Mtime(mtime)).
				File("app/node_modules/left-pad.js", "module.exports = 1", tartest.Mtime(mtime)).
				File("app/package-lock.json", "{}", tartest.Mtime(mtime))
		}
		left := tarLayer(t, 1, build(time.Unix(1700000000, 0)))
		right := tarLayer(t, 1, build(time.Unix(1700009999, 0)))

		require.NotEqual(t, left.DiffID, right.DiffID, "mtimes must change the tar bytes")
		require.Equal(t, left.ChangesetDigest, right.ChangesetDigest)

		base := stack("base")[0]
		edges := analyze.CouldBeSharedEdges(
			[]domain.Layer{base, left}, []domain.Layer{base, right}, 1)
		require.Len(t, edges, 1)
		assert.Equal(t, analyze.CouldBeSharedEdge{
			LeftIndex: 1, RightIndex: 1, DiffIDEqual: false,
			ChangesetDigest: left.ChangesetDigest,
		}, edges[0])
	})

	t.Run("diffIdEqual_flag_set_when_tar_identical", func(t *testing.T) {
		t.Parallel()
		// The very same layer tar, at a different position in each
		// stack: byte-identical, and still not a cache hit, because the
		// layer store keys on the whole chain below it.
		same := tartest.New().File("opt/tool", "identical bytes")
		left := tarLayer(t, 1, same)
		right := tarLayer(t, 2, same)

		a := []domain.Layer{stack("baseL")[0], left}
		b := []domain.Layer{stack("baseR")[0], stack("extra")[0], right}
		edges := analyze.CouldBeSharedEdges(a, b, analyze.TrunkLCP(a, b))
		require.Len(t, edges, 1)
		assert.True(t, edges[0].DiffIDEqual)
		assert.Equal(t, 1, edges[0].LeftIndex)
		assert.Equal(t, 2, edges[0].RightIndex)
	})

	t.Run("mode_only_change_no_edge", func(t *testing.T) {
		t.Parallel()
		// Mode IS in the tarsum-v1 field set, so a chmod is a real
		// difference and must not produce an edge.
		left := tarLayer(t, 1, tartest.New().File("app/run.sh", "#!/bin/sh", tartest.Mode(0o644)))
		right := tarLayer(t, 1, tartest.New().File("app/run.sh", "#!/bin/sh", tartest.Mode(0o755)))
		require.NotEqual(t, left.ChangesetDigest, right.ChangesetDigest)

		base := stack("base")[0]
		assert.Empty(t, analyze.CouldBeSharedEdges(
			[]domain.Layer{base, left}, []domain.Layer{base, right}, 1))
	})

	t.Run("empty_changesets_excluded", func(t *testing.T) {
		t.Parallel()
		// Two no-op layers have the same (empty) changeset digest. Left
		// unchecked, every metadata-only instruction would be linked to
		// every other one.
		empty := tarLayer(t, 1, tartest.New())
		require.Zero(t, empty.EntryCount)

		a := []domain.Layer{stack("baseL")[0], empty}
		b := []domain.Layer{stack("baseR")[0], empty}
		require.Equal(t, a[1].ChangesetDigest, b[1].ChangesetDigest)
		assert.Empty(t, analyze.CouldBeSharedEdges(a, b, 0))
	})

	t.Run("trunk_layers_never_in_edges", func(t *testing.T) {
		t.Parallel()
		// The trunk layers are genuinely shared; a "could be shared"
		// edge over them would be noise, and only layers at index >= k
		// are considered.
		a := stack("base", "deps", "appA")
		b := stack("base", "deps", "appB")
		b[2].ChangesetDigest = a[2].ChangesetDigest

		edges := analyze.CouldBeSharedEdges(a, b, 2)
		require.Len(t, edges, 1)
		assert.Equal(t, 2, edges[0].LeftIndex)
		assert.Equal(t, 2, edges[0].RightIndex)

		// With k = 0 the trunk layers become eligible and their own
		// (equal) digests match — proof that k is what excludes them.
		assert.Len(t, analyze.CouldBeSharedEdges(a, b, 0), 3)
	})

	t.Run("m_x_n_duplicate_layers_all_pairs", func(t *testing.T) {
		t.Parallel()
		// A digest that genuinely repeats within an image yields every
		// pair, deterministically ordered by right index then left.
		a := stack("base", "dup1", "dup2", "other")
		b := stack("base", "dupA", "solo", "dupB")
		shared := tartest.SHA256("shared changeset")
		a[1].ChangesetDigest, a[2].ChangesetDigest = shared, shared
		b[1].ChangesetDigest, b[3].ChangesetDigest = shared, shared

		edges := analyze.CouldBeSharedEdges(a, b, 1)
		require.Len(t, edges, 4)
		assert.Equal(t, []analyze.CouldBeSharedEdge{
			{LeftIndex: 1, RightIndex: 1, ChangesetDigest: shared},
			{LeftIndex: 2, RightIndex: 1, ChangesetDigest: shared},
			{LeftIndex: 1, RightIndex: 3, ChangesetDigest: shared},
			{LeftIndex: 2, RightIndex: 3, ChangesetDigest: shared},
		}, edges)
	})

	t.Run("identical_images_have_no_branches_and_no_edges", func(t *testing.T) {
		t.Parallel()
		a := stack("base", "deps", "app")
		assert.Empty(t, analyze.CouldBeSharedEdges(a, a, analyze.TrunkLCP(a, a)))
	})

	t.Run("strict_prefix_has_no_edges", func(t *testing.T) {
		t.Parallel()
		a := stack("base", "deps")
		b := stack("base", "deps", "app")
		assert.Empty(t, analyze.CouldBeSharedEdges(a, b, analyze.TrunkLCP(a, b)),
			"one branch is empty, so there is nothing to connect")
	})

	t.Run("no_matches_yields_no_edges", func(t *testing.T) {
		t.Parallel()
		a := stack("base", "appA")
		b := stack("base", "appB")
		assert.Empty(t, analyze.CouldBeSharedEdges(a, b, 1))
	})

	t.Run("malformed_changeset_digest_is_ineligible", func(t *testing.T) {
		t.Parallel()
		a := stack("base", "appA")
		b := stack("base", "appB")
		a[1].ChangesetDigest = ""
		b[1].ChangesetDigest = ""
		assert.Empty(t, analyze.CouldBeSharedEdges(a, b, 1))
	})

	t.Run("negative_k_is_clamped", func(t *testing.T) {
		t.Parallel()
		a := stack("x")
		b := stack("x")
		assert.NotPanics(t, func() { analyze.CouldBeSharedEdges(a, b, -3) })
	})
}
