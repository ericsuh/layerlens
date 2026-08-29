package server_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/server"
	"github.com/ericsuh/layerlens/internal/webui"
)

const indexHTML = `<!doctype html><title>layerlens</title><div id="root"></div>`

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestServer() *server.Server {
	assets := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(indexHTML)},
		"app.js":     &fstest.MapFile{Data: []byte("console.log('layerlens')\n")},
	}
	return server.New(server.Options{Logger: discardLogger(), UI: webui.HandlerFS(assets)})
}

func do(t *testing.T, method, target string) *http.Response {
	t.Helper()
	return doOn(t, newTestServer(), method, target)
}

func doOn(t *testing.T, h http.Handler, method, target string) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec.Result()
}

func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer func() { require.NoError(t, resp.Body.Close()) }()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(b)
}

func decodeError(t *testing.T, resp *http.Response) server.APIError {
	t.Helper()
	var got server.APIError
	require.NoError(t, json.Unmarshal([]byte(body(t, resp)), &got))
	return got
}

func TestHealthz(t *testing.T) {
	resp := do(t, http.MethodGet, "/healthz")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))
	assert.Equal(t, "ok", body(t, resp))
}

func TestHealthzRejectsNonGET(t *testing.T) {
	resp := do(t, http.MethodPost, "/healthz")

	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	assert.Equal(t, "GET", resp.Header.Get("Allow"))
	// ARCHITECTURE §6.1 pins bad_request to 400; 405 has its own code.
	assert.Equal(t, server.CodeMethodNotAllowed, decodeError(t, resp).Error.Code)
}

// TestHealthzSubpathIsNotTheShell: a mistyped health check URL must fail, not
// come back 200 with HTML that a naive checker treats as healthy.
func TestHealthzSubpathIsNotTheShell(t *testing.T) {
	for _, path := range []string{"/healthz/", "/healthz/ready", "/healthz/deep/path"} {
		t.Run(path, func(t *testing.T) {
			resp := do(t, http.MethodGet, path)
			assert.Equal(t, http.StatusNotFound, resp.StatusCode)
			assert.NotContains(t, body(t, resp), `id="root"`)
		})
	}
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
		{
			name:       "missing asset is a 404, not the shell",
			path:       "/nonexistent.js",
			wantStatus: http.StatusNotFound,
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
			switch {
			case tc.wantIsIndex:
				assert.Equal(t, indexHTML, got)
			case tc.wantBody != "":
				assert.Equal(t, tc.wantBody, got)
			default:
				assert.NotContains(t, got, `id="root"`)
			}
		})
	}
}

func TestAssetsRejectNonReadMethods(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		for _, path := range []string{"/", "/app.js", "/compare"} {
			t.Run(method+" "+path, func(t *testing.T) {
				resp := do(t, method, path)
				assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
				assert.Equal(t, "GET, HEAD", resp.Header.Get("Allow"))
				assert.NotContains(t, body(t, resp), `id="root"`)
			})
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	paths := []string{"/", "/app.js", "/healthz", "/api/v1/nope", "/nonexistent.js"}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			resp := do(t, http.MethodGet, path)
			assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
			assert.Equal(t, "no-referrer", resp.Header.Get("Referrer-Policy"))
		})
	}

	t.Run("the SPA shell carries a CSP", func(t *testing.T) {
		resp := do(t, http.MethodGet, "/")
		assert.Contains(t, resp.Header.Get("Content-Security-Policy"), "default-src 'none'")
	})
}

func TestUnknownAPIPathReturnsErrorEnvelope(t *testing.T) {
	resp := do(t, http.MethodGet, "/api/v1/nonexistent")

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "application/json; charset=utf-8", resp.Header.Get("Content-Type"))

	got := decodeError(t, resp)
	assert.Equal(t, server.CodeNotFound, got.Error.Code)
	assert.NotEmpty(t, got.Error.Message)
	assert.Equal(t, "/api/v1/nonexistent", got.Error.Details["path"])
}

