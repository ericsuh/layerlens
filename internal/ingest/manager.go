package ingest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/go-containerregistry/pkg/name"

	"github.com/ericsuh/layerlens/internal/analyze"
	"github.com/ericsuh/layerlens/internal/cachestore"
	"github.com/ericsuh/layerlens/internal/domain"
	"github.com/ericsuh/layerlens/internal/imgref"
)

// Failure codes carried inside PullStatus.Error. They extend the §6.1 table
// for the two outcomes only a pull can have.
const (
	// CodePullUpstreamDenied is the non-leaky collapse of 401/403/404.
	CodePullUpstreamDenied = "pull_upstream_denied"
	// CodePullRateLimited is a registry rate limit (DESIGN state #11).
	CodePullRateLimited = "pull_rate_limited"
	// CodeCacheFull mirrors the §6.1 code of the same name.
	CodeCacheFull = "cache_full"
	// CodeDockerUnavailable mirrors the §6.1 code of the same name.
	CodeDockerUnavailable = "docker_unavailable"
	// CodeInvalidReference mirrors the §6.1 code of the same name.
	CodeInvalidReference = "invalid_reference"
	// CodePullFailed is everything else. The message is generic; the cause
	// is logged server-side.
	CodePullFailed = "pull_failed"
	// CodePullTooLarge is an image that exceeds one of the structural
	// bounds on work: too many layers, or one layer with too many entries.
	// Distinct from CodeCacheFull, which is about the configured disk
	// budget, and from CodePullFailed, which says nothing actionable.
	CodePullTooLarge = "pull_too_large"
)

// maxRetainedPulls bounds the in-memory pull table. Statuses are process-local
// and lost on restart by design (ARCHITECTURE §10.4); this bounds them within
// one process's lifetime too.
//
// It is a cap on *terminal* history only: evictLocked will not cancel live
// work to honour it, so on its own it bounded nothing — 400 concurrent submits
// produced a 400-entry table. What actually bounds the table is
// DefaultMaxInFlightPulls below; this cap then trims the finished ones behind
// it, and the table's true ceiling is the sum of the two.
const maxRetainedPulls = 64

// DefaultMaxInFlightPulls bounds how many pulls may be running at once.
//
// Without it, every POST /api/v1/pulls bought the caller a goroutine and a real
// outbound registry session, with nothing in between: 400 concurrent submits
// produced 400 of each, aimed at five major registries from the server's own
// IP, and multiplied the cost of every other bound in the system (each in-flight
// pull can hold a layer index of up to analyze.DefaultMaxEntries in memory).
//
// The number is small on purpose. A pull is a foreground action a human is
// watching, and a machine that is already streaming four images is not made
// faster by starting a fifth; the surplus is refused with 429 rather than
// queued, so the client learns immediately instead of waiting behind work it
// cannot see.
const DefaultMaxInFlightPulls = 4

// DefaultPullTimeout is the wall-clock ceiling on a single pull.
//
// It is deliberately far larger than any real pull. The finding it answers is
// that a pull had no deadline at all — safehttp bounded only time-to-first-byte,
// and the manager ran on context.Background() — so a trickling upstream could
// hold a goroutine, a slot and a socket forever. The primary control for that
// is safehttp's stall detector (a *throughput* floor, which cannot misjudge a
// slow-but-progressing transfer); this is the backstop for what the detector
// cannot see, chiefly a docker-save stream over the unix socket.
//
// Six hours carries a 25 GiB image at ~1.2 MB/s sustained, which is slower than
// any link worth pulling over and roughly two orders of magnitude below what
// the design envelope assumes. A legitimate pull that hits this was never going
// to finish.
const DefaultPullTimeout = 6 * time.Hour

// ErrPullNotFound reports an unknown pull id.
var ErrPullNotFound = errors.New("ingest: no such pull")

// ErrTooManyPulls reports that the concurrent-pull limit is reached. It is the
// admission control the API surfaces as 429 (§6.1).
var ErrTooManyPulls = errors.New("ingest: too many pulls are already in flight")

// ManagerOptions configures a Manager.
type ManagerOptions struct {
	Ingester  *Ingester
	Registry  RegistrySource
	Docker    DockerSource
	Allowlist *imgref.Allowlist
	Images    domain.ImageStore
	Logger    *slog.Logger
	Now       func() time.Time
	// MaxInFlight bounds concurrent running pulls. Zero means
	// DefaultMaxInFlightPulls; negative means unbounded, which nothing in
	// production asks for.
	MaxInFlight int
	// PullTimeout is the wall-clock ceiling on one pull. Zero means
	// DefaultPullTimeout; negative means none.
	PullTimeout time.Duration
}

