package cachestore

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/ericsuh/layerlens/internal/domain"
	"github.com/ericsuh/layerlens/internal/index"
)

// DefaultTouchDebounce is the minimum interval between two rewrites of an
// image record for an LRU bump (ARCHITECTURE §5). Every comparison request
// touches both of its images; without a debounce a browser holding a tree open
// would rewrite two files per expand.
const DefaultTouchDebounce = 60 * time.Second

// Options configures Open.
type Options struct {
	// Root is the --data-dir. It is created if absent.
	Root string
	// MaxBytes is --cache-max-bytes. It is injectable precisely so tests
	// can drive eviction and the refusal path with a few kilobytes
	// (RESEARCH Q7).
	MaxBytes int64
	// Now defaults to time.Now. Tests substitute a controllable clock so
	// LRU ordering and Touch debouncing are asserted, not slept on.
	Now func() time.Time
	// TouchDebounce defaults to DefaultTouchDebounce.
	TouchDebounce time.Duration
	// Logger defaults to slog.Default().
	Logger *slog.Logger
}

type layerEntry struct {
	summary LayerSummary
	// bytes is the on-disk size of the whole layer directory.
	bytes int64
}

type imageEntry struct {
	rec domain.ImageRecord
	// bytes is the size of the record file.
	bytes int64
}

// Store is the durable cache. It implements domain.LayerIndexSource and
// domain.ImageStore.
type Store struct {
	root     string
	max      int64
	now      func() time.Time
	debounce time.Duration
	log      *slog.Logger
	lock     *fileLock

	// mu guards the in-memory tables and the byte accounting. File reads
	// happen outside it: committed files are immutable, so a reader either
	// opens the file it looked up or finds it gone (§5).
	mu        sync.RWMutex
	layers    map[domain.Digest]*layerEntry
	images    map[domain.Digest]*imageEntry
	accounted int64
	// inflight holds open transactions so that layers they have committed
	// but not yet attached to an image record are not mistaken for garbage
	// by the evictor.
	inflight map[*Txn]struct{}
	trashSeq uint64
}

var (
	_ domain.LayerIndexSource = (*Store)(nil)
	_ domain.ImageStore       = (*Store)(nil)
)

