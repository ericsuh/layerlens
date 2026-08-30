package ingest

import (
	"github.com/ericsuh/layerlens/internal/analyze"
	"github.com/ericsuh/layerlens/internal/domain"
)

// Ingest phases, reported to the UI as the step list of DESIGN §4.4.
const (
	// PhaseResolving covers the manifest/index fetch and platform
	// selection — indeterminate, sub-second.
	PhaseResolving = "resolving"
	// PhaseDownloading is the determinate one: bytes and layers are known.
	PhaseDownloading = "downloading"
	// PhaseFinalizing covers chain IDs, history mapping and the record
	// write. Short, indeterminate.
	PhaseFinalizing = "finalizing"
)

// Reporter receives ingest progress.
//
// Progress here is *measured*, never simulated: the byte counter it hands out
// is the same one analyze.IndexLayer increments as it pulls bytes off the
// wire, so what the progress bar shows is exactly what has been read. That is
// the difference between a 25 GiB pull you can trust and a spinner.
//
// Implementations must be safe for concurrent use: the counter is written from
// the streaming goroutine while an HTTP poll reads the snapshot.
type Reporter interface {
	// Phase announces a new phase.
	Phase(phase string)
	// Totals announces the denominators once the manifest is known.
	// bytesTotal is 0 when unknown; estimated is true on the docker path,
	// where the only available total is the daemon's own size estimate.
	Totals(bytesTotal int64, layersTotal int, estimated bool)
	// LayerStarted announces the layer now streaming. size is its
	// compressed size, or 0 when unknown.
	LayerStarted(index int, digest domain.Digest, size int64)
	// LayerFinished closes out a layer. skipped is true when the layer was
	// already indexed and cost nothing; size then advances the byte
	// counter so the bar still reaches its total.
	LayerFinished(index int, skipped bool, size int64)
	// Bytes is the counter IndexLayer accumulates into. The same counter is
	// used for every layer of one ingest, so it reads as bytes-done for the
	// whole pull.
	Bytes() *analyze.ByteCounter
}

// NopReporter discards progress. It is what the fixture load and every test
// that does not assert on progress uses.
type NopReporter struct {
	counter analyze.ByteCounter
}

// Phase implements Reporter.
func (n *NopReporter) Phase(string) {}

// Totals implements Reporter.
func (n *NopReporter) Totals(int64, int, bool) {}

// LayerStarted implements Reporter.
func (n *NopReporter) LayerStarted(int, domain.Digest, int64) {}

// LayerFinished implements Reporter.
func (n *NopReporter) LayerFinished(int, bool, int64) {}

// Bytes implements Reporter. The counter is per-instance, so a NopReporter is
// still a usable sink for a real ingest.
func (n *NopReporter) Bytes() *analyze.ByteCounter { return &n.counter }