// Manager owns the lifecycle of every pull: validation, deduplication, the
// state machine, byte progress and cancellation (ARCHITECTURE §6.3).
//
// Statuses live in memory only. That is a deliberate scope choice, not an
// oversight: a pull is a foreground action a user is watching, and the durable
// artifact it produces — the per-layer indexes and the image record — is
// already durable in the cache store. A restart mid-pull loses the status and
// keeps every committed layer, so a retry resumes at layer granularity.
type Manager struct {
	ingester *Ingester
	registry RegistrySource
	docker   DockerSource
	allow    *imgref.Allowlist
	images   domain.ImageStore
	log      *slog.Logger
	now      func() time.Time

	maxInFlight int
	pullTimeout time.Duration

	mu       sync.Mutex
	pulls    map[domain.PullID]*pull
	order    []domain.PullID
	byKey    map[string]domain.PullID
	inFlight int
}

// NewManager builds a pull manager.
func NewManager(opts ManagerOptions) *Manager {
	m := &Manager{
		ingester:    opts.Ingester,
		registry:    opts.Registry,
		docker:      opts.Docker,
		allow:       opts.Allowlist,
		images:      opts.Images,
		log:         opts.Logger,
		now:         opts.Now,
		maxInFlight: opts.MaxInFlight,
		pullTimeout: opts.PullTimeout,
		pulls:       map[domain.PullID]*pull{},
		byKey:       map[string]domain.PullID{},
	}
	if m.maxInFlight == 0 {
		m.maxInFlight = DefaultMaxInFlightPulls
	}
	if m.pullTimeout == 0 {
		m.pullTimeout = DefaultPullTimeout
	}
	if m.log == nil {
		m.log = slog.Default()
	}
	if m.now == nil {
		m.now = time.Now
	}
	if m.allow == nil {
		m.allow = imgref.Default()
	}
	return m
}

// pull is one entry of the table. Byte counters are atomic and everything else
// is behind the mutex, so a 25 GiB stream updates progress without contending
// with the poller — no server-side throttle is needed because the hot path
// never takes a lock.
type pull struct {
	id        domain.PullID
	reference string
	source    string
	startedAt time.Time

	bytes analyze.ByteCounter
	// bytesBase is subtracted from the counter so a fallback path can
	// restart the numbers without a second counter.
	bytesBase    atomic.Int64
	skippedBytes atomic.Int64
	layersDone   atomic.Int64
	layersSkip   atomic.Int64

	mu           sync.Mutex
	state        string
	bytesTotal   *int64
	estimated    bool
	layersTotal  *int
	currentLayer *domain.PullProgress
	// layerStartOffset is the byte count when the current layer began, so
	// per-layer progress can be derived from the one shared counter.
	layerStartOffset int64
	imageID          domain.Digest
	failure          *domain.PullFailure
	terminal         bool

	cancel context.CancelFunc
	done   chan struct{}
}

// Start validates a request and begins (or joins) a pull.
//
// The allowlist verdict happens here, synchronously, before any socket is
// opened — that ordering is the §7.1 guarantee that a refused registry is
// never contacted at all.
func (m *Manager) Start(ctx context.Context, req domain.IngestRequest) (domain.StartResult, error) {
	switch req.Source {
	case domain.IngestSourceRegistry:
		return m.startRegistry(ctx, req)
	case domain.IngestSourceDocker:
		return m.startDocker(ctx, req)
	default:
		return domain.StartResult{}, fmt.Errorf("%w: unknown source %q", ErrInvalidSource, req.Source)
	}
}

// Manager is the production domain.Ingester.
var _ domain.Ingester = (*Manager)(nil)

// ErrInvalidSource reports a source other than "registry" or "docker".
var ErrInvalidSource = errors.New("ingest: unknown ingest source")

