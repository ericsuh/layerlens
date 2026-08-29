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
	"syscall"
	"time"

	"github.com/ericsuh/layerlens/internal/server"
	"github.com/ericsuh/layerlens/internal/webui"
)

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

// dockerSocketPath is the local endpoint probed when neither --docker-host nor
// $DOCKER_HOST is set (ARCHITECTURE §1.3). A variable so tests can point the
// autodetect at a path they control.
var dockerSocketPath = "/var/run/docker.sock"

// config holds the process-level flags described in ARCHITECTURE §1.3. Flags
// whose subsystems arrive in later phases are parsed and carried on the config
// now so the command-line surface stays stable.
type config struct {
	listen        string
	dataDir       string
	cacheMaxBytes int64
	fixturesDir   string
	dockerHost    string
	uiDir         string
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
	fs.StringVar(&cfg.dockerHost, "docker-host", "", "Docker endpoint to ingest images from (default $DOCKER_HOST, else the local socket if present)")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if rest := fs.Args(); len(rest) > 0 {
		return nil, fmt.Errorf("unexpected arguments: %v", rest)
	}
	if cfg.dockerHost == "" {
		cfg.dockerHost = resolveDockerHost()
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
	return nil
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

// run builds the HTTP handler and serves it until ctx is cancelled.
func run(ctx context.Context, cfg *config, log *slog.Logger, ready func(net.Addr)) error {
	handler, err := buildHandler(cfg, log)
	if err != nil {
		return err
	}
	return serve(ctx, cfg, log, handler, ready)
}

// buildHandler assembles the root handler from cfg.
func buildHandler(cfg *config, log *slog.Logger) (http.Handler, error) {
	opts := server.Options{Logger: log}
	if cfg.uiDir != "" {
		// Fail fast: a typo here would otherwise surface as every route
		// answering 500 "SPA assets are not built".
		if err := checkUIDir(cfg.uiDir); err != nil {
			return nil, err
		}
		log.Warn("serving SPA assets from disk instead of the embedded bundle", "ui-dir", cfg.uiDir)
		opts.UI = webui.HandlerFS(os.DirFS(cfg.uiDir))
	}
	return server.New(opts), nil
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
