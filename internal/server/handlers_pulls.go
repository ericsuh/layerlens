package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/ericsuh/layerlens/internal/cachestore"
	"github.com/ericsuh/layerlens/internal/domain"
	"github.com/ericsuh/layerlens/internal/imgref"
	"github.com/ericsuh/layerlens/internal/ingest"
)

// maxPullRequestBytes bounds the POST /pulls body. It carries a source and a
// reference; a megabyte of JSON is not a reference.
const maxPullRequestBytes = 4 << 10

// PullList is the GET /api/v1/pulls response body.
type PullList struct {
	Pulls []domain.PullStatus `json:"pulls"`
}

// handleCreatePull serves POST /api/v1/pulls (§6.3).
//
// Validation order is the security-relevant part: the reference is parsed and
// the registry is checked against the allowlist *inside* Start, synchronously,
// before the pull goroutine exists — so a refused registry never becomes a
// socket (§7.1).
func (s *Server) handleCreatePull(w http.ResponseWriter, r *http.Request) {
	var req domain.IngestRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxPullRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		badRequest(w, "the request body must be {\"source\": \"registry\"|\"docker\", \"reference\": \"…\"}")
		return
	}

	result, err := s.ingester.Start(r.Context(), req)
	if err != nil {
		s.writePullStartError(w, r, err)
		return
	}
	status, err := s.ingester.Status(result.ID)
	if err != nil {
		s.writeStoreError(w, r, err, "")
		return
	}
	// 202 for work that has just been started, 200 for the idempotent
	// cases: an identical pull already in flight, or an image the cache
	// already holds (§6.3).
	code := http.StatusOK
	if result.Created {
		code = http.StatusAccepted
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(status); err != nil {
		s.log.Error("write pull response", "err", err)
	}
}

// handleListPulls serves GET /api/v1/pulls.
func (s *Server) handleListPulls(w http.ResponseWriter, _ *http.Request) {
	pulls := s.ingester.Pulls()
	if pulls == nil {
		pulls = []domain.PullStatus{}
	}
	s.writeJSON(w, PullList{Pulls: pulls})
}

// handleGetPull serves GET /api/v1/pulls/{id}.
func (s *Server) handleGetPull(w http.ResponseWriter, r *http.Request) {
	status, err := s.ingester.Status(domain.PullID(r.PathValue("id")))
	if err != nil {
		s.writePullLookupError(w, r, err)
		return
	}
	s.writeJSON(w, status)
}

// handleCancelPull serves DELETE /api/v1/pulls/{id}.
//
// Cancellation deletes the pull's staging directory and keeps every layer
// index already committed: the durable checkpoint unit is the layer, so
// retrying resumes where this left off (§4.1).
func (s *Server) handleCancelPull(w http.ResponseWriter, r *http.Request) {
	id := domain.PullID(r.PathValue("id"))
	if err := s.ingester.Cancel(id); err != nil {
		s.writePullLookupError(w, r, err)
		return
	}
	status, err := s.ingester.Status(id)
	if err != nil {
		s.writePullLookupError(w, r, err)
		return
	}
	s.writeJSON(w, status)
}

// handleDockerImages serves GET /api/v1/docker/images (§6.2).
//
// It never fails for "no Docker": an absent daemon is reported as
// available:false with a reason, because the client's job is to hide the
// section, not to render an error for a deployment choice.
func (s *Server) handleDockerImages(w http.ResponseWriter, r *http.Request) {
	listing, err := s.ingester.ListDockerImages(r.Context())
	if err != nil {
		// A daemon that answered and then failed *is* an error
		// (DESIGN state #6 offers a Retry for it).
		s.log.Warn("docker listing failed", "err", err)
		WriteError(w, http.StatusServiceUnavailable, CodeDockerUnavailable,
			"The Docker daemon could not be queried.", nil)
		return
	}
	if listing.Images == nil {
		listing.Images = []domain.DockerImageSummary{}
	}
	s.writeJSON(w, listing)
}

// writePullStartError maps a rejected pull request onto the §6.1 code table.
func (s *Server) writePullStartError(w http.ResponseWriter, r *http.Request, err error) {
	var notAllowed *imgref.ErrRegistryNotAllowed
	switch {
	case errors.As(err, &notAllowed):
		WriteError(w, http.StatusForbidden, CodeRegistryNotAllowed,
			notAllowed.Registry+" is not on the allowlist of registries layerlens may pull from.",
			map[string]any{
				"registry": notAllowed.Registry,
				"allowed":  s.allowed,
			})
	case errors.Is(err, imgref.ErrInvalidReference):
		WriteError(w, http.StatusBadRequest, CodeInvalidReference,
			"That is not a valid image reference.", nil)
	case errors.Is(err, ingest.ErrInvalidSource):
		badRequest(w, `source must be "registry" or "docker"`)
	case errors.Is(err, ingest.ErrDockerUnavailable):
		WriteError(w, http.StatusServiceUnavailable, CodeDockerUnavailable,
			"No Docker daemon is reachable on this server.", nil)
	case errors.Is(err, cachestore.ErrCacheFull):
		WriteError(w, http.StatusInsufficientStorage, CodeCacheFull, err.Error(), nil)
	default:
		s.log.Error("starting pull failed", "path", truncateForReflection(r.URL.Path), "err", err)
		WriteError(w, http.StatusInternalServerError, CodeInternal, "internal server error", nil)
	}
}

// writePullLookupError maps a pull-id lookup failure. An unknown pull id is a
// 404 with the same code as an unknown image id (§6.1).
func (s *Server) writePullLookupError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ingest.ErrPullNotFound) {
		WriteError(w, http.StatusNotFound, CodeImageNotFound, "no such pull", nil)
		return
	}
	s.writeStoreError(w, r, err, "")
}
