package server_test

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/domain"
	"github.com/ericsuh/layerlens/internal/server"
)

// TestLayersGoldenPair is the layer half of the demo: five genuinely shared
// layers, a three-layer branch each side, and the two dotted edges that make
// the point of the whole tool.
func TestLayersGoldenPair(t *testing.T) {
	var got server.LayerGraph
	getJSON(t, apiServer(t), layersURL(id(t, "example:v1"), id(t, "example:v2")), &got)

	assert.Equal(t, []string{"example:v1"}, got.Left.RefNames)
	assert.Equal(t, []string{"example:v2"}, got.Right.RefNames)

	require.Equal(t, 5, got.TrunkLength, "the two builds share a five-layer node base")
	require.Len(t, got.Trunk, 5)
	require.Len(t, got.LeftBranch, 3)
	require.Len(t, got.RightBranch, 3)

	for i, layer := range got.Trunk {
		assert.Equal(t, server.OwnerShared, layer.Owner)
		assert.Equal(t, i, layer.Index, "trunk layers keep their absolute positions")
	}
	assert.Equal(t, []string{"COPY . .", "RUN npm install", "RUN apt-get update -y && apt-get install -y ffmpeg && rm -rf /var/lib/{apt,dpkg,cache,log}/"},
		instructions(got.LeftBranch))
	assert.Equal(t, instructions(got.LeftBranch), instructions(got.RightBranch),
		"the two images are two builds of the same Dockerfile")
	for _, layer := range got.LeftBranch {
		assert.Equal(t, server.OwnerLeft, layer.Owner)
		assert.GreaterOrEqual(t, layer.Index, got.TrunkLength)
	}
	for _, layer := range got.RightBranch {
		assert.Equal(t, server.OwnerRight, layer.Owner)
	}

	assert.Equal(t, []server.CouldBeSharedEdge{
		// npm install: same files, different mtimes — the lesson.
		{LeftIndex: 6, RightIndex: 6, DiffIDEqual: false},
		// ffmpeg: unpacked .deb files keep the archive's timestamps, so
		// the tar reproduces byte for byte.
		{LeftIndex: 7, RightIndex: 7, DiffIDEqual: true},
	}, got.CouldBeShared)

	var maximum int64
	for _, layer := range append(append([]server.GraphLayer{}, got.Trunk...), got.RightBranch...) {
		if layer.ContentBytes > maximum {
			maximum = layer.ContentBytes
		}
	}
	assert.Equal(t, maximum, got.MaxLayerBytes, "the size-bar denominator spans both images")
	assert.Positive(t, got.MaxLayerBytes)
}

// TestCouldBeSharedIsNeverPresentedAsACacheHit is the honesty requirement,
// asserted on the wire format itself rather than on a Go struct: the two
// layers linked by a dotted edge are in the branches, owned by one side each,
// and nothing anywhere calls them shared.
func TestCouldBeSharedIsNeverPresentedAsACacheHit(t *testing.T) {
	srv := apiServer(t)
	target := layersURL(id(t, "example:v1"), id(t, "example:v2"))

	var got server.LayerGraph
	getJSON(t, srv, target, &got)
	require.NotEmpty(t, got.CouldBeShared)

	byIndex := map[int]server.GraphLayer{}
	for _, layer := range got.Trunk {
		byIndex[layer.Index] = layer
	}
	for _, edge := range got.CouldBeShared {
		left, right := got.LeftBranch, got.RightBranch
		leftLayer := findLayer(t, left, edge.LeftIndex)
		rightLayer := findLayer(t, right, edge.RightIndex)

		assert.Equal(t, server.OwnerLeft, leftLayer.Owner)
		assert.Equal(t, server.OwnerRight, rightLayer.Owner)
		assert.NotContains(t, byIndex, edge.LeftIndex,
			"a could-be-shared layer is never on the trunk")

		if !edge.DiffIDEqual {
			assert.NotEqual(t, leftLayer.DiffID, rightLayer.DiffID,
				"diffIdEqual:false must mean the DiffIDs really differ")
		} else {
			assert.Equal(t, leftLayer.DiffID, rightLayer.DiffID)
		}
	}

	// The raw payload names the field `couldBeShared` and reserves the word
	// "shared" on its own for the trunk's owner value.
	raw := body(t, doOn(t, srv, http.MethodGet, target))
	assert.Contains(t, raw, `"couldBeShared":`)
	assert.Contains(t, raw, `"diffIdEqual":false`)
	assert.Equal(t, 5, strings.Count(raw, `"owner":"shared"`),
		`only the five trunk layers may be owned "shared"`)
}

