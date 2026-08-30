package main

import (
	"context"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/ingest"
	"github.com/ericsuh/layerlens/internal/safehttp"
	"github.com/ericsuh/layerlens/internal/webui"
)

// noDockerSocket points the --docker-host autodetect at a path that cannot
// exist, so flag tests see the "nothing configured" default.
func noDockerSocket(t *testing.T) {
	t.Helper()
	previous := dockerSocketPath
	dockerSocketPath = filepath.Join(t.TempDir(), "absent.sock")
	t.Cleanup(func() { dockerSocketPath = previous })
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestParseFlags(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
		want    config
	}{
		{
			name: "defaults match ARCHITECTURE 1.3",
			args: nil,
			want: config{
				listen:          ":8080",
				dataDir:         "/var/lib/layerlens/images",
				cacheMaxBytes:   50 << 30,
				fixturesDir:     "fixtures",
				maxPulls:        ingest.DefaultMaxInFlightPulls,
				pullTimeout:     ingest.DefaultPullTimeout,
				maxLayerEntries: ingest.MaxLayerEntries,
			},
		},
		{
			name: "all flags override",
			args: []string{
				"--listen", "127.0.0.1:9999",
				"--data-dir", "/tmp/ll",
				"--cache-max-bytes", "1048576",
				"--fixtures-dir", "./fx",
				"--docker-host", "unix:///run/docker.sock",
				"--ui-dir", "internal/webui/dist",
				"--max-concurrent-pulls", "2",
				"--pull-timeout", "45m",
				"--max-layer-entries", "1000",
			},
			want: config{
				listen:          "127.0.0.1:9999",
				dataDir:         "/tmp/ll",
				cacheMaxBytes:   1 << 20,
				fixturesDir:     "./fx",
				dockerHost:      "unix:///run/docker.sock",
				uiDir:           "internal/webui/dist",
				maxPulls:        2,
				pullTimeout:     45 * time.Minute,
				maxLayerEntries: 1000,
			},
		},
		{
			// The opt-out matters on a host that has a socket: it is
			// the only way to say "do not offer the daemon source",
			// and it is what keeps the e2e suite deterministic.
			name: "--docker-host off disables the daemon source",
			args: []string{"--docker-host", "off"},
			want: config{
				listen:        ":8080",
				dataDir:       "/var/lib/layerlens/images",
				cacheMaxBytes: 50 << 30,
				fixturesDir:   "fixtures",
				// Empty host AND the explicit-off marker: the UI has to
				// say "turned off", not "none found" (the deployed unit
				// sets `off` by default).
				dockerHost:      "",
				dockerOff:       true,
				maxPulls:        ingest.DefaultMaxInFlightPulls,
				pullTimeout:     ingest.DefaultPullTimeout,
				maxLayerEntries: ingest.MaxLayerEntries,
			},
		},
		{
			name:    "non-positive concurrent-pull cap is rejected",
			args:    []string{"--max-concurrent-pulls", "0"},
			wantErr: "--max-concurrent-pulls must be positive",
		},
		{
			name:    "non-positive layer entry cap is rejected",
			args:    []string{"--max-layer-entries", "0"},
			wantErr: "--max-layer-entries must be positive",
		},
		{
			name:    "non-positive cache cap is rejected",
			args:    []string{"--cache-max-bytes", "0"},
			wantErr: "--cache-max-bytes must be positive",
		},
		{
			name:    "empty listen address is rejected",
			args:    []string{"--listen", ""},
			wantErr: "--listen must not be empty",
		},
		{
			name:    "listen without a port is rejected",
			args:    []string{"--listen", "localhost"},
			wantErr: "is not a host:port address",
		},
		{
			name:    "empty data dir is rejected",
			args:    []string{"--data-dir", ""},
			wantErr: "--data-dir must not be empty",
		},
		{
			name:    "empty fixtures dir is rejected",
			args:    []string{"--fixtures-dir", ""},
			wantErr: "--fixtures-dir must not be empty",
		},
		{
			name:    "positional arguments are rejected",
			args:    []string{"serve"},
			wantErr: "unexpected arguments",
		},
		{
			name:    "unknown flag is rejected",
			args:    []string{"--nope"},
			wantErr: "flag provided but not defined",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DOCKER_HOST", "")
			noDockerSocket(t)
			got, err := parseFlags(tc.args, io.Discard)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, *got)
		})
	}
}

func TestParseFlagsDefaultsDockerHostFromEnv(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///run/user/1000/docker.sock")
	noDockerSocket(t)
	got, err := parseFlags(nil, io.Discard)
	require.NoError(t, err)
	assert.Equal(t, "unix:///run/user/1000/docker.sock", got.dockerHost)
}

