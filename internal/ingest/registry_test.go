package ingest_test

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/domain"
	"github.com/ericsuh/layerlens/internal/ingest"
	"github.com/ericsuh/layerlens/internal/safehttp"
)

// fakeRegistry is a real registry — go-containerregistry's own in-memory
// implementation — served over TLS on loopback and reached through the real
// safehttp transport.
//
// Nothing here is stubbed out on the layerlens side: the pull under test does
// the token dance, the manifest fetch, the platform selection and the blob
// streaming exactly as it would against Docker Hub, and every socket goes
// through the guarded dialer. The only concession to running in a test is that
// the transport is told loopback is acceptable and given the server's own
// certificate.
type fakeRegistry struct {
	server    *httptest.Server
	transport *safehttp.Transport
	host      string // "example.com:PORT"
}

func newFakeRegistry(t *testing.T, handler http.Handler) *fakeRegistry {
	t.Helper()
	if handler == nil {
		handler = registry.New(registry.Logger(log.New(io.Discard, "", 0)))
	}
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)

	parsed, err := url.Parse(server.URL)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())

	// "example.com" is what httptest's certificate is issued for, so the
	// handshake against the *name* succeeds while the socket goes to the
	// vetted loopback literal.
	transport := safehttp.New(safehttp.Options{
		Resolver:       staticResolver{"example.com": netip.MustParseAddr("127.0.0.1")},
		PermitLoopback: true,
		RootCAs:        pool,
	})
	t.Cleanup(transport.CloseIdleConnections)
	return &fakeRegistry{server: server, transport: transport, host: "example.com:" + parsed.Port()}
}

// ref builds a domain.ImageRef pointing at the fake registry.
//
// It is constructed directly rather than through imgref.Parse because the fake
// listens on an ephemeral port and imgref refuses explicit ports by design
// (§7.1). The allowlist gate is tested where it lives, in imgref and in the
// pull manager.
func (f *fakeRegistry) ref(repository, tag string) domain.ImageRef {
	return domain.ImageRef{
		Raw:        f.host + "/" + repository + ":" + tag,
		Registry:   f.host,
		Repository: repository,
		Tag:        tag,
	}
}

// push uploads an image to the fake registry.
func (f *fakeRegistry) push(t *testing.T, img v1.Image, repository, tag string) {
	t.Helper()
	target, err := name.ParseReference(f.host + "/" + repository + ":" + tag)
	require.NoError(t, err)
	require.NoError(t, remote.Write(target, img,
		remote.WithTransport(f.transport),
		remote.WithAuth(authn.Anonymous),
	))
}

func (f *fakeRegistry) source() *ingest.Registry {
	return ingest.NewRegistry(ingest.RegistryOptions{Transport: f.transport, UserAgent: "layerlens-test"})
}

// staticResolver answers from a table and never touches DNS.
type staticResolver map[string]netip.Addr

func (r staticResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	addr, ok := r[host]
	if !ok {
		return nil, fmt.Errorf("no such host: %s", host)
	}
	return []netip.Addr{addr}, nil
}

// fixtureImage loads one vendored demo image.
func fixtureImage(t *testing.T, layout, ref string) v1.Image {
	t.Helper()
	images, err := ingest.OpenLayout(fixturesDir(t) + "/" + layout)
	require.NoError(t, err)
	for _, img := range images {
		if img.Ref == ref {
			return img.Image
		}
	}
	t.Fatalf("fixture %s not found in %s", ref, layout)
	return nil
}

func TestRegistryPullReportsExactByteProgress(t *testing.T) {
	fake := newFakeRegistry(t, nil)
	img := fixtureImage(t, "example", "example:v1")
	fake.push(t, img, "demo/app", "v1")

	store := newStore(t, t.TempDir(), 1<<30)
	ingester := newIngester(store)
	ctx := context.Background()

	remoteImg, err := fake.source().Open(ctx, fake.ref("demo/app", "v1"))
	require.NoError(t, err)
	defer remoteImg.Close()

	manifest, err := img.Manifest()
	require.NoError(t, err)
	var expected int64
	for _, l := range manifest.Layers {
		expected += l.Size
	}
	assert.Equal(t, expected, remoteImg.BytesTotal,
		"the denominator comes from the manifest, so progress is exact rather than estimated")
	assert.Equal(t, len(manifest.Layers), remoteImg.LayerCount)

	reporter := &RecordingReporter{}
	res, err := ingester.Ingest(ctx, remoteImg.Image, ingest.Meta{
		RefNames:       []string{"demo/app:v1"},
		Source:         domain.SourceRegistry,
		ManifestDigest: remoteImg.ManifestDigest,
		Progress:       reporter,
	})
	require.NoError(t, err)
	require.NotNil(t, res.Record)
	assert.Equal(t, domain.SourceRegistry, res.Record.Source)
	assert.Equal(t, len(manifest.Layers), res.LayersIndexed)
	assert.Equal(t, expected, reporter.Bytes().Load(),
		"every compressed byte the manifest declared was actually streamed")

	phases, started, finished, skipped := reporter.Snapshot()
	assert.Equal(t, []string{ingest.PhaseResolving, ingest.PhaseDownloading, ingest.PhaseFinalizing}, phases)
	assert.Len(t, started, len(manifest.Layers))
	assert.Len(t, finished, len(manifest.Layers))
	assert.Empty(t, skipped)
}