// Open prepares the cache directory, takes the process lock, sweeps whatever a
// previous crash left behind and rebuilds the byte accounting.
func Open(opts Options) (*Store, error) {
	if opts.Root == "" {
		return nil, errors.New("cachestore: Root must not be empty")
	}
	if opts.MaxBytes <= 0 {
		return nil, errors.New("cachestore: MaxBytes must be positive")
	}
	root, err := filepath.Abs(opts.Root)
	if err != nil {
		return nil, fmt.Errorf("cachestore: resolve root: %w", err)
	}
	s := &Store{
		root:     root,
		max:      opts.MaxBytes,
		now:      opts.Now,
		debounce: opts.TouchDebounce,
		log:      opts.Logger,
		layers:   map[domain.Digest]*layerEntry{},
		images:   map[domain.Digest]*imageEntry{},
		inflight: map[*Txn]struct{}{},
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.debounce <= 0 {
		s.debounce = DefaultTouchDebounce
	}
	if s.log == nil {
		s.log = slog.Default()
	}

	for _, dir := range []string{s.layersRoot(), s.imagesRoot(), s.stagingRoot()} {
		if err := os.MkdirAll(dir, dirPerm); err != nil {
			return nil, fmt.Errorf("cachestore: create %s: %w", dir, err)
		}
	}

	// The lock is taken before the sweep: sweeping a directory another
	// process is actively writing would delete its staging area.
	lock, err := acquireLock(filepath.Join(root, lockFileName))
	if err != nil {
		return nil, err
	}
	s.lock = lock

	if err := s.sweep(); err != nil {
		_ = lock.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the process lock. The store must not be used afterwards.
func (s *Store) Close() error { return s.lock.Close() }

// Root reports the resolved data directory.
func (s *Store) Root() string { return s.root }

// MaxBytes reports the configured cap.
func (s *Store) MaxBytes() int64 { return s.max }

// UsedBytes reports the currently accounted bytes under the schema directory.
func (s *Store) UsedBytes() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.accounted
}

func (s *Store) layersRoot() string  { return filepath.Join(s.root, schemaDir, layersDir, algoDir) }
func (s *Store) imagesRoot() string  { return filepath.Join(s.root, schemaDir, imagesDir, algoDir) }
func (s *Store) stagingRoot() string { return filepath.Join(s.root, schemaDir, stagingDir) }

// ---------------------------------------------------------------- startup

// sweep restores the invariants a crash can break and rebuilds the accounting.
//
// The three things it can find are exactly the three things the commit order
// makes possible: a staging directory from an ingest that never finished, a
// layer directory without its layer.json (the index was renamed in and the
// process died before the sidecar), and an image record whose layers are not
// all present (which the commit order forbids, so it means corruption).
func (s *Store) sweep() error {
	if err := os.RemoveAll(s.stagingRoot()); err != nil {
		return fmt.Errorf("cachestore: clear staging: %w", err)
	}
	if err := os.MkdirAll(s.stagingRoot(), dirPerm); err != nil {
		return fmt.Errorf("cachestore: recreate staging: %w", err)
	}
	if err := s.sweepLayers(); err != nil {
		return err
	}
	return s.sweepImages()
}

func (s *Store) sweepLayers() error {
	entries, err := os.ReadDir(s.layersRoot())
	if err != nil {
		return fmt.Errorf("cachestore: read layers: %w", err)
	}
	for _, e := range entries {
		path := filepath.Join(s.layersRoot(), e.Name())
		diffID, ok := digestFromHexName(e.Name())
		if !e.IsDir() || !ok {
			s.log.Warn("cachestore: discarding unrecognized entry in layer store", "path", path)
			if err := os.RemoveAll(path); err != nil {
				return fmt.Errorf("cachestore: remove %s: %w", path, err)
			}
			continue
		}
		summary, size, err := readLayerDir(path, diffID)
		if err != nil {
			// A layer dir without its sidecar is the documented
			// half-committed state; anything else unreadable is
			// equally worthless, and both are re-derivable by
			// re-ingesting.
			s.log.Warn("cachestore: sweeping incomplete layer", "diffId", diffID, "err", err)
			if err := os.RemoveAll(path); err != nil {
				return fmt.Errorf("cachestore: remove %s: %w", path, err)
			}
			continue
		}
		s.layers[diffID] = &layerEntry{summary: summary, bytes: size}
		s.accounted += size
	}
	return nil
}

func (s *Store) sweepImages() error {
	entries, err := os.ReadDir(s.imagesRoot())
	if err != nil {
		return fmt.Errorf("cachestore: read images: %w", err)
	}
	for _, e := range entries {
		path := filepath.Join(s.imagesRoot(), e.Name())
		rec, size, err := s.readImageFile(path, e)
		if err != nil {
			s.log.Warn("cachestore: sweeping unusable image record", "path", path, "err", err)
			if err := os.RemoveAll(path); err != nil {
				return fmt.Errorf("cachestore: remove %s: %w", path, err)
			}
			continue
		}
		s.images[rec.ID] = &imageEntry{rec: *rec, bytes: size}
		s.accounted += size
	}
	return nil
}

// readImageFile decodes and validates one record file. A record is only usable
// if every layer it names is present: the commit order guarantees that, so a
// violation means the file is corrupt and the image must be re-ingested.
func (s *Store) readImageFile(path string, e os.DirEntry) (*domain.ImageRecord, int64, error) {
	if e.IsDir() {
		return nil, 0, errors.New("not a file")
	}
	name := e.Name()
	if filepath.Ext(name) != recordFileExt {
		return nil, 0, fmt.Errorf("unexpected file name %q", name)
	}
	id, ok := digestFromHexName(name[:len(name)-len(recordFileExt)])
	if !ok {
		return nil, 0, fmt.Errorf("unexpected file name %q", name)
	}
	info, err := e.Info()
	if err != nil {
		return nil, 0, err
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is built from a validated digest under the data dir
	if err != nil {
		return nil, 0, err
	}
	var doc imageDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, 0, err
	}
	if doc.V != RecordSchemaVersion {
		return nil, 0, fmt.Errorf("record schema version %d (want %d)", doc.V, RecordSchemaVersion)
	}
	if doc.ID != id {
		return nil, 0, fmt.Errorf("record id %q does not match file name", doc.ID)
	}
	for _, l := range doc.Layers {
		if _, ok := s.layers[l.DiffID]; !ok {
			return nil, 0, fmt.Errorf("layer %s is missing", l.DiffID)
		}
	}
	rec := doc.ImageRecord
	return &rec, info.Size(), nil
}

// readLayerDir reads a committed layer directory's sidecar and measures the
// directory. The absence of layer.json is what "not committed" means.
func readLayerDir(dir string, diffID domain.Digest) (LayerSummary, int64, error) {
	data, err := os.ReadFile(filepath.Join(dir, layerFileName)) //nolint:gosec // dir is built from a validated digest under the data dir
	if err != nil {
		return LayerSummary{}, 0, err
	}
	var doc layerDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return LayerSummary{}, 0, err
	}
	if doc.V != RecordSchemaVersion {
		return LayerSummary{}, 0, fmt.Errorf("layer schema version %d (want %d)", doc.V, RecordSchemaVersion)
	}
	if doc.DiffID != diffID {
		return LayerSummary{}, 0, fmt.Errorf("layer.json diffId %q does not match its directory", doc.DiffID)
	}
	if _, err := os.Stat(filepath.Join(dir, indexFileName)); err != nil {
		return LayerSummary{}, 0, err
	}
	size, err := dirBytes(dir)
	if err != nil {
		return LayerSummary{}, 0, err
	}
	return LayerSummary{
		DiffID:          doc.DiffID,
		ChangesetDigest: doc.ChangesetDigest,
		ContentBytes:    doc.ContentBytes,
		EntryCount:      doc.EntryCount,
		IndexBytes:      size,
		Warnings:        doc.Warnings,
	}, size, nil
}

func dirBytes(dir string) (int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			return 0, err
		}
		total += info.Size()
	}
	return total, nil
}

