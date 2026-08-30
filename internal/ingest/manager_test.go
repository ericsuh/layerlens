package ingest_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/cachestore"
	"github.com/ericsuh/layerlens/internal/domain"
	"github.com/ericsuh/layerlens/internal/imgref"
	"github.com/ericsuh/layerlens/internal/ingest"
)

// fakeRegistrySource stands in for a real registry so the manager's state
// machine can be driven deterministically.
type fakeRegistrySource struct {
	img v1.Image
	err error
	// gate, when non-nil, blocks the layer at index gateLayer until the
	// pull's own context is cancelled — which is exactly how a real HTTP
	// body behaves when a pull is cancelled mid-stream.
	gate      chan struct{}
	gateLayer int
	opens     atomic.Int64
}

func (f *fakeRegistrySource) Open(ctx context.Context, _ domain.ImageRef) (*ingest.RemoteImage, error) {
	f.opens.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	img := f.img
	if f.gate != nil {
		img = &gatedImage{Image: f.img, ctx: ctx, gate: f.gate, layer: f.gateLayer}
	}
	manifest, err := f.img.Manifest()
	if err != nil {
		return nil, err
	}
	out := &ingest.RemoteImage{Image: img, LayerCount: len(manifest.Layers)}
	for _, l := range manifest.Layers {
		out.BytesTotal += l.Size
	}
	return out, nil
}

// gatedImage delays one layer's stream until the pull is cancelled.
type gatedImage struct {
	v1.Image
	ctx   context.Context
	gate  chan struct{}
	layer int
}

func (g *gatedImage) Layers() ([]v1.Layer, error) {
	layers, err := g.Image.Layers()
	if err != nil {
		return nil, err
	}
	if g.layer < len(layers) {
		layers[g.layer] = &gatedLayer{Layer: layers[g.layer], ctx: g.ctx, gate: g.gate}
	}
	return layers, nil
}

type gatedLayer struct {
	v1.Layer
	ctx  context.Context
	gate chan struct{}
}

func (g *gatedLayer) Compressed() (io.ReadCloser, error) {
	close(g.gate)
	return io.NopCloser(blockingReader{ctx: g.ctx}), nil
}

type blockingReader struct{ ctx context.Context }