func (m *Manager) startRegistry(ctx context.Context, req domain.IngestRequest) (domain.StartResult, error) {
	ref, err := m.allow.Parse(req.Reference)
	if err != nil {
		return domain.StartResult{}, err
	}
	canonical := imgref.Canonical(ref)
	if id, ok := m.existing(ctx, domain.IngestSourceRegistry, canonical); ok {
		return domain.StartResult{ID: id}, nil
	}
	id, joined, err := m.launch(domain.IngestSourceRegistry, canonical, func(ctx context.Context, p *pull) error {
		return m.runRegistry(ctx, p, ref)
	})
	if err != nil {
		return domain.StartResult{}, err
	}
	return domain.StartResult{ID: id, Created: !joined}, nil
}

func (m *Manager) startDocker(ctx context.Context, req domain.IngestRequest) (domain.StartResult, error) {
	reference := strings.TrimSpace(req.Reference)
	if reference == "" {
		return domain.StartResult{}, fmt.Errorf("%w: the reference is empty", imgref.ErrInvalidReference)
	}
	// A local reference is not allowlisted — the daemon is local trust —
	// but it still has to be a reference, not an arbitrary string being
	// handed to the Engine API.
	if err := validLocalReference(reference); err != nil {
		return domain.StartResult{}, err
	}
	if m.docker == nil || m.docker.Host() == "" {
		return domain.StartResult{}, ErrDockerUnavailable
	}
	if id, ok := m.existing(ctx, domain.IngestSourceDocker, reference); ok {
		return domain.StartResult{ID: id}, nil
	}
	id, joined, err := m.launch(domain.IngestSourceDocker, reference, func(ctx context.Context, p *pull) error {
		return m.runDocker(ctx, p, reference)
	})
	if err != nil {
		return domain.StartResult{}, err
	}
	return domain.StartResult{ID: id, Created: !joined}, nil
}

// validLocalReference checks a reference destined for the Docker Engine API.
//
// Parsing is not enough on its own. go-containerregistry's grammar allows "."
// and ".." as repository path segments, and the Engine client builds its URL by
// concatenating the reference into "/images/<ref>/json" — so a reference like
// "../../containers" would traverse out of the images endpoint and address a
// different part of the API. Nothing useful is reachable that way (the path
// always ends in a literal segment and carries no query parameters), but a
// reference with a traversal segment in it is not a reference anyone means to
// type, and refusing it is cheaper than reasoning about what it could reach.
func validLocalReference(reference string) error {
	if _, err := name.ParseReference(reference); err != nil {
		return fmt.Errorf("%w: %s", imgref.ErrInvalidReference, reference)
	}
	// Checked on the raw string, not on the parsed repository, for the
	// reason ValidatePathSegments documents. The registry path applies the
	// same rule inside imgref.Parse.
	return imgref.ValidatePathSegments(reference)
}

// existing implements the idempotency of §6.3: an identical request that is
// already in flight, or whose image is already cached, returns the pull that
// covers it instead of starting a second one.
func (m *Manager) existing(ctx context.Context, source, reference string) (domain.PullID, bool) {
	key := source + "|" + reference
	m.mu.Lock()
	if id, ok := m.byKey[key]; ok {
		if p, live := m.pulls[id]; live && !p.isTerminal() {
			m.mu.Unlock()
			return id, true
		}
	}
	m.mu.Unlock()

	// Already analyzed: report a finished pull rather than re-fetching an
	// image the cache already holds.
	if rec, ok := m.cachedByRef(ctx, reference); ok {
		return m.record(source, reference, rec.ID), true
	}
	return "", false
}

// cachedByRef finds an analyzed image carrying this reference.
func (m *Manager) cachedByRef(ctx context.Context, reference string) (*domain.ImageRecord, bool) {
	if m.images == nil {
		return nil, false
	}
	records, err := m.images.Images(ctx)
	if err != nil {
		return nil, false
	}
	for i := range records {
		for _, ref := range records[i].RefNames {
			if ref == reference {
				return &records[i], true
			}
		}
	}
	return nil, false
}

// record inserts a pull that is already finished.
//
// It takes no admission slot: nothing is running, so there is nothing for the
// concurrency limit to protect against.
func (m *Manager) record(source, reference string, id domain.Digest) domain.PullID {
	m.mu.Lock()
	// No cancel function: there is nothing running to cancel.
	p := m.newPullLocked(source, reference, nil)
	m.mu.Unlock()
	p.mu.Lock()
	p.state = domain.PullDone
	p.imageID = id
	p.terminal = true
	p.mu.Unlock()
	close(p.done)
	return p.id
}

