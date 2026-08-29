// Package index serializes per-layer changesets to the on-disk index format:
// a zstd stream of JSON lines, header line first (ARCHITECTURE §5).
//
// JSON *lines* rather than one big array is deliberate: the indexer can write
// entries as it streams a layer, a reader can stop after the header, and a
// truncated file fails loudly instead of silently losing its tail.
//
// It imports domain and a zstd codec, nothing else.
package index

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"

	"github.com/ericsuh/layerlens/internal/domain"
)

// SchemaVersion is the major version written into every index header.
const SchemaVersion = domain.LayerIndexSchemaVersion

// ErrSchemaVersion reports an index written by an incompatible version of
// layerlens. Indexes are a cache, so the caller re-indexes rather than
// migrating.
var ErrSchemaVersion = errors.New("unsupported index schema version")

// ErrTruncated reports an index whose entry count does not match its header,
// or whose compressed stream ended mid-value.
var ErrTruncated = errors.New("truncated layer index")

// Header is the first line of an index file. It carries everything needed to
// describe a layer without reading its entries, so callers that only need the
// summary never pay for the body.
//
// The DiffID/EntryCount pair is what ARCHITECTURE §5 specifies; the remaining
// fields make the codec a lossless round-trip of domain.LayerIndex on its own
// (see DECISIONS "Implementation deltas", phase 002).
type Header struct {
	V               int           `json:"v"`
	DiffID          domain.Digest `json:"diffId"`
	EntryCount      int           `json:"entryCount"`
	ChangesetDigest domain.Digest `json:"changesetDigest,omitempty"`
	ContentBytes    int64         `json:"contentBytes,omitempty"`
	Warnings        []string      `json:"warnings,omitempty"`
}

// Write serializes idx as a zstd-compressed JSONL stream.
func Write(w io.Writer, idx *domain.LayerIndex) (err error) {
	if idx == nil {
		return errors.New("index: nil layer index")
	}
	zw, err := zstd.NewWriter(w)
	if err != nil {
		return fmt.Errorf("index: open zstd writer: %w", err)
	}
	defer func() {
		cerr := zw.Close()
		if err == nil {
			err = cerr
		}
	}()

	enc := json.NewEncoder(zw)
	hdr := Header{
		V:               SchemaVersion,
		DiffID:          idx.DiffID,
		EntryCount:      len(idx.Entries),
		ChangesetDigest: idx.ChangesetDigest,
		ContentBytes:    idx.ContentBytes,
		Warnings:        idx.Warnings,
	}
	if err := enc.Encode(&hdr); err != nil {
		return fmt.Errorf("index: write header: %w", err)
	}
	for i := range idx.Entries {
		if err := enc.Encode(&idx.Entries[i]); err != nil {
			return fmt.Errorf("index: write entry %d: %w", i, err)
		}
	}
	return nil
}

// ReadHeader decodes only the header line. The rest of the stream is left
// unread.
func ReadHeader(r io.Reader) (Header, error) {
	zr, err := zstd.NewReader(r)
	if err != nil {
		return Header{}, fmt.Errorf("index: open zstd reader: %w", err)
	}
	defer zr.Close()

	return decodeHeader(json.NewDecoder(zr.IOReadCloser()))
}

// Read decodes a complete layer index and verifies that the stream was not
// truncated.
func Read(r io.Reader) (*domain.LayerIndex, error) {
	zr, err := zstd.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("index: open zstd reader: %w", err)
	}
	defer zr.Close()

	dec := json.NewDecoder(zr.IOReadCloser())
	hdr, err := decodeHeader(dec)
	if err != nil {
		return nil, err
	}

	// The header is untrusted input as far as allocation goes: preallocate
	// a sane chunk and let append grow it, rather than honouring a hostile
	// entryCount up front.
	entries := make([]domain.Entry, 0, min(hdr.EntryCount, 4096))
	for {
		var e domain.Entry
		err := dec.Decode(&e)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// A short zstd frame or a half-written line both land
			// here; either way the file is unusable.
			if errors.Is(err, io.ErrUnexpectedEOF) {
				return nil, fmt.Errorf("%w: %w", ErrTruncated, err)
			}
			return nil, fmt.Errorf("index: read entry %d: %w", len(entries), err)
		}
		entries = append(entries, e)
	}
	if len(entries) != hdr.EntryCount {
		return nil, fmt.Errorf("%w: header declares %d entries, found %d",
			ErrTruncated, hdr.EntryCount, len(entries))
	}

	return &domain.LayerIndex{
		SchemaVersion:   hdr.V,
		DiffID:          hdr.DiffID,
		ChangesetDigest: hdr.ChangesetDigest,
		ContentBytes:    hdr.ContentBytes,
		Entries:         entries,
		Warnings:        hdr.Warnings,
	}, nil
}

func decodeHeader(dec *json.Decoder) (Header, error) {
	var hdr Header
	if err := dec.Decode(&hdr); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return Header{}, fmt.Errorf("%w: missing header", ErrTruncated)
		}
		return Header{}, fmt.Errorf("index: read header: %w", err)
	}
	if hdr.V != SchemaVersion {
		return Header{}, fmt.Errorf("%w: %d (want %d)", ErrSchemaVersion, hdr.V, SchemaVersion)
	}
	return hdr, nil
}
