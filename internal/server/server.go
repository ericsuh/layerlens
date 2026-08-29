// Package server wires the HTTP surface of layerlens: the JSON API under
// /api/v1, the health endpoint, and the embedded SPA fallback.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"runtime/debug"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ericsuh/layerlens/internal/webui"
)

// APIPrefix is the versioned JSON API root. Breaking changes bump the version.
const APIPrefix = "/api/v1"

// apiNamespace is the reserved prefix that must never fall through to the SPA
// shell. It is deliberately unversioned: /api/v2 and a bare /api are equally
// reserved, and all of them answer with the JSON envelope.
const apiNamespace = "/api"

// maxReflectedPathBytes caps how much of an attacker-controlled request path
// is echoed back in an error envelope or written to the access log. A 60 KB
// URL should not become a 60 KB response body or a 60 KB log line.
const maxReflectedPathBytes = 256

// Error codes returned in the API error envelope (ARCHITECTURE §6.1). Later
// phases add handlers that use the codes their endpoints can produce.
const (
	CodeInvalidReference   = "invalid_reference"
	CodeRegistryNotAllowed = "registry_not_allowed"
	CodeImageNotFound      = "image_not_found"
	CodePullUpstreamDenied = "pull_upstream_denied"
	CodeDockerUnavailable  = "docker_unavailable"
	CodeCacheFull          = "cache_full"
	CodePullConflict       = "pull_conflict"
	CodeBadRequest         = "bad_request"
	CodeInternal           = "internal"
	// CodeNotFound covers an unrouted path inside the reserved /api namespace.
	CodeNotFound = "not_found"
	// CodeMethodNotAllowed covers a known path reached with the wrong method.
	CodeMethodNotAllowed = "method_not_allowed"
)

// ErrorBody is the payload of an APIError.
type ErrorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// APIError is the envelope returned with every non-2xx API response.
type APIError struct {
	Error ErrorBody `json:"error"`
}

// WriteError writes an API error envelope with the given HTTP status.
func WriteError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	payload := APIError{Error: ErrorBody{Code: code, Message: message, Details: details}}
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Error("write error envelope", "err", err)
	}
}

// Options configures a Server. Fields that later phases populate (cache store,
// ingester) are added as those phases land.
type Options struct {
	// Logger receives server-side diagnostics. Defaults to slog.Default().
	Logger *slog.Logger
	// UI serves the single-page application. Defaults to the SPA embedded in
	// the binary; tests substitute an in-memory asset tree.
	UI http.Handler
}

// Server is the root http.Handler for the application.
type Server struct {
	log     *slog.Logger
	ui      http.Handler
	mux     *http.ServeMux
	apiMux  *http.ServeMux
	handler http.Handler
}