// launch registers a new pull and runs it on its own context.
//
// The context is deliberately not the HTTP request's: a pull outlives the POST
// that started it, and tying it to the request would cancel a 25 GiB download
// the moment the browser got its 202. It carries a generous wall-clock
// deadline instead (see DefaultPullTimeout).
//
// It reports joined=true when an identical pull was already in flight, in
// which case that pull's id comes back and no admission slot is consumed —
// idempotent resubmission is free by construction, because the duplicate check
// and the slot acquisition happen under one hold of the same lock.
func (m *Manager) launch(source, reference string, run func(context.Context, *pull) error) (domain.PullID, bool, error) {
	// The cancel function is installed before the pull is published, not
	// after: a DELETE that arrived in between would otherwise read the
	// field while this goroutine wrote it, and would mark the pull
	// cancelled without actually stopping the stream.
	ctx, cancel := m.pullContext()
	p, joined, err := m.admit(source, reference, cancel)
	if err != nil || joined {
		// Nothing was started, so the context this call built is dead
		// on arrival; releasing it here is what keeps a refused submit
		// from leaking a timer.
		cancel()
		if err != nil {
			return "", false, err
		}
		return p.id, true, nil
	}

	go func() {
		defer close(p.done)
		defer cancel()
		defer m.releaseSlot()
		err := run(ctx, p)
		p.finish(ctx, err)
		if err != nil && !errors.Is(err, context.Canceled) {
			m.log.Warn("pull failed", "id", p.id, "reference", reference, "err", err)
		}
	}()
	return p.id, false, nil
}

// pullContext builds the context one pull runs on.
//
// The deadline is a backstop, not a service-level expectation: the thing that
// actually kills a hostile trickle is safehttp's stall detector, which needs no
// guess about how long legitimate work takes. This exists for the cases the
// stall detector cannot see — a docker-save stream, or an upstream that keeps
// meeting the throughput floor forever — and is set generously enough that a
// real 25 GiB pull never reaches it (see DefaultPullTimeout).
func (m *Manager) pullContext() (context.Context, context.CancelFunc) {
	if m.pullTimeout <= 0 {
		return context.WithCancel(context.Background())
	}
	return context.WithTimeout(context.Background(), m.pullTimeout)
}

// admit is the single lock hold that decides a submission's fate: join an
// identical in-flight pull, take an admission slot, or be refused.
//
// Doing all three here is what makes the guarantees composable. Checking for a
// duplicate and then acquiring a slot in two steps would let two identical
// concurrent submits both miss and both take a slot, which is the case a burst
// of retries from one browser produces.
func (m *Manager) admit(source, reference string, cancel context.CancelFunc) (*pull, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id, ok := m.byKey[source+"|"+reference]; ok {
		if existing, live := m.pulls[id]; live && !existing.isTerminal() {
			return existing, true, nil
		}
	}
	if m.maxInFlight > 0 && m.inFlight >= m.maxInFlight {
		return nil, false, fmt.Errorf("%w: %d are running and the limit is %d",
			ErrTooManyPulls, m.inFlight, m.maxInFlight)
	}
	m.inFlight++
	return m.newPullLocked(source, reference, cancel), false, nil
}

// releaseSlot returns an admission slot once the run goroutine has exited.
func (m *Manager) releaseSlot() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.inFlight > 0 {
		m.inFlight--
	}
	// The table could not be trimmed while this pull was live; now that it
	// is terminal, the retention cap can take effect.
	m.evictLocked()
}

// InFlight reports how many pulls are currently running. Exported for tests
// and for the operator-facing log line; it is not on the wire.
func (m *Manager) InFlight() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inFlight
}

// newPullLocked allocates an entry and evicts the oldest terminal ones. The
// caller holds m.mu.
func (m *Manager) newPullLocked(source, reference string, cancel context.CancelFunc) *pull {
	p := &pull{
		id:        newPullID(),
		reference: reference,
		source:    source,
		startedAt: m.now(),
		state:     domain.PullResolving,
		done:      make(chan struct{}),
		cancel:    cancel,
	}
	m.pulls[p.id] = p
	m.order = append(m.order, p.id)
	m.byKey[source+"|"+reference] = p.id
	m.evictLocked()
	return p
}

