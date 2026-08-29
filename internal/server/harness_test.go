package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/cachestore"
	"github.com/ericsuh/layerlens/internal/domain"
	"github.com/ericsuh/layerlens/internal/ingest"
	"github.com/ericsuh/layerlens/internal/server"
	"github.com/ericsuh/layerlens/internal/webui"
)

// The API tests run against a real cache store seeded from the real vendored
// fixtures, not against a mock. The endpoints under test are mostly a
// projection of what ingestion produced, so a hand-built store would let the
// wire format drift away from the data the server actually serves.
//
// Seeding costs about a fifth of a second, so it happens once for the whole
// package and every test reads the same store.

type fixtureCache struct {
	store *cachestore.Store
	root  string
	byRef map[string]domain.Digest
}

var (
	fixtureOnce sync.Once
	fixtures    *fixtureCache
	fixtureErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if fixtures != nil {
		_ = fixtures.store.Close()
		_ = os.RemoveAll(fixtures.root)
	}
	os.Exit(code)
}

func seeded(t *testing.T) *fixtureCache {
	t.Helper()
	fixtureOnce.Do(func() {
		root, err := os.MkdirTemp("", "layerlens-server-test-")
		if err != nil {
			fixtureErr = err
			return
		}
		store, err := cachestore.Open(cachestore.Options{
			Root: root, MaxBytes: 1 << 30, Logger: discardLogger(),
		})
		if err != nil {
			fixtureErr = err
			return
		}
		dir, err := filepath.Abs(filepath.Join("..", "..", "fixtures"))
		if err != nil {
			fixtureErr = err
			return
		}
		ing := ingest.New(store, ingest.Options{Logger: discardLogger()})
		res, err := ing.LoadFixtures(context.Background(), dir)
		if err != nil {
			fixtureErr = err
			return
		}
		byRef := map[string]domain.Digest{}
		for i := range res.Images {
			for _, ref := range res.Images[i].RefNames {
				byRef[ref] = res.Images[i].ID
			}
		}
		fixtures = &fixtureCache{store: store, root: root, byRef: byRef}
	})
	require.NoError(t, fixtureErr)
	require.NotNil(t, fixtures)
	return fixtures
}

// id resolves a fixture display reference to its image id.
func id(t *testing.T, ref string) domain.Digest {
	t.Helper()
	got, ok := seeded(t).byRef[ref]
	require.True(t, ok, "no fixture image tagged %s", ref)
	return got
}

// apiServer builds a Server backed by the seeded fixture cache.
func apiServer(t *testing.T, customize ...func(*server.Options)) *server.Server {
	t.Helper()
	cache := seeded(t)
	opts := server.Options{
		Logger:  discardLogger(),
		UI:      webui.HandlerFS(fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(indexHTML)}}),
		Images:  cache.store,
		Layers:  cache.store,
		Cache:   cache.store,
		Version: "test",
	}
	for _, fn := range customize {
		fn(&opts)
	}
	return server.New(opts)
}

// getJSON issues a GET and decodes a 200 body into out.
func getJSON(t *testing.T, h http.Handler, target string, out any) {
	t.Helper()
	resp := doOn(t, h, http.MethodGet, target)
	raw := body(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode, "GET %s: %s", target, raw)
	require.Equal(t, "application/json; charset=utf-8", resp.Header.Get("Content-Type"))
	require.NoError(t, json.Unmarshal([]byte(raw), out), "GET %s: %s", target, raw)
}

// getError issues a GET expecting a §6.1 error envelope with the given status.
func getError(t *testing.T, h http.Handler, target string, status int) server.APIError {
	t.Helper()
	resp := doOn(t, h, http.MethodGet, target)
	raw := body(t, resp)
	require.Equal(t, status, resp.StatusCode, "GET %s: %s", target, raw)
	require.Equal(t, "application/json; charset=utf-8", resp.Header.Get("Content-Type"),
		"every failure inside /api must be a JSON envelope, never HTML")
	var got server.APIError
	require.NoError(t, json.Unmarshal([]byte(raw), &got), "GET %s: %s", target, raw)
	require.NotEmpty(t, got.Error.Code)
	require.NotEmpty(t, got.Error.Message)
	return got
}

// treeURL builds a /diff/tree request with the given extra parameters.
func treeURL(left, right domain.Digest, extra url.Values) string {
	values := url.Values{"left": {string(left)}, "right": {string(right)}}
	for k, v := range extra {
		values[k] = v
	}
	return server.APIPrefix + "/diff/tree?" + values.Encode()
}

func layersURL(left, right domain.Digest) string {
	return fmt.Sprintf("%s/diff/layers?left=%s&right=%s", server.APIPrefix, left, right)
}