// The Docker path is the one egress safehttp never screens, so it may only
// reach a local socket unless the operator opts in out loud — including when
// the endpoint arrives through the environment rather than a flag.
func TestParseFlagsRefusesTCPDockerHostWithoutOptIn(t *testing.T) {
	noDockerSocket(t)
	t.Run("from the environment", func(t *testing.T) {
		t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:2375")
		_, err := parseFlags(nil, io.Discard)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--docker-allow-tcp")
	})
	t.Run("from the flag", func(t *testing.T) {
		_, err := parseFlags([]string{"--docker-host", "tcp://10.0.0.5:2375"}, io.Discard)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--docker-allow-tcp")
	})
	t.Run("opted in", func(t *testing.T) {
		got, err := parseFlags([]string{"--docker-host", "tcp://10.0.0.5:2375", "--docker-allow-tcp"}, io.Discard)
		require.NoError(t, err)
		assert.Equal(t, "tcp://10.0.0.5:2375", got.dockerHost)
	})
	t.Run("nonsense endpoint", func(t *testing.T) {
		_, err := parseFlags([]string{"--docker-host", "ssh://box/docker.sock"}, io.Discard)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a supported endpoint")
	})
	t.Run("a bare socket path", func(t *testing.T) {
		got, err := parseFlags([]string{"--docker-host", "/var/run/docker.sock"}, io.Discard)
		require.NoError(t, err)
		assert.Equal(t, "/var/run/docker.sock", got.dockerHost)
	})
}

// TestDockerHostDefaultIsNotPrintedInHelp is the point of resolving the
// environment after parsing: flag defaults land in -h output, and DOCKER_HOST
// can carry a hostname and credentials.
func TestDockerHostDefaultIsNotPrintedInHelp(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///run/secret.internal/docker.sock")
	noDockerSocket(t)

	var help stringWriter
	_, err := parseFlags([]string{"-h"}, &help)
	require.Error(t, err)
	assert.NotContains(t, help.String(), "secret.internal")
}

type stringWriter struct{ b []byte }

func (w *stringWriter) Write(p []byte) (int, error) { w.b = append(w.b, p...); return len(p), nil }
func (w *stringWriter) String() string              { return string(w.b) }

// TestParseFlagsAutodetectsLocalSocket covers the ARCHITECTURE §1.3 fallback.
func TestParseFlagsAutodetectsLocalSocket(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")

	sock := filepath.Join(t.TempDir(), "docker.sock")
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	defer func() { require.NoError(t, ln.Close()) }()

	previous := dockerSocketPath
	dockerSocketPath = sock
	t.Cleanup(func() { dockerSocketPath = previous })

	got, err := parseFlags(nil, io.Discard)
	require.NoError(t, err)
	assert.Equal(t, "unix://"+sock, got.dockerHost)
}

// TestParseFlagsIgnoresNonSocketAtSocketPath guards against treating a stray
// regular file as a Docker endpoint.
func TestParseFlagsIgnoresNonSocketAtSocketPath(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")

	notASocket := filepath.Join(t.TempDir(), "docker.sock")
	require.NoError(t, os.WriteFile(notASocket, nil, 0o600))

	previous := dockerSocketPath
	dockerSocketPath = notASocket
	t.Cleanup(func() { dockerSocketPath = previous })

	got, err := parseFlags(nil, io.Discard)
	require.NoError(t, err)
	assert.Empty(t, got.dockerHost)
}

func TestCheckUIDir(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "regular")
	require.NoError(t, os.WriteFile(file, nil, 0o600))

	assert.ErrorContains(t, checkUIDir(filepath.Join(dir, "absent")), "--ui-dir")
	assert.ErrorContains(t, checkUIDir(file), "is not a directory")
	assert.ErrorContains(t, checkUIDir(dir), "has no index.html")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>"), 0o600))
	assert.NoError(t, checkUIDir(dir))
}

// uiDir builds a minimal SPA asset directory for the run() tests.
func uiDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"),
		[]byte(`<!doctype html><div id="root"></div>`), 0o600))
	return dir
}

func testConfig(t *testing.T) *config {
	t.Helper()
	return &config{
		listen:        "127.0.0.1:0",
		dataDir:       t.TempDir(),
		cacheMaxBytes: 1 << 20,
		fixturesDir:   "fixtures",
		uiDir:         uiDir(t),
	}
}

// startRun launches run() on an ephemeral port. It returns the server's base
// URL, a function that triggers shutdown, and the channel carrying run's error.
func startRun(t *testing.T, cfg *config) (string, func(), <-chan error) {
	t.Helper()
	return start(t, func(ctx context.Context, ready func(net.Addr)) error {
		return run(ctx, cfg, discardLogger(), ready)
	})
}

// startServe launches serve() with an arbitrary handler, which is how the
// drain test gets a request that is still in flight when shutdown begins.
func startServe(t *testing.T, cfg *config, handler http.Handler) (string, func(), <-chan error) {
	t.Helper()
	return start(t, func(ctx context.Context, ready func(net.Addr)) error {
		return serve(ctx, cfg, discardLogger(), handler, ready)
	})
}