// TestLayersDegeneratePairs covers the three shapes that are not a fork: one
// image a strict prefix of the other, no shared layers at all, and an image
// compared with itself.
func TestLayersDegeneratePairs(t *testing.T) {
	srv := apiServer(t)

	t.Run("strict prefix", func(t *testing.T) {
		var got server.LayerGraph
		getJSON(t, srv, layersURL(id(t, "prefix:base"), id(t, "prefix:extended")), &got)

		assert.Equal(t, 3, got.TrunkLength)
		assert.Empty(t, got.LeftBranch, "the base image is entirely trunk")
		assert.Len(t, got.RightBranch, 2)
		assert.Empty(t, got.CouldBeShared, "one empty branch means nothing to connect")
	})

	t.Run("disjoint", func(t *testing.T) {
		var got server.LayerGraph
		getJSON(t, srv, layersURL(id(t, "disjoint:a"), id(t, "disjoint:b")), &got)

		assert.Zero(t, got.TrunkLength)
		assert.Empty(t, got.Trunk)
		assert.Len(t, got.LeftBranch, 2)
		assert.Len(t, got.RightBranch, 3)
	})

	t.Run("identical", func(t *testing.T) {
		self := id(t, "example:v1")
		var got server.LayerGraph
		getJSON(t, srv, layersURL(self, self), &got)

		assert.Equal(t, 8, got.TrunkLength)
		assert.Empty(t, got.LeftBranch)
		assert.Empty(t, got.RightBranch)
		assert.Empty(t, got.CouldBeShared)
	})
}

func TestLayersRejectsUnknownImages(t *testing.T) {
	srv := apiServer(t)
	absent := domain.Digest("sha256:" + strings.Repeat("b", 64))

	got := getError(t, srv, layersURL(absent, id(t, "example:v2")), http.StatusNotFound)
	assert.Equal(t, server.CodeImageNotFound, got.Error.Code)

	got = getError(t, srv, server.APIPrefix+"/diff/layers?right="+string(absent), http.StatusBadRequest)
	assert.Equal(t, server.CodeBadRequest, got.Error.Code)
	assert.Contains(t, got.Error.Message, "left")
}