// ---------------------------------------------------------------- reads

// LayerIndex implements domain.LayerIndexSource.
func (s *Store) LayerIndex(ctx context.Context, diffID domain.Digest) (*domain.LayerIndex, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir, err := layerDirFor(s.root, diffID)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	_, known := s.layers[diffID]
	s.mu.RUnlock()
	if !known {
		return nil, fmt.Errorf("%w: %s", domain.ErrNotIndexed, diffID)
	}

	// Deliberately outside the lock. The file is immutable once renamed
	// in, and eviction renames the whole directory away in one step, so a
	// racing reader either opens the complete old file or sees ENOENT —
	// never a torn one.
	f, err := os.Open(filepath.Join(dir, indexFileName)) //nolint:gosec // dir is built from a validated digest under the data dir
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", domain.ErrNotIndexed, diffID)
		}
		return nil, fmt.Errorf("cachestore: open layer index %s: %w", diffID, err)
	}
	defer func() { _ = f.Close() }()

	idx, err := index.Read(bufio.NewReader(f))
	if err != nil {
		return nil, fmt.Errorf("cachestore: read layer index %s: %w", diffID, err)
	}
	return idx, nil
}

// LayerSummary returns the sidecar description of a stored layer.
func (s *Store) LayerSummary(diffID domain.Digest) (LayerSummary, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.layers[diffID]
	if !ok {
		return LayerSummary{}, false
	}
	return e.summary, true
}

// HasLayer reports whether diffID is already indexed. It is what lets an
// ingest skip a layer without streaming a byte of it (§4.1).
func (s *Store) HasLayer(diffID domain.Digest) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.layers[diffID]
	return ok
}

