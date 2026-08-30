package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	mobyclient "github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/ericsuh/layerlens/internal/domain"
)

// ErrDockerUnavailable reports that no Docker daemon is reachable. It is
// returned only by endpoints that genuinely need the socket; the listing
// endpoint reports unavailability as data instead (§6.3).
var ErrDockerUnavailable = errors.New("ingest: no reachable Docker daemon")

// maxListedImages bounds the daemon listing. A picker is not a place to render
// a five-thousand-image daemon, and each row costs an inspect round trip.
const maxListedImages = 250

// dockerConnectTimeout bounds the availability probe so a wedged socket
// cannot hang the images tab.
const dockerConnectTimeout = 5 * time.Second

// DockerInspect is the subset of `docker inspect` layerlens uses.
type DockerInspect struct {
	ID           string
	RepoTags     []string
	RepoDigests  []string
	SizeBytes    int64
	OS           string
	Architecture string
	DiffIDs      []string
}

// Platform renders the inspected platform as "os/arch".
func (d *DockerInspect) Platform() string {
	if d.OS == "" || d.Architecture == "" {
		return ""
	}
	return d.OS + "/" + d.Architecture
}

// DockerAPI is the slice of the Engine API layerlens uses. It is exported so
// the daemon path is testable without a daemon: the moby types stop at the
// adapter below, and a test supplies its own fake through DockerOptions.Dial.
type DockerAPI interface {
	Ping(ctx context.Context) error
	List(ctx context.Context) ([]DockerInspect, error)
	Inspect(ctx context.Context, ref string) (*DockerInspect, error)
	Save(ctx context.Context, ref string) (io.ReadCloser, error)
	Close() error
}

// DockerOptions configures the daemon source.
type DockerOptions struct {
	// Host is the endpoint to dial, e.g. "unix:///var/run/docker.sock". An
	// empty host means "no daemon configured" and every call reports
	// unavailability rather than failing.
	Host string
	// Disabled distinguishes `--docker-host off` from "nothing was found".
	// Both leave Host empty, but only one of them is a decision the
	// operator made, and telling them apart is the difference between
	// "your server has no Docker" and "your server was told not to use
	// it" — the deployed systemd unit sets `off` by default, so the
	// wrong one of those is what every deployment would show.
	Disabled bool
	// Images is consulted to mark rows that are already analyzed.
	Images domain.ImageStore
	Logger *slog.Logger
	// Dial builds the API adapter. Nil means the real Engine API; tests
	// substitute a fake daemon.
	Dial func(host string) (DockerAPI, error)
}

// DockerSource is the daemon-side surface the pull manager depends on. The
// interface (rather than *Docker) is what lets the manager's state machine be
// tested against a fake daemon.
type DockerSource interface {
	// Host is the configured endpoint; empty means "no daemon".
	Host() string
	List(ctx context.Context) (domain.DockerListing, error)
	Inspect(ctx context.Context, ref string) (*DockerInspect, error)
	Save(ctx context.Context, ref string) (io.ReadCloser, error)
}

// Docker is the local-daemon image source.
type Docker struct {
	host     string
	disabled bool
	images   domain.ImageStore
	log      *slog.Logger
	dial     func(host string) (DockerAPI, error)

	mu  sync.Mutex
	api DockerAPI
}

// Docker is the production DockerSource.
var _ DockerSource = (*Docker)(nil)

// NewDocker builds the daemon source. It performs no I/O: a server with no
// Docker must start exactly as fast as one with it.
func NewDocker(opts DockerOptions) *Docker {
	d := &Docker{host: opts.Host, disabled: opts.Disabled, images: opts.Images, log: opts.Logger, dial: opts.Dial}
	if d.log == nil {
		d.log = slog.Default()
	}
	if d.dial == nil {
		d.dial = dialMoby
	}
	return d
}

// Host reports the configured endpoint, for diagnostics.
func (d *Docker) Host() string { return d.host }

