// Package server wires the HTTP surface of layerlens: the JSON API under
// /api/v1, the health endpoint, and the embedded SPA fallback.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"runtime/debug"
	"strings"
	"time"

	"github.com/ericsuh/layerlens/internal/domain"
	"github.com/ericsuh/layerlens/internal/webui"
)

// APIPrefix is the versioned JSON API root. Breaking changes bump the version.
const APIPrefix = "/api/v1"

// apiNamespace is the reserved prefix that must never fall through to the SPA
// shell. It is deliberately unversioned: /api/v2 and a bare /api are equally
// reserved, and all of them answer with the JSON envelope.
const apiNamespace = "/api"

// CacheStats is the slice of the cache store /api/v1/meta reports on.
type CacheStats interface {
	UsedBytes() int64
	MaxBytes() int64
}

// Options configures a Server. The ingest-side options (pull manager, docker
// client) arrive with the phase that adds those endpoints.
type Options struct {
	// Logger receives server-side diagnostics. Defaults to slog.Default().
	Logger *slog.Logger
	// UI serves the single-page application. Defaults to the SPA embedded in
	// the binary; tests substitute an in-memory asset tree.
	UI http.Handler
	// Images lists and fetches analyzed images. Defaults to an empty store,
	// so a server built without one answers "no images" rather than panicking.
	Images domain.ImageStore
	// Layers yields per-layer changesets for tree assembly. Defaults to an
	// empty source.
	Layers domain.LayerIndexSource
	// Cache backs /api/v1/meta. Optional.
	Cache CacheStats
	// Ingester backs the pulls and docker endpoints. Defaults to one that
	// reports no Docker and refuses pulls, so a server built without it
	// (routing tests) still answers the whole surface.
	Ingester domain.Ingester
	// Ready gates /healthz. It reports false until the vendored fixtures
	// have been ingested (ARCHITECTURE §1.3). Nil means "always ready".
	Ready func() bool
	// Version is reported by /api/v1/meta.
	Version string
	// AllowedRegistries is reported by /api/v1/meta so the UI can name the
	// registries a pull may target without hardcoding the list.
	AllowedRegistries []string
	// ComparisonCacheSize caps the in-memory assembled-comparison LRU.
	// Defaults to DefaultComparisonCacheSize (§4.6).
	ComparisonCacheSize int
	// onComparisonAssembled is a test hook counting real assemblies, which
	// is how the single-flight property is asserted without timing.
	onComparisonAssembled func()
}

// Server is the root http.Handler for the application.
type Server struct {
	log         *slog.Logger
	ui          http.Handler
	mux         *http.ServeMux
	apiMux      *http.ServeMux
	handler     http.Handler
	images      domain.ImageStore
	layers      domain.LayerIndexSource
	ingester    domain.Ingester
	cache       CacheStats
	ready       func() bool
	version     string
	allowed     []string
	comparisons *comparisonCache
}

