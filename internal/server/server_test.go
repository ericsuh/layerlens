package server_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/server"
	"github.com/ericsuh/layerlens/internal/webui"
)

const indexHTML = `<!doctype html><title>layerlens</title><div id="root"></div>`

func newTestServer() *server.Server {
	assets := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(indexHTML)},
		"app.js":     &fstest.MapFile{Data: []byte("console.log('layerlens')\n")},
	}
	return server.New(server.Options{UI: webui.HandlerFS(assets)})
}

func do(t *testing.T, method, target string) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	newTestServer().ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec.Result()
}

func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer func() { require.NoError(t, resp.Body.Close()) }()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(b)
}

func TestHealthz(t *testing.T) {
	resp := do(t, http.MethodGet, "/healthz")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))
	assert.Equal(t, "ok", body(t, resp))
}

func TestHealthzRejectsNonGET(t *testing.T) {
	resp := do(t, http.MethodPost, "/healthz")
	defer func() { require.NoError(t, resp.Body.Close()) }()

	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

func TestSPAFallback(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		wantStatus  int
		wantBody    string
		wantCtype   string
		wantIsIndex bool
	}{
		{
			name:        "root serves the shell",
			path:        "/",
			wantStatus:  http.StatusOK,
			wantIsIndex: true,
			wantCtype:   "text/html; charset=utf-8",
		},
		{
			name:        "unknown client route serves the shell",
			path:        "/compare",
			wantStatus:  http.StatusOK,
			wantIsIndex: true,
			wantCtype:   "text/html; charset=utf-8",
		},
		{
			name:        "deep client route serves the shell",
			path:        "/some/client/route",
			wantStatus:  http.StatusOK,
			wantIsIndex: true,
			wantCtype:   "text/html; charset=utf-8",
		},
		{
			name:       "existing asset is served verbatim",
			path:       "/app.js",
			wantStatus: http.StatusOK,
			wantBody:   "console.log('layerlens')\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := do(t, http.MethodGet, tc.path)
			assert.Equal(t, tc.wantStatus, resp.StatusCode)
			if tc.wantCtype != "" {
				assert.Equal(t, tc.wantCtype, resp.Header.Get("Content-Type"))
			}
			got := body(t, resp)
			if tc.wantIsIndex {
				assert.Equal(t, indexHTML, got)
			} else {
				assert.Equal(t, tc.wantBody, got)
			}
		})
	}
}

func TestUnknownAPIPathReturnsErrorEnvelope(t *testing.T) {
	resp := do(t, http.MethodGet, "/api/v1/nonexistent")

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "application/json; charset=utf-8", resp.Header.Get("Content-Type"))

	var got server.APIError
	require.NoError(t, json.Unmarshal([]byte(body(t, resp)), &got))
	assert.Equal(t, server.CodeNotFound, got.Error.Code)
	assert.NotEmpty(t, got.Error.Message)
	assert.Equal(t, "/api/v1/nonexistent", got.Error.Details["path"])
}

func TestErrorEnvelopeShape(t *testing.T) {
	tests := []struct {
		name string
		err  server.APIError
		want string
	}{
		{
			name: "details omitted when absent",
			err: server.APIError{Error: server.ErrorBody{
				Code:    server.CodeBadRequest,
				Message: "bad cursor",
			}},
			want: `{"error":{"code":"bad_request","message":"bad cursor"}}`,
		},
		{
			name: "details included when present",
			err: server.APIError{Error: server.ErrorBody{
				Code:    server.CodeRegistryNotAllowed,
				Message: "registry not allowed",
				Details: map[string]any{"registry": "evil.example"},
			}},
			want: `{"error":{"code":"registry_not_allowed","message":"registry not allowed","details":{"registry":"evil.example"}}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(tc.err)
			require.NoError(t, err)
			assert.JSONEq(t, tc.want, string(encoded))
			assert.Equal(t, tc.want, string(encoded), "field order and omitempty behavior are part of the contract")
		})
	}
}

func TestWriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	server.WriteError(rec, http.StatusForbidden, server.CodeRegistryNotAllowed, "registry not allowed", map[string]any{"registry": "evil.example"})

	resp := rec.Result()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, "application/json; charset=utf-8", resp.Header.Get("Content-Type"))
	assert.JSONEq(t, `{"error":{"code":"registry_not_allowed","message":"registry not allowed","details":{"registry":"evil.example"}}}`, body(t, resp))
}