func TestRegistryPullSkipsLayersAlreadyIndexed(t *testing.T) {
	fake := newFakeRegistry(t, nil)
	v1img := fixtureImage(t, "example", "example:v1")
	v2img := fixtureImage(t, "example", "example:v2")
	fake.push(t, v1img, "demo/app", "v1")
	fake.push(t, v2img, "demo/app", "v2")

	store := newStore(t, t.TempDir(), 1<<30)
	ingester := newIngester(store)
	ctx := context.Background()
	source := fake.source()

	first, err := source.Open(ctx, fake.ref("demo/app", "v1"))
	require.NoError(t, err)
	defer first.Close()
	_, err = ingester.Ingest(ctx, first.Image, ingest.Meta{Source: domain.SourceRegistry})
	require.NoError(t, err)

	second, err := source.Open(ctx, fake.ref("demo/app", "v2"))
	require.NoError(t, err)
	defer second.Close()
	reporter := &RecordingReporter{}
	res, err := ingester.Ingest(ctx, second.Image, ingest.Meta{
		Source: domain.SourceRegistry, Progress: reporter,
	})
	require.NoError(t, err)

	assert.Positive(t, res.LayersSkipped,
		"the pair shares a trunk, and a shared layer must cost no download at all")
	_, _, _, skipped := reporter.Snapshot()
	assert.Len(t, skipped, res.LayersSkipped)
	assert.Less(t, reporter.Bytes().Load(), second.BytesTotal,
		"fewer bytes were streamed than the manifest declares, because the trunk was already indexed")
}

// The dialer is the security boundary, and go-containerregistry builds its own
// http.Client — so this asserts the boundary holds on gcr's path, not just on
// ours.
func TestRegistryPullRefusesAPrivateAddress(t *testing.T) {
	transport := safehttp.New(safehttp.Options{
		Resolver: staticResolver{"registry.internal": netip.MustParseAddr("10.0.0.7")},
	})
	source := ingest.NewRegistry(ingest.RegistryOptions{Transport: transport})

	_, err := source.Open(context.Background(), domain.ImageRef{
		Registry: "registry.internal", Repository: "team/app", Tag: "v1",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, safehttp.ErrForbiddenAddress)
}

func TestRegistryUpstreamDenialsCollapse(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			fake := newFakeRegistry(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v2/" {
					w.WriteHeader(http.StatusOK)
					return
				}
				w.WriteHeader(status)
			}))
			_, err := fake.source().Open(context.Background(), fake.ref("private/app", "v1"))
			require.Error(t, err)
			assert.ErrorIs(t, err, ingest.ErrUpstreamDenied,
				"401, 403 and 404 must be indistinguishable: anonymous pulls must not "+
					"become a probe for private repositories")
			assert.NotContains(t, err.Error(), fmt.Sprint(status))
		})
	}
}

func TestRegistryRateLimitIsReported(t *testing.T) {
	fake := newFakeRegistry(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	_, err := fake.source().Open(context.Background(), fake.ref("busy/app", "v1"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ingest.ErrRateLimited)
}

// A hostile-but-allowlisted upstream must not be able to turn one manifest
// into unbounded work or unbounded memory.
func TestRegistryRefusesOversizedManifests(t *testing.T) {
	t.Run("too many layers", func(t *testing.T) {
		manifest := syntheticManifest(t, ingest.MaxLayers+1, 100, 1024)
		fake := newFakeRegistry(t, manifestServer(manifest))
		_, err := fake.source().Open(context.Background(), fake.ref("hostile/app", "v1"))
		require.Error(t, err)
		assert.ErrorIs(t, err, ingest.ErrTooManyLayers)
	})

	t.Run("config too large", func(t *testing.T) {
		manifest := syntheticManifest(t, 1, ingest.MaxMetadataBytes+1, 1024)
		fake := newFakeRegistry(t, manifestServer(manifest))
		_, err := fake.source().Open(context.Background(), fake.ref("hostile/app", "v1"))
		require.Error(t, err)
		assert.ErrorIs(t, err, ingest.ErrConfigTooLarge)
	})

	t.Run("manifest body past the transport cap", func(t *testing.T) {
		// The body is far larger than the 8 MiB cap and is refused while
		// streaming, so the bytes never accumulate in memory.
		fake := newFakeRegistry(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v2/" {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			chunk := make([]byte, 1<<20)
			for written := 0; written < safehttp.MaxMetadataBytes+(2<<20); written += len(chunk) {
				if _, err := w.Write(chunk); err != nil {
					return
				}
			}
		}))
		_, err := fake.source().Open(context.Background(), fake.ref("hostile/app", "v1"))
		require.Error(t, err)
		assert.Contains(t, strings.ToLower(err.Error()), "size limit")
	})
}

// manifestServer answers every manifest request with one fixed document.
func manifestServer(manifest []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		_, _ = w.Write(manifest)
	})
}

// syntheticManifest builds a manifest document with the given shape. The blobs
// it references do not exist: every check it is used for happens before any
// blob is fetched, which is the point.
func syntheticManifest(t *testing.T, layers int, configSize, layerSize int64) []byte {
	t.Helper()
	doc := map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"config": map[string]any{
			"mediaType": "application/vnd.oci.image.config.v1+json",
			"size":      configSize,
			"digest":    "sha256:" + strings.Repeat("a", 64),
		},
	}
	list := make([]map[string]any, 0, layers)
	for i := 0; i < layers; i++ {
		list = append(list, map[string]any{
			"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip",
			"size":      layerSize,
			"digest":    fmt.Sprintf("sha256:%064x", i),
		})
	}
	doc["layers"] = list
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	return raw
}