// TestTreeRootPageShape checks the ordering contract and the aggregates the UI
// renders, at the golden pair's full layer stacks.
func TestTreeRootPageShape(t *testing.T) {
	srv := apiServer(t)
	left, right := id(t, "example:v1"), id(t, "example:v2")

	var page server.TreePage
	getJSON(t, srv, treeURL(left, right, url.Values{"path": {"/app"}}), &page)

	assert.Equal(t, "/app", page.Path)
	assert.Equal(t, "modified", page.PathStatus)
	assert.Equal(t, len(page.Rows), page.TotalRows)
	assert.Empty(t, page.NextCursor, "the whole directory fits in one default page")
	assertRowOrder(t, page.Rows)

	rows := byName(page.Rows)

	// The .dockerignore mistake: a whole .git directory and a debug log
	// that only exist in v2.
	git := rows[".git"]
	assert.Equal(t, "added", git.Status)
	assert.Nil(t, git.Left)
	require.NotNil(t, git.Right)
	assert.Equal(t, "dir", git.Right.Kind)
	assert.Zero(t, git.Agg.LeftBytes)
	assert.Positive(t, git.Agg.RightBytes)
	assert.Equal(t, git.Agg.RightBytes, git.Agg.AddedBytes)
	assert.Equal(t, git.Agg.RightFiles, git.Agg.AddedFiles)
	assert.True(t, git.HasChildren)
	assert.Positive(t, git.ChildCount)

	assert.Equal(t, "added", rows["debug.log"].Status)
	assert.Equal(t, "modified", rows["main.js"].Status)
	assert.False(t, rows["main.js"].HasChildren)
	assert.Equal(t, int64(1), rows["main.js"].Agg.ModifiedFiles)

	// src/ lost three files and had one rewritten.
	src := rows["src"]
	assert.Equal(t, "modified", src.Status)
	assert.Equal(t, int64(3), src.Agg.RemovedFiles)
	assert.Equal(t, int64(1), src.Agg.ModifiedFiles)
	assert.Equal(t, int64(7), src.Agg.LeftFiles)
	assert.Equal(t, int64(4), src.Agg.RightFiles)

	// package.json is in both builds untouched.
	assert.Equal(t, "unchanged", rows["package.json"].Status)

	// The parent aggregate is exactly the sum of the children's — the
	// invariant every size bar depends on.
	var sum server.TreeAgg
	for _, row := range page.Rows {
		sum.LeftBytes += row.Agg.LeftBytes
		sum.RightBytes += row.Agg.RightBytes
		sum.LeftFiles += row.Agg.LeftFiles
		sum.RightFiles += row.Agg.RightFiles
		sum.AddedFiles += row.Agg.AddedFiles
		sum.RemovedFiles += row.Agg.RemovedFiles
		sum.ModifiedFiles += row.Agg.ModifiedFiles
	}
	assert.Equal(t, page.PathAgg.LeftBytes, sum.LeftBytes)
	assert.Equal(t, page.PathAgg.RightBytes, sum.RightBytes)
	assert.Equal(t, page.PathAgg.LeftFiles, sum.LeftFiles)
	assert.Equal(t, page.PathAgg.RightFiles, sum.RightFiles)
	assert.Equal(t, page.PathAgg.ModifiedFiles, sum.ModifiedFiles)

	// The size-bar denominator is the largest sibling, over all siblings.
	var maxSibling int64
	for _, row := range page.Rows {
		if total := row.Agg.LeftBytes + row.Agg.RightBytes; total > maxSibling {
			maxSibling = total
		}
	}
	assert.Equal(t, maxSibling, page.MaxSiblingBytes)
}

// TestTreeWhiteoutsShowAsRemoved: selecting the pre-cleanup layer on the left
// and the post-cleanup layer on the right is how the apt whiteouts become
// visible as removed rows.
func TestTreeWhiteoutsShowAsRemoved(t *testing.T) {
	srv := apiServer(t)
	left, right := id(t, "example:v1"), id(t, "example:v1")

	var page server.TreePage
	getJSON(t, srv, treeURL(left, right, url.Values{
		"path":        {"/var/lib"},
		"leftLayers":  {"7"},
		"rightLayers": {"8"},
		"filter":      {"changed"},
	}), &page)

	rows := byName(page.Rows)
	require.Contains(t, rows, "apt")
	assert.Equal(t, "removed", rows["apt"].Status)
	assert.Nil(t, rows["apt"].Right)
	assert.Positive(t, rows["apt"].Agg.RemovedFiles)
}