func (b blockingReader) Read([]byte) (int, error) {
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func newManager(t *testing.T, store *cachestore.Store, opts ingest.ManagerOptions) *ingest.Manager {
	t.Helper()
	if opts.Ingester == nil {
		opts.Ingester = newIngester(store)
	}
	if opts.Images == nil {
		opts.Images = store
	}
	opts.Logger = discard()
	return ingest.NewManager(opts)
}

// requireNoPullsInFlight waits for every admission slot to come back. A pull's
// status goes terminal a moment before its goroutine returns the slot — the
// status is written by the run, the slot is released by the deferred cleanup —
// so this is a state transition to wait for, not one to assert on directly.
func requireNoPullsInFlight(t *testing.T, m *ingest.Manager) {
	t.Helper()
	require.Eventually(t, func() bool { return m.InFlight() == 0 }, 10*time.Second, 2*time.Millisecond,
		"every slot is returned when the run goroutine exits")
}

// waitFor polls until the predicate holds; every wait in this file is on a
// state transition, never on a duration.
func waitFor(t *testing.T, m *ingest.Manager, id domain.PullID, ok func(*domain.PullStatus) bool) *domain.PullStatus {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		status, err := m.Status(id)
		require.NoError(t, err)
		if ok(status) {
			return status
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting; last status %+v", status)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func terminal(s *domain.PullStatus) bool {
	switch s.State {
	case domain.PullDone, domain.PullFailed, domain.PullCancelled:
		return true
	}
	return false
}

func TestPullRegistryHappyPath(t *testing.T) {
	store := newStore(t, t.TempDir(), 1<<30)
	source := &fakeRegistrySource{img: fixtureImage(t, "example", "example:v1")}
	m := newManager(t, store, ingest.ManagerOptions{Registry: source})

	started, err := m.Start(context.Background(), domain.IngestRequest{
		Source: domain.IngestSourceRegistry, Reference: "ghcr.io/demo/app:v1",
	})
	require.NoError(t, err)
	assert.True(t, started.Created)

	status := waitFor(t, m, started.ID, terminal)
	require.Equal(t, domain.PullDone, status.State, "%+v", status.Error)
	assert.NotEmpty(t, status.ImageID)
	assert.Equal(t, "ghcr.io/demo/app:v1", status.Reference)
	require.NotNil(t, status.BytesTotal)
	assert.Equal(t, *status.BytesTotal, status.BytesDone,
		"a finished pull has consumed exactly the bytes the manifest declared")
	require.NotNil(t, status.LayersTotal)
	assert.Equal(t, *status.LayersTotal, status.LayersDone)
	assert.Nil(t, status.CurrentLayer)

	// The image is now selectable.
	rec, err := store.Image(context.Background(), status.ImageID)
	require.NoError(t, err)
	assert.Equal(t, []string{"ghcr.io/demo/app:v1"}, rec.RefNames)
	assert.Equal(t, domain.SourceRegistry, rec.Source)

	assert.Len(t, m.Pulls(), 1)
}

func TestPullIsIdempotent(t *testing.T) {
	store := newStore(t, t.TempDir(), 1<<30)
	gate := make(chan struct{})
	source := &fakeRegistrySource{img: fixtureImage(t, "example", "example:v1"), gate: gate, gateLayer: 0}
	m := newManager(t, store, ingest.ManagerOptions{Registry: source})
	ctx := context.Background()

	first, err := m.Start(ctx, domain.IngestRequest{Source: domain.IngestSourceRegistry, Reference: "ghcr.io/demo/app:v1"})
	require.NoError(t, err)
	<-gate // the pull is now mid-stream

	// The same image spelled differently is the same pull: the key is the
	// canonical reference, not the string the user typed.
	second, err := m.Start(ctx, domain.IngestRequest{Source: domain.IngestSourceRegistry, Reference: "  ghcr.io/demo/app:v1  "})
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID)
	assert.False(t, second.Created, "a duplicate submission joins the pull in flight rather than starting a second one")
	assert.Equal(t, int64(1), source.opens.Load())

	require.NoError(t, m.Cancel(first.ID))
	waitFor(t, m, first.ID, terminal)
}

func TestPullOfAnAlreadyAnalyzedImageReturnsItImmediately(t *testing.T) {
	store := newStore(t, t.TempDir(), 1<<30)
	source := &fakeRegistrySource{img: fixtureImage(t, "example", "example:v1")}
	m := newManager(t, store, ingest.ManagerOptions{Registry: source})
	ctx := context.Background()

	first, err := m.Start(ctx, domain.IngestRequest{Source: domain.IngestSourceRegistry, Reference: "ghcr.io/demo/app:v1"})
	require.NoError(t, err)
	waitFor(t, m, first.ID, terminal)

	second, err := m.Start(ctx, domain.IngestRequest{Source: domain.IngestSourceRegistry, Reference: "ghcr.io/demo/app:v1"})
	require.NoError(t, err)
	assert.False(t, second.Created, "the cache already holds it, so nothing is fetched again")
	status, err := m.Status(second.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.PullDone, status.State)
	assert.NotEmpty(t, status.ImageID)
	assert.Equal(t, int64(1), source.opens.Load(), "the registry was contacted exactly once")
}

// Cancellation is the §4.1 checkpoint story: staging is discarded, committed
// layer indexes are kept, and a retry resumes at layer granularity.
func TestCancelMidLayerKeepsCommittedLayers(t *testing.T) {
	store := newStore(t, t.TempDir(), 1<<30)
	gate := make(chan struct{})
	img := fixtureImage(t, "example", "example:v1")
	source := &fakeRegistrySource{img: img, gate: gate, gateLayer: 2}
	m := newManager(t, store, ingest.ManagerOptions{Registry: source})
	ctx := context.Background()

	first, err := m.Start(ctx, domain.IngestRequest{Source: domain.IngestSourceRegistry, Reference: "ghcr.io/demo/app:v1"})
	require.NoError(t, err)
	<-gate

	require.NoError(t, m.Cancel(first.ID))
	status := waitFor(t, m, first.ID, terminal)
	assert.Equal(t, domain.PullCancelled, status.State)
	assert.Empty(t, status.ImageID)
	assert.Nil(t, status.Error, "a cancellation the user asked for is not an error")

	cfg, err := img.ConfigFile()
	require.NoError(t, err)
	committed := 0
	for _, diffID := range cfg.RootFS.DiffIDs {
		if store.HasLayer(domain.Digest(diffID.String())) {
			committed++
		}
	}
	assert.Equal(t, 2, committed, "the layers that finished before the cancel are durable")
	_, err = store.Image(ctx, "")
	assert.Error(t, err, "and no image record was written")

	// The retry resumes: the committed layers cost nothing the second time.
	source.gate = nil
	retry, err := m.Start(ctx, domain.IngestRequest{Source: domain.IngestSourceRegistry, Reference: "ghcr.io/demo/app:v1"})
	require.NoError(t, err)
	assert.True(t, retry.Created)
	done := waitFor(t, m, retry.ID, terminal)
	require.Equal(t, domain.PullDone, done.State, "%+v", done.Error)
	assert.Equal(t, 2, done.LayersSkipped, "resumed at layer granularity")
}

func TestCancelOfAFinishedPullDoesNotRewriteIt(t *testing.T) {
	store := newStore(t, t.TempDir(), 1<<30)
	m := newManager(t, store, ingest.ManagerOptions{
		Registry: &fakeRegistrySource{img: fixtureImage(t, "example", "example:v1")},
	})
	started, err := m.Start(context.Background(), domain.IngestRequest{
		Source: domain.IngestSourceRegistry, Reference: "ghcr.io/demo/app:v1",
	})
	require.NoError(t, err)
	waitFor(t, m, started.ID, terminal)

	require.NoError(t, m.Cancel(started.ID))
	status, err := m.Status(started.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.PullDone, status.State)
}

func TestPullFailuresAreClassified(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"denied", ingest.ErrUpstreamDenied, ingest.CodePullUpstreamDenied},
		{"rate limited", ingest.ErrRateLimited, ingest.CodePullRateLimited},
		{"cache full", cachestore.ErrCacheFull, ingest.CodeCacheFull},
		{"anything else", errors.New("connection reset by peer at 10.0.0.1"), ingest.CodePullFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newStore(t, t.TempDir(), 1<<30)
			m := newManager(t, store, ingest.ManagerOptions{Registry: &fakeRegistrySource{err: tc.err}})
			started, err := m.Start(context.Background(), domain.IngestRequest{
				Source: domain.IngestSourceRegistry, Reference: "ghcr.io/demo/app:v1",
			})
			require.NoError(t, err)
			status := waitFor(t, m, started.ID, terminal)
			require.Equal(t, domain.PullFailed, status.State)
			require.NotNil(t, status.Error)
			assert.Equal(t, tc.code, status.Error.Code)
			assert.NotEmpty(t, status.Error.Message)
			assert.NotContains(t, status.Error.Message, "10.0.0.1",
				"an upstream's error text is never rendered to a browser")
		})
	}
}

