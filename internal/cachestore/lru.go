package cachestore

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ericsuh/layerlens/internal/domain"
)

// reserve charges n bytes against the cap on behalf of t, evicting if it has
// to and refusing if it cannot.
//
// The order of the two checks is the whole of RESEARCH Q7's "refuse, don't
// thrash": the *refusal* test runs first and looks only at what this ingest
// itself needs versus the space that could ever be freed (the cap minus the
// pinned images, which are never evictable). An image that fails it is
// rejected without a single byte of anyone else's cache being deleted. Only
// once we know the image *can* fit do we start evicting to make room for it.
func (s *Store) reserve(t *Txn, n int64) error {
	if n <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if t != nil {
		evictable := s.max - s.pinnedBytesLocked()
		if t.ownBytes+n > evictable {
			return fmt.Errorf(
				"%w: this image needs at least %d bytes of index but only %d are available under the %d byte cap (%d bytes are pinned)",
				ErrCacheFull, t.ownBytes+n, evictable, s.max, s.max-evictable)
		}
	}

	for s.accounted+n > s.max {
		if !s.evictLRULocked() {
			break
		}
	}
	if s.accounted+n > s.max {
		return fmt.Errorf("%w: %d bytes in use of a %d byte cap, and nothing else is evictable",
			ErrCacheFull, s.accounted, s.max)
	}

	s.accounted += n
	if t != nil {
		t.ownBytes += n
	}
	return nil
}

// reserveCeiling is the most t could ever be allowed to reserve: the cap minus
// what pinning has made unreclaimable, minus what this transaction already
// holds. It is the same quantity reserve's refusal test uses, read ahead of
// time so a staged write can be aborted the moment it passes it instead of
// being written in full and then refused.
//
// It is an upper bound, not a promise: reserve still runs afterwards and is
// still the authority. A write that exceeds this would have been refused
// there, so stopping it early can only turn a slow refusal into a fast one.
func (s *Store) reserveCeiling(t *Txn) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	ceiling := s.max - s.pinnedBytesLocked()
	if t != nil {
		ceiling -= t.ownBytes
	}
	if ceiling < 0 {
		return 0
	}
	return ceiling
}

// release gives back bytes reserved for a write that then failed.
func (s *Store) release(t *Txn, n int64) {
	if n <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accounted -= n
	if t != nil {
		t.ownBytes -= n
	}
}

// pinnedBytesLocked is the part of the cache that eviction can never reclaim:
// the pinned image records plus every layer any of them references.
func (s *Store) pinnedBytesLocked() int64 {
	var total int64
	counted := make(map[domain.Digest]struct{})
	for _, e := range s.images {
		if !e.rec.Pinned {
			continue
		}
		total += e.bytes
		for _, l := range e.rec.Layers {
			if _, dup := counted[l.DiffID]; dup {
				continue
			}
			counted[l.DiffID] = struct{}{}
			if le, ok := s.layers[l.DiffID]; ok {
				total += le.bytes
			}
		}
	}
	return total
}

// evictLRULocked removes the least recently used un-pinned image and any layer
// directories that nothing references any more. It reports whether it evicted
// anything.
//
// The retention unit is the image, and the record is removed *first*: from that
// moment readers cannot reach the image at all, so they never observe it with
// some of its layers already deleted. Comparisons already assembled in memory
// are unaffected — they hold no file handles.
func (s *Store) evictLRULocked() bool {
	var victim *imageEntry
	for _, e := range s.images {
		if e.rec.Pinned {
			continue
		}
		if victim == nil || e.rec.LastUsedAt.Before(victim.rec.LastUsedAt) ||
			(e.rec.LastUsedAt.Equal(victim.rec.LastUsedAt) && e.rec.ID < victim.rec.ID) {
			victim = e
		}
	}
	if victim == nil {
		return false
	}

	id := victim.rec.ID
	path, err := imagePathFor(s.root, id)
	if err != nil {
		// A record whose id is unusable as a path cannot have been
		// written by us; drop it from the table so the loop terminates.
		s.log.Error("cachestore: evicting record with malformed id", "id", id, "err", err)
		delete(s.images, id)
		s.accounted -= victim.bytes
		return true
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		s.log.Error("cachestore: remove image record", "id", id, "err", err)
		return false
	}
	delete(s.images, id)
	s.accounted -= victim.bytes
	s.log.Info("cachestore: evicted image", "id", id, "refNames", victim.rec.RefNames,
		"lastUsedAt", victim.rec.LastUsedAt, "bytes", victim.bytes)

	for _, l := range victim.rec.Layers {
		if s.layerReferencedLocked(l.DiffID) {
			continue
		}
		s.dropLayerLocked(l.DiffID)
	}
	return true
}

// layerReferencedLocked reports whether any retained image record — or any
// ingest still in flight — depends on diffID. This is the refcount that makes
// a layer shared between two images survive the eviction of one of them.
func (s *Store) layerReferencedLocked(diffID domain.Digest) bool {
	for _, e := range s.images {
		for _, l := range e.rec.Layers {
			if l.DiffID == diffID {
				return true
			}
		}
	}
	for t := range s.inflight {
		if _, ok := t.layers[diffID]; ok {
			return true
		}
	}
	return false
}

// dropLayerLocked removes a layer directory. The directory is renamed out of
// the layer store before it is deleted, so a concurrent reader that resolved
// the path a moment ago sees the whole directory disappear at once instead of
// finding it with files missing.
func (s *Store) dropLayerLocked(diffID domain.Digest) {
	e, ok := s.layers[diffID]
	if !ok {
		return
	}
	dir, err := layerDirFor(s.root, diffID)
	if err != nil {
		s.log.Error("cachestore: evicting layer with malformed digest", "diffId", diffID, "err", err)
		delete(s.layers, diffID)
		s.accounted -= e.bytes
		return
	}
	s.trashSeq++
	trash := filepath.Join(s.stagingRoot(), fmt.Sprintf("%s%d", trashDirPrefix, s.trashSeq))
	if err := os.Rename(dir, trash); err != nil {
		if !os.IsNotExist(err) {
			s.log.Error("cachestore: unlink layer dir", "diffId", diffID, "err", err)
			return
		}
	} else if err := os.RemoveAll(trash); err != nil {
		// The directory is already unreachable; a leftover under
		// staging/ is swept at the next start.
		s.log.Warn("cachestore: remove evicted layer", "diffId", diffID, "err", err)
	}
	delete(s.layers, diffID)
	s.accounted -= e.bytes
}