func start(t *testing.T, fn func(context.Context, func(net.Addr)) error) (string, func(), <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	addrCh := make(chan net.Addr, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- fn(ctx, func(a net.Addr) { addrCh <- a })
	}()

	select {
	case addr := <-addrCh:
		return "http://" + addr.String(), cancel, errCh
	case err := <-errCh:
		t.Fatalf("run returned before listening: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("run did not start listening")
	}
	return "", cancel, errCh
}

func waitForRun(t *testing.T, errCh <-chan error) {
	t.Helper()
	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(shutdownTimeout + 5*time.Second):
		t.Fatal("run did not return after shutdown was requested")
	}
}

func TestRunServesAndShutsDownCleanly(t *testing.T) {
	base, stop, errCh := startRun(t, testConfig(t))

	resp, err := http.Get(base + "/healthz")
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "ok", string(body))

	stop()
	waitForRun(t, errCh)

	// The listener is released, so nothing answers on that port any more.
	_, err = http.Get(base + "/healthz")
	assert.Error(t, err)
}

// TestRunDrainsInFlightRequests is the property graceful shutdown exists for:
// a request that is already being served finishes instead of being cut off.
func TestRunDrainsInFlightRequests(t *testing.T) {
	release := make(chan struct{})
	slow := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("done"))
	})

	base, stop, errCh := startServe(t, testConfig(t), slow)

	type result struct {
		status int
		body   string
		err    error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := http.Get(base + "/slow")
		if err != nil {
			done <- result{err: err}
			return
		}
		b, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		done <- result{status: resp.StatusCode, body: string(b), err: readErr}
	}()

	// Give the request time to reach the handler, then ask for shutdown while
	// it is still in flight.
	time.Sleep(200 * time.Millisecond)
	stop()
	time.Sleep(200 * time.Millisecond)
	close(release)

	select {
	case got := <-done:
		require.NoError(t, got.err)
		assert.Equal(t, http.StatusOK, got.status)
		assert.Equal(t, "done", got.body)
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight request was cut off instead of drained")
	}
	waitForRun(t, errCh)
}

func TestRunRejectsMissingUIDir(t *testing.T) {
	cfg := testConfig(t)
	cfg.uiDir = filepath.Join(t.TempDir(), "absent")

	err := run(context.Background(), cfg, discardLogger(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--ui-dir")
}

func TestRunReportsListenFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { require.NoError(t, ln.Close()) }()

	cfg := testConfig(t)
	cfg.listen = ln.Addr().String()

	err = run(context.Background(), cfg, discardLogger(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "listen on")
}

// TestRunUsesEmbeddedUIWhenUIDirIsEmpty proves the production path (no
// --ui-dir) serves the go:embed'd bundle rather than a test double.
func TestRunUsesEmbeddedUIWhenUIDirIsEmpty(t *testing.T) {
	requireBuiltBundle(t)

	cfg := testConfig(t)
	cfg.uiDir = ""
	base, stop, errCh := startRun(t, cfg)

	resp, err := http.Get(base + "/")
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(body), `id="root"`)
	assert.NotEmpty(t, resp.Header.Get("Content-Security-Policy"))
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))

	stop()
	waitForRun(t, errCh)
}

// requireBuiltBundle skips when internal/webui/dist holds only .gitkeep, which
// is the state of a clean checkout before `mise run build-web`. The mise
// test-go task depends on build-web, so the CI-equivalent run never skips.
func requireBuiltBundle(t *testing.T) {
	t.Helper()
	if _, err := fs.Stat(webui.FS(), "index.html"); err != nil {
		t.Skip("SPA bundle is not built; run `mise run build-web`")
	}
}

// TestOutboundTransportIsHardened is a wiring assertion, not a unit test of
// safehttp: it checks that the transport this command actually builds refuses
// a private destination and refuses plaintext.
//
// The controls themselves are tested in internal/safehttp. What can only be
// checked here is that the process uses them — a server wired with
// http.DefaultTransport would pass every test in that package and still be an
// SSRF.
func TestOutboundTransportIsHardened(t *testing.T) {
	transport := outboundTransport()
	t.Cleanup(transport.CloseIdleConnections)
	client := transport.Client()

	// 169.254.169.254 is the cloud metadata endpoint every SSRF is aimed at.
	_, err := client.Get("https://169.254.169.254/latest/meta-data/") //nolint:noctx // short-lived test request
	require.Error(t, err)
	assert.ErrorIs(t, err, safehttp.ErrForbiddenAddress)

	_, err = client.Get("http://example.com/") //nolint:noctx // short-lived test request
	require.Error(t, err)
	assert.ErrorIs(t, err, safehttp.ErrPlaintextRefused)

	_, err = client.Get("https://example.com:8443/") //nolint:noctx // short-lived test request
	require.Error(t, err)
	assert.ErrorIs(t, err, safehttp.ErrForbiddenPort)
}