// The allowlist gates the pull synchronously, before any source is touched.
func TestStartRefusesNonAllowlistedRegistriesWithoutContactingAnything(t *testing.T) {
	store := newStore(t, t.TempDir(), 1<<30)
	source := &fakeRegistrySource{img: fixtureImage(t, "example", "example:v1")}
	m := newManager(t, store, ingest.ManagerOptions{Registry: source})

	_, err := m.Start(context.Background(), domain.IngestRequest{
		Source: domain.IngestSourceRegistry, Reference: "evil.example.com/x",
	})
	var notAllowed *imgref.ErrRegistryNotAllowed
	require.ErrorAs(t, err, &notAllowed)
	assert.Equal(t, "evil.example.com", notAllowed.Registry)
	assert.Zero(t, source.opens.Load(), "a refused registry is never contacted")
	assert.Empty(t, m.Pulls(), "and no pull is created for it")

	_, err = m.Start(context.Background(), domain.IngestRequest{
		Source: domain.IngestSourceRegistry, Reference: "not a ref!",
	})
	assert.ErrorIs(t, err, imgref.ErrInvalidReference)

	_, err = m.Start(context.Background(), domain.IngestRequest{Source: "elsewhere", Reference: "x"})
	assert.ErrorIs(t, err, ingest.ErrInvalidSource)
}