// TestAPINamespaceNeverReturnsHTML covers ARCHITECTURE §6.1's promise that the
// reserved namespace never falls through to the SPA. The interesting cases are
// the ones http.ServeMux would answer with a 30x whose body is HTML.
func TestAPINamespaceNeverReturnsHTML(t *testing.T) {
	paths := []string{
		"/api",
		"/api/",
		"/api/v1",
		"/api/v1/",
		"/api/v1//images",
		"/api/v1/../../etc/passwd",
		"/api/v1/images/../../..",
		"/api/./v1/images",
		"/api/v2/anything",
		"/api/v1/%2e%2e/%2e%2e/etc/passwd",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
				resp := do(t, method, path)
				payload := body(t, resp)

				assert.Equal(t, http.StatusNotFound, resp.StatusCode, method)
				assert.Equal(t, "application/json; charset=utf-8", resp.Header.Get("Content-Type"), method)
				assert.NotContains(t, payload, "<a href", method)
				assert.NotContains(t, payload, `id="root"`, method)

				var got server.APIError
				require.NoError(t, json.Unmarshal([]byte(payload), &got), method)
				assert.Equal(t, server.CodeNotFound, got.Error.Code, method)
			}
		})
	}
}

// TestAPISiblingPathsAreStillTheSPA guards the namespace check against being
// a naive prefix match: /apidocs is a client route, not reserved.
func TestAPISiblingPathsAreStillTheSPA(t *testing.T) {
	for _, path := range []string{"/apidocs", "/apiary/thing"} {
		resp := do(t, http.MethodGet, path)
		assert.Equal(t, http.StatusOK, resp.StatusCode, path)
		assert.Equal(t, indexHTML, body(t, resp), path)
	}
}

// TestReflectedPathIsTruncated: a 60 KB URL must not become a 60 KB response
// body or a 60 KB log line.
func TestReflectedPathIsTruncated(t *testing.T) {
	long := "/api/v1/" + strings.Repeat("a", 60_000)
	resp := do(t, http.MethodGet, long)

	got := decodeError(t, resp)
	reflected, ok := got.Error.Details["path"].(string)
	require.True(t, ok)
	assert.Less(t, len(reflected), 512)
	assert.True(t, strings.HasPrefix(reflected, "/api/v1/aaa"))
}

func TestMethodNotAllowedMessageIsTruncated(t *testing.T) {
	long := "/healthz?x=" + strings.Repeat("b", 60_000)
	resp := do(t, http.MethodPost, long)

	got := decodeError(t, resp)
	assert.Equal(t, server.CodeMethodNotAllowed, got.Error.Code)
	assert.Less(t, len(got.Error.Message), 512)
}

// TestPanicRecovery: §6.1 defines internal/500, and this is what produces it.
func TestPanicRecovery(t *testing.T) {
	panicking := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom: secret=hunter2")
	})
	var logged strings.Builder
	log := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))
	srv := server.New(server.Options{Logger: log, UI: panicking})

	resp := doOn(t, srv, http.MethodGet, "/")
	payload := body(t, resp)

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Equal(t, "application/json; charset=utf-8", resp.Header.Get("Content-Type"))

	var got server.APIError
	require.NoError(t, json.Unmarshal([]byte(payload), &got))
	assert.Equal(t, server.CodeInternal, got.Error.Code)
	assert.NotEmpty(t, got.Error.Message)
	// The panic value and stack are diagnostics, not a response body.
	assert.NotContains(t, payload, "hunter2")
	assert.NotContains(t, payload, "goroutine")
	assert.Empty(t, got.Error.Details)

	// ...but they must reach the log.
	assert.Contains(t, logged.String(), "hunter2")
	assert.Contains(t, logged.String(), "goroutine")
}

// TestPanicAfterWriteDoesNotDoubleWrite: once bytes are on the wire the
// recovery must log and stop, not append a second body.
func TestPanicAfterWriteDoesNotDoubleWrite(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		panic("late")
	})
	srv := server.New(server.Options{Logger: discardLogger(), UI: handler})

	resp := doOn(t, srv, http.MethodGet, "/")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "partial", body(t, resp))
}

func TestRequestLogging(t *testing.T) {
	var logged strings.Builder
	log := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))
	assets := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(indexHTML)}}
	srv := server.New(server.Options{Logger: log, UI: webui.HandlerFS(assets)})

	resp := doOn(t, srv, http.MethodGet, "/healthz")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	line := logged.String()
	assert.Contains(t, line, "msg=request")
	assert.Contains(t, line, "method=GET")
	assert.Contains(t, line, "path=/healthz")
	assert.Contains(t, line, "status=200")
	assert.Contains(t, line, "duration=")
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