// newPullID mints an unguessable pull id.
//
// A sequence number would do for uniqueness — ids are process-local and the
// table is behind a mutex. Randomness buys something else: any client can
// cancel any pull, and on a trusted network that is acceptable, but a
// guessable "p<unixnano>-<seq>" makes it acceptable *and* trivial. Sixteen
// bytes from crypto/rand costs nothing and removes the guessing half.
// (GET /api/v1/pulls still lists every id, so this is a speed bump, not
// authorization; §7.3 already specified server-generated random hex.)
func newPullID() domain.PullID {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand.Read never fails on any platform layerlens runs
		// on; if it somehow did, refusing to mint an id would take the
		// server down for a cosmetic property.
		panic("ingest: crypto/rand is unavailable: " + err.Error())
	}
	return domain.PullID("p" + hex.EncodeToString(b[:]))
}

// evictLocked drops the oldest terminal pulls once the table is over its cap.
func (m *Manager) evictLocked() {
	for len(m.order) > maxRetainedPulls {
		for i, id := range m.order {
			p, ok := m.pulls[id]
			if !ok || p.isTerminal() {
				m.order = append(m.order[:i], m.order[i+1:]...)
				if ok {
					delete(m.pulls, id)
					key := p.source + "|" + p.reference
					if m.byKey[key] == id {
						delete(m.byKey, key)
					}
				}
				break
			}
			if i == len(m.order)-1 {
				// Every retained pull is still running; the cap
				// yields rather than cancelling live work.
				return
			}
		}
	}
}

// Status returns one pull's snapshot.
func (m *Manager) Status(id domain.PullID) (*domain.PullStatus, error) {
	m.mu.Lock()
	p, ok := m.pulls[id]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrPullNotFound, id)
	}
	status := p.snapshot()
	return &status, nil
}

// Pulls lists every known pull, newest first.
func (m *Manager) Pulls() []domain.PullStatus {
	m.mu.Lock()
	ids := append([]domain.PullID(nil), m.order...)
	table := make(map[domain.PullID]*pull, len(m.pulls))
	for id, p := range m.pulls {
		table[id] = p
	}
	m.mu.Unlock()

	out := make([]domain.PullStatus, 0, len(ids))
	for i := len(ids) - 1; i >= 0; i-- {
		if p, ok := table[ids[i]]; ok {
			out = append(out, p.snapshot())
		}
	}
	return out
}

// Cancel stops a running pull. Committed per-layer indexes survive: the
// durable checkpoint unit is the layer, so a retry resumes rather than
// restarts (§4.1).
func (m *Manager) Cancel(id domain.PullID) error {
	m.mu.Lock()
	p, ok := m.pulls[id]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrPullNotFound, id)
	}
	p.markCancelled()
	if p.cancel != nil {
		p.cancel()
	}
	return nil
}

// ListDockerImages implements domain.Ingester.
func (m *Manager) ListDockerImages(ctx context.Context) (domain.DockerListing, error) {
	if m.docker == nil {
		return domain.DockerListing{
			Reason: "No Docker socket found at /var/run/docker.sock — the daemon source is unavailable on this server.",
			Images: []domain.DockerImageSummary{},
		}, nil
	}
	return m.docker.List(ctx)
}

// runRegistry performs a registry pull.
func (m *Manager) runRegistry(ctx context.Context, p *pull, ref domain.ImageRef) error {
	if m.registry == nil {
		return errors.New("ingest: no registry source configured")
	}
	p.Phase(PhaseResolving)
	remote, err := m.registry.Open(ctx, ref)
	if err != nil {
		return err
	}
	defer remote.Close()

	res, err := m.ingester.Ingest(ctx, remote.Image, Meta{
		RefNames:       []string{imgref.Canonical(ref)},
		Source:         domain.SourceRegistry,
		ManifestDigest: remote.ManifestDigest,
		Progress:       p,
	})
	if err != nil {
		return err
	}
	p.setImage(res.Record.ID)
	return nil
}