// Images implements domain.ImageStore. The order is stable (by first display
// reference, then id) so the picker does not reshuffle between polls.
func (s *Store) Images(ctx context.Context) ([]domain.ImageRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	out := make([]domain.ImageRecord, 0, len(s.images))
	for _, e := range s.images {
		out = append(out, cloneRecord(&e.rec))
	}
	s.mu.RUnlock()

	sort.Slice(out, func(i, j int) bool {
		li, lj := firstRef(&out[i]), firstRef(&out[j])
		if li != lj {
			return li < lj
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Image implements domain.ImageStore.
func (s *Store) Image(ctx context.Context, id domain.Digest) (*domain.ImageRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	e, ok := s.images[id]
	var rec domain.ImageRecord
	if ok {
		rec = cloneRecord(&e.rec)
	}
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: image %s", domain.ErrNotFound, id)
	}
	return &rec, nil
}

func firstRef(r *domain.ImageRecord) string {
	if len(r.RefNames) == 0 {
		return ""
	}
	return r.RefNames[0]
}

// cloneRecord copies a record deeply enough that a caller cannot mutate the
// store's copy through the returned slices.
func cloneRecord(r *domain.ImageRecord) domain.ImageRecord {
	out := *r
	out.RefNames = append([]string(nil), r.RefNames...)
	out.Layers = append([]domain.Layer(nil), r.Layers...)
	return out
}

// ---------------------------------------------------------------- writes

// Begin opens an ingest transaction with its own staging directory.
//
// The transaction is the unit that owns the cache-full budget: its committed
// layers are charged to it, and aborting it removes only what it wrote.
func (s *Store) Begin() (*Txn, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, fmt.Errorf("cachestore: generate staging id: %w", err)
	}
	// Server-generated random hex, never user input — ARCHITECTURE §7.3.
	id := hex.EncodeToString(raw[:])
	dir := filepath.Join(s.stagingRoot(), id)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return nil, fmt.Errorf("cachestore: create staging dir: %w", err)
	}
	t := &Txn{s: s, id: id, dir: dir, layers: map[domain.Digest]struct{}{}}
	s.mu.Lock()
	s.inflight[t] = struct{}{}
	s.mu.Unlock()
	return t, nil
}

// Txn is one image ingest in progress. It is not safe for concurrent use by
// multiple goroutines; one ingest owns one transaction.
type Txn struct {
	s   *Store
	id  string
	dir string
	// layers are the DiffIDs this ingest depends on, whether it wrote them
	// or found them already present. Holding them here is what stops the
	// evictor from deleting a layer that no image record references *yet*.
	layers map[domain.Digest]struct{}
	// ownBytes is what this ingest has added to the accounting.
	ownBytes int64
	done     bool
}

// ID is the staging identifier, for logging.
func (t *Txn) ID() string { return t.id }

// UseLayer records a dependency on an already-indexed layer and returns its
// summary. It reports false when the layer is not in the cache.
//
// Declaring the dependency matters even though the transaction did not write
// the layer: until the image record commits, nothing else references it, and
// without this the evictor would be free to delete a layer the in-flight
// ingest is about to name.
func (t *Txn) UseLayer(diffID domain.Digest) (LayerSummary, bool) {
	// The lookup and the dependency record happen under one lock hold:
	// the evictor inspects t.layers while holding the write lock, so a
	// gap here would let it delete the layer between the two steps.
	t.s.mu.RLock()
	defer t.s.mu.RUnlock()
	e, ok := t.s.layers[diffID]
	if !ok {
		return LayerSummary{}, false
	}
	t.layers[diffID] = struct{}{}
	return e.summary, true
}

// PutLayer stores idx unless its DiffID is already indexed, and in either case
// records the dependency so the layer survives until the image record commits.
//
// Returns ErrCacheFull when the image cannot fit under the cap.
func (t *Txn) PutLayer(idx *domain.LayerIndex) error {
	if t.done {
		return errors.New("cachestore: transaction already finished")
	}
	if idx == nil {
		return errors.New("cachestore: nil layer index")
	}
	if _, err := pathComponent(idx.DiffID); err != nil {
		return err
	}
	if _, ok := t.UseLayer(idx.DiffID); ok {
		return nil
	}

	stagedIndex := filepath.Join(t.dir, idx.DiffID.Hex()+"-"+indexFileName+tmpFileSuffix)
	stagedDoc := filepath.Join(t.dir, idx.DiffID.Hex()+"-"+layerFileName+tmpFileSuffix)
	// Both files are written before anything is reserved or renamed: the
	// compressed index size is not knowable in advance (§5), so the budget
	// check is enforced here, against the real number.
	indexBytes, err := writeStaged(stagedIndex, func(w io.Writer) error {
		return index.Write(w, idx)
	})
	if err != nil {
		return err
	}
	doc := layerDoc{
		V:               RecordSchemaVersion,
		DiffID:          idx.DiffID,
		ChangesetDigest: idx.ChangesetDigest,
		ContentBytes:    idx.ContentBytes,
		EntryCount:      len(idx.Entries),
		IndexBytes:      indexBytes,
		Warnings:        idx.Warnings,
	}
	docBytes, err := writeStaged(stagedDoc, func(w io.Writer) error {
		return json.NewEncoder(w).Encode(&doc)
	})
	if err != nil {
		return err
	}

	total := indexBytes + docBytes
	if err := t.s.reserve(t, total); err != nil {
		_ = os.Remove(stagedIndex)
		_ = os.Remove(stagedDoc)
		return err
	}

	if err := t.commitLayer(idx.DiffID, stagedIndex, stagedDoc); err != nil {
		t.s.release(t, total)
		return err
	}

	summary := LayerSummary{
		DiffID:          doc.DiffID,
		ChangesetDigest: doc.ChangesetDigest,
		ContentBytes:    doc.ContentBytes,
		EntryCount:      doc.EntryCount,
		IndexBytes:      total,
		Warnings:        doc.Warnings,
	}
	t.s.mu.Lock()
	t.s.layers[idx.DiffID] = &layerEntry{summary: summary, bytes: total}
	t.layers[idx.DiffID] = struct{}{}
	t.s.mu.Unlock()
	return nil
}

// commitLayer performs the §5 commit order: create the directory, rename the
// index in, then the sidecar. A crash anywhere before the last rename leaves a
// directory the next sweep deletes; there is no window in which a half-written
// index is visible under a name a reader would trust.
func (t *Txn) commitLayer(diffID domain.Digest, stagedIndex, stagedDoc string) error {
	dir, err := layerDirFor(t.s.root, diffID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("cachestore: create layer dir: %w", err)
	}
	if err := renameSynced(stagedIndex, filepath.Join(dir, indexFileName)); err != nil {
		return err
	}
	if err := renameSynced(stagedDoc, filepath.Join(dir, layerFileName)); err != nil {
		return err
	}
	// Publish the directory itself durably, so the parent's entry cannot
	// outlive a crash without the files it points at.
	return syncDir(filepath.Dir(dir))
}

// Commit writes the image record, which is the moment the image becomes
// visible. Every layer it names is already committed by construction.
func (t *Txn) Commit(rec *domain.ImageRecord) error {
	if t.done {
		return errors.New("cachestore: transaction already finished")
	}
	if rec == nil {
		return errors.New("cachestore: nil image record")
	}
	final, err := imagePathFor(t.s.root, rec.ID)
	if err != nil {
		return err
	}
	for _, l := range rec.Layers {
		if !t.s.HasLayer(l.DiffID) {
			return fmt.Errorf("cachestore: image %s references uncommitted layer %s", rec.ID, l.DiffID)
		}
	}

	now := t.s.now()
	stored := cloneRecord(rec)
	if stored.IngestedAt.IsZero() {
		stored.IngestedAt = now
	}
	stored.LastUsedAt = now

	staged := filepath.Join(t.dir, rec.ID.Hex()+recordFileExt+tmpFileSuffix)
	size, err := writeStaged(staged, func(w io.Writer) error {
		return json.NewEncoder(w).Encode(&imageDoc{V: RecordSchemaVersion, ImageRecord: stored})
	})
	if err != nil {
		return err
	}
	if err := t.s.reserve(t, size); err != nil {
		_ = os.Remove(staged)
		return err
	}
	if err := renameSynced(staged, final); err != nil {
		t.s.release(t, size)
		return err
	}

	t.s.mu.Lock()
	if prev, ok := t.s.images[stored.ID]; ok {
		// Re-ingesting an existing image replaces its record; the old
		// file's bytes are no longer on disk.
		t.s.accounted -= prev.bytes
	}
	t.s.images[stored.ID] = &imageEntry{rec: stored, bytes: size}
	t.s.mu.Unlock()
	return t.finish()
}

// Abort discards the staging area. Layers this transaction already committed
// are kept: they are valid, self-contained and resumable, which is what makes
// the layer the durable checkpoint unit (§4.1). Anything they leave
// unreferenced becomes evictable the moment the transaction closes.
func (t *Txn) Abort() error { return t.finish() }

func (t *Txn) finish() error {
	if t.done {
		return nil
	}
	t.done = true
	t.s.mu.Lock()
	delete(t.s.inflight, t)
	t.s.mu.Unlock()
	if err := os.RemoveAll(t.dir); err != nil {
		return fmt.Errorf("cachestore: clean staging %s: %w", t.dir, err)
	}
	return nil
}

// Touch implements domain.ImageStore: it bumps the LRU clock, debounced.
func (s *Store) Touch(ctx context.Context, id domain.Digest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	now := s.now()

	s.mu.Lock()
	e, ok := s.images[id]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("%w: image %s", domain.ErrNotFound, id)
	}
	if now.Sub(e.rec.LastUsedAt) < s.debounce {
		s.mu.Unlock()
		return nil
	}
	updated := cloneRecord(&e.rec)
	updated.LastUsedAt = now
	// The in-memory clock moves first so a burst of concurrent requests
	// debounces against the new value rather than all deciding to write.
	e.rec.LastUsedAt = now
	s.mu.Unlock()

	if err := s.writeRecord(&updated); err != nil {
		return err
	}
	return nil
}