// New builds the routing tree: /healthz, the reserved /api namespace, and the
// embedded SPA for everything else, behind the security-header, access-log and
// panic-recovery middleware.
func New(opts Options) *Server {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	ui := opts.UI
	if ui == nil {
		ui = webui.Handler()
	}
	s := &Server{log: log, ui: ui, mux: http.NewServeMux(), apiMux: http.NewServeMux()}
	s.routes()
	// Outermost first: headers are set before any handler can write, the
	// access log wraps the recovered status, and recovery sits closest to the
	// handlers so a panic anywhere below it becomes a 500 envelope.
	s.handler = securityHeaders(s.logRequests(s.recoverPanics(http.HandlerFunc(s.dispatch))))
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	// Without this the SPA catch-all would answer a non-GET /healthz with the
	// HTML shell instead of a method error.
	s.mux.HandleFunc("/healthz", methodNotAllowed(http.MethodGet))
	// /healthz/anything is not a client route: answering it with the SPA shell
	// and a 200 would make a mistyped health check pass against HTML.
	s.mux.HandleFunc("/healthz/{rest...}", s.handleHealthzNotFound)
	s.mux.Handle("/", s.ui)

	// The API mux holds only the catch-all in phase 001. Later phases register
	// exact patterns on it; they must not register trailing-slash subtree
	// patterns, which would make ServeMux emit an HTML redirect for the
	// slash-less form and break §6.1's "never HTML under /api" promise.
	s.apiMux.HandleFunc("/", s.handleAPINotFound)
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// dispatch routes a request, keeping the reserved /api namespace away from
// http.ServeMux's redirect behaviour.
func (s *Server) dispatch(w http.ResponseWriter, r *http.Request) {
	if !inAPINamespace(r.URL.Path) {
		s.mux.ServeHTTP(w, r)
		return
	}
	// ServeMux answers a non-canonical path (/api/v1//images) or a slash-less
	// subtree (/api) with a 30x whose body is HTML. Inside /api that would
	// hand a JSON client a redirect it cannot parse, so a non-canonical path
	// is simply not a route.
	if !isCanonicalPath(r.URL.Path) {
		s.handleAPINotFound(w, r)
		return
	}
	s.apiMux.ServeHTTP(w, r)
}

// inAPINamespace reports whether p is inside the reserved /api tree. A bare
// /api and /apifoo differ: only the former (and /api/...) is reserved.
func inAPINamespace(p string) bool {
	return p == apiNamespace || strings.HasPrefix(p, apiNamespace+"/")
}

// isCanonicalPath reports whether p is already in the form http.ServeMux would
// redirect it to: rooted, with no empty, "." or ".." segments.
func isCanonicalPath(p string) bool {
	if !strings.HasPrefix(p, "/") {
		return false
	}
	cleaned := path.Clean(p)
	if cleaned != "/" && strings.HasSuffix(p, "/") {
		cleaned += "/"
	}
	return cleaned == p
}

// securityHeaders applies the headers that every response needs regardless of
// which handler produced it. The SPA shell adds its own Content-Security-Policy
// in package webui; a JSON body does not need one.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// statusRecorder captures the response status and size for the access log and
// tells the panic recovery whether a response has already been committed.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusRecorder) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}

func (w *statusRecorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer, so flushing
// and hijacking keep working for the streaming endpoints of later phases.
func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// logRequests emits one access-log line per request.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		status := rec.status
		if status == 0 {
			// A handler that returned without writing anything still sends 200.
			status = http.StatusOK
		}
		level := slog.LevelInfo
		if status >= http.StatusInternalServerError {
			level = slog.LevelError
		}
		s.log.LogAttrs(r.Context(), level, "request",
			slog.String("method", r.Method),
			// Truncated: the path is attacker-controlled and unbounded.
			slog.String("path", truncateForReflection(r.URL.Path)),
			slog.Int("status", status),
			slog.Int("bytes", rec.bytes),
			slog.Duration("duration", time.Since(start)),
		)
	})
}

// recoverPanics turns a panic in any handler into the §6.1 internal/500
// envelope. The panic value and stack are logged; neither reaches the client.
func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			// ErrAbortHandler is net/http's documented way of aborting a
			// response; it is not a bug and must keep propagating.
			if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(rec)
			}
			s.log.Error("panic serving request",
				"method", r.Method,
				"path", truncateForReflection(r.URL.Path),
				"panic", fmt.Sprint(rec),
				"stack", string(debug.Stack()),
			)
			if sr, ok := w.(*statusRecorder); ok && sr.status != 0 {
				// Headers are already on the wire; the client sees a truncated
				// body, which is the best available signal.
				return
			}
			WriteError(w, http.StatusInternalServerError, CodeInternal,
				"internal server error", nil)
		}()
		next.ServeHTTP(w, r)
	})
}

// handleHealthz reports process readiness. From phase 005 it also gates on
// fixture ingestion having completed.
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("ok")); err != nil {
		s.log.Debug("write healthz response", "err", err)
	}
}

// handleHealthzNotFound answers /healthz/<anything>. Plain text, to match the
// plain-text /healthz it shadows.
func (s *Server) handleHealthzNotFound(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "not found", http.StatusNotFound)
}

// methodNotAllowed answers with 405 and an Allow header listing allowed.
func methodNotAllowed(allowed ...string) http.HandlerFunc {
	allow := strings.Join(allowed, ", ")
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", allow)
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
			fmt.Sprintf("method %s is not allowed for %s", r.Method,
				truncateForReflection(r.URL.Path)), nil)
	}
}

func (s *Server) handleAPINotFound(w http.ResponseWriter, r *http.Request) {
	WriteError(w, http.StatusNotFound, CodeNotFound, "no such API endpoint", map[string]any{
		"path": truncateForReflection(r.URL.Path),
	})
}

// truncateForReflection bounds a string that came from the request before it
// is echoed into a response body or a log line, cutting on a rune boundary so
// the result is still valid UTF-8.
func truncateForReflection(s string) string {
	if len(s) <= maxReflectedPathBytes {
		return s
	}
	cut := s[:maxReflectedPathBytes]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut + "…"
}
