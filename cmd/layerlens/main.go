// Command layerlens serves the layerlens web application: an HTTP JSON API
// plus the embedded single-page UI, from one static binary.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ericsuh/layerlens/internal/cachestore"
	"github.com/ericsuh/layerlens/internal/imgref"
	"github.com/ericsuh/layerlens/internal/ingest"
	"github.com/ericsuh/layerlens/internal/safehttp"
	"github.com/ericsuh/layerlens/internal/server"
	"github.com/ericsuh/layerlens/internal/webui"
)

// version is reported by GET /api/v1/meta. It is a build-time constant rather
// than something derived at runtime so that a binary always reports what it
// actually is.
const version = "0.8.0"

const (
	defaultListen        = ":8080"
	defaultDataDir       = "/var/lib/layerlens/images"
	defaultCacheMaxBytes = int64(50) << 30 // 50 GiB
	defaultFixturesDir   = "fixtures"

	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 120 * time.Second
	shutdownTimeout   = 15 * time.Second
	// maxHeaderBytes bounds the request head. The default (1 MiB) is generous
	// for an app whose longest URL is an image digest.
	maxHeaderBytes = 64 << 10
)

// allowedRegistries is the ARCHITECTURE §7.1 host allowlist, reported
// verbatim by GET /api/v1/meta so the UI can name the registries a pull may
// target instead of hardcoding a second copy of the list.
//
// The list is not duplicated here: it comes from the package that enforces it,
// so "what the UI says is allowed" and "what the server will actually accept"
// cannot drift apart.
var allowedRegistries = imgref.DefaultPatterns

// dockerHostOff is the --docker-host value that disables the daemon source.
const dockerHostOff = "off"

// dockerSocketPath is the local endpoint probed when neither --docker-host nor
// $DOCKER_HOST is set (ARCHITECTURE §1.3). A variable so tests can point the
// autodetect at a path they control.
var dockerSocketPath = "/var/run/docker.sock"

// config holds the process-level flags described in ARCHITECTURE §1.3. Flags
// whose subsystems arrive in later phases are parsed and carried on the config
// now so the command-line surface stays stable.
type config struct {
	listen         string
	dataDir        string
	cacheMaxBytes  int64
	fixturesDir    string
	dockerHost     string
	dockerOff      bool
	dockerAllowTCP bool
	uiDir          string

	maxPulls        int
	pullTimeout     time.Duration
	maxLayerEntries int
}