// A cap so small the image cannot fit is a clean refusal, not a thrash-evict
// of everything else (RESEARCH Q7).
func TestPullRefusedWhenTheCacheIsFull(t *testing.T) {
	store := newStore(t, t.TempDir(), 8<<10)
	m := newManager(t, store, ingest.ManagerOptions{
		Registry: &fakeRegistrySource{img: fixtureImage(t, "wide", "wide:v1")},
	})
	started, err := m.Start(context.Background(), domain.IngestRequest{
		Source: domain.IngestSourceRegistry, Reference: "ghcr.io/demo/wide:v1",
	})
	require.NoError(t, err)
	status := waitFor(t, m, started.ID, terminal)
	require.Equal(t, domain.PullFailed, status.State)
	require.NotNil(t, status.Error)
	assert.Equal(t, ingest.CodeCacheFull, status.Error.Code)
}

// --- docker source ---------------------------------------------------------

func TestStartDockerWithoutADaemon(t *testing.T) {
	store := newStore(t, t.TempDir(), 1<<30)
	m := newManager(t, store, ingest.ManagerOptions{})
	_, err := m.Start(context.Background(), domain.IngestRequest{
		Source: domain.IngestSourceDocker, Reference: "alpine:3.20",
	})
	assert.ErrorIs(t, err, ingest.ErrDockerUnavailable)

	listing, err := m.ListDockerImages(context.Background())
	require.NoError(t, err, "listing never fails for a missing daemon")
	assert.False(t, listing.Available)
	assert.NotEmpty(t, listing.Reason)
}

func TestDockerPullPrefersTheRegistryWhenARepoDigestAllowsIt(t *testing.T) {
	ingest.SetSaveBufferLimit(t, 10_000)
	store := newStore(t, t.TempDir(), 1<<30)
	img := fixtureImage(t, "example", "example:v1")
	digestRef := "ghcr.io/demo/app@sha256:1111111111111111111111111111111111111111111111111111111111111111"
	daemon := &fakeDaemon{
		images: []ingest.DockerInspect{{
			ID:       "sha256:" + "0000000000000000000000000000000000000000000000000000000000000001",
			RepoTags: []string{"demo/app:v1"}, RepoDigests: []string{digestRef},
			SizeBytes: 1234, OS: "linux", Architecture: "amd64",
		}},
		saves: map[string][]byte{
			"sha256:" + "0000000000000000000000000000000000000000000000000000000000000001": ociSaveTar(t, img),
		},
	}
	source := &fakeRegistrySource{img: img}
	m := newManager(t, store, ingest.ManagerOptions{
		Registry: source,
		Docker:   newFakeDocker(t, daemon, store),
	})

	started, err := m.Start(context.Background(), domain.IngestRequest{
		Source: domain.IngestSourceDocker, Reference: "demo/app:v1",
	})
	require.NoError(t, err)
	status := waitFor(t, m, started.ID, terminal)
	require.Equal(t, domain.PullDone, status.State, "%+v", status.Error)

	assert.Equal(t, int64(1), source.opens.Load(),
		"the registry path is preferred: the bytes are identical and it can skip known layers")
	assert.Zero(t, daemon.saveCalls, "so no 25 GiB save stream was opened at all")

	rec, err := store.Image(context.Background(), status.ImageID)
	require.NoError(t, err)
	assert.Equal(t, domain.SourceDocker, rec.Source, "provenance stays 'docker': that is where the user picked it")
	assert.Equal(t, []string{"demo/app:v1"}, rec.RefNames)
}

// A local reference reaches the Engine API by string concatenation, so a
// traversal segment in it is refused before it gets there.
func TestStartDockerRefusesTraversalReferences(t *testing.T) {
	store := newStore(t, t.TempDir(), 1<<30)
	daemon := &fakeDaemon{}
	m := newManager(t, store, ingest.ManagerOptions{Docker: newFakeDocker(t, daemon, store)})

	for _, reference := range []string{
		"../../containers", "a/../../../info", "./x", "a/./b", "..",
	} {
		_, err := m.Start(context.Background(), domain.IngestRequest{
			Source: domain.IngestSourceDocker, Reference: reference,
		})
		assert.ErrorIs(t, err, imgref.ErrInvalidReference, "reference %q", reference)
	}
	assert.Empty(t, m.Pulls(), "a refused reference never becomes a pull")
}

