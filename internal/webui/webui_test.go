package webui_test

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/webui"
)

const indexHTML = `<!doctype html><div id="root"></div>`

func assets() fstest.MapFS {
	return fstest.MapFS{
		"index.html":     &fstest.MapFile{Data: []byte(indexHTML)},
		"app.js":         &fstest.MapFile{Data: []byte("main()\n")},
		"assets/app.css": &fstest.MapFile{Data: []byte(":root{}\n")},
	}
}

func do(t *testing.T, h http.Handler, method, path string, header http.Header) (*http.Response, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	resp := rec.Result()
	defer func() { require.NoError(t, resp.Body.Close()) }()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, string(b)
}

func get(t *testing.T, h http.Handler, path string) (*http.Response, string) {
	t.Helper()
	return do(t, h, http.MethodGet, path, nil)
}

func TestHandlerFS(t *testing.T) {
	h := webui.HandlerFS(assets())

	tests := []struct {
		name     string
		path     string
		wantBody string
	}{
		{name: "root falls back to the shell", path: "/", wantBody: indexHTML},
		{name: "client route falls back to the shell", path: "/compare", wantBody: indexHTML},
		{name: "nested asset is served", path: "/assets/app.css", wantBody: ":root{}\n"},
		{name: "top-level asset is served", path: "/app.js", wantBody: "main()\n"},
		{name: "traversal attempt falls back to the shell", path: "/../../etc/passwd", wantBody: indexHTML},
		{name: "directory falls back to the shell", path: "/assets", wantBody: indexHTML},
		// A tag looks like an extension; a client route carrying one must not
		// be mistaken for a missing asset.
		{name: "client route with a dotted image tag falls back to the shell", path: "/images/nginx:1.27", wantBody: indexHTML},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := get(t, h, tc.path)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, tc.wantBody, body)
		})
	}
}

// TestHandlerFSMissingAssetIsNotTheShell is the regression that matters most:
// serving index.html for /app.js makes a browser parse HTML as JavaScript, so
// a renamed-bundle regression looks like a blank page instead of a 404.
func TestHandlerFSMissingAssetIsNotTheShell(t *testing.T) {
	h := webui.HandlerFS(assets())

	for _, path := range []string{"/nonexistent.js", "/app.css", "/assets/missing", "/vendor.js.map"} {
		t.Run(path, func(t *testing.T) {
			resp, body := get(t, h, path)
			assert.Equal(t, http.StatusNotFound, resp.StatusCode)
			assert.NotContains(t, body, "<div id=\"root\">")
		})
	}
}

func TestHandlerFSRejectsNonReadMethods(t *testing.T) {
	h := webui.HandlerFS(assets())

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			for _, path := range []string{"/", "/app.js", "/compare"} {
				resp, _ := do(t, h, method, path, nil)
				assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode, path)
				assert.Equal(t, "GET, HEAD", resp.Header.Get("Allow"), path)
			}
		})
	}
}

func TestHandlerFSHead(t *testing.T) {
	h := webui.HandlerFS(assets())

	resp, body := do(t, h, http.MethodHead, "/app.js", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, body)
}

func TestHandlerFSSetsNosniff(t *testing.T) {
	h := webui.HandlerFS(assets())

	for _, path := range []string{"/", "/app.js", "/missing.js"} {
		resp, _ := get(t, h, path)
		assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"), path)
	}
}

func TestHandlerFSShellCarriesCSP(t *testing.T) {
	h := webui.HandlerFS(assets())

	resp, _ := get(t, h, "/")
	csp := resp.Header.Get("Content-Security-Policy")
	assert.Contains(t, csp, "default-src 'none'")
	assert.Contains(t, csp, "script-src 'self'")
	assert.Contains(t, csp, "frame-ancestors 'none'")
	// Style *attributes* are allowed (Radix's portalled overlays and the layer
	// diagram's measured positioning need them, see webui.go), but nothing
	// else is: script must stay 'self', and style elements/stylesheets are
	// still governed by the unmodified `style-src 'self'`.
	assert.Contains(t, csp, "style-src 'self'")
	assert.Contains(t, csp, "style-src-attr 'unsafe-inline'")
	assert.NotContains(t, csp, "script-src 'self' 'unsafe-inline'")
	assert.NotContains(t, csp, "style-src 'self' 'unsafe-inline'")
	assert.NotContains(t, csp, "style-src-elem")
	assert.NotContains(t, csp, "unsafe-eval")
	// Nothing in the bundle is loaded cross-origin, so no external origin
	// may appear.
	assert.NotContains(t, csp, "http")
	assert.Equal(t, "no-cache", resp.Header.Get("Cache-Control"))
}

