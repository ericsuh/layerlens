package ingest

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"

	"github.com/ericsuh/layerlens/internal/domain"
	"github.com/ericsuh/layerlens/internal/imgref"
	"github.com/ericsuh/layerlens/internal/safehttp"
)

// MaxMetadataBytes caps a manifest, index or config (ARCHITECTURE §7.2). The
// transport enforces it on the wire; this package enforces it again against
// the manifest's *declared* config size, before the config is fetched.
const MaxMetadataBytes = safehttp.MaxMetadataBytes

var (
	// ErrUpstreamDenied is the deliberate collapse of the registry's
	// 401, 403 and 404 into one outcome (§6.1). layerlens pulls
	// anonymously (RESEARCH Q3), so "you may not see this" and "this does
	// not exist" are indistinguishable from here — and reporting them
	// separately would turn the pull endpoint into a probe for the
	// existence of private repositories.
	ErrUpstreamDenied = errors.New("ingest: image not found, or requires authentication")
	// ErrRateLimited reports a 429 from the registry (DESIGN state #11).
	ErrRateLimited = errors.New("ingest: registry rate limit reached")
	// ErrConfigTooLarge reports an image config past MaxMetadataBytes.
	ErrConfigTooLarge = errors.New("ingest: image config is too large")
)

// smallBlobLimiter is the slice of safehttp.Transport the registry source
// needs. An interface so tests can drive the source with a plain transport.
type smallBlobLimiter interface {
	ExpectSmallBlob(hexDigest string) func()
}

// RegistryOptions configures a Registry source.
type RegistryOptions struct {
	// Transport carries every registry request. In production this is the
	// SSRF-hardened transport from internal/safehttp; it is handed to
	// go-containerregistry as a plain http.RoundTripper so gcr still layers
	// its own auth and retry on top (ARCHITECTURE §7.2).
	Transport http.RoundTripper
	// UserAgent identifies layerlens to registries.
	UserAgent string
}

// RegistrySource resolves a validated reference to a streamable remote image.
// The manager depends on the interface so its state machine can be tested
// without a registry.
type RegistrySource interface {
	Open(ctx context.Context, ref domain.ImageRef) (*RemoteImage, error)
}

// Registry pulls images from a remote registry, anonymously.
type Registry struct {
	transport http.RoundTripper
	limiter   smallBlobLimiter
	userAgent string
}

// Registry is the production RegistrySource.
var _ RegistrySource = (*Registry)(nil)

// NewRegistry builds a registry source.
func NewRegistry(opts RegistryOptions) *Registry {
	r := &Registry{transport: opts.Transport, userAgent: opts.UserAgent}
	if r.transport == nil {
		r.transport = http.DefaultTransport
	}
	if limiter, ok := r.transport.(smallBlobLimiter); ok {
		r.limiter = limiter
	}
	if r.userAgent == "" {
		r.userAgent = "layerlens"
	}
	return r
}

// RemoteImage is a resolved remote image, ready to stream.
type RemoteImage struct {
	Image          v1.Image
	ManifestDigest domain.Digest
	// BytesTotal is the sum of the manifest's compressed layer sizes: the
	// exact denominator for pull progress.
	BytesTotal int64
	LayerCount int
	release    func()
}

// Close releases the transport-level size cap held for the config blob.
func (r *RemoteImage) Close() {
	if r.release != nil {
		r.release()
		r.release = nil
	}
}

// Open resolves a reference to its linux/amd64 image and validates the parts
// of the manifest that bound the work ahead: layer count and config size.
//
// No credential source is consulted anywhere in this path — not
// ~/.docker/config.json, not a credential helper, not a cloud SDK chain.
// authn.Anonymous is passed explicitly rather than relying on a keychain
// falling back to it (RESEARCH Q3).
func (r *Registry) Open(ctx context.Context, ref domain.ImageRef) (*RemoteImage, error) {
	parsed, err := name.ParseReference(imgref.Canonical(ref))
	if err != nil {
		// Canonical() is built from an already-parsed reference, so this
		// is an internal inconsistency rather than user input.
		return nil, fmt.Errorf("ingest: re-parse %s: %w", ref.Raw, err)
	}

	img, err := remote.Image(parsed,
		remote.WithContext(ctx),
		remote.WithTransport(r.transport),
		remote.WithAuth(authn.Anonymous),
		remote.WithUserAgent(r.userAgent),
		remote.WithPlatform(v1.Platform{OS: PlatformOS, Architecture: PlatformArch}),
	)
	if err != nil {
		return nil, classifyRegistryError(err)
	}

	manifest, err := img.Manifest()
	if err != nil {
		return nil, classifyRegistryError(err)
	}
	if n := len(manifest.Layers); n > MaxLayers {
		return nil, fmt.Errorf("%w: %d layers", ErrTooManyLayers, n)
	}
	if manifest.Config.Size > MaxMetadataBytes {
		return nil, fmt.Errorf("%w: %d bytes", ErrConfigTooLarge, manifest.Config.Size)
	}

	out := &RemoteImage{Image: img, LayerCount: len(manifest.Layers)}
	for _, l := range manifest.Layers {
		out.BytesTotal += l.Size
	}
	if r.limiter != nil {
		// The config is metadata and must be capped like a manifest, but
		// on the wire it is just another blob GET. Declaring its digest
		// is how the transport tells the two apart (§7.2).
		out.release = r.limiter.ExpectSmallBlob(manifest.Config.Digest.Hex)
	}

	digest, err := img.Digest()
	if err != nil {
		out.Close()
		return nil, classifyRegistryError(err)
	}
	parsedDigest, err := domain.ParseDigest(digest.String())
	if err != nil {
		out.Close()
		return nil, fmt.Errorf("ingest: manifest digest: %w", err)
	}
	out.ManifestDigest = parsedDigest
	return out, nil
}

// classifyRegistryError maps a registry failure onto the small set of
// outcomes the API is allowed to distinguish.
//
// The collapse of 401/403/404 is deliberate and load-bearing: an anonymous
// puller that reported them separately would let anyone use this endpoint to
// enumerate private repositories.
func classifyRegistryError(err error) error {
	if err == nil {
		return nil
	}
	var terr *transport.Error
	if errors.As(err, &terr) {
		switch terr.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
			return ErrUpstreamDenied
		case http.StatusTooManyRequests:
			return ErrRateLimited
		}
	}
	// A denial can also arrive as the token endpoint refusing the scope,
	// which gcr surfaces without a transport.Error.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return err
}
