package server_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"

	"github.com/ericsuh/layerlens/internal/analyze"
	"github.com/ericsuh/layerlens/internal/domain"
)

// synthStore is an in-memory ImageStore + LayerIndexSource over generated
// layers.
//
// The vendored fixtures are the right substrate for conformance tests — they
// are the data the server actually serves — but they are deliberately small,
// and two of the properties under test here are about SHAPE and SCALE: a
// response's bound against a tree fat enough to blow it, and peak memory
// against a layer count no fixture has. Generating those in memory is the only
// way to assert them without shipping a gigabyte of fixture.
//
// Layer indexes are BUILT ON DEMAND, never cached: that is what makes the
// squash memory test meaningful — an index the store held would be alive
// whatever the squasher did with it.
type synthStore struct {
	mu   sync.Mutex
	recs map[domain.Digest]*domain.ImageRecord
	// build produces a layer's changeset from scratch on every call.
	build map[domain.Digest]func() []domain.Entry
	// onLoad, when set, runs before a layer index is built. The argument
	// is the layer's position in the order the images declare it.
	onLoad func(nth int)
	loads  int
}

func newSynthStore() *synthStore {
	return &synthStore{
		recs:  map[domain.Digest]*domain.ImageRecord{},
		build: map[domain.Digest]func() []domain.Entry{},
	}
}

// synthDigest is a well-formed digest derived from a label, so tests can name
// layers and images instead of juggling 64-character constants.
func synthDigest(label string) domain.Digest {
	sum := sha256.Sum256([]byte(label))
	return domain.DigestFromBytes(sum[:])
}

// addImage registers an image whose i-th layer is produced by layers[i].
func (s *synthStore) addImage(ref string, layers ...func() []domain.Entry) domain.Digest {
	id := synthDigest("image:" + ref)
	rec := &domain.ImageRecord{
		ID: id, RefNames: []string{ref}, Source: domain.SourceFixture,
		Platform: "linux/amd64", Pinned: true,
	}
	for i, build := range layers {
		diffID := synthDigest(fmt.Sprintf("layer:%s:%d", ref, i))
		s.build[diffID] = build
		rec.Layers = append(rec.Layers, domain.Layer{Index: i, DiffID: diffID})
	}
	s.recs[id] = rec
	return id
}

// evictLayer makes one already-declared layer unreadable, reproducing the
// state an LRU eviction between two requests leaves behind: the image record
// still names the layer, and loading it fails.
func (s *synthStore) evictLayer(ref string, i int) {
	delete(s.build, synthDigest(fmt.Sprintf("layer:%s:%d", ref, i)))
}

func (s *synthStore) Images(context.Context) ([]domain.ImageRecord, error) {
	out := make([]domain.ImageRecord, 0, len(s.recs))
	for _, rec := range s.recs {
		out = append(out, *rec)
	}
	return out, nil
}

func (s *synthStore) Image(_ context.Context, id domain.Digest) (*domain.ImageRecord, error) {
	rec, ok := s.recs[id]
	if !ok {
		return nil, fmt.Errorf("%w: image %s", domain.ErrNotFound, id)
	}
	return rec, nil
}

func (s *synthStore) Touch(context.Context, domain.Digest) error { return nil }

func (s *synthStore) LayerIndex(_ context.Context, diffID domain.Digest) (*domain.LayerIndex, error) {
	build, ok := s.build[diffID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", domain.ErrNotIndexed, diffID)
	}
	s.mu.Lock()
	nth := s.loads
	s.loads++
	hook := s.onLoad
	s.mu.Unlock()
	if hook != nil {
		hook(nth)
	}
	entries := build()
	return &domain.LayerIndex{
		SchemaVersion:   domain.LayerIndexSchemaVersion,
		DiffID:          diffID,
		ChangesetDigest: analyze.ChangesetDigest(entries),
		Entries:         entries,
	}, nil
}

// fanOutLayer builds a directory tree of `dirs` directories under /fan, each
// holding `children` files: the shape that turns a depth=2 response quadratic.
func fanOutLayer(dirs, children int, salt string) func() []domain.Entry {
	return func() []domain.Entry {
		entries := make([]domain.Entry, 0, dirs*(children+1)+1)
		entries = append(entries, domain.Entry{Path: "/fan", Kind: domain.KindDir, Mode: 0o755})
		for d := range dirs {
			dir := fmt.Sprintf("/fan/dir-%04d", d)
			entries = append(entries, domain.Entry{Path: dir, Kind: domain.KindDir, Mode: 0o755})
			for c := range children {
				p := fmt.Sprintf("%s/file-%04d.dat", dir, c)
				entries = append(entries, domain.Entry{
					Path: p, Kind: domain.KindFile, Mode: 0o644,
					Size:       int64(1000 + c),
					ContentSHA: synthDigest(p + salt),
				})
			}
		}
		return entries
	}
}

// flatLayer restates the same `files` paths every time, with content that
// depends on salt. Layers built this way stack to a tree of exactly `files`
// paths however many of them are applied — so a memory measurement over N of
// them varies only in N.
func flatLayer(files int, salt string) func() []domain.Entry {
	return func() []domain.Entry {
		entries := make([]domain.Entry, 0, files+1)
		entries = append(entries, domain.Entry{Path: "/data", Kind: domain.KindDir, Mode: 0o755})
		for i := range files {
			p := fmt.Sprintf("/data/f-%07d.bin", i)
			entries = append(entries, domain.Entry{
				Path: p, Kind: domain.KindFile, Mode: 0o644,
				Uname: "root", Gname: "root",
				Size:       int64(4096 + i),
				ContentSHA: synthDigest(p + salt),
			})
		}
		return entries
	}
}
