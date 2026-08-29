package index_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/domain"
	"github.com/ericsuh/layerlens/internal/index"
)

func sample() *domain.LayerIndex {
	return &domain.LayerIndex{
		SchemaVersion:   index.SchemaVersion,
		DiffID:          domain.MustDigest("sha256:" + rep64('a')),
		ChangesetDigest: domain.MustDigest("sha256:" + rep64('b')),
		ContentBytes:    4108,
		Entries: []domain.Entry{
			{
				Path: "/app", Kind: domain.KindDir, Mode: 0o755,
				Uname: "root", Gname: "root", MtimeUnix: 1700000000,
			},
			{
				Path: "/app/current", Kind: domain.KindSymlink, Mode: 0o777,
				LinkTarget: "server.js", MtimeUnix: 1700000001,
			},
			{
				Path: "/app/server.js", Kind: domain.KindFile, Mode: 0o644,
				UID: 1000, GID: 1000, Uname: "node", Gname: "node",
				Size: 4096, MtimeUnix: 1700000002,
				ContentSHA: domain.MustDigest("sha256:" + rep64('c')),
				Xattrs: map[string]string{
					"security.capability": "\x01\x00\x00\x02",
					"user.note":           "kept",
				},
			},
			{Path: "/app/old.js", Kind: domain.KindWhiteout},
			{Path: "/var/cache", Kind: domain.KindOpaque},
			{
				Path: "/dev/sda", Kind: domain.KindDevice, Mode: 0o660,
				Devmajor: 8, Devminor: 0,
			},
			{Path: "/bin/sh", Kind: domain.KindHardlink, Mode: 0o755, LinkTarget: "/bin/busybox"},
			{Path: "/run/pipe", Kind: domain.KindFifo, Mode: 0o600},
		},
		Warnings: []string{`skipped entry with unsafe name "../../etc/passwd"`},
	}
}

func rep64(c byte) string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = c
	}
	return string(b)
}

func encode(t *testing.T, idx *domain.LayerIndex) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, index.Write(&buf, idx))
	return buf.Bytes()
}

func TestRoundtrip(t *testing.T) {
	t.Parallel()

	want := sample()
	got, err := index.Read(bytes.NewReader(encode(t, want)))
	require.NoError(t, err)
	// Deep equality, xattr maps and warnings included: the codec is the
	// only thing between a layer we streamed once and every later answer
	// about it, so nothing may be silently dropped.
	assert.Equal(t, want, got)
}

func TestRoundtripEmptyIndex(t *testing.T) {
	t.Parallel()

	want := &domain.LayerIndex{
		SchemaVersion: index.SchemaVersion,
		DiffID:        domain.MustDigest("sha256:" + rep64('d')),
		Entries:       []domain.Entry{},
	}
	got, err := index.Read(bytes.NewReader(encode(t, want)))
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestHeaderFirstLine(t *testing.T) {
	t.Parallel()

	idx := sample()
	hdr, err := index.ReadHeader(bytes.NewReader(encode(t, idx)))
	require.NoError(t, err)

	assert.Equal(t, index.SchemaVersion, hdr.V)
	assert.Equal(t, idx.DiffID, hdr.DiffID)
	assert.Equal(t, len(idx.Entries), hdr.EntryCount)
	assert.Equal(t, idx.ChangesetDigest, hdr.ChangesetDigest)
	assert.Equal(t, idx.ContentBytes, hdr.ContentBytes)
	assert.Equal(t, idx.Warnings, hdr.Warnings)
}

func TestUnknownMajorVersionRejected(t *testing.T) {
	t.Parallel()

	future := sample()
	raw := encode(t, future)
	// Rewrite the header line with a version this build cannot understand.
	tampered := encodeWithHeaderVersion(t, future, index.SchemaVersion+1)
	require.NotEqual(t, raw, tampered)

	_, err := index.Read(bytes.NewReader(tampered))
	require.ErrorIs(t, err, index.ErrSchemaVersion)

	_, err = index.ReadHeader(bytes.NewReader(tampered))
	require.ErrorIs(t, err, index.ErrSchemaVersion)

	// Version 0 (an absent "v") is equally unacceptable: a reader must
	// never treat an unlabeled file as current.
	_, err = index.Read(bytes.NewReader(encodeWithHeaderVersion(t, future, 0)))
	require.ErrorIs(t, err, index.ErrSchemaVersion)
}

func TestTruncatedStreamDetected(t *testing.T) {
	t.Parallel()

	raw := encode(t, sample())

	t.Run("cut_mid_stream", func(t *testing.T) {
		t.Parallel()
		for _, frac := range []int{2, 3, 4} {
			cut := raw[:len(raw)*(frac-1)/frac]
			_, err := index.Read(bytes.NewReader(cut))
			require.Error(t, err, "a stream cut at %d/%d must not decode", frac-1, frac)
		}
	})

	t.Run("entry_count_mismatch", func(t *testing.T) {
		t.Parallel()
		// A header that promises more entries than the body carries is
		// exactly what a crash mid-write leaves behind.
		idx := sample()
		lying := encodeWithEntryCount(t, idx, len(idx.Entries)+1)
		_, err := index.Read(bytes.NewReader(lying))
		require.ErrorIs(t, err, index.ErrTruncated)
	})

	t.Run("empty_file", func(t *testing.T) {
		t.Parallel()
		_, err := index.Read(bytes.NewReader(nil))
		require.Error(t, err)
	})

	t.Run("not_zstd", func(t *testing.T) {
		t.Parallel()
		_, err := index.Read(bytes.NewReader([]byte("{\"v\":1}\n")))
		require.Error(t, err)
	})
}

func TestWriteNilRejected(t *testing.T) {
	t.Parallel()
	require.Error(t, index.Write(&bytes.Buffer{}, nil))
}

// encodeWithHeaderVersion writes a well-formed index whose header claims a
// different schema version, standing in for a file written by another build.
func encodeWithHeaderVersion(t *testing.T, idx *domain.LayerIndex, v int) []byte {
	t.Helper()
	return encodeRaw(t, index.Header{
		V:               v,
		DiffID:          idx.DiffID,
		EntryCount:      len(idx.Entries),
		ChangesetDigest: idx.ChangesetDigest,
		ContentBytes:    idx.ContentBytes,
		Warnings:        idx.Warnings,
	}, idx.Entries)
}

// encodeWithEntryCount writes an index whose header over-promises, standing in
// for a write that was interrupted after the header landed.
func encodeWithEntryCount(t *testing.T, idx *domain.LayerIndex, n int) []byte {
	t.Helper()
	return encodeRaw(t, index.Header{
		V:               index.SchemaVersion,
		DiffID:          idx.DiffID,
		EntryCount:      n,
		ChangesetDigest: idx.ChangesetDigest,
		ContentBytes:    idx.ContentBytes,
		Warnings:        idx.Warnings,
	}, idx.Entries)
}

func encodeRaw(t *testing.T, hdr index.Header, entries []domain.Entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf)
	require.NoError(t, err)
	enc := json.NewEncoder(zw)
	require.NoError(t, enc.Encode(&hdr))
	for i := range entries {
		require.NoError(t, enc.Encode(&entries[i]))
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}