// New builds the routing tree: /healthz, the reserved /api namespace, and the
// embedded SPA for everything else, behind the security-header, access-log and
// panic-recovery middleware.
func New(opts Options) *Server {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	ui := opts.UI
	if ui == nil {
		ui = webui.Handler()
	}
	images := opts.Images
	if images == nil {
		images = emptyStore{}
	}
	layers := opts.Layers
	if layers == nil {
		layers = emptyStore{}
	}
	ingester := opts.Ingester
	if ingester == nil {
		ingester = unconfiguredIngester{}
	}
	size := opts.ComparisonCacheSize
	if size <= 0 {
		size = DefaultComparisonCacheSize
	}
	s := &Server{
		log:         log,
		ui:          ui,
		mux:         http.NewServeMux(),
		apiMux:      http.NewServeMux(),
		images:      images,
		layers:      layers,
		ingester:    ingester,
		cache:       opts.Cache,
		ready:       opts.Ready,
		version:     opts.Version,
		allowed:     opts.AllowedRegistries,
		comparisons: newComparisonCache(size, opts.onComparisonAssembled),
	}
	s.routes()
	// Outermost first: headers are set before any handler can write, the
	// access log wraps the recovered status, and recovery sits closest to the
	// handlers so a panic anywhere below it becomes a 500 envelope.
	s.handler = securityHeaders(s.logRequests(s.recoverPanics(http.HandlerFunc(s.dispatch))))
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	// Without this the SPA catch-all would answer a non-GET /healthz with the
	// HTML shell instead of a method error.
	s.mux.HandleFunc("/healthz", methodNotAllowed(http.MethodGet))
	// /healthz/anything is not a client route: answering it with the SPA shell
	// and a 200 would make a mistyped health check pass against HTML.
	s.mux.HandleFunc("/healthz/{rest...}", s.handleHealthzNotFound)
	s.mux.Handle("/", s.ui)

	// Exact patterns only: a trailing-slash subtree pattern would make
	// ServeMux emit an HTML redirect for the slash-less form and break
	// §6.1's "never HTML under /api" promise. Each route is registered
	// twice — once for GET, once method-less — so a wrong method gets the
	// JSON 405 envelope with an Allow header instead of ServeMux's own
	// plain-text 405.
	for _, route := range []struct {
		pattern  string
		handlers map[string]http.HandlerFunc
	}{
		{APIPrefix + "/images", map[string]http.HandlerFunc{http.MethodGet: s.handleImages}},
		{APIPrefix + "/images/{id}", map[string]http.HandlerFunc{http.MethodGet: s.handleImage}},
		{APIPrefix + "/diff/layers", map[string]http.HandlerFunc{http.MethodGet: s.handleDiffLayers}},
		{APIPrefix + "/diff/tree", map[string]http.HandlerFunc{http.MethodGet: s.handleDiffTree}},
		{APIPrefix + "/meta", map[string]http.HandlerFunc{http.MethodGet: s.handleMeta}},
		{APIPrefix + "/docker/images", map[string]http.HandlerFunc{http.MethodGet: s.handleDockerImages}},
		{APIPrefix + "/pulls", map[string]http.HandlerFunc{
			http.MethodGet:  s.handleListPulls,
			http.MethodPost: s.handleCreatePull,
		}},
		{APIPrefix + "/pulls/{id}", map[string]http.HandlerFunc{
			http.MethodGet:    s.handleGetPull,
			http.MethodDelete: s.handleCancelPull,
		}},
	} {
		allowed := make([]string, 0, len(route.handlers))
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
			handler, ok := route.handlers[method]
			if !ok {
				continue
			}
			allowed = append(allowed, method)
			s.apiMux.HandleFunc(method+" "+route.pattern, handler)
		}
		s.apiMux.HandleFunc(route.pattern, methodNotAllowed(allowed...))
	}
	s.apiMux.HandleFunc("/", s.handleAPINotFound)
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// dispatch routes a request, keeping the reserved /api namespace away from
// http.ServeMux's redirect behaviour.
func (s *Server) dispatch(w http.ResponseWriter, r *http.Request) {
	if !inAPINamespace(r.URL.Path) {
		s.mux.ServeHTTP(w, r)
		return
	}
	// ServeMux answers a non-canonical path (/api/v1//images) or a slash-less
	// subtree (/api) with a 30x whose body is HTML. Inside /api that would
	// hand a JSON client a redirect it cannot parse, so a non-canonical path
	// is simply not a route.
	if !isCanonicalPath(r.URL.Path) {
		s.handleAPINotFound(w, r)
		return
	}
	s.apiMux.ServeHTTP(w, r)
}

// inAPINamespace reports whether p is inside the reserved /api tree. A bare
// /api and /apifoo differ: only the former (and /api/...) is reserved.
func inAPINamespace(p string) bool {
	return p == apiNamespace || strings.HasPrefix(p, apiNamespace+"/")
}

// isCanonicalPath reports whether p is already in the form http.ServeMux would
// redirect it to: rooted, with no empty, "." or ".." segments.
func isCanonicalPath(p string) bool {
	if !strings.HasPrefix(p, "/") {
		return false
	}
	cleaned := path.Clean(p)
	if cleaned != "/" && strings.HasSuffix(p, "/") {
		cleaned += "/"
	}
	return cleaned == p
}

// securityHeaders applies the headers that every response needs regardless of
// which handler produced it. The SPA shell adds its own Content-Security-Policy
// in package webui; a JSON body does not need one.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// statusRecorder captures the response status and size for the access log and
// tells the panic recovery whether a response has already been committed.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusRecorder) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}