func TestDockerPullFallsBackToTheSaveStream(t *testing.T) {
	ingest.SetSaveBufferLimit(t, 10_000)
	store := newStore(t, t.TempDir(), 1<<30)
	img := fixtureImage(t, "example", "example:v1")
	daemon := &fakeDaemon{
		images: []ingest.DockerInspect{{
			ID:        "sha256:" + "0000000000000000000000000000000000000000000000000000000000000002",
			RepoTags:  []string{"local/only:v1"},
			SizeBytes: 4096, OS: "linux", Architecture: "amd64",
		}},
		// Keyed by the daemon's id: that is what the save is asked for.
		saves: map[string][]byte{
			"sha256:" + "0000000000000000000000000000000000000000000000000000000000000002": ociSaveTar(t, img),
		},
	}
	m := newManager(t, store, ingest.ManagerOptions{
		// A locally built image has no RepoDigests, so there is nothing
		// to prefer and the save stream is the only route.
		Registry: &fakeRegistrySource{err: errors.New("must not be used")},
		Docker:   newFakeDocker(t, daemon, store),
	})

	started, err := m.Start(context.Background(), domain.IngestRequest{
		Source: domain.IngestSourceDocker, Reference: "local/only:v1",
	})
	require.NoError(t, err)
	status := waitFor(t, m, started.ID, terminal)
	require.Equal(t, domain.PullDone, status.State, "%+v", status.Error)
	assert.Equal(t, 1, daemon.saveCalls)
	assert.True(t, status.BytesEstimated, "the daemon's size is an estimate and is labelled as one")
}

// The status table is process-local and bounded; a long-lived server must not
// accumulate pulls forever. Evicting only terminal entries is the property
// that matters: a running pull is never dropped out from under its poller.
func TestPullTableIsBounded(t *testing.T) {
	store := newStore(t, t.TempDir(), 1<<30)
	m := newManager(t, store, ingest.ManagerOptions{
		Registry: &fakeRegistrySource{err: ingest.ErrUpstreamDenied},
	})
	for i := 0; i < 80; i++ {
		started, err := m.Start(context.Background(), domain.IngestRequest{
			Source:    domain.IngestSourceRegistry,
			Reference: fmt.Sprintf("ghcr.io/demo/app:v%d", i),
		})
		require.NoError(t, err)
		// Each pull is allowed to finish before the next begins, so the
		// eviction under test is of terminal entries only.
		for {
			status, err := m.Status(started.ID)
			if err != nil || terminal(status) {
				break
			}
			time.Sleep(time.Millisecond)
		}
	}
	assert.LessOrEqual(t, len(m.Pulls()), 64)
	assert.NotEmpty(t, m.Pulls())
}

// TestPullEntryCapSurfacesAsAClassifiedFailure is the client-visible half of
// H1: a layer past the entry cap must end the pull with a code the UI knows,
// not with a generic 500 or a message quoting the upstream.
func TestPullEntryCapSurfacesAsAClassifiedFailure(t *testing.T) {
	store := newStore(t, t.TempDir(), 1<<30)
	// One entry per layer is enough to refuse every fixture, and it proves
	// the cap is the ingester's to set rather than a compile-time constant.
	ingester := ingest.New(store, ingest.Options{Logger: discard(), MaxLayerEntries: 1})
	m := newManager(t, store, ingest.ManagerOptions{
		Ingester: ingester,
		Registry: &fakeRegistrySource{img: fixtureImage(t, "example", "example:v1")},
	})

	started, err := m.Start(context.Background(), domain.IngestRequest{
		Source: domain.IngestSourceRegistry, Reference: "ghcr.io/demo/app:v1",
	})
	require.NoError(t, err, "the refusal is a pull outcome, not a rejected request")

	status := waitFor(t, m, started.ID, terminal)
	require.Equal(t, domain.PullFailed, status.State)
	require.NotNil(t, status.Error)
	assert.Equal(t, ingest.CodePullTooLarge, status.Error.Code)
	assert.Contains(t, status.Error.Message, "files")

	// And nothing was committed: a layer that was refused mid-index is not
	// a layer the cache may serve.
	images, err := store.Images(context.Background())
	require.NoError(t, err)
	assert.Empty(t, images)
}