func TestHandlerFSWithoutIndexFails(t *testing.T) {
	h := webui.HandlerFS(fstest.MapFS{})
	resp, _ := get(t, h, "/compare")
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestHandlerFSConditionalRequest(t *testing.T) {
	h := webui.HandlerFS(assets())

	first, body := get(t, h, "/app.js")
	require.Equal(t, http.StatusOK, first.StatusCode)
	assert.Equal(t, "main()\n", body)

	etag := first.Header.Get("ETag")
	require.NotEmpty(t, etag, "assets need a validator or a reload can never 304")
	assert.NotEmpty(t, first.Header.Get("Cache-Control"))

	second, secondBody := do(t, h, http.MethodGet, "/app.js", http.Header{"If-None-Match": {etag}})
	assert.Equal(t, http.StatusNotModified, second.StatusCode)
	assert.Empty(t, secondBody)

	stale, staleBody := do(t, h, http.MethodGet, "/app.js", http.Header{"If-None-Match": {`"deadbeef"`}})
	assert.Equal(t, http.StatusOK, stale.StatusCode)
	assert.Equal(t, "main()\n", staleBody)
}

// TestHandlerFSETagTracksContent proves the validator is derived from the
// bytes, so a rebuilt --ui-dir bundle is never served from a stale cache.
func TestHandlerFSETagTracksContent(t *testing.T) {
	before := assets()
	after := assets()
	after["app.js"] = &fstest.MapFile{Data: []byte("main(2)\n")}

	first, _ := get(t, webui.HandlerFS(before), "/app.js")
	second, _ := get(t, webui.HandlerFS(after), "/app.js")

	assert.NotEqual(t, first.Header.Get("ETag"), second.Header.Get("ETag"))
}

// requireBuiltBundle skips when internal/webui/dist holds only .gitkeep, which
// is the state of a clean checkout before `mise run build-web`. The mise
// test-go task depends on build-web, so the CI-equivalent run never skips.
func requireBuiltBundle(t *testing.T) {
	t.Helper()
	if _, err := fs.Stat(webui.FS(), "index.html"); err != nil {
		t.Skip("SPA bundle is not built; run `mise run build-web`")
	}
}

// TestEmbeddedFS proves the go:embed directive resolves and that the tree it
// exposes is the build output, not an empty directory.
func TestEmbeddedFS(t *testing.T) {
	entries, err := fs.ReadDir(webui.FS(), ".")
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	// .gitkeep is the one file guaranteed in a clean checkout; it is what makes
	// the embed directive resolve before anything has been built.
	assert.Contains(t, names, ".gitkeep")

	requireBuiltBundle(t)
	assert.Contains(t, names, "index.html")
	assert.Contains(t, names, "app.js")
	assert.Contains(t, names, "app.css")
	// The production build must not ship the 1.8 MB source map.
	assert.NotContains(t, names, "app.js.map")
}

// TestHandler exercises the real embedded handler, not a fstest.MapFS stand-in.
func TestHandler(t *testing.T) {
	requireBuiltBundle(t)
	h := webui.Handler()

	t.Run("shell is served for a client route", func(t *testing.T) {
		resp, body := get(t, h, "/compare")
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "text/html; charset=utf-8", resp.Header.Get("Content-Type"))
		assert.Contains(t, body, `id="root"`)
		assert.Contains(t, body, "/app.js")
		assert.NotEmpty(t, resp.Header.Get("Content-Security-Policy"))
	})

	t.Run("the bundle is served as JavaScript", func(t *testing.T) {
		resp, body := get(t, h, "/app.js")
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, resp.Header.Get("Content-Type"), "javascript")
		assert.NotEmpty(t, body)
	})

	t.Run("the stylesheet is served as CSS", func(t *testing.T) {
		resp, _ := get(t, h, "/app.css")
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, resp.Header.Get("Content-Type"), "text/css")
	})

	t.Run("the source map is absent from the production bundle", func(t *testing.T) {
		resp, _ := get(t, h, "/app.js.map")
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("precomputed ETags support 304", func(t *testing.T) {
		first, _ := get(t, h, "/app.js")
		etag := first.Header.Get("ETag")
		require.NotEmpty(t, etag)

		second, body := do(t, h, http.MethodGet, "/app.js", http.Header{"If-None-Match": {etag}})
		assert.Equal(t, http.StatusNotModified, second.StatusCode)
		assert.Empty(t, body)
	})

	t.Run("non-read methods are refused", func(t *testing.T) {
		resp, _ := do(t, h, http.MethodPut, "/app.js", nil)
		assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
		assert.Equal(t, "GET, HEAD", resp.Header.Get("Allow"))
	})
}