// TestTreeSelfDiffAtATrunkPointIsAllUnchanged: picking the same trunk layer on
// both sides is the "nothing has diverged yet" state, and it must render as an
// entirely unchanged tree rather than as an error or an empty one.
func TestTreeSelfDiffAtATrunkPointIsAllUnchanged(t *testing.T) {
	srv := apiServer(t)
	left, right := id(t, "example:v1"), id(t, "example:v2")

	var page server.TreePage
	getJSON(t, srv, treeURL(left, right, url.Values{
		"leftLayers": {"5"}, "rightLayers": {"5"},
	}), &page)

	require.NotEmpty(t, page.Rows)
	assert.Equal(t, "unchanged", page.PathStatus)
	for _, row := range page.Rows {
		assert.Equal(t, "unchanged", row.Status, "%s", row.Path)
		assert.Equal(t, row.Agg.LeftBytes, row.Agg.RightBytes)
		assert.Zero(t, row.Agg.AddedFiles+row.Agg.RemovedFiles+row.Agg.ModifiedFiles)
	}

	var filtered server.TreePage
	getJSON(t, srv, treeURL(left, right, url.Values{
		"leftLayers": {"5"}, "rightLayers": {"5"}, "filter": {"changed"},
	}), &filtered)
	assert.Empty(t, filtered.Rows)
	assert.Zero(t, filtered.TotalRows, "totalRows is post-filter")
	assert.Zero(t, filtered.MaxSiblingBytes)
}

// TestTreeEmptyLayerSelection: leftLayers=0 is the empty filesystem, so every
// row is added.
func TestTreeEmptyLayerSelection(t *testing.T) {
	srv := apiServer(t)
	left, right := id(t, "example:v1"), id(t, "example:v2")

	var page server.TreePage
	getJSON(t, srv, treeURL(left, right, url.Values{"leftLayers": {"0"}}), &page)

	require.NotEmpty(t, page.Rows)
	for _, row := range page.Rows {
		assert.Equal(t, "added", row.Status)
		assert.Nil(t, row.Left)
	}
}

// TestTreePaginationWideDir walks a 2,500-child directory and asserts the three
// properties paging has to have: bounded pages, a cursor chain that terminates,
// and a walk that visits every row exactly once.
func TestTreePaginationWideDir(t *testing.T) {
	srv := apiServer(t)
	left, right := id(t, "wide:v1"), id(t, "wide:v2")

	const limit = 500
	seen := map[string]int{}
	var order []string
	var pages int
	var denominators []int64
	var totalRows int
	cursor := ""

	for {
		values := url.Values{"path": {"/data/shards"}, "limit": {strconv.Itoa(limit)}}
		if cursor != "" {
			values.Set("cursor", cursor)
		}
		var page server.TreePage
		getJSON(t, srv, treeURL(left, right, values), &page)

		pages++
		require.LessOrEqual(t, len(page.Rows), limit, "a page must never exceed its limit")
		require.NotEmpty(t, page.Rows)
		denominators = append(denominators, page.MaxSiblingBytes)
		totalRows = page.TotalRows
		for _, row := range page.Rows {
			seen[row.Name]++
			order = append(order, row.Name)
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
		require.LessOrEqual(t, pages, 10, "the cursor chain must terminate")
	}

	assert.Equal(t, 5, pages, "2500 children at limit 500 is exactly five pages")
	assert.Equal(t, 2500, totalRows)
	assert.Len(t, seen, 2500, "every child is visited")
	assert.Len(t, order, 2500, "and none of them twice")
	for name, count := range seen {
		require.Equal(t, 1, count, "%s appeared %d times", name, count)
	}
	assert.True(t, sortedStrings(order), "the walk follows one deterministic order")
	for _, denominator := range denominators {
		assert.Equal(t, denominators[0], denominator,
			"maxSiblingBytes spans all siblings, so it cannot change between pages")
	}
}

// TestTreeDefaultPageIsBounded is the §6.5 payload budget, measured on the
// widest fixture directory there is.
func TestTreeDefaultPageIsBounded(t *testing.T) {
	srv := apiServer(t)
	target := treeURL(id(t, "wide:v1"), id(t, "wide:v2"), url.Values{"path": {"/data/shards"}})

	raw := body(t, doOn(t, srv, http.MethodGet, target))

	assert.LessOrEqual(t, len(raw), 70*1024,
		"the default page (limit=200, depth=1) must stay inside the ~70 KB budget")
	assert.Greater(t, len(raw), 10*1024, "…while actually carrying a full page of rows")
}

func TestTreeStaleOrForeignCursorIsRejected(t *testing.T) {
	srv := apiServer(t)
	left, right := id(t, "wide:v1"), id(t, "wide:v2")
	base := url.Values{"path": {"/data/shards"}, "limit": {"100"}}

	var page server.TreePage
	getJSON(t, srv, treeURL(left, right, base), &page)
	require.NotEmpty(t, page.NextCursor)

	// A cursor is valid only for the tuple that issued it.
	foreign := []url.Values{
		{"path": {"/data"}, "limit": {"100"}, "cursor": {page.NextCursor}},
		{"path": {"/data/shards"}, "limit": {"100"}, "filter": {"changed"}, "cursor": {page.NextCursor}},
		{"path": {"/data/shards"}, "limit": {"100"}, "leftLayers": {"0"}, "cursor": {page.NextCursor}},
		{"path": {"/data/shards"}, "limit": {"100"}, "cursor": {"not-base64!!"}},
		{"path": {"/data/shards"}, "limit": {"100"}, "cursor": {"eyJzIjoibm9wZSIsIm4iOiJ4IiwicSI6InkifQ"}},
	}
	for i, values := range foreign {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			got := getError(t, srv, treeURL(left, right, values), http.StatusBadRequest)
			assert.Equal(t, server.CodeBadRequest, got.Error.Code)
			assert.Contains(t, got.Error.Message, "cursor")
		})
	}

	// The same cursor against the query that issued it still works, so the
	// rejections above are about the binding and not about the encoding.
	base.Set("cursor", page.NextCursor)
	var second server.TreePage
	getJSON(t, srv, treeURL(left, right, base), &second)
	assert.Len(t, second.Rows, 100)
}