// runDocker performs a daemon ingest, preferring the registry when the local
// image carries a digest for an allowlisted one.
func (m *Manager) runDocker(ctx context.Context, p *pull, reference string) error {
	if m.docker == nil {
		return ErrDockerUnavailable
	}
	p.Phase(PhaseResolving)
	inspect, err := m.docker.Inspect(ctx, reference)
	if err != nil {
		return err
	}

	// The registry path is preferred when it is available: the bytes are
	// identical, but it can skip layers already indexed by DiffID instead
	// of draining them out of a sequential save stream (DECISIONS A2).
	if m.registry != nil {
		if ref, ok := m.registryRefFor(inspect); ok {
			m.log.Info("pull: using the registry for a local image",
				"reference", reference, "registryRef", imgref.Canonical(ref))
			err := m.runRegistryForDocker(ctx, p, ref, reference)
			if err == nil {
				return nil
			}
			if ctx.Err() != nil {
				return err
			}
			// The shortcut is an optimization, not a requirement:
			// the daemon still has the bytes, so a registry that
			// refuses us falls back rather than failing the pull.
			m.log.Warn("pull: registry shortcut failed, falling back to docker save",
				"reference", reference, "err", err)
			p.reset()
		}
	}

	p.Totals(inspect.SizeBytes, len(inspect.DiffIDs), true)
	// The daemon's own id, not the string the user typed: it is a digest
	// the daemon just handed us, so nothing user-shaped reaches the Engine
	// API path a second time.
	saveTarget := reference
	if inspect.ID != "" {
		saveTarget = inspect.ID
	}
	stream, err := m.docker.Save(ctx, saveTarget)
	if err != nil {
		return err
	}
	defer func() { _ = stream.Close() }()

	res, err := m.ingester.IngestDockerSave(ctx, stream, Meta{
		RefNames: []string{reference},
		Source:   domain.SourceDocker,
		Progress: p,
	})
	if err != nil {
		return err
	}
	p.setImage(res.Record.ID)
	return nil
}

// runRegistryForDocker runs the registry path but records the image under the
// local reference the user selected.
func (m *Manager) runRegistryForDocker(ctx context.Context, p *pull, ref domain.ImageRef, localRef string) error {
	remote, err := m.registry.Open(ctx, ref)
	if err != nil {
		return err
	}
	defer remote.Close()
	res, err := m.ingester.Ingest(ctx, remote.Image, Meta{
		RefNames:       []string{localRef},
		Source:         domain.SourceDocker,
		ManifestDigest: remote.ManifestDigest,
		Progress:       p,
	})
	if err != nil {
		return err
	}
	p.setImage(res.Record.ID)
	return nil
}

// registryRefFor finds an allowlisted RepoDigest for a local image.
func (m *Manager) registryRefFor(inspect *DockerInspect) (domain.ImageRef, bool) {
	for _, repoDigest := range inspect.RepoDigests {
		ref, err := m.allow.Parse(repoDigest)
		if err != nil {
			continue
		}
		if ref.Digest == "" {
			continue
		}
		return ref, true
	}
	return domain.ImageRef{}, false
}

// --- Reporter implementation ----------------------------------------------

func (p *pull) Bytes() *analyze.ByteCounter { return &p.bytes }

func (p *pull) Phase(phase string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.terminal {
		return
	}
	switch phase {
	case PhaseResolving:
		p.state = domain.PullResolving
	default:
		p.state = domain.PullRunning
	}
}

func (p *pull) Totals(bytesTotal int64, layersTotal int, estimated bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if bytesTotal > 0 {
		total := bytesTotal
		p.bytesTotal = &total
	}
	if layersTotal > 0 {
		count := layersTotal
		p.layersTotal = &count
	}
	p.estimated = estimated
}

func (p *pull) LayerStarted(index int, digest domain.Digest, size int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	current := &domain.PullProgress{Index: index, Digest: string(digest)}
	if size > 0 {
		total := size
		current.BytesTotal = &total
	}
	current.BytesDone = 0
	p.currentLayer = current
	p.layerStartOffset = p.bytes.Load() - p.bytesBase.Load()
}

func (p *pull) LayerFinished(_ int, skipped bool, size int64) {
	p.layersDone.Add(1)
	if skipped {
		p.layersSkip.Add(1)
		// A skipped layer costs nothing but still advances the bar: the
		// denominator came from the manifest and includes it.
		p.skippedBytes.Add(size)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.currentLayer = nil
}

// reset clears progress so a fallback path starts from zero rather than
// continuing somebody else's numbers.
func (p *pull) reset() {
	p.layersDone.Store(0)
	p.layersSkip.Store(0)
	p.skippedBytes.Store(0)
	p.bytesBase.Store(p.bytes.Load())
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bytesTotal = nil
	p.layersTotal = nil
	p.currentLayer = nil
}

// --- state machine ---------------------------------------------------------

func (p *pull) isTerminal() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.terminal
}

// markCancelled moves a live pull to the cancelled state. It is idempotent and
// never overwrites a pull that has already finished.
func (p *pull) markCancelled() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.terminal {
		return
	}
	p.state = domain.PullCancelled
	p.terminal = true
	p.currentLayer = nil
}

