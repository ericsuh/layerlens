// Package server wires the HTTP surface of layerlens: the JSON API under
// /api/v1, the health endpoint, and the embedded SPA fallback.
package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/ericsuh/layerlens/internal/webui"
)

// APIPrefix is the versioned JSON API root. Breaking changes bump the version.
const APIPrefix = "/api/v1"

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
	log *slog.Logger
	ui  http.Handler
	mux *http.ServeMux
}

// New builds the routing tree: /healthz, the reserved /api/v1 namespace, and
// the embedded SPA for everything else.
func New(opts Options) *Server {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	ui := opts.UI
	if ui == nil {
		ui = webui.Handler()
	}
	s := &Server{log: log, ui: ui, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	// Without this the SPA catch-all would answer a non-GET /healthz with the
	// HTML shell instead of a method error.
	s.mux.HandleFunc("/healthz", methodNotAllowed(http.MethodGet))
	// Reserved namespace: an unmatched API path must never fall through to the
	// SPA shell, or clients would parse HTML as JSON.
	s.mux.HandleFunc("/api/", s.handleAPINotFound)
	s.mux.Handle("/", s.ui)
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
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

// methodNotAllowed answers with 405 and an Allow header listing allowed.
func methodNotAllowed(allowed ...string) http.HandlerFunc {
	allow := strings.Join(allowed, ", ")
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", allow)
		WriteError(w, http.StatusMethodNotAllowed, CodeBadRequest,
			fmt.Sprintf("method %s is not allowed for %s", r.Method, r.URL.Path), nil)
	}
}

func (s *Server) handleAPINotFound(w http.ResponseWriter, r *http.Request) {
	WriteError(w, http.StatusNotFound, CodeNotFound, "no such API endpoint", map[string]any{
		"path": r.URL.Path,
	})
}