// writeRecord rewrites a record via staging + rename, so a concurrent reader
// sees the old file or the new one and never a partial write.
func (s *Store) writeRecord(rec *domain.ImageRecord) error {
	final, err := imagePathFor(s.root, rec.ID)
	if err != nil {
		return err
	}
	staged := filepath.Join(s.stagingRoot(), rec.ID.Hex()+recordFileExt+tmpFileSuffix)
	size, err := writeStaged(staged, func(w io.Writer) error {
		return json.NewEncoder(w).Encode(&imageDoc{V: RecordSchemaVersion, ImageRecord: *rec})
	})
	if err != nil {
		return err
	}
	if err := renameSynced(staged, final); err != nil {
		return err
	}
	s.mu.Lock()
	if e, ok := s.images[rec.ID]; ok {
		s.accounted += size - e.bytes
		e.bytes = size
	}
	s.mu.Unlock()
	return nil
}

// ---------------------------------------------------------------- file io

// writeStaged writes a staging file durably and returns its size. The fsync is
// what makes the subsequent rename a commit rather than a promise.
func writeStaged(path string, write func(io.Writer) error) (int64, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, filePerm) //nolint:gosec // path is under the data dir, built from a validated digest
	if err != nil {
		return 0, fmt.Errorf("cachestore: create %s: %w", path, err)
	}
	bw := bufio.NewWriter(f)
	if err := write(bw); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return 0, fmt.Errorf("cachestore: write %s: %w", path, err)
	}
	if err := bw.Flush(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return 0, fmt.Errorf("cachestore: flush %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return 0, fmt.Errorf("cachestore: sync %s: %w", path, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return 0, fmt.Errorf("cachestore: stat %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return 0, fmt.Errorf("cachestore: close %s: %w", path, err)
	}
	return info.Size(), nil
}

// renameSynced renames a staged file into place and fsyncs the destination
// directory so the new name survives a crash.
func renameSynced(from, to string) error {
	if err := os.Rename(from, to); err != nil {
		return fmt.Errorf("cachestore: commit %s: %w", to, err)
	}
	return syncDir(filepath.Dir(to))
}

func syncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // dir is under the data dir
	if err != nil {
		return fmt.Errorf("cachestore: open %s: %w", dir, err)
	}
	defer func() { _ = d.Close() }()
	if err := d.Sync(); err != nil {
		// Some filesystems refuse fsync on a directory. The rename is
		// still atomic; only its durability across a power cut is
		// weaker, which is not worth failing an ingest over.
		return nil //nolint:nilerr // documented above
	}
	return nil
}
