package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/server"
)

// This file drives the golden workflow end to end against the real thing: the
// real flag parsing, the real cache store on a real directory, the real
// fixture ingest, and the real HTTP handlers over a real listener — with no
// network and no Docker daemon involved at any point.
//
// It is the automated form of the acceptance check a reviewer would run with
// curl: list images, ask for the layer graph, then walk the filesystem diff.

func fixturesConfig(t *testing.T, dataDir string) *config {
	t.Helper()
	fixtures, err := filepath.Abs(filepath.Join("..", "..", "fixtures"))
	require.NoError(t, err)
	require.DirExists(t, fixtures)
	return &config{
		listen:        "127.0.0.1:0",
		dataDir:       dataDir,
		cacheMaxBytes: 1 << 30,
		fixturesDir:   fixtures,
		uiDir:         uiDir(t),
	}
}

// waitReady polls /healthz until the background fixture load finishes, which
// is exactly what a systemd ExecStartPost check or a Playwright webServer does.
func waitReady(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/healthz") //nolint:noctx // short-lived test poll
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				require.Equal(t, "ok", string(body))
				return
			}
			require.Equal(t, "loading", string(body),
				"before the fixtures are loaded, healthz must say so")
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("server never became ready")
}

func getAPI(t *testing.T, base, path string, out any) {
	t.Helper()
	resp, err := http.Get(base + path) //nolint:noctx // short-lived test request
	require.NoError(t, err)
	defer func() { require.NoError(t, resp.Body.Close()) }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "GET %s: %s", path, raw)
	require.NoError(t, json.Unmarshal(raw, out), "GET %s: %s", path, raw)
}

// TestGoldenWorkflowEndToEnd is the ARCHITECTURE §1.2 lifecycle, start to
// finish, over HTTP.
func TestGoldenWorkflowEndToEnd(t *testing.T) {
	cfg := fixturesConfig(t, t.TempDir())
	base, stop, errCh := startRun(t, cfg)
	waitReady(t, base)

	// 1. The picker lists the vendored demo images, sourced from fixtures
	//    and pinned so no cache pressure can take them away.
	var list server.ImageList
	getAPI(t, base, server.APIPrefix+"/images", &list)
	require.Len(t, list.Images, 10)

	byRef := map[string]server.ImageSummary{}
	for _, img := range list.Images {
		for _, ref := range img.RefNames {
			byRef[ref] = img
		}
	}
	left, ok := byRef["example:v1"]
	require.True(t, ok, "the golden demo image example:v1 must be loaded at startup")
	right, ok := byRef["example:v2"]
	require.True(t, ok)
	for _, img := range []server.ImageSummary{left, right} {
		assert.Equal(t, "fixture", img.Source)
		assert.True(t, img.Pinned)
		assert.Equal(t, 8, img.LayerCount)
	}

	// 2. The layer graph: a five-layer shared trunk, a fork, and the two
	//    dotted edges — including the one the whole demo exists to explain,
	//    where the npm layers have identical content and different tars.
	var graph server.LayerGraph
	getAPI(t, base, fmt.Sprintf("%s/diff/layers?left=%s&right=%s",
		server.APIPrefix, left.ID, right.ID), &graph)

	assert.Equal(t, 5, graph.TrunkLength)
	require.Len(t, graph.Trunk, 5)
	require.Len(t, graph.LeftBranch, 3)
	require.Len(t, graph.RightBranch, 3)
	for _, layer := range graph.Trunk {
		assert.Equal(t, server.OwnerShared, layer.Owner)
	}

	require.Len(t, graph.CouldBeShared, 2)
	npm := graph.CouldBeShared[0]
	assert.Equal(t, 6, npm.LeftIndex)
	assert.Equal(t, 6, npm.RightIndex)
	assert.False(t, npm.DiffIDEqual,
		"the npm layers have equal changesets and different DiffIDs — not a cache hit")
	assert.Equal(t, "RUN npm install", graph.LeftBranch[1].Instruction)
	assert.NotEqual(t, graph.LeftBranch[1].DiffID, graph.RightBranch[1].DiffID)
	assert.True(t, graph.CouldBeShared[1].DiffIDEqual, "the ffmpeg layers reproduce byte for byte")

	// 3. The filesystem diff at the fork: the .dockerignore mistake, in
	//    full.
	values := url.Values{
		"left": {string(left.ID)}, "right": {string(right.ID)},
		"leftLayers": {"6"}, "rightLayers": {"6"},
		"path": {"/app"}, "filter": {"changed"},
	}
	var page server.TreePage
	getAPI(t, base, server.APIPrefix+"/diff/tree?"+values.Encode(), &page)

	statuses := map[string]string{}
	for _, row := range page.Rows {
		statuses[row.Name] = row.Status
	}
	assert.Equal(t, "added", statuses["debug.log"])
	assert.Equal(t, "added", statuses[".env"])
	assert.Equal(t, "added", statuses[".git"], "a whole .git directory shipped into the image")
	assert.Equal(t, "modified", statuses["main.js"])
	assert.Equal(t, "modified", statuses["src"])
	assert.Equal(t, len(page.Rows), page.TotalRows)
	assert.Positive(t, page.MaxSiblingBytes)

	// 4. Drilling into a folder is the same endpoint rooted deeper.
	values.Set("path", "/app/src")
	var src server.TreePage
	getAPI(t, base, server.APIPrefix+"/diff/tree?"+values.Encode(), &src)
	srcStatuses := map[string]string{}
	for _, row := range src.Rows {
		srcStatuses[row.Name] = row.Status
	}
	assert.Equal(t, "removed", srcStatuses["old-util.js"], "the file v1 shipped and v2 dropped")
	assert.Equal(t, "removed", srcStatuses["legacy"])
	assert.Equal(t, "modified", srcStatuses["util.js"])

	// 5. The apt cleanup whiteouts are visible across the ffmpeg layer.
	whiteouts := url.Values{
		"left": {string(left.ID)}, "right": {string(right.ID)},
		"leftLayers": {"7"}, "rightLayers": {"8"},
		"path": {"/var/lib"}, "filter": {"changed"},
	}
	var varlib server.TreePage
	getAPI(t, base, server.APIPrefix+"/diff/tree?"+whiteouts.Encode(), &varlib)
	removed := map[string]bool{}
	for _, row := range varlib.Rows {
		removed[row.Name] = row.Status == "removed"
	}
	assert.True(t, removed["apt"], "the rm -rf /var/lib/{apt,...} cleanup shows as removed rows")

	stop()
	waitForRun(t, errCh)
}

