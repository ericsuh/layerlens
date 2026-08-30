// Package webui embeds the built single-page application and serves it with
// SPA-style fallback routing.
package webui

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// dist holds the esbuild/Tailwind output produced by `mise run build-web`.
// The directory is present in a clean checkout via dist/.gitkeep so that the
// embed directive always resolves.
//
//go:embed all:dist
var dist embed.FS

// assetPrefixes are path prefixes that only ever hold build output. A miss
// under one of them is a 404, never the SPA shell, even when the request has
// no file extension.
var assetPrefixes = []string{"assets/", "static/"}

// assetExtensions are the suffixes a browser fetches as a subresource. A miss
// on one of these is a 404 rather than the SPA shell.
//
// This is an allowlist rather than "has any extension" on purpose: client
// routes will carry image references, and a tag like `nginx:1.27` would make
// path.Ext report ".27".
var assetExtensions = map[string]bool{
	".css": true, ".gif": true, ".htm": true, ".html": true, ".ico": true,
	".jpeg": true, ".jpg": true, ".js": true, ".json": true, ".map": true,
	".mjs": true, ".png": true, ".svg": true, ".ttf": true, ".txt": true,
	".webmanifest": true, ".webp": true, ".woff": true, ".woff2": true,
	".xml": true,
}

const (
	// indexName is the SPA shell served for every client-side route.
	indexName = "index.html"

	// shellCSP locks the shell down to same-origin resources. layerlens is
	// entirely self-hosted: the bundle, the stylesheet and the JSON API all
	// come from this origin, so 'none' is the right default and every
	// directive below is something the app actually uses. `data:` images are
	// allowed because inline SVG/PNG data URIs are cheap and inert.
	//
	// `style-src 'self'` also forbids `style="…"` attributes, which the UI does
	// need: Radix positions its portalled popovers and tooltips with inline
	// styles, and the layer diagram's could-be-shared pills and selection rules
	// are placed from measured card geometry. Phase 006 therefore takes the
	// option this comment already anticipated and adds `style-src-attr
	// 'unsafe-inline'` — style *attributes* only. `style-src` itself stays
	// `'self'`, so `style-src-elem` still inherits it and no <style> element or
	// remote stylesheet can be injected, and `img-src 'self' data:` denies the
	// `url()` exfiltration that is the main thing a hostile style attribute
	// could otherwise attempt.
	shellCSP = "default-src 'none'; " +
		"script-src 'self'; " +
		"style-src 'self'; " +
		"style-src-attr 'unsafe-inline'; " +
		"img-src 'self' data:; " +
		"font-src 'self'; " +
		"connect-src 'self'; " +
		"base-uri 'none'; " +
		"form-action 'none'; " +
		"frame-ancestors 'none'"

	// assetCacheControl lets a browser reuse an asset for a short window and
	// then revalidate. Filenames are not content-hashed, so the ETag rather
	// than the URL is what makes a redeploy visible.
	assetCacheControl = "public, max-age=300, must-revalidate"
)

// FS returns the embedded asset tree rooted at the build output directory.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		// Unreachable: "dist" is embedded at compile time.
		panic(err)
	}
	return sub
}

// Handler serves the embedded SPA. The embedded tree is immutable, so every
// asset's ETag is computed once at startup.
func Handler() http.Handler {
	assets := FS()
	return &handler{assets: assets, etags: computeETags(assets)}
}

// HandlerFS serves an asset tree with SPA-style fallback. Requests that match
// a file in assets are served as that file; a path that looks like a client
// route falls back to index.html so that /compare survives a hard reload,
// while a path that looks like an asset 404s rather than handing a browser
// HTML where it asked for JavaScript.
//
// The tree is assumed to be mutable (it backs --ui-dir, whose contents an
// esbuild watcher rewrites), so ETags are computed per request.
//
// Callers are responsible for routing /api/* elsewhere: this handler treats an
// unknown extension-less path as a client route and answers with the shell.
func HandlerFS(assets fs.FS) http.Handler {
	return &handler{assets: assets}
}

type handler struct {
	assets fs.FS
	// etags maps asset name to a strong ETag. Nil when the tree may change
	// underneath us, in which case the ETag is computed per request.
	etags map[string]string
}

// computeETags hashes every regular file in assets once. The embedded tree is
// ~230 KiB, so this is a sub-millisecond cost paid at process start.
func computeETags(assets fs.FS) map[string]string {
	etags := make(map[string]string)
	err := fs.WalkDir(assets, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		tag, err := hashFile(assets, name)
		if err != nil {
			return err
		}
		etags[name] = tag
		return nil
	})
	if err != nil {
		// Unreachable for embed.FS: reads cannot fail.
		panic(fmt.Errorf("hash embedded assets: %w", err))
	}
	return etags
}

func hashFile(assets fs.FS, name string) (string, error) {
	f, err := assets.Open(name)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", err
	}
	return `"` + hex.EncodeToString(sum.Sum(nil)[:16]) + `"`, nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Set on every response, including the 404 and 405 paths: the whole point
	// is that a browser must never sniff a type out of a body we did not label.
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	switch {
	case name == "" || name == ".":
		h.serveIndex(w, r)
	case h.isFile(name):
		h.serveAsset(w, r, name)
	case looksLikeAsset(name):
		// A miss on app.js must not hand the browser the HTML shell: it would
		// be parsed as JavaScript and a renamed-bundle regression would look
		// like a blank page rather than a 404.
		http.Error(w, "not found", http.StatusNotFound)
	default:
		h.serveIndex(w, r)
	}
}

// looksLikeAsset reports whether a miss on name should be a 404 rather than
// the SPA shell. Build output carries a known subresource extension or lives
// under a known asset prefix; everything else is treated as a client route.
func looksLikeAsset(name string) bool {
	if assetExtensions[strings.ToLower(path.Ext(path.Base(name)))] {
		return true
	}
	for _, prefix := range assetPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func (h *handler) isFile(name string) bool {
	if !fs.ValidPath(name) {
		return false
	}
	info, err := fs.Stat(h.assets, name)
	return err == nil && !info.IsDir()
}

func (h *handler) etag(name string) string {
	if h.etags != nil {
		return h.etags[name]
	}
	tag, err := hashFile(h.assets, name)
	if err != nil {
		return ""
	}
	return tag
}

func (h *handler) serveAsset(w http.ResponseWriter, r *http.Request, name string) {
	body, err := fs.ReadFile(h.assets, name)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if tag := h.etag(name); tag != "" {
		w.Header().Set("ETag", tag)
	}
	w.Header().Set("Cache-Control", assetCacheControl)
	// ServeContent picks the Content-Type from the extension and turns an
	// If-None-Match hit into a 304, so a reload costs a request instead of
	// 220 KiB. embed.FS has a zero ModTime, so time.Time{} is passed
	// deliberately: it suppresses Last-Modified and leaves the ETag as the
	// only validator.
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(body))
}

func (h *handler) serveIndex(w http.ResponseWriter, r *http.Request) {
	body, err := fs.ReadFile(h.assets, indexName)
	if err != nil {
		http.Error(w, "SPA assets are not built", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The shell names the bundle, so it must never be cached past a redeploy.
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Security-Policy", shellCSP)
	if tag := h.etag(indexName); tag != "" {
		w.Header().Set("ETag", tag)
	}
	http.ServeContent(w, r, indexName, time.Time{}, bytes.NewReader(body))
}
