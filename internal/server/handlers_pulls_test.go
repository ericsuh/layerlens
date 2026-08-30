package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/domain"
	"github.com/ericsuh/layerlens/internal/imgref"
	"github.com/ericsuh/layerlens/internal/ingest"
	"github.com/ericsuh/layerlens/internal/server"
)

// stubIngester drives the HTTP layer without a registry or a daemon: these
// tests are about status codes, the error envelope and the wire shape, all of
// which must hold whatever the source underneath does.
type stubIngester struct {
	startErr   error
	result     domain.StartResult
	status     *domain.PullStatus
	statusErr  error
	listing    domain.DockerListing
	listingErr error
	cancelErr  error

	started   []domain.IngestRequest
	cancelled []domain.PullID
}

func (s *stubIngester) Start(_ context.Context, req domain.IngestRequest) (domain.StartResult, error) {
	s.started = append(s.started, req)
	if s.startErr != nil {
		return domain.StartResult{}, s.startErr
	}
	return s.result, nil
}

func (s *stubIngester) Status(domain.PullID) (*domain.PullStatus, error) {
	if s.statusErr != nil {
		return nil, s.statusErr
	}
	return s.status, nil
}

func (s *stubIngester) Pulls() []domain.PullStatus {
	if s.status == nil {
		return nil
	}
	return []domain.PullStatus{*s.status}
}

func (s *stubIngester) Cancel(id domain.PullID) error {
	s.cancelled = append(s.cancelled, id)
	return s.cancelErr
}

func (s *stubIngester) ListDockerImages(context.Context) (domain.DockerListing, error) {
	return s.listing, s.listingErr
}

func withIngester(stub *stubIngester) func(*server.Options) {
	return func(o *server.Options) {
		o.Ingester = stub
		o.AllowedRegistries = imgref.DefaultPatterns
	}
}

func postJSON(t *testing.T, h http.Handler, target string, payload any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func runningStatus() *domain.PullStatus {
	total := int64(4096)
	layers := 3
	return &domain.PullStatus{
		ID: "p1", Reference: "ghcr.io/demo/app:v1", Source: domain.IngestSourceRegistry,
		State: domain.PullRunning, StartedAt: time.Unix(1700000000, 0).UTC(),
		BytesTotal: &total, BytesDone: 1024, LayersTotal: &layers, LayersDone: 1,
	}
}

func TestCreatePullAcceptedAndIdempotent(t *testing.T) {
	stub := &stubIngester{result: domain.StartResult{ID: "p1", Created: true}, status: runningStatus()}
	h := apiServer(t, withIngester(stub))

	resp := postJSON(t, h, server.APIPrefix+"/pulls",
		map[string]string{"source": "registry", "reference": "ghcr.io/demo/app:v1"})
	raw := body(t, resp)
	require.Equal(t, http.StatusAccepted, resp.StatusCode, raw)
	var status domain.PullStatus
	require.NoError(t, json.Unmarshal([]byte(raw), &status))
	assert.Equal(t, domain.PullID("p1"), status.ID)
	assert.Equal(t, domain.PullRunning, status.State)
	require.Len(t, stub.started, 1)
	assert.Equal(t, "ghcr.io/demo/app:v1", stub.started[0].Reference)

	// The idempotent case: an identical request already covered.
	stub.result = domain.StartResult{ID: "p1", Created: false}
	resp = postJSON(t, h, server.APIPrefix+"/pulls",
		map[string]string{"source": "registry", "reference": "ghcr.io/demo/app:v1"})
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"a duplicate submission is not new work, so it is not 202")
	_ = body(t, resp)
}

func TestCreatePullRejections(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"not allowlisted", &imgref.ErrRegistryNotAllowed{Registry: "evil.example.com"},
			http.StatusForbidden, server.CodeRegistryNotAllowed},
		{"unparseable", fmt.Errorf("%w: nope", imgref.ErrInvalidReference),
			http.StatusBadRequest, server.CodeInvalidReference},
		{"no daemon", ingest.ErrDockerUnavailable,
			http.StatusServiceUnavailable, server.CodeDockerUnavailable},
		{"unknown source", ingest.ErrInvalidSource,
			http.StatusBadRequest, server.CodeBadRequest},
		// Admission control: the server is healthy and the request is
		// well formed, so this is 429 and not 503.
		{"too many pulls in flight", fmt.Errorf("%w: 4 running", ingest.ErrTooManyPulls),
			http.StatusTooManyRequests, server.CodeTooManyPulls},
		{"anything else", errors.New("boom"),
			http.StatusInternalServerError, server.CodeInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := apiServer(t, withIngester(&stubIngester{startErr: tc.err}))
			resp := postJSON(t, h, server.APIPrefix+"/pulls",
				map[string]string{"source": "registry", "reference": "x"})
			require.Equal(t, tc.status, resp.StatusCode)
			require.Equal(t, "application/json; charset=utf-8", resp.Header.Get("Content-Type"))
			envelope := decodeError(t, resp)
			assert.Equal(t, tc.code, envelope.Error.Code)
			assert.NotEmpty(t, envelope.Error.Message)
		})
	}
}