// TestRestartPreservesAnalyzedImages: the cache is durable, so a restart serves
// the same images without re-analyzing anything.
func TestRestartPreservesAnalyzedImages(t *testing.T) {
	dataDir := t.TempDir()

	first := fixturesConfig(t, dataDir)
	base, stop, errCh := startRun(t, first)
	waitReady(t, base)

	var before server.ImageList
	getAPI(t, base, server.APIPrefix+"/images", &before)
	require.Len(t, before.Images, 10)
	var meta server.MetaResponse
	getAPI(t, base, server.APIPrefix+"/meta", &meta)
	require.Positive(t, meta.CacheBytesUsed)
	// §6.6: the UI reads this list to name the registries a pull may
	// target. An empty list is not "no policy", it is a field the client
	// cannot use — the plumbing has to reach the binary, not just the
	// Options struct.
	assert.NotEmpty(t, meta.AllowedRegistries,
		"the binary must report the registries it will accept, not an empty list")
	assert.Contains(t, meta.AllowedRegistries, "ghcr.io")

	stop()
	waitForRun(t, errCh)

	// The lock is released with the process, so the same directory opens
	// again immediately.
	second := fixturesConfig(t, dataDir)
	base, stop, errCh = startRun(t, second)
	waitReady(t, base)

	var after server.ImageList
	getAPI(t, base, server.APIPrefix+"/images", &after)
	assert.Equal(t, before.Images, after.Images,
		"a restart must serve exactly the images the previous run analyzed")

	var metaAfter server.MetaResponse
	getAPI(t, base, server.APIPrefix+"/meta", &metaAfter)
	assert.Equal(t, meta.CacheBytesUsed, metaAfter.CacheBytesUsed,
		"and must not have grown the store by re-ingesting")

	stop()
	waitForRun(t, errCh)
}

// TestCacheFullRefusesOverBudgetFixtures is UAT §9.5 item 14, automated: with a
// cap too small for the demo images, the server still starts and answers, and
// the ingest is refused rather than thrashing.
func TestCacheFullRefusesOverBudgetFixtures(t *testing.T) {
	cfg := fixturesConfig(t, t.TempDir())
	cfg.cacheMaxBytes = 20 << 10 // enough for a couple of small images, not for all ten

	base, stop, errCh := startRun(t, cfg)
	waitReady(t, base)

	var list server.ImageList
	getAPI(t, base, server.APIPrefix+"/images", &list)
	assert.Less(t, len(list.Images), 10,
		"an over-budget fixture set is refused, not force-fitted")

	var meta server.MetaResponse
	getAPI(t, base, server.APIPrefix+"/meta", &meta)
	assert.LessOrEqual(t, meta.CacheBytesUsed, meta.CacheMaxBytes,
		"the cap is never exceeded")

	stop()
	waitForRun(t, errCh)
}

// TestSecondProcessOnTheSameDataDirFails: exactly one server per cache root.
func TestSecondProcessOnTheSameDataDirFails(t *testing.T) {
	dataDir := t.TempDir()
	cfg := fixturesConfig(t, dataDir)
	base, stop, errCh := startRun(t, cfg)
	waitReady(t, base)

	second := fixturesConfig(t, dataDir)
	second.listen = "127.0.0.1:0"
	err := run(t.Context(), second, discardLogger(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "locked by another layerlens process")

	stop()
	waitForRun(t, errCh)
}
