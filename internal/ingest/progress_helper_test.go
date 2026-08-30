package ingest_test

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"

	"github.com/ericsuh/layerlens/internal/domain"
	"github.com/ericsuh/layerlens/internal/ingest"
)

// RecordingReporter captures progress for assertions.
type RecordingReporter struct {
	ingest.NopReporter
	mu       sync.Mutex
	Phases   []string
	Started  []int
	Skipped  []int
	Finished []int
}

func (r *RecordingReporter) Phase(phase string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Phases = append(r.Phases, phase)
}

func (r *RecordingReporter) LayerStarted(index int, _ domain.Digest, _ int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Started = append(r.Started, index)
}

func (r *RecordingReporter) LayerFinished(index int, skipped bool, _ int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Finished = append(r.Finished, index)
	if skipped {
		r.Skipped = append(r.Skipped, index)
	}
}

// Snapshot returns a copy of what has been recorded.
func (r *RecordingReporter) Snapshot() (phases []string, started, finished, skipped []int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.Phases...), append([]int(nil), r.Started...),
		append([]int(nil), r.Finished...), append([]int(nil), r.Skipped...)
}

// sha256Hash renders the digest of a blob the way a descriptor spells it.
func sha256Hash(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}
