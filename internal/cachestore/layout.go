// Package cachestore implements the durable on-disk analysis cache described
// in ARCHITECTURE §5: a schema-versioned tree under --data-dir holding one
// metadata index per layer (keyed by DiffID, so identical layers across images
// are stored exactly once) and one record per analyzed image.
//
// Three properties are load-bearing and are what the rest of this package is
// shaped around:
//
//   - **Atomicity.** Every file is written to a staging area, fsync'd and
//     renamed into place. A layer directory is committed index-first and
//     layer.json-last, and an image record is renamed in only after all of its
//     layers are committed, so a crash can never make a partially written
//     index or a record with missing layers visible. Whatever a crash does
//     leave behind is swept at the next Open.
//   - **Single writer.** One process per data directory, enforced by an
//     exclusive advisory flock taken at Open and held for the process
//     lifetime.
//   - **Bounded size.** Accounted bytes are tracked incrementally against an
//     injectable cap. Over the cap, un-pinned images are evicted in
//     lastUsedAt order; an image that cannot fit even with everything
//     evictable gone is refused with ErrCacheFull rather than thrash-evicting
//     the cache on its behalf (RESEARCH Q7).
package cachestore

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/ericsuh/layerlens/internal/domain"
)

// On-disk layout constants (ARCHITECTURE §5). The schema directory is a
// sibling, never a migration: a breaking format change writes v2/ and leaves
// v1/ to be swept by an operator.
const (
	schemaDir  = "v1"
	layersDir  = "layers"
	imagesDir  = "images"
	stagingDir = "staging"
	// algoDir is the digest algorithm component. Only sha256 exists
	// (domain.Digest validates the prefix), but keeping it in the path
	// leaves room for another one without moving every file.
	algoDir = "sha256"

	indexFileName  = "index.jsonl.zst"
	layerFileName  = "layer.json"
	recordFileExt  = ".json"
	lockFileName   = "lock"
	trashDirPrefix = "trash-"
	tmpFileSuffix  = ".tmp"

	// dirPerm and filePerm are deliberately owner-writable only for the
	// group/other bits that matter: the cache holds analysis metadata for
	// a service that runs as its own user.
	dirPerm  = 0o750
	filePerm = 0o640
)

// RecordSchemaVersion is the "v" field written into every image record and
// layer summary. Readers reject anything else and re-ingest instead of
// migrating — the whole tree is a cache.
const RecordSchemaVersion = 1

// ErrInvalidDigest reports a digest that cannot become a path component.
// It is an internal error: every digest reaching the store has already been
// validated at the boundary, and this is the backstop that keeps a malformed
// one out of filepath.Join (ARCHITECTURE §7.3).
var ErrInvalidDigest = errors.New("cachestore: invalid digest")

// ErrCacheFull reports that an image cannot fit under the configured cap even
// after everything evictable is gone. It maps to the API's cache_full/507.
var ErrCacheFull = errors.New("cachestore: cache is full")

// pathComponent validates d and returns the single path component derived from
// it: the 64-character lowercase-hex half, never the raw string.
//
// This is the choke point ARCHITECTURE §7.3 requires. A hostile digest such as
// "sha256:../../etc/passwd" fails domain.Digest.Validate (wrong length, and
// non-hex bytes), so it never reaches filepath.Join.
func pathComponent(d domain.Digest) (string, error) {
	if err := d.Validate(); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidDigest, err)
	}
	hex := d.Hex()
	if hex == "" {
		// Unreachable while Validate is correct; cheap insurance
		// against a future refactor decoupling the two.
		return "", fmt.Errorf("%w: %q has no hex component", ErrInvalidDigest, string(d))
	}
	return hex, nil
}

// layerDirFor returns the directory holding the index for diffID.
func layerDirFor(base string, diffID domain.Digest) (string, error) {
	hex, err := pathComponent(diffID)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, schemaDir, layersDir, algoDir, hex), nil
}

// imagePathFor returns the record file for an image id.
func imagePathFor(base string, id domain.Digest) (string, error) {
	hex, err := pathComponent(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, schemaDir, imagesDir, algoDir, hex+recordFileExt), nil
}

// isHexName reports whether name is a well-formed 64-character lowercase-hex
// digest body, i.e. something this package could have written.
func isHexName(name string) bool {
	if len(name) != 64 {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// digestFromHexName rebuilds a Digest from a validated path component.
func digestFromHexName(name string) (domain.Digest, bool) {
	if !isHexName(name) {
		return "", false
	}
	return domain.Digest(domain.DigestPrefix + name), true
}

// layerDoc is the layer.json sidecar: everything a reader needs about a layer
// without opening (and decompressing) its index. Its presence is also what
// marks a layer directory as committed.
type layerDoc struct {
	V               int           `json:"v"`
	DiffID          domain.Digest `json:"diffId"`
	ChangesetDigest domain.Digest `json:"changesetDigest"`
	ContentBytes    int64         `json:"contentBytes"`
	EntryCount      int           `json:"entryCount"`
	IndexBytes      int64         `json:"indexBytes"`
	Warnings        []string      `json:"warnings,omitempty"`
}

// imageDoc wraps an ImageRecord with the schema version. The record's fields
// are inlined by encoding/json, so the file is the §5 shape:
// {"v":1,"id":...,"lastUsedAt":...,"pinned":...,"layers":[...]}.
type imageDoc struct {
	V int `json:"v"`
	domain.ImageRecord
}

// LayerSummary is the cheap description of a stored layer, read from
// layer.json. It is what an ingest needs for a layer it is *not* re-streaming
// because the index already exists.
type LayerSummary struct {
	DiffID          domain.Digest
	ChangesetDigest domain.Digest
	ContentBytes    int64
	EntryCount      int
	// IndexBytes is the on-disk size of the whole layer directory, which is
	// what the byte accounting charges for.
	IndexBytes int64
	Warnings   []string
}
