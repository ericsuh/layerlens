package ingest_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/domain"
	"github.com/ericsuh/layerlens/internal/imgref"
	"github.com/ericsuh/layerlens/internal/ingest"
	"github.com/ericsuh/layerlens/internal/safehttp"
)

// Opt-in switches. `mise run test` must stay hermetic — no network, no Docker
// — so these suites are off unless the operator says otherwise (RESEARCH Q4).
const (
	networkEnv = "LAYERLENS_NETWORK_TESTS"
	dockerEnv  = "LAYERLENS_DOCKER_TESTS"
)

func requireEnv(t *testing.T, name string) {
	t.Helper()
	if os.Getenv(name) != "1" {
		t.Skipf("set %s=1 to run this suite", name)
	}
}

// TestLiveRegistryPull is the RESEARCH Q4 end-to-end check against the two
// registries that get real coverage: Docker Hub and GHCR. GCR, ACR and ECR go
// through this identical code path and are on the allowlist, but are not
// verified against the live services — a documented limitation, not an implied
// guarantee.
func TestLiveRegistryPull(t *testing.T) {
	requireEnv(t, networkEnv)

	for _, reference := range []string{
		"alpine:3.20",
		"ghcr.io/linuxserver/baseimage-alpine:3.20",
	} {
		t.Run(reference, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()

			// The production transport, unmodified: system resolver,
			// system roots, port 443 only, no loopback exemption.
			transport := safehttp.New(safehttp.Options{})
			defer transport.CloseIdleConnections()

			ref, err := imgref.Default().Parse(reference)
			require.NoError(t, err)

			source := ingest.NewRegistry(ingest.RegistryOptions{Transport: transport, UserAgent: "layerlens-test"})
			remote, err := source.Open(ctx, ref)
			if errors.Is(err, ingest.ErrRateLimited) {
				t.Skip("upstream rate limit reached; not a layerlens failure")
			}
			require.NoError(t, err)
			defer remote.Close()
			assert.Positive(t, remote.BytesTotal)
			assert.Positive(t, remote.LayerCount)

			store := newStore(t, t.TempDir(), 2<<30)
			reporter := &RecordingReporter{}
			res, err := newIngester(store).Ingest(ctx, remote.Image, ingest.Meta{
				RefNames:       []string{imgref.Canonical(ref)},
				Source:         domain.SourceRegistry,
				ManifestDigest: remote.ManifestDigest,
				Progress:       reporter,
			})
			require.NoError(t, err)
			require.NotNil(t, res.Record)
			assert.Equal(t, ingest.PlatformString, res.Record.Platform)
			assert.Positive(t, res.Record.TotalBytes)
			assert.NotEmpty(t, res.Record.Layers)
			assert.Equal(t, remote.BytesTotal, reporter.Bytes().Load(),
				"progress counted exactly the compressed bytes the manifest declared")
			t.Logf("%s → %s, %d layers, %d compressed bytes, %d content bytes",
				reference, res.Record.ID.Short(), len(res.Record.Layers),
				remote.BytesTotal, res.Record.TotalBytes)
		})
	}
}

// A private repository must fail with the non-leaky collapse, never with a
// credential prompt or a distinguishable 401 (RESEARCH Q3).
func TestLivePrivateRepositoryIsDenied(t *testing.T) {
	requireEnv(t, networkEnv)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	transport := safehttp.New(safehttp.Options{})
	defer transport.CloseIdleConnections()
	ref, err := imgref.Default().Parse("ghcr.io/layerlens-test/definitely-not-a-real-repo:v1")
	require.NoError(t, err)

	_, err = ingest.NewRegistry(ingest.RegistryOptions{Transport: transport}).Open(ctx, ref)
	require.Error(t, err)
	assert.ErrorIs(t, err, ingest.ErrUpstreamDenied)
}

// TestLiveDockerDaemon exercises the real save-stream path against the local
// daemon.
func TestLiveDockerDaemon(t *testing.T) {
	requireEnv(t, dockerEnv)
	host := os.Getenv("DOCKER_HOST")
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	reference := os.Getenv("LAYERLENS_DOCKER_IMAGE")
	if reference == "" {
		reference = "alpine:3.20"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	store := newStore(t, t.TempDir(), 2<<30)
	docker := ingest.NewDocker(ingest.DockerOptions{Host: host, Images: store, Logger: discard()})
	defer func() { _ = docker.Close() }()

	listing, err := docker.List(ctx)
	require.NoError(t, err)
	require.True(t, listing.Available, "reason: %s", listing.Reason)
	t.Logf("daemon offers %d images", len(listing.Images))

	inspect, err := docker.Inspect(ctx, reference)
	require.NoError(t, err)
	t.Logf("%s: %s, %d bytes, %d layers, repoDigests=%v",
		reference, inspect.Platform(), inspect.SizeBytes, len(inspect.DiffIDs), inspect.RepoDigests)

	stream, err := docker.Save(ctx, reference)
	require.NoError(t, err)
	defer func() { _ = stream.Close() }()

	reporter := &RecordingReporter{}
	res, err := newIngester(store).IngestDockerSave(ctx, stream, ingest.Meta{
		RefNames: []string{reference}, Source: domain.SourceDocker, Progress: reporter,
	})
	require.NoError(t, err)
	require.NotNil(t, res.Record)
	assert.Equal(t, domain.SourceDocker, res.Record.Source)
	assert.NotEmpty(t, res.Record.Layers)
	assert.Positive(t, res.Record.TotalBytes)
	t.Logf("analyzed %s → %s, %d layers, %d content bytes (%d streamed bytes)",
		reference, res.Record.ID.Short(), len(res.Record.Layers),
		res.Record.TotalBytes, reporter.Bytes().Load())
}