// Close releases the daemon connection.
func (d *Docker) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.api == nil {
		return nil
	}
	err := d.api.Close()
	d.api = nil
	return err
}

// client returns a connected adapter, dialing lazily.
func (d *Docker) client() (DockerAPI, error) {
	if d.host == "" {
		return nil, fmt.Errorf("%w: no Docker endpoint configured", ErrDockerUnavailable)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.api != nil {
		return d.api, nil
	}
	api, err := d.dial(d.host)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDockerUnavailable, err)
	}
	d.api = api
	return api, nil
}

// unavailableReason renders the DESIGN §9 state-#4 explanation.
func (d *Docker) unavailableReason(err error) string {
	if d.disabled {
		return "The Docker daemon source is turned off on this server (--docker-host off)."
	}
	if d.host == "" {
		return "No Docker socket found at /var/run/docker.sock — the daemon source is unavailable on this server."
	}
	if err == nil {
		return fmt.Sprintf("The Docker daemon at %s is not reachable.", d.host)
	}
	// The endpoint is operator configuration, not user input, and the
	// underlying error is a local connection failure — neither leaks
	// anything about an upstream.
	return fmt.Sprintf("The Docker daemon at %s is not reachable: %v", d.host, err)
}

// List enumerates the daemon's images. It never returns an error for "no
// Docker": that is reported as Available=false with a reason (§6.3).
func (d *Docker) List(ctx context.Context) (domain.DockerListing, error) {
	api, err := d.client()
	if err != nil {
		return domain.DockerListing{Reason: d.unavailableReason(nil), Images: []domain.DockerImageSummary{}}, nil
	}
	pingCtx, cancel := context.WithTimeout(ctx, dockerConnectTimeout)
	defer cancel()
	if err := api.Ping(pingCtx); err != nil {
		return domain.DockerListing{Reason: d.unavailableReason(err), Images: []domain.DockerImageSummary{}}, nil
	}

	images, err := api.List(ctx)
	if err != nil {
		// The daemon answered the ping and then failed the list: that is
		// a real error (permission denied, timeout) and DESIGN state #6
		// offers a Retry for it.
		return domain.DockerListing{}, fmt.Errorf("ingest: list docker images: %w", err)
	}
	if len(images) > maxListedImages {
		images = images[:maxListedImages]
	}

	analyzed := d.analyzedIndex(ctx)
	out := domain.DockerListing{Available: true, Images: make([]domain.DockerImageSummary, 0, len(images))}
	for i := range images {
		summary := domain.DockerImageSummary{
			Reference: preferredRef(&images[i]),
			DockerID:  images[i].ID,
			SizeBytes: images[i].SizeBytes,
			Platform:  images[i].Platform(),
		}
		if summary.Reference == "" {
			// An untagged image cannot be named back to POST /pulls,
			// so offering it would be offering a dead row.
			continue
		}
		if id, ok := analyzed[images[i].ID]; ok {
			summary.AlreadyAnalyzed = true
			summary.AnalyzedID = id
		} else if id, ok := analyzed[summary.Reference]; ok {
			summary.AlreadyAnalyzed = true
			summary.AnalyzedID = id
		}
		out.Images = append(out.Images, summary)
	}
	sort.SliceStable(out.Images, func(a, b int) bool {
		return out.Images[a].Reference < out.Images[b].Reference
	})
	return out, nil
}

// analyzedIndex maps every way a daemon row might name an image it has
// already analyzed onto that image's id.
//
// Two keys, because one is not enough. The daemon's image id is usually the
// config digest, which is layerlens' own id — but under the containerd image
// store a multi-platform image is identified by its *index* digest instead,
// which matches nothing. The display reference catches that case, since an
// image analyzed from either source records the reference it arrived under.
func (d *Docker) analyzedIndex(ctx context.Context) map[string]domain.Digest {
	out := map[string]domain.Digest{}
	if d.images == nil {
		return out
	}
	records, err := d.images.Images(ctx)
	if err != nil {
		d.log.Debug("docker listing: cross-reference analyzed images", "err", err)
		return out
	}
	for i := range records {
		out[string(records[i].ID)] = records[i].ID
		for _, ref := range records[i].RefNames {
			out[ref] = records[i].ID
		}
	}
	return out
}

