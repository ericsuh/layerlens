package cachestore

import (
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/domain"
)

// These tests are white-box (package cachestore, not cachestore_test) for one
// reason: the on-disk layout *is* the contract being tested. Asserting that a
// crash between two renames leaves a sweepable state means constructing that
// state with the same path helpers the implementation uses, so that the test
// cannot pass against a layout the code no longer writes.

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// clock is an injectable time source. LRU order and Touch debouncing are both
// defined in terms of wall-clock differences, and a test that slept for them
// would be both slow and flaky.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock {
	return &clock{t: time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)}
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// dig derives a well-formed digest from a label, so tests can name layers
// ("base", "npm") instead of juggling 64-character constants.
func dig(label string) domain.Digest {
	sum := sha256.Sum256([]byte(label))
	return domain.DigestFromBytes(sum[:])
}

// makeIndex builds a layer index of roughly predictable size: entries is the
// only knob that matters for the byte accounting tests.
func makeIndex(label string, entries int) *domain.LayerIndex {
	idx := &domain.LayerIndex{
		SchemaVersion:   domain.LayerIndexSchemaVersion,
		DiffID:          dig(label),
		ChangesetDigest: dig("changeset:" + label),
		Entries:         make([]domain.Entry, 0, entries),
	}
	for i := 0; i < entries; i++ {
		path := fmt.Sprintf("/%s/file-%06d", label, i)
		idx.Entries = append(idx.Entries, domain.Entry{
			Path:       path,
			Kind:       domain.KindFile,
			Mode:       0o644,
			Size:       int64(1000 + i),
			ContentSHA: dig(path),
		})
		idx.ContentBytes += int64(1000 + i)
	}
	return idx
}

// openStore opens a store with a controllable clock and a discard logger.
func openStore(t *testing.T, root string, maxBytes int64, c *clock) *Store {
	t.Helper()
	s, err := Open(Options{
		Root:          root,
		MaxBytes:      maxBytes,
		Now:           c.now,
		TouchDebounce: time.Minute,
		Logger:        discard(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// putImage commits one image made of the given layer indexes.
func putImage(t *testing.T, s *Store, name string, pinned bool, indexes ...*domain.LayerIndex) *domain.ImageRecord {
	t.Helper()
	rec, err := tryPutImage(s, name, pinned, indexes...)
	require.NoError(t, err)
	return rec
}

// tryPutImage is putImage without the assertion, for the refusal tests.
func tryPutImage(s *Store, name string, pinned bool, indexes ...*domain.LayerIndex) (*domain.ImageRecord, error) {
	txn, err := s.Begin()
	if err != nil {
		return nil, err
	}
	rec := &domain.ImageRecord{
		ID:       dig("image:" + name),
		RefNames: []string{name},
		Source:   domain.SourceFixture,
		Platform: "linux/amd64",
		Pinned:   pinned,
	}
	for _, idx := range indexes {
		if err := txn.PutLayer(idx); err != nil {
			_ = txn.Abort()
			return nil, err
		}
		rec.Layers = append(rec.Layers, domain.Layer{
			Index:           len(rec.Layers),
			DiffID:          idx.DiffID,
			ChangesetDigest: idx.ChangesetDigest,
			ContentBytes:    idx.ContentBytes,
			EntryCount:      len(idx.Entries),
		})
		rec.TotalBytes += idx.ContentBytes
	}
	if err := txn.Commit(rec); err != nil {
		_ = txn.Abort()
		return nil, err
	}
	return rec, nil
}
