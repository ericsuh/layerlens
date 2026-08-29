// Package webui embeds the built single-page application and serves it with
// SPA-style fallback routing.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// dist holds the esbuild/Tailwind output produced by `mise run build-web`.
// The directory is present in a clean checkout via dist/.gitkeep so that the
// embed directive always resolves.
//
//go:embed all:dist
var dist embed.FS

// FS returns the embedded asset tree rooted at the build output directory.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		// Unreachable: "dist" is embedded at compile time.
		panic(err)
	}
	return sub
}

// Handler serves the embedded SPA.
func Handler() http.Handler {
	return HandlerFS(FS())
}

// HandlerFS serves an asset tree with SPA-style fallback. Requests that match
// a file in assets are served as that file; every other path falls back to
// index.html so that client-side routes (for example /compare) survive a hard
// reload.
//
// Callers are responsible for routing /api/* elsewhere: this handler treats an
// unknown path as a client route and answers with the SPA shell.
func HandlerFS(assets fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(assets))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" || name == "." || !exists(assets, name) {
			serveIndex(w, r, assets)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func exists(assets fs.FS, name string) bool {
	if !fs.ValidPath(name) {
		return false
	}
	info, err := fs.Stat(assets, name)
	return err == nil && !info.IsDir()
}

func serveIndex(w http.ResponseWriter, r *http.Request, assets fs.FS) {
	body, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		http.Error(w, "SPA assets are not built", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