// preferredRef picks the reference to show and to submit back.
func preferredRef(inspect *DockerInspect) string {
	for _, tag := range inspect.RepoTags {
		if tag != "" && !strings.HasSuffix(tag, "<none>:<none>") {
			return tag
		}
	}
	return ""
}

// Inspect reads one image's metadata. Cheap: no image data is transferred.
func (d *Docker) Inspect(ctx context.Context, ref string) (*DockerInspect, error) {
	api, err := d.client()
	if err != nil {
		return nil, err
	}
	return api.Inspect(ctx, ref)
}

// Save opens the `docker save` stream for one image, restricted to
// linux/amd64.
//
// One call, one stream, read once. go-containerregistry's daemon.Image would
// re-open the save for every layer access and, without
// daemon.WithUnbufferedOpener, buffer the whole tarball in memory — fatal at
// 25 GiB (DECISIONS A2).
func (d *Docker) Save(ctx context.Context, ref string) (io.ReadCloser, error) {
	api, err := d.client()
	if err != nil {
		return nil, err
	}
	return api.Save(ctx, ref)
}

// mobyAdapter implements DockerAPI against the real Engine API.
type mobyAdapter struct {
	cli *mobyclient.Client
}

func dialMoby(host string) (DockerAPI, error) {
	cli, err := mobyclient.New(
		// API-version negotiation is on by default in this client, so
		// the host is the only thing to configure.
		mobyclient.WithHost(host),
	)
	if err != nil {
		return nil, err
	}
	return &mobyAdapter{cli: cli}, nil
}

func (m *mobyAdapter) Ping(ctx context.Context) error {
	_, err := m.cli.Ping(ctx, mobyclient.PingOptions{NegotiateAPIVersion: true})
	return err
}

func (m *mobyAdapter) List(ctx context.Context) ([]DockerInspect, error) {
	listed, err := m.cli.ImageList(ctx, mobyclient.ImageListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]DockerInspect, 0, len(listed.Items))
	for _, item := range listed.Items {
		entry := DockerInspect{ID: item.ID, RepoTags: item.RepoTags, SizeBytes: item.Size}
		// One inspect per row: it transfers no image data and is what
		// supplies the platform and the digests the registry-preference
		// optimization keys on.
		if full, err := m.Inspect(ctx, item.ID); err == nil {
			entry = *full
			entry.RepoTags = item.RepoTags
		}
		out = append(out, entry)
	}
	return out, nil
}

func (m *mobyAdapter) Inspect(ctx context.Context, ref string) (*DockerInspect, error) {
	res, err := m.cli.ImageInspect(ctx, ref)
	if err != nil {
		return nil, err
	}
	out := &DockerInspect{
		ID:           res.ID,
		RepoTags:     res.RepoTags,
		RepoDigests:  res.RepoDigests,
		SizeBytes:    res.Size,
		OS:           res.Os,
		Architecture: res.Architecture,
	}
	out.DiffIDs = append(out.DiffIDs, res.RootFS.Layers...)
	return out, nil
}

func (m *mobyAdapter) Save(ctx context.Context, ref string) (io.ReadCloser, error) {
	// The platform is explicit: with the containerd image store a
	// multi-platform image otherwise saves every platform, which would
	// stream gigabytes layerlens then discards (DECISIONS A2 gotcha).
	res, err := m.cli.ImageSave(ctx, []string{ref},
		mobyclient.ImageSaveWithPlatforms(ocispec.Platform{OS: PlatformOS, Architecture: PlatformArch}))
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (m *mobyAdapter) Close() error { return m.cli.Close() }