// The 403 has to tell the user what *is* allowed, and the list must come from
// the server so the UI never carries a stale copy.
func TestNotAllowlistedResponseNamesTheAllowedRegistries(t *testing.T) {
	h := apiServer(t, withIngester(&stubIngester{
		startErr: &imgref.ErrRegistryNotAllowed{Registry: "evil.example.com"},
	}))
	resp := postJSON(t, h, server.APIPrefix+"/pulls",
		map[string]string{"source": "registry", "reference": "evil.example.com/x"})
	envelope := decodeError(t, resp)
	assert.Contains(t, envelope.Error.Message, "evil.example.com")
	assert.Equal(t, "evil.example.com", envelope.Error.Details["registry"])
	allowed, ok := envelope.Error.Details["allowed"].([]any)
	require.True(t, ok)
	assert.Contains(t, allowed, "ghcr.io")
}

func TestCreatePullRejectsMalformedBodies(t *testing.T) {
	h := apiServer(t, withIngester(&stubIngester{}))
	for _, payload := range []string{`not json`, `{"source":"registry","surprise":1}`, ``} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, server.APIPrefix+"/pulls", bytes.NewReader([]byte(payload)))
		h.ServeHTTP(rec, req)
		resp := rec.Result()
		require.Equal(t, http.StatusBadRequest, resp.StatusCode, "payload %q", payload)
		assert.Equal(t, server.CodeBadRequest, decodeError(t, resp).Error.Code)
	}
}

func TestGetAndListAndCancelPulls(t *testing.T) {
	stub := &stubIngester{status: runningStatus()}
	h := apiServer(t, withIngester(stub))

	var list server.PullList
	getJSON(t, h, server.APIPrefix+"/pulls", &list)
	require.Len(t, list.Pulls, 1)
	assert.Equal(t, domain.PullID("p1"), list.Pulls[0].ID)

	var one domain.PullStatus
	getJSON(t, h, server.APIPrefix+"/pulls/p1", &one)
	assert.Equal(t, domain.PullRunning, one.State)
	require.NotNil(t, one.BytesTotal)
	assert.Equal(t, int64(4096), *one.BytesTotal)

	cancelled := *runningStatus()
	cancelled.State = domain.PullCancelled
	stub.status = &cancelled
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, server.APIPrefix+"/pulls/p1", nil))
	resp := rec.Result()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var after domain.PullStatus
	require.NoError(t, json.Unmarshal([]byte(body(t, resp)), &after))
	assert.Equal(t, domain.PullCancelled, after.State)
	assert.Equal(t, []domain.PullID{"p1"}, stub.cancelled)
}

func TestUnknownPullIsA404Envelope(t *testing.T) {
	h := apiServer(t, withIngester(&stubIngester{
		statusErr: fmt.Errorf("%w: p9", ingest.ErrPullNotFound),
		cancelErr: fmt.Errorf("%w: p9", ingest.ErrPullNotFound),
	}))
	envelope := getError(t, h, server.APIPrefix+"/pulls/p9", http.StatusNotFound)
	assert.Equal(t, server.CodeImageNotFound, envelope.Error.Code)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, server.APIPrefix+"/pulls/p9", nil))
	assert.Equal(t, http.StatusNotFound, rec.Result().StatusCode)
}

func TestPullsRejectWrongMethods(t *testing.T) {
	h := apiServer(t, withIngester(&stubIngester{status: runningStatus()}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, server.APIPrefix+"/pulls", nil))
	resp := rec.Result()
	require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	assert.Equal(t, "GET, POST", resp.Header.Get("Allow"))

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, server.APIPrefix+"/pulls/p1", nil))
	resp = rec.Result()
	require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	assert.Equal(t, "GET, DELETE", resp.Header.Get("Allow"))
}

// "No Docker" is a fact about the deployment, not a failed request.
func TestDockerListingWithoutADaemonIsA200(t *testing.T) {
	h := apiServer(t, withIngester(&stubIngester{
		listing: domain.DockerListing{Available: false, Reason: "No Docker socket found at /var/run/docker.sock"},
	}))
	var listing domain.DockerListing
	getJSON(t, h, server.APIPrefix+"/docker/images", &listing)
	assert.False(t, listing.Available)
	assert.Contains(t, listing.Reason, "No Docker socket")
	assert.NotNil(t, listing.Images, "the client's empty state keys on length")
	assert.Empty(t, listing.Images)
}

func TestDockerListingRows(t *testing.T) {
	h := apiServer(t, withIngester(&stubIngester{
		listing: domain.DockerListing{Available: true, Images: []domain.DockerImageSummary{
			{Reference: "alpine:3.20", DockerID: "sha256:abc", SizeBytes: 7, Platform: "linux/amd64"},
		}},
	}))
	var listing domain.DockerListing
	getJSON(t, h, server.APIPrefix+"/docker/images", &listing)
	require.Len(t, listing.Images, 1)
	assert.Equal(t, "alpine:3.20", listing.Images[0].Reference)
	assert.False(t, listing.Images[0].AlreadyAnalyzed)
}

func TestDockerListingFailureIsA503(t *testing.T) {
	h := apiServer(t, withIngester(&stubIngester{listingErr: errors.New("permission denied")}))
	envelope := getError(t, h, server.APIPrefix+"/docker/images", http.StatusServiceUnavailable)
	assert.Equal(t, server.CodeDockerUnavailable, envelope.Error.Code)
	assert.NotContains(t, envelope.Error.Message, "permission denied",
		"a daemon's own error text is logged, not rendered")
}

// A server built without a pull manager still answers the whole surface.
func TestPullEndpointsWithoutAnIngester(t *testing.T) {
	h := apiServer(t)
	var listing domain.DockerListing
	getJSON(t, h, server.APIPrefix+"/docker/images", &listing)
	assert.False(t, listing.Available)

	var list server.PullList
	getJSON(t, h, server.APIPrefix+"/pulls", &list)
	assert.Empty(t, list.Pulls)
}