// TestConcurrentPullsAreBounded is H2. Before admission control, 400 concurrent
// POSTs produced 400 goroutines, 400 outbound sessions, and a pull table of
// 400 despite maxRetainedPulls = 64.
func TestConcurrentPullsAreBounded(t *testing.T) {
	const (
		submissions = 400
		limit       = 4
	)
	store := newStore(t, t.TempDir(), 1<<30)
	// Every pull blocks on its first layer until the test releases it, so
	// all of them are genuinely in flight at once.
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	source := &blockingRegistrySource{img: fixtureImage(t, "example", "example:v1"), release: release}
	m := newManager(t, store, ingest.ManagerOptions{Registry: source, MaxInFlight: limit})

	var accepted, refused atomic.Int64
	var wg sync.WaitGroup
	for i := range submissions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := m.Start(context.Background(), domain.IngestRequest{
				Source:    domain.IngestSourceRegistry,
				Reference: fmt.Sprintf("ghcr.io/demo/app%d:v1", i),
			})
			switch {
			case err == nil:
				accepted.Add(1)
			case errors.Is(err, ingest.ErrTooManyPulls):
				refused.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	assert.LessOrEqual(t, accepted.Load(), int64(limit),
		"no more pulls may be admitted than the limit allows")
	assert.Equal(t, int64(submissions), accepted.Load()+refused.Load())
	assert.Positive(t, refused.Load())
	assert.LessOrEqual(t, m.InFlight(), limit)

	// The outbound sessions are the point: a refused submit must not have
	// reached the registry at all.
	assert.LessOrEqual(t, source.opens.Load(), int64(limit))

	// The table cannot grow with the submissions either. Its ceiling is the
	// retained-history cap plus whatever is live, never the request count.
	assert.LessOrEqual(t, len(m.Pulls()), 64+limit)

	close(release)
	for _, p := range m.Pulls() {
		waitFor(t, m, p.ID, terminal)
	}
	requireNoPullsInFlight(t, m)
}

// A resubmission of a pull that is already running is idempotent by contract
// (§6.3), so it must be answered from the table rather than charged an
// admission slot — otherwise a browser retrying its own POST would lock itself
// out of the limit it is already inside.
func TestIdempotentResubmissionDoesNotConsumeASlot(t *testing.T) {
	store := newStore(t, t.TempDir(), 1<<30)
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	source := &blockingRegistrySource{img: fixtureImage(t, "example", "example:v1"), release: release}
	m := newManager(t, store, ingest.ManagerOptions{Registry: source, MaxInFlight: 1})

	first, err := m.Start(context.Background(), domain.IngestRequest{
		Source: domain.IngestSourceRegistry, Reference: "ghcr.io/demo/app:v1",
	})
	require.NoError(t, err)
	require.True(t, first.Created)

	// Twenty resubmissions of the same reference against a limit of one.
	for range 20 {
		again, err := m.Start(context.Background(), domain.IngestRequest{
			Source: domain.IngestSourceRegistry, Reference: "ghcr.io/demo/app:v1",
		})
		require.NoError(t, err)
		assert.False(t, again.Created)
		assert.Equal(t, first.ID, again.ID)
	}
	assert.Equal(t, 1, m.InFlight())
	require.Eventually(t, func() bool { return source.opens.Load() == 1 }, 10*time.Second, 5*time.Millisecond,
		"one reference, one outbound session")
	assert.Equal(t, int64(1), source.opens.Load())

	// A *different* reference is still refused: the slot is genuinely taken.
	_, err = m.Start(context.Background(), domain.IngestRequest{
		Source: domain.IngestSourceRegistry, Reference: "ghcr.io/demo/other:v1",
	})
	assert.ErrorIs(t, err, ingest.ErrTooManyPulls)

	close(release)
	waitFor(t, m, first.ID, terminal)
	requireNoPullsInFlight(t, m)
}

// Pull ids are unguessable. Any client can cancel any pull and that is
// acceptable on the trusted network layerlens is specified for, but the ids
// should not also be derivable from a wall clock (ARCHITECTURE §7.3).
func TestPullIDsAreUnguessable(t *testing.T) {
	store := newStore(t, t.TempDir(), 1<<30)
	m := newManager(t, store, ingest.ManagerOptions{
		Registry: &fakeRegistrySource{img: fixtureImage(t, "example", "example:v1")},
	})
	seen := map[domain.PullID]struct{}{}
	for i := range 8 {
		started, err := m.Start(context.Background(), domain.IngestRequest{
			Source:    domain.IngestSourceRegistry,
			Reference: fmt.Sprintf("ghcr.io/demo/app%d:v1", i),
		})
		require.NoError(t, err)
		waitFor(t, m, started.ID, terminal)
		_, dup := seen[started.ID]
		require.False(t, dup, "ids must be unique")
		seen[started.ID] = struct{}{}
		assert.Len(t, string(started.ID), 33, "p + 16 random bytes as hex")
		assert.NotContains(t, string(started.ID), "-", "no sequence counter to walk")
	}
}

// blockingRegistrySource holds every pull open on its first layer until
// release is closed, so a test can have N pulls genuinely in flight at once.
type blockingRegistrySource struct {
	img     v1.Image
	release <-chan struct{}
	opens   atomic.Int64
}

func (b *blockingRegistrySource) Open(ctx context.Context, _ domain.ImageRef) (*ingest.RemoteImage, error) {
	b.opens.Add(1)
	manifest, err := b.img.Manifest()
	if err != nil {
		return nil, err
	}
	out := &ingest.RemoteImage{
		Image:      &heldImage{Image: b.img, ctx: ctx, release: b.release},
		LayerCount: len(manifest.Layers),
	}
	for _, l := range manifest.Layers {
		out.BytesTotal += l.Size
	}
	return out, nil
}

type heldImage struct {
	v1.Image
	ctx     context.Context
	release <-chan struct{}
}

func (h *heldImage) Layers() ([]v1.Layer, error) {
	layers, err := h.Image.Layers()
	if err != nil {
		return nil, err
	}
	if len(layers) > 0 {
		layers[0] = &heldLayer{Layer: layers[0], ctx: h.ctx, release: h.release}
	}
	return layers, nil
}

type heldLayer struct {
	v1.Layer
	ctx     context.Context
	release <-chan struct{}
}

func (h *heldLayer) Compressed() (io.ReadCloser, error) {
	select {
	case <-h.release:
	case <-h.ctx.Done():
		return nil, h.ctx.Err()
	}
	return h.Layer.Compressed()
}

// The wall-clock backstop is the second half of M2: safehttp's throughput floor
// cannot see the docker-save path, and nothing else bounded a pull's lifetime.
// A pull that hits it is a failure, not a cancellation — nobody asked for it.
func TestPullTimeoutFailsTheStalledPull(t *testing.T) {
	store := newStore(t, t.TempDir(), 1<<30)
	blocked := make(chan struct{}) // never closed: the source hangs forever
	t.Cleanup(func() { close(blocked) })
	source := &blockingRegistrySource{img: fixtureImage(t, "example", "example:v1"), release: blocked}
	m := newManager(t, store, ingest.ManagerOptions{
		Registry:    source,
		PullTimeout: 50 * time.Millisecond,
	})

	started, err := m.Start(context.Background(), domain.IngestRequest{
		Source: domain.IngestSourceRegistry, Reference: "ghcr.io/demo/app:v1",
	})
	require.NoError(t, err)

	status := waitFor(t, m, started.ID, terminal)
	assert.Equal(t, domain.PullFailed, status.State,
		"a deadline nobody asked for is a failure, not a cancellation")
	require.NotNil(t, status.Error)
	assert.Equal(t, ingest.CodePullFailed, status.Error.Code)
	assert.Contains(t, status.Error.Message, "longer")
	requireNoPullsInFlight(t, m)
}