func parseFlags(args []string, stderr io.Writer) (*config, error) {
	fs := flag.NewFlagSet("layerlens", flag.ContinueOnError)
	fs.SetOutput(stderr)

	cfg := &config{}
	fs.StringVar(&cfg.listen, "listen", defaultListen, "address to listen on")
	fs.StringVar(&cfg.dataDir, "data-dir", defaultDataDir, "directory holding the durable analysis cache")
	fs.Int64Var(&cfg.cacheMaxBytes, "cache-max-bytes", defaultCacheMaxBytes, "maximum bytes the analysis cache may occupy")
	fs.StringVar(&cfg.fixturesDir, "fixtures-dir", defaultFixturesDir, "directory of vendored OCI-layout demo images")
	fs.StringVar(&cfg.uiDir, "ui-dir", "", "development only: serve SPA assets from this directory instead of the embedded copy")
	// The default is empty rather than os.Getenv("DOCKER_HOST"): flag defaults
	// are printed by -h, and DOCKER_HOST can carry a host and credentials.
	// The environment is consulted after parsing instead.
	fs.StringVar(&cfg.dockerHost, "docker-host", "",
		`Docker endpoint to ingest images from (default $DOCKER_HOST, else the local socket if present; "off" disables the daemon source)`)
	// The daemon path is local trust: it bypasses safehttp by design, which
	// is defensible for a unix socket on the same host and not defensible
	// for an arbitrary TCP endpoint. Requiring an explicit opt-in keeps a
	// stray DOCKER_HOST=tcp://... in the environment from quietly turning
	// the one un-screened egress path into a remote one.
	fs.BoolVar(&cfg.dockerAllowTCP, "docker-allow-tcp", false,
		"permit a tcp:// --docker-host (the daemon path is not screened by the SSRF dialer; unix sockets only by default)")
	fs.IntVar(&cfg.maxPulls, "max-concurrent-pulls", ingest.DefaultMaxInFlightPulls,
		"maximum pulls that may run at once; further submissions are refused with 429")
	fs.DurationVar(&cfg.pullTimeout, "pull-timeout", ingest.DefaultPullTimeout,
		"wall-clock ceiling on a single pull (0 disables it; the throughput floor still applies)")
	fs.IntVar(&cfg.maxLayerEntries, "max-layer-entries", ingest.MaxLayerEntries,
		"maximum filesystem entries layerlens will index in one layer")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if rest := fs.Args(); len(rest) > 0 {
		return nil, fmt.Errorf("unexpected arguments: %v", rest)
	}
	switch cfg.dockerHost {
	case "":
		cfg.dockerHost = resolveDockerHost()
	case dockerHostOff:
		// An explicit opt-out. Without it there is no way to say "do not
		// offer the daemon source" on a machine that happens to have a
		// socket — which the e2e suite needs to stay deterministic, and
		// an operator may want for a server that should only ever pull.
		// The deployed systemd unit sets it by default, so the flag is
		// carried through to the source rather than collapsed into an
		// empty host: "turned off" and "none found" are different
		// answers to the user's question.
		cfg.dockerHost = ""
		cfg.dockerOff = true
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// resolveDockerHost implements the ARCHITECTURE §1.3 default: $DOCKER_HOST if
// set, otherwise the local socket when it exists, otherwise empty (phase 008
// reports docker_unavailable rather than failing at startup).
func resolveDockerHost() string {
	if env := os.Getenv("DOCKER_HOST"); env != "" {
		return env
	}
	if info, err := os.Stat(dockerSocketPath); err == nil && info.Mode()&os.ModeSocket != 0 {
		return "unix://" + dockerSocketPath
	}
	return ""
}

// outboundTransport builds the one transport this process ever uses to reach a
// registry.
//
// Everything that leaves here — the token dance, the manifest, every blob, and
// every CDN redirect hop — goes through its guarded dialer (ARCHITECTURE §7.2).
// No credential source is consulted anywhere (RESEARCH Q3), and the
// tests-only PermitLoopback escape hatch is deliberately left false. It is a
// function so a test can assert on the transport the command actually builds
// rather than on one a test constructed to look like it.
func outboundTransport() *safehttp.Transport {
	return safehttp.New(safehttp.Options{})
}

func (c *config) validate() error {
	if c.listen == "" {
		// flag.StringVar leaves an empty value alone, and net/http reads an
		// empty Addr as ":http" — silently port 80, which is never intended.
		return errors.New("--listen must not be empty")
	}
	if _, _, err := net.SplitHostPort(c.listen); err != nil {
		return fmt.Errorf("--listen %q is not a host:port address: %w", c.listen, err)
	}
	if c.dataDir == "" {
		return errors.New("--data-dir must not be empty")
	}
	if c.fixturesDir == "" {
		return errors.New("--fixtures-dir must not be empty")
	}
	if c.cacheMaxBytes <= 0 {
		return errors.New("--cache-max-bytes must be positive")
	}
	if c.maxPulls <= 0 {
		return errors.New("--max-concurrent-pulls must be positive")
	}
	if c.pullTimeout < 0 {
		return errors.New("--pull-timeout must not be negative")
	}
	if c.maxLayerEntries <= 0 {
		return errors.New("--max-layer-entries must be positive")
	}
	return checkDockerHost(c.dockerHost, c.dockerAllowTCP)
}

// checkDockerHost enforces the one place layerlens talks to something that the
// safehttp dialer never screens.
//
// A unix socket is a local-trust decision an operator makes by installing the
// daemon; "tcp://10.0.0.5:2375" is a network destination that would reach the
// Engine API with no address screen, no allowlist and no TLS requirement in
// front of it. Both spellings are accepted, but the second only when the
// operator says so out loud.
func checkDockerHost(host string, allowTCP bool) error {
	switch {
	case host == "":
		return nil
	case strings.HasPrefix(host, "unix://"), strings.HasPrefix(host, "/"):
		return nil
	case strings.HasPrefix(host, "tcp://"), strings.HasPrefix(host, "http://"), strings.HasPrefix(host, "https://"):
		if allowTCP {
			return nil
		}
		return fmt.Errorf(
			"--docker-host %q is not a unix socket; the Docker path is not screened by the outbound "+
				"address checks, so a TCP endpoint must be opted into with --docker-allow-tcp "+
				"(or set --docker-host=off, or unset DOCKER_HOST)", host)
	default:
		return fmt.Errorf(
			"--docker-host %q is not a supported endpoint: use a unix socket path, "+
				`"unix://<path>", or "off"`, host)
	}
}

func main() {
	cfg, err := parseFlags(os.Args[1:], os.Stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintf(os.Stderr, "layerlens: %v\n", err)
		os.Exit(2)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg, log, nil); err != nil {
		log.Error("layerlens exited with an error", "err", err)
		os.Exit(1)
	}
}

// run opens the durable cache, starts the fixture load, and serves until ctx
// is cancelled.
//
// The lifecycle is ARCHITECTURE §1.2: take the cache lock, load the vendored
// OCI-layout fixtures, then serve — with zero network involved. Fixture
// analysis runs in the background so a cold cache does not delay the listener,
// and /healthz reports "loading" until it finishes, which is what a supervisor
// or an e2e harness waits on.
func run(ctx context.Context, cfg *config, log *slog.Logger, ready func(net.Addr)) error {
	if cfg.uiDir != "" {
		// Checked before anything is opened: a typo here would
		// otherwise surface as every route answering 500 "SPA assets
		// are not built", after the cache lock had been taken.
		if err := checkUIDir(cfg.uiDir); err != nil {
			return err
		}
	}

	store, err := cachestore.Open(cachestore.Options{
		Root:     cfg.dataDir,
		MaxBytes: cfg.cacheMaxBytes,
		Logger:   log,
	})
	if err != nil {
		return err
	}
	defer func() {
		if err := store.Close(); err != nil {
			log.Error("release cache lock", "err", err)
		}
	}()
	log.Info("cache opened", "data-dir", store.Root(),
		"usedBytes", store.UsedBytes(), "maxBytes", store.MaxBytes())

	ingester := ingest.New(store, ingest.Options{Logger: log, MaxLayerEntries: cfg.maxLayerEntries})
	loaded := startFixtureLoad(ctx, cfg, log, ingester)

	transport := outboundTransport()
	defer transport.CloseIdleConnections()

	docker := ingest.NewDocker(ingest.DockerOptions{
		Host:     cfg.dockerHost,
		Disabled: cfg.dockerOff,
		Images:   store,
		Logger:   log,
	})
	defer func() {
		if err := docker.Close(); err != nil {
			log.Debug("close docker client", "err", err)
		}
	}()
	if cfg.dockerHost == "" {
		if cfg.dockerOff {
			log.Info("the Docker daemon source is turned off (--docker-host off)")
		} else {
			log.Info("no Docker endpoint configured; the daemon source will report itself unavailable")
		}
	} else {
		log.Info("docker endpoint configured", "host", cfg.dockerHost)
	}

	pulls := ingest.NewManager(ingest.ManagerOptions{
		Ingester: ingester,
		Registry: ingest.NewRegistry(ingest.RegistryOptions{
			Transport: transport,
			UserAgent: "layerlens/" + version,
		}),
		Docker:      docker,
		Allowlist:   imgref.Default(),
		Images:      store,
		Logger:      log,
		MaxInFlight: cfg.maxPulls,
		// The Manager reads zero as "use the default" and negative as
		// "no deadline", so an operator's explicit 0 is translated here
		// rather than silently becoming six hours.
		PullTimeout: pullTimeoutFor(cfg.pullTimeout),
	})

	opts := server.Options{
		Logger:            log,
		Images:            store,
		Layers:            store,
		Cache:             store,
		Ingester:          pulls,
		Version:           version,
		AllowedRegistries: allowedRegistries,
		Ready: func() bool {
			select {
			case <-loaded:
				return true
			default:
				return false
			}
		},
	}
	if cfg.uiDir != "" {
		log.Warn("serving SPA assets from disk instead of the embedded bundle", "ui-dir", cfg.uiDir)
		opts.UI = webui.HandlerFS(os.DirFS(cfg.uiDir))
	}
	return serve(ctx, cfg, log, server.New(opts), ready)
}

// pullTimeoutFor maps the flag's "0 means no deadline" onto the manager's
// "negative means no deadline".
func pullTimeoutFor(d time.Duration) time.Duration {
	if d == 0 {
		return -1
	}
	return d
}

// startFixtureLoad kicks off the vendored-fixture ingest and returns a channel
// closed once the server is ready to answer /healthz with "ok".
//
// Discovery is synchronous and analysis is not: whether there is anything to
// load is answered before the listener opens (so a deployment with no fixtures
// is ready immediately and cannot report a spurious "loading"), while the
// analysis itself — the part that is slow on a cold cache — happens in the
// background.
func startFixtureLoad(ctx context.Context, cfg *config, log *slog.Logger, ingester *ingest.Ingester) <-chan struct{} {
	done := make(chan struct{})
	layouts, err := ingest.DiscoverLayouts(cfg.fixturesDir)
	if err != nil || len(layouts) == 0 {
		// A missing or empty fixtures directory is a warning, not a
		// failure: the fixtures are a demo convenience, and refusing to
		// start without them would make the binary useless anywhere
		// they are not deployed.
		log.Warn("no demo fixtures loaded", "fixtures-dir", cfg.fixturesDir, "err", err)
		close(done)
		return done
	}

	log.Info("loading demo fixtures", "fixtures-dir", cfg.fixturesDir, "layouts", len(layouts))
	go func() {
		defer close(done)
		if _, err := ingester.LoadFixtures(ctx, cfg.fixturesDir); err != nil {
			// Still marked ready: an operator needs the API and the
			// error message far more than they need a process that
			// refuses to answer.
			log.Error("loading demo fixtures failed", "err", err)
		}
	}()
	return done
}

// serve starts the HTTP server and blocks until ctx is cancelled, then drains
// in-flight requests within shutdownTimeout. ready, when non-nil, is called
// with the bound address once the listener is open; tests use it to reach a
// server started on port 0.
func serve(ctx context.Context, cfg *config, log *slog.Logger, handler http.Handler, ready func(net.Addr)) error {
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
	}

	// Listening synchronously so an address-in-use error is returned from run
	// rather than racing the shutdown path.
	ln, err := net.Listen("tcp", cfg.listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.listen, err)
	}
	if ready != nil {
		ready(ln.Addr())
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", ln.Addr().String(), "data-dir", cfg.dataDir, "fixtures-dir", cfg.fixturesDir)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutdown signal received, draining connections")
	}

	// A second signal aborts the drain: an operator who has already asked once
	// should never be held for shutdownTimeout by a wedged connection. The
	// watch is registered here, after the first signal, so it can only fire on
	// the next one.
	forceCtx, forceStop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer forceStop()
	drained := make(chan struct{})
	go func() {
		select {
		case <-forceCtx.Done():
			log.Warn("second shutdown signal received, closing connections immediately")
			_ = srv.Close()
		case <-drained:
		}
	}()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	shutdownErr := srv.Shutdown(shutdownCtx)
	close(drained)
	if shutdownErr != nil {
		return fmt.Errorf("graceful shutdown: %w", shutdownErr)
	}
	return <-errCh
}

// checkUIDir verifies that --ui-dir names a directory holding the SPA shell.
func checkUIDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("--ui-dir %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("--ui-dir %q is not a directory", dir)
	}
	index := filepath.Join(dir, "index.html")
	if _, err := os.Stat(index); err != nil {
		return fmt.Errorf("--ui-dir %q has no index.html: %w", dir, err)
	}
	return nil
}
