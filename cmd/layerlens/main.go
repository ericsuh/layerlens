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
	"net/http"
	"os"
	"os/signal"
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
	shutdownTimeout   = 15 * time.Second
)

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
	fs.StringVar(&cfg.dockerHost, "docker-host", os.Getenv("DOCKER_HOST"), "Docker endpoint to ingest images from (default $DOCKER_HOST, else the local socket)")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if rest := fs.Args(); len(rest) > 0 {
		return nil, fmt.Errorf("unexpected arguments: %v", rest)
	}
	if cfg.cacheMaxBytes <= 0 {
		return nil, errors.New("--cache-max-bytes must be positive")
	}
	return cfg, nil
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

	if err := run(ctx, cfg, log); err != nil {
		log.Error("layerlens exited with an error", "err", err)
		os.Exit(1)
	}
}

// run starts the HTTP server and blocks until ctx is cancelled, then drains
// in-flight requests within shutdownTimeout.
func run(ctx context.Context, cfg *config, log *slog.Logger) error {
	opts := server.Options{Logger: log}
	if cfg.uiDir != "" {
		log.Warn("serving SPA assets from disk instead of the embedded bundle", "ui-dir", cfg.uiDir)
		opts.UI = webui.HandlerFS(os.DirFS(cfg.uiDir))
	}

	srv := &http.Server{
		Addr:              cfg.listen,
		Handler:           server.New(opts),
		ReadHeaderTimeout: readHeaderTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.listen, "data-dir", cfg.dataDir, "fixtures-dir", cfg.fixturesDir)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
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

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	return <-errCh
}