// TestTreeDepth2Truncation: depth=2 embeds grandchildren, capped at `limit`,
// and says so.
func TestTreeDepth2Truncation(t *testing.T) {
	srv := apiServer(t)
	left, right := id(t, "wide:v1"), id(t, "wide:v2")

	var page server.TreePage
	getJSON(t, srv, treeURL(left, right, url.Values{
		"path": {"/data"}, "depth": {"2"}, "limit": {"7"},
	}), &page)

	require.Len(t, page.Rows, 1)
	shards := page.Rows[0]
	assert.Equal(t, "/data/shards", shards.Path)
	assert.Equal(t, 2500, shards.ChildCount, "the count is the whole directory…")
	assert.Len(t, shards.Children, 7, "…while the embedded page respects the limit")
	assert.True(t, shards.ChildrenTruncated)
	assertRowOrder(t, shards.Children)
	for _, grandchild := range shards.Children {
		assert.True(t, strings.HasPrefix(grandchild.Path, "/data/shards/"))
		assert.Empty(t, grandchild.Children, "depth=2 means two levels, not more")
	}

	// depth=1 (the default) carries no children at all.
	var shallow server.TreePage
	getJSON(t, srv, treeURL(left, right, url.Values{"path": {"/data"}}), &shallow)
	assert.Empty(t, shallow.Rows[0].Children)
	assert.False(t, shallow.Rows[0].ChildrenTruncated)
}

// TestTreeFilterChangedPrunesUnchangedSubtrees also pins the §6.5 subtlety that
// childCount is post-filter: it is what the client's "N of M" chip and
// aria-setsize are computed from.
func TestTreeFilterChangedPrunesUnchangedSubtrees(t *testing.T) {
	srv := apiServer(t)
	left, right := id(t, "example:v1"), id(t, "example:v2")
	values := url.Values{"path": {"/app"}}

	var all server.TreePage
	getJSON(t, srv, treeURL(left, right, values), &all)

	values.Set("filter", "changed")
	var changed server.TreePage
	getJSON(t, srv, treeURL(left, right, values), &changed)

	assert.Less(t, changed.TotalRows, all.TotalRows)
	assert.Equal(t, len(changed.Rows), changed.TotalRows)
	for _, row := range changed.Rows {
		assert.NotEqual(t, "unchanged", row.Status)
	}
	assert.Equal(t, []string{".git", "src", ".env", "debug.log", "main.js"}, names(changed.Rows))

	srcAll := byName(all.Rows)["src"]
	srcChanged := byName(changed.Rows)["src"]
	assert.Greater(t, srcAll.ChildCount, srcChanged.ChildCount,
		"childCount is post-filter, so it shrinks with the filter")
}