func (w *statusRecorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer, so flushing
// and hijacking keep working for the streaming endpoints of later phases.
func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// logRequests emits one access-log line per request.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		status := rec.status
		if status == 0 {
			// A handler that returned without writing anything still sends 200.
			status = http.StatusOK
		}
		level := slog.LevelInfo
		if status >= http.StatusInternalServerError {
			level = slog.LevelError
		}
		s.log.LogAttrs(r.Context(), level, "request",
			slog.String("method", r.Method),
			// Truncated: the path is attacker-controlled and unbounded.
			slog.String("path", truncateForReflection(r.URL.Path)),
			slog.Int("status", status),
			slog.Int("bytes", rec.bytes),
			slog.Duration("duration", time.Since(start)),
		)
	})
}

// recoverPanics turns a panic in any handler into the §6.1 internal/500
// envelope. The panic value and stack are logged; neither reaches the client.
func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			// ErrAbortHandler is net/http's documented way of aborting a
			// response; it is not a bug and must keep propagating.
			if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(rec)
			}
			s.log.Error("panic serving request",
				"method", r.Method,
				"path", truncateForReflection(r.URL.Path),
				"panic", fmt.Sprint(rec),
				"stack", string(debug.Stack()),
			)
			if sr, ok := w.(*statusRecorder); ok && sr.status != 0 {
				// Headers are already on the wire; the client sees a truncated
				// body, which is the best available signal.
				return
			}
			WriteError(w, http.StatusInternalServerError, CodeInternal,
				"internal server error", nil)
		}()
		next.ServeHTTP(w, r)
	})
}

// handleHealthz reports process readiness, gated on fixture ingestion having
// completed (ARCHITECTURE §1.3).
//
// The gate is what lets a supervisor, an e2e harness or a load balancer wait
// for a server that is *useful* rather than merely listening: until the
// vendored demo images are analyzed, /api/v1/images would answer an empty list
// and the golden workflow would look broken.
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if s.ready != nil && !s.ready() {
		w.WriteHeader(http.StatusServiceUnavailable)
		if _, err := w.Write([]byte("loading")); err != nil {
			s.log.Debug("write healthz response", "err", err)
		}
		return
	}
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("ok")); err != nil {
		s.log.Debug("write healthz response", "err", err)
	}
}

// emptyStore stands in for an unconfigured cache so that a Server built
// without one (unit tests of the routing tree, for instance) answers honestly
// instead of panicking.
type emptyStore struct{}

func (emptyStore) Images(context.Context) ([]domain.ImageRecord, error) {
	return nil, nil
}

func (emptyStore) Image(_ context.Context, id domain.Digest) (*domain.ImageRecord, error) {
	return nil, fmt.Errorf("%w: image %s", domain.ErrNotFound, id)
}

func (emptyStore) Touch(context.Context, domain.Digest) error { return nil }

func (emptyStore) LayerIndex(_ context.Context, diffID domain.Digest) (*domain.LayerIndex, error) {
	return nil, fmt.Errorf("%w: %s", domain.ErrNotIndexed, diffID)
}

// unconfiguredIngester stands in when no pull manager was wired: the routes
// still exist and answer honestly rather than 404ing or panicking.
type unconfiguredIngester struct{}

func (unconfiguredIngester) Start(context.Context, domain.IngestRequest) (domain.StartResult, error) {
	return domain.StartResult{}, errors.New("server: no ingest source is configured")
}

func (unconfiguredIngester) Status(domain.PullID) (*domain.PullStatus, error) {
	return nil, fmt.Errorf("%w: pulls are not configured", domain.ErrNotFound)
}

func (unconfiguredIngester) Pulls() []domain.PullStatus { return nil }

func (unconfiguredIngester) Cancel(domain.PullID) error {
	return fmt.Errorf("%w: pulls are not configured", domain.ErrNotFound)
}

func (unconfiguredIngester) ListDockerImages(context.Context) (domain.DockerListing, error) {
	return domain.DockerListing{
		Reason: "No Docker socket found at /var/run/docker.sock — the daemon source is unavailable on this server.",
		Images: []domain.DockerImageSummary{},
	}, nil
}

// handleHealthzNotFound answers /healthz/<anything>. Plain text, to match the
// plain-text /healthz it shadows.
func (s *Server) handleHealthzNotFound(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "not found", http.StatusNotFound)
}
