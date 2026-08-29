package domain

import (
	"context"
	"time"
)

// LayerIndexSource yields the stored per-layer changeset for a DiffID
// (ARCHITECTURE §2.1). Implemented by internal/cachestore.
type LayerIndexSource interface {
	// LayerIndex returns the index for diffID, or ErrNotIndexed.
	LayerIndex(ctx context.Context, diffID Digest) (*LayerIndex, error)
}

// ImageStore lists and fetches analyzed-image records. Implemented by
// internal/cachestore.
type ImageStore interface {
	Images(ctx context.Context) ([]ImageRecord, error)
	// Image returns the record for id, or ErrNotFound.
	Image(ctx context.Context, id Digest) (*ImageRecord, error)
	// Touch bumps the LRU clock; implementations debounce it.
	Touch(ctx context.Context, id Digest) error
}

// PullID is an opaque, process-local identifier for an in-flight or finished
// ingest.
type PullID string

// Ingest sources accepted by IngestRequest.
const (
	IngestSourceRegistry = "registry"
	IngestSourceDocker   = "docker"
)

// IngestRequest asks the Ingester to analyze one image.
type IngestRequest struct {
	// Source is IngestSourceRegistry or IngestSourceDocker.
	Source string `json:"source"`
	// Reference is the user-supplied image reference, unvalidated.
	Reference string `json:"reference"`
}

// Pull states reported by PullStatus.
const (
	PullResolving = "resolving"
	PullRunning   = "running"
	PullDone      = "done"
	PullFailed    = "error"
	PullCancelled = "cancelled"
)

// PullProgress describes the layer currently being streamed.
type PullProgress struct {
	Index      int    `json:"index"`
	Digest     string `json:"digest"`
	BytesDone  int64  `json:"bytesDone"`
	BytesTotal *int64 `json:"bytesTotal,omitempty"`
}

// PullFailure is the machine-readable failure of a pull.
type PullFailure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// PullStatus is the polling payload for an ingest (ARCHITECTURE §6.3).
type PullStatus struct {
	ID        PullID    `json:"id"`
	Reference string    `json:"reference"`
	Source    string    `json:"source"`
	State     string    `json:"state"`
	StartedAt time.Time `json:"startedAt"`

	// BytesTotal is exact on the registry path and absent on the daemon
	// path, where BytesEstimated is true.
	BytesTotal     *int64 `json:"bytesTotal,omitempty"`
	BytesDone      int64  `json:"bytesDone"`
	BytesEstimated bool   `json:"bytesEstimated"`

	LayersTotal *int `json:"layersTotal,omitempty"`
	// LayersDone includes layers skipped because they were already indexed.
	LayersDone    int `json:"layersDone"`
	LayersSkipped int `json:"layersSkipped"`

	CurrentLayer *PullProgress `json:"currentLayer,omitempty"`
	ImageID      Digest        `json:"imageId,omitempty"`
	Error        *PullFailure  `json:"error,omitempty"`
}

// DockerImageSummary is one image offered by the local Docker daemon.
type DockerImageSummary struct {
	ID       Digest   `json:"id"`
	RefNames []string `json:"refNames"`
	Size     int64    `json:"size"`
}

// DockerListing is the daemon's image list, plus whether the daemon was
// reachable at all.
type DockerListing struct {
	Available bool                 `json:"available"`
	Images    []DockerImageSummary `json:"images"`
}

// Ingester is what the HTTP layer sees of pulling and analyzing images
// (ARCHITECTURE §2.1). Implemented by internal/ingest.
type Ingester interface {
	// Start validates the request, dedupes against in-flight pulls and
	// returns the pull's id.
	Start(ctx context.Context, req IngestRequest) (PullID, error)
	Status(id PullID) (*PullStatus, error)
	Cancel(id PullID) error
	ListDockerImages(ctx context.Context) (DockerListing, error)
}