// finish records the outcome of the run.
func (p *pull) finish(ctx context.Context, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.terminal {
		// Already cancelled by DELETE; the run's own error is the
		// cancellation and must not overwrite the state the user asked
		// for.
		return
	}
	p.terminal = true
	p.currentLayer = nil
	switch {
	case err == nil:
		p.state = domain.PullDone
	case errors.Is(err, context.DeadlineExceeded), errors.Is(ctx.Err(), context.DeadlineExceeded):
		// The wall-clock backstop expired. That is a failure, not a
		// cancellation: nobody asked for it, and reporting it as
		// "cancelled" would tell the user they did.
		p.state = domain.PullFailed
		p.failure = classifyPullFailure(context.DeadlineExceeded)
	case errors.Is(err, context.Canceled), ctx.Err() != nil:
		p.state = domain.PullCancelled
	default:
		p.state = domain.PullFailed
		p.failure = classifyPullFailure(err)
	}
}

// setImage records the analyzed image id on success.
func (p *pull) setImage(id domain.Digest) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.imageID = id
}

// snapshot builds the wire status.
func (p *pull) snapshot() domain.PullStatus {
	bytesDone := p.bytes.Load() - p.bytesBase.Load() + p.skippedBytes.Load()
	if bytesDone < 0 {
		bytesDone = 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	status := domain.PullStatus{
		ID:             p.id,
		Reference:      p.reference,
		Source:         p.source,
		State:          p.state,
		StartedAt:      p.startedAt,
		BytesTotal:     p.bytesTotal,
		BytesDone:      bytesDone,
		BytesEstimated: p.estimated,
		LayersTotal:    p.layersTotal,
		LayersDone:     int(p.layersDone.Load()),
		LayersSkipped:  int(p.layersSkip.Load()),
		ImageID:        p.imageID,
		Error:          p.failure,
	}
	if p.currentLayer != nil {
		current := *p.currentLayer
		current.BytesDone = bytesDone - p.layerStartOffset
		if current.BytesDone < 0 {
			current.BytesDone = 0
		}
		status.CurrentLayer = &current
	}
	return status
}

// classifyPullFailure maps an ingest error onto the code and the message the
// UI shows verbatim (DESIGN §9 states #9–#13).
func classifyPullFailure(err error) *domain.PullFailure {
	switch {
	case errors.Is(err, ErrUpstreamDenied):
		return &domain.PullFailure{
			Code: CodePullUpstreamDenied,
			Message: "That image was not found, or it requires authentication. " +
				"layerlens supports anonymous public pulls only.",
		}
	case errors.Is(err, ErrRateLimited):
		return &domain.PullFailure{
			Code:    CodePullRateLimited,
			Message: "The registry's rate limit was reached — try again later.",
		}
	case errors.Is(err, cachestore.ErrCacheFull):
		return &domain.PullFailure{
			Code:    CodeCacheFull,
			Message: "This image does not fit in the server's cache budget.",
		}
	case errors.Is(err, ErrDockerUnavailable):
		return &domain.PullFailure{
			Code:    CodeDockerUnavailable,
			Message: "The Docker daemon is not reachable.",
		}
	case errors.Is(err, ErrTooManyLayers):
		return &domain.PullFailure{
			Code:    CodePullTooLarge,
			Message: "That image has more layers than layerlens will analyze.",
		}
	case errors.Is(err, analyze.ErrTooManyEntries):
		return &domain.PullFailure{
			Code: CodePullTooLarge,
			Message: "One of that image's layers contains more files than layerlens will index. " +
				"Analyzing it would cost more memory than this server is allowed to spend.",
		}
	case errors.Is(err, context.DeadlineExceeded):
		return &domain.PullFailure{
			Code:    CodePullFailed,
			Message: "The pull took longer than this server allows and was stopped.",
		}
	case errors.Is(err, ErrNoAmd64Manifest):
		return &domain.PullFailure{
			Code:    CodePullFailed,
			Message: "That image has no linux/amd64 variant, which is the only platform layerlens analyzes.",
		}
	default:
		// Generic on purpose: the cause is in the server log, and an
		// upstream's error text is not something to render in a browser.
		return &domain.PullFailure{
			Code:    CodePullFailed,
			Message: "The image could not be analyzed. See the server log for details.",
		}
	}
}
