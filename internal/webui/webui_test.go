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

func get(t *testing.T, h http.Handler, path string) (*http.Response, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	resp := rec.Result()
	defer func() { require.NoError(t, resp.Body.Close()) }()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, string(b)
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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := get(t, h, tc.path)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, tc.wantBody, body)
		})
	}
}

func TestHandlerFSWithoutIndexFails(t *testing.T) {
	h := webui.HandlerFS(fstest.MapFS{})
	resp, _ := get(t, h, "/compare")
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

// TestEmbeddedFS proves the go:embed directive resolves in a clean checkout,
// where internal/webui/dist holds only .gitkeep.
func TestEmbeddedFS(t *testing.T) {
	entries, err := fs.ReadDir(webui.FS(), ".")
	require.NoError(t, err)
	assert.NotNil(t, entries)
}