func TestTreeParameterValidation(t *testing.T) {
	srv := apiServer(t)
	left, right := id(t, "example:v1"), id(t, "example:v2")

	cases := map[string]url.Values{
		"layer count above the stack": {"leftLayers": {"9"}},
		"negative layer count":        {"rightLayers": {"-1"}},
		"non-numeric layer count":     {"leftLayers": {"all"}},
		"limit over the maximum":      {"limit": {"1001"}},
		"zero limit":                  {"limit": {"0"}},
		"non-numeric limit":           {"limit": {"lots"}},
		"depth zero":                  {"depth": {"0"}},
		"depth over the maximum":      {"depth": {"3"}},
		"relative path":               {"path": {"app"}},
		"unclean path":                {"path": {"/app/../app"}},
		"trailing slash path":         {"path": {"/app/"}},
		"dot path":                    {"path": {"/app/."}},
		"unknown filter":              {"filter": {"added"}},
	}
	for name, values := range cases {
		t.Run(name, func(t *testing.T) {
			got := getError(t, srv, treeURL(left, right, values), http.StatusBadRequest)
			assert.Equal(t, server.CodeBadRequest, got.Error.Code)
		})
	}

	// A well-formed path that simply is not in this comparison is also a
	// bad request rather than a 404: the images exist, the parameter does
	// not describe anything in them.
	got := getError(t, srv, treeURL(left, right, url.Values{"path": {"/nope/missing"}}), http.StatusBadRequest)
	assert.Contains(t, got.Error.Message, "/nope/missing")
}

// ---------------------------------------------------------------- helpers

func instructions(layers []server.GraphLayer) []string {
	out := make([]string, 0, len(layers))
	for _, layer := range layers {
		out = append(out, layer.Instruction)
	}
	return out
}

func findLayer(t *testing.T, layers []server.GraphLayer, index int) server.GraphLayer {
	t.Helper()
	for _, layer := range layers {
		if layer.Index == index {
			return layer
		}
	}
	t.Fatalf("no layer with index %d", index)
	return server.GraphLayer{}
}

func byName(rows []server.TreeRow) map[string]server.TreeRow {
	out := make(map[string]server.TreeRow, len(rows))
	for _, row := range rows {
		out[row.Name] = row
	}
	return out
}

func names(rows []server.TreeRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Name)
	}
	return out
}

// assertRowOrder checks the §6.5 ordering the cursor depends on: directories
// first, then everything else, each group name-ascending.
func assertRowOrder(t *testing.T, rows []server.TreeRow) {
	t.Helper()
	seenNonDir := false
	prev := ""
	for _, row := range rows {
		isDir := row.HasChildren || rowKind(row) == "dir"
		if !isDir {
			if !seenNonDir {
				seenNonDir, prev = true, ""
			}
		} else {
			require.False(t, seenNonDir, "directory %q sorted after a non-directory", row.Name)
		}
		require.Less(t, prev, row.Name, "%q is out of name order", row.Name)
		prev = row.Name
	}
}

func rowKind(row server.TreeRow) string {
	if row.Right != nil {
		return row.Right.Kind
	}
	if row.Left != nil {
		return row.Left.Kind
	}
	return ""
}

func sortedStrings(values []string) bool {
	for i := 1; i < len(values); i++ {
		if values[i-1] >= values[i] {
			return false
		}
	}
	return true
}
