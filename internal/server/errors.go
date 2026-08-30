// Error envelope and code table for the JSON API (ARCHITECTURE §6.1). Every
// non-2xx response inside the reserved /api namespace carries this shape, so a
// client never has to guess whether a failure came back as JSON or as HTML.

package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/ericsuh/layerlens/internal/cachestore"
	"github.com/ericsuh/layerlens/internal/domain"
)

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

// badRequest reports a malformed parameter. The message is written for a human
// and is safe to display verbatim, per §6.1.
func badRequest(w http.ResponseWriter, format string, args ...any) {
	WriteError(w, http.StatusBadRequest, CodeBadRequest, fmt.Sprintf(format, args...), nil)
}

// imageNotFound reports an unknown (or evicted) image id.
func imageNotFound(w http.ResponseWriter, id domain.Digest) {
	WriteError(w, http.StatusNotFound, CodeImageNotFound,
		"no analyzed image with that id", map[string]any{"id": string(id)})
}

// writeStoreError maps a store or analysis failure onto the code table.
// Anything unrecognized is an internal error whose detail stays in the log:
// §6.1 requires a generic message for 500s.
func (s *Server) writeStoreError(w http.ResponseWriter, r *http.Request, err error, id domain.Digest) {
	// A failure that knows which image it belongs to overrides the
	// caller's guess: a comparison touches two images, and telling a
	// client to refetch the left one when the right one was evicted sends
	// it round the loop again.
	var attributed *imageError
	if errors.As(err, &attributed) {
		id = attributed.id
	}
	switch {
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrNotIndexed):
		// A layer index that vanished under us means the image was
		// evicted mid-request, which the client should treat exactly
		// like an unknown image: refetch the list and pick again.
		imageNotFound(w, id)
	case errors.Is(err, cachestore.ErrCacheFull):
		WriteError(w, http.StatusInsufficientStorage, CodeCacheFull, err.Error(), nil)
	default:
		s.log.Error("request failed", "path", truncateForReflection(r.URL.Path), "err", err)
		WriteError(w, http.StatusInternalServerError, CodeInternal, "internal server error", nil)
	}
}

// writeJSON writes a 200 response body.
func (s *Server) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The status is already on the wire, so a truncated body plus
		// this log line is the only remaining signal.
		s.log.Error("write JSON response", "err", err)
	}
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
