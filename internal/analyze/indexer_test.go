package analyze_test

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/analyze"
	"github.com/ericsuh/layerlens/internal/analyze/tartest"
	"github.com/ericsuh/layerlens/internal/domain"
)

// indexTar runs the indexer over an uncompressed tar, verifying the DiffID the
// way ingest will.
func indexTar(t *testing.T, raw []byte) *domain.LayerIndex {
	t.Helper()
	idx, err := analyze.IndexLayer(t.Context(), analyze.LayerSource{
		Reader:    bytes.NewReader(raw),
		MediaType: analyze.MediaTypeOCILayerTar,
		DiffID:    tartest.DiffID(raw),
	})
	require.NoError(t, err)
	return idx
}

// entryByPath finds an indexed entry, failing the test if it is absent.
func entryByPath(t *testing.T, idx *domain.LayerIndex, p string) domain.Entry {
	t.Helper()
	for _, e := range idx.Entries {
		if e.Path == p {
			return e
		}
	}
	require.Failf(t, "entry not found", "no entry for %q in %v", p, paths(idx))
	return domain.Entry{}
}

func paths(idx *domain.LayerIndex) []string {
	out := make([]string, 0, len(idx.Entries))
	for _, e := range idx.Entries {
		out = append(out, e.Path)
	}
	return out
}

func TestIndexLayerWhiteouts(t *testing.T) {
	t.Parallel()

	t.Run("whiteout_entry_captured", func(t *testing.T) {
		t.Parallel()
		// ".wh." applies to the *basename*: "app/.wh.old.txt" deletes
		// "app/old.txt", and the marker's own name must never surface.
		raw := tartest.New().
			Dir("app").
			Whiteout("app/old.txt").
			File("app/new.txt", "new").
			Bytes()

		idx := indexTar(t, raw)
		assert.Equal(t, []string{"/app", "/app/new.txt", "/app/old.txt"}, paths(idx))

		wh := entryByPath(t, idx, "/app/old.txt")
		assert.Equal(t, domain.KindWhiteout, wh.Kind)
		assert.Zero(t, wh.Mode)
		assert.Empty(t, wh.ContentSHA)
	})

	t.Run("root_level_whiteout", func(t *testing.T) {
		t.Parallel()
		idx := indexTar(t, tartest.New().Whiteout("etc").Bytes())
		wh := entryByPath(t, idx, "/etc")
		assert.Equal(t, domain.KindWhiteout, wh.Kind)
	})

	t.Run("opaque_entry_captured", func(t *testing.T) {
		t.Parallel()
		raw := tartest.New().Dir("var/cache", tartest.Mode(0o700)).Opaque("var/cache").Bytes()
		idx := indexTar(t, raw)

		// The dir entry and the opaque marker share a path and BOTH
		// survive: that pair is the standard overlay representation of
		// an opaque directory, and squashing needs the marker to clear
		// the lower children *and* the dir entry to carry this layer's
		// own metadata (ARCHITECTURE §4.2). Object first, marker
		// second, so the order is deterministic.
		require.Len(t, idx.Entries, 2)
		assert.Equal(t, domain.KindDir, idx.Entries[0].Kind)
		assert.Equal(t, "/var/cache", idx.Entries[0].Path)
		assert.Equal(t, uint32(0o700), idx.Entries[0].Mode)
		assert.Equal(t, domain.KindOpaque, idx.Entries[1].Kind)
		assert.Equal(t, "/var/cache", idx.Entries[1].Path)
	})

	t.Run("root_opaque", func(t *testing.T) {
		t.Parallel()
		idx := indexTar(t, tartest.New().Opaque("").Bytes())
		require.Len(t, idx.Entries, 1)
		assert.Equal(t, "/", idx.Entries[0].Path)
		assert.Equal(t, domain.KindOpaque, idx.Entries[0].Kind)
	})

	t.Run("malformed_whiteout_warned", func(t *testing.T) {
		t.Parallel()
		raw := tartest.New().
			Raw(tar.Header{Typeflag: tar.TypeReg, Name: "app/.wh.", Mode: 0o644}, "").
			File("app/keep", "k").
			Bytes()
		idx := indexTar(t, raw)
		assert.Equal(t, []string{"/app/keep"}, paths(idx))
		require.Len(t, idx.Warnings, 1)
		assert.Contains(t, idx.Warnings[0], "malformed whiteout")
	})
}

func TestIndexLayerEntries(t *testing.T) {
	t.Parallel()

	t.Run("duplicate_path_last_wins", func(t *testing.T) {
		t.Parallel()
		raw := tartest.New().
			File("app/dup.txt", "first", tartest.Mode(0o600)).
			File("app/dup.txt", "second!", tartest.Mode(0o755)).
			Bytes()

		idx := indexTar(t, raw)
		require.Len(t, idx.Entries, 1)
		e := idx.Entries[0]
		assert.Equal(t, uint32(0o755), e.Mode)
		assert.Equal(t, int64(len("second!")), e.Size)
		assert.Equal(t, tartest.SHA256("second!"), e.ContentSHA)
		// ContentBytes must reflect the surviving entry only, not the
		// sum of both writes.
		assert.Equal(t, int64(len("second!")), idx.ContentBytes)
	})

	t.Run("hardlink_size_once", func(t *testing.T) {
		t.Parallel()
		raw := tartest.New().
			File("bin/busybox", "0123456789").
			Hardlink("bin/sh", "./bin/busybox").
			Bytes()

		idx := indexTar(t, raw)
		link := entryByPath(t, idx, "/bin/sh")
		assert.Equal(t, domain.KindHardlink, link.Kind)
		assert.Zero(t, link.Size, "a hardlink's bytes are counted at its target")
		assert.Empty(t, link.ContentSHA)
		assert.Equal(t, "/bin/busybox", link.LinkTarget, "hardlink targets are cleaned")
		assert.Equal(t, int64(10), idx.ContentBytes)
	})

	t.Run("hardlink_with_unsafe_target_warned", func(t *testing.T) {
		t.Parallel()
		raw := tartest.New().Hardlink("bin/sh", "../../etc/shadow").Bytes()
		idx := indexTar(t, raw)
		link := entryByPath(t, idx, "/bin/sh")
		assert.Empty(t, link.LinkTarget)
		require.Len(t, idx.Warnings, 1)
		assert.Contains(t, idx.Warnings[0], "unsafe target")
	})

	t.Run("content_sha_streams", func(t *testing.T) {
		t.Parallel()
		raw := tartest.New().
			File("a.txt", "alpha").
			File("b.txt", "").
			File("c.txt", strings.Repeat("x", 4096)).
			Bytes()

		idx := indexTar(t, raw)
		assert.Equal(t, tartest.SHA256("alpha"), entryByPath(t, idx, "/a.txt").ContentSHA)
		assert.Equal(t, tartest.SHA256(""), entryByPath(t, idx, "/b.txt").ContentSHA)
		assert.Equal(t, tartest.SHA256(strings.Repeat("x", 4096)), entryByPath(t, idx, "/c.txt").ContentSHA)
		assert.Equal(t, int64(5+0+4096), idx.ContentBytes)
	})

	t.Run("kinds_and_metadata", func(t *testing.T) {
		t.Parallel()
		raw := tartest.New().
			Dir("usr", tartest.Mode(0o755), tartest.Owner(0, 0), tartest.Names("root", "root")).
			File("usr/bin/tool", "T", tartest.Mode(0o4755), tartest.Owner(1000, 2000), tartest.Names("app", "grp")).
			Symlink("usr/bin/link", "../../elsewhere/tool").
			Device("dev/sda", false, 8, 0).
			Device("dev/tty", true, 5, 0).
			Fifo("run/pipe").
			Bytes()

		idx := indexTar(t, raw)

		dir := entryByPath(t, idx, "/usr")
		assert.Equal(t, domain.KindDir, dir.Kind)
		assert.Equal(t, uint32(0o755), dir.Mode)

		file := entryByPath(t, idx, "/usr/bin/tool")
		assert.Equal(t, domain.KindFile, file.Kind)
		assert.Equal(t, uint32(0o4755), file.Mode, "setuid is part of the 12-bit mode")
		assert.Equal(t, 1000, file.UID)
		assert.Equal(t, 2000, file.GID)
		assert.Equal(t, "app", file.Uname)
		assert.Equal(t, "grp", file.Gname)
		assert.Equal(t, tartest.DefaultMtime.Unix(), file.MtimeUnix)

		link := entryByPath(t, idx, "/usr/bin/link")
		assert.Equal(t, domain.KindSymlink, link.Kind)
		assert.Equal(t, "../../elsewhere/tool", link.LinkTarget,
			"symlink targets are stored verbatim and never resolved")

		blk := entryByPath(t, idx, "/dev/sda")
		assert.Equal(t, domain.KindDevice, blk.Kind)
		assert.Equal(t, int64(8), blk.Devmajor)
		chr := entryByPath(t, idx, "/dev/tty")
		assert.Equal(t, domain.KindDevice, chr.Kind, "block and char devices collapse to one kind")
		assert.Equal(t, int64(5), chr.Devmajor)

		assert.Equal(t, domain.KindFifo, entryByPath(t, idx, "/run/pipe").Kind)
	})

	t.Run("entries_sorted_by_path", func(t *testing.T) {
		t.Parallel()
		raw := tartest.New().
			File("z.txt", "z").
			File("a.txt", "a").
			Dir("m").
			File("m/b.txt", "b").
			Bytes()
		idx := indexTar(t, raw)
		assert.Equal(t, []string{"/a.txt", "/m", "/m/b.txt", "/z.txt"}, paths(idx))
	})

	t.Run("root_member_ignored", func(t *testing.T) {
		t.Parallel()
		// GNU tar emits a "./" member; it says nothing about the
		// changeset and must not become a warning.
		raw := tartest.New().Dir(".").File("./etc/hostname", "h").Bytes()
		idx := indexTar(t, raw)
		assert.Equal(t, []string{"/etc/hostname"}, paths(idx))
		assert.Empty(t, idx.Warnings)
	})

	t.Run("bad_entry_warned_and_skipped", func(t *testing.T) {
		t.Parallel()
		raw := tartest.New().
			File("good/before.txt", "b").
			Raw(tar.Header{Typeflag: tar.TypeReg, Name: "../../etc/passwd", Mode: 0o644}, "pwned").
			File("good/after.txt", "a").
			Bytes()

		idx := indexTar(t, raw)
		assert.Equal(t, []string{"/good/after.txt", "/good/before.txt"}, paths(idx))
		require.Len(t, idx.Warnings, 1)
		assert.Contains(t, idx.Warnings[0], "../../etc/passwd")
		// The traversal body still has to be drained, or the DiffID
		// check below would have failed.
		assert.Equal(t, int64(2), idx.ContentBytes)
	})

	t.Run("unsupported_typeflag_warned", func(t *testing.T) {
		t.Parallel()
		raw := tartest.New().
			Raw(tar.Header{Typeflag: tar.TypeCont, Name: "weird", Mode: 0o644}, "").
			File("ok", "o").
			Bytes()
		idx := indexTar(t, raw)
		assert.Equal(t, []string{"/ok"}, paths(idx))
		require.Len(t, idx.Warnings, 1)
		assert.Contains(t, idx.Warnings[0], "unsupported tar type")
	})

	t.Run("warnings_are_bounded", func(t *testing.T) {
		t.Parallel()
		b := tartest.New()
		for i := range 500 {
			b.Raw(tar.Header{Typeflag: tar.TypeReg, Name: fmt.Sprintf("../escape-%d", i), Mode: 0o644}, "")
		}
		idx := indexTar(t, b.Bytes())
		assert.Empty(t, idx.Entries)
		assert.Len(t, idx.Warnings, 65, "64 warnings plus one suppression summary")
		assert.Contains(t, idx.Warnings[64], "further warnings suppressed")
	})
}

func TestIndexLayerPAX(t *testing.T) {
	t.Parallel()

	longName := "usr/share/" + strings.Repeat("deep/", 30) + "file.txt"
	require.Greater(t, len(longName), 100, "must exceed the ustar name limit")

	raw := tartest.New().
		File(longName, "payload",
			tartest.Xattr("security.capability", "\x01\x00\x00\x02"),
			tartest.Xattr("security.selinux", "system_u:object_r:bin_t:s0"),
			tartest.Xattr("system.posix_acl_access", "acl-bytes"),
			tartest.Xattr("user.note", "keep me"),
		).
		Bytes()

	idx := indexTar(t, raw)
	e := entryByPath(t, idx, "/"+longName)
	assert.Equal(t, tartest.SHA256("payload"), e.ContentSHA)
	// BuildKit's filter: security.capability survives, the rest of
	// security.* and all of system.* do not.
	assert.Equal(t, map[string]string{
		"security.capability": "\x01\x00\x00\x02",
		"user.note":           "keep me",
	}, e.Xattrs)
}

func TestIndexLayerDiffIDVerification(t *testing.T) {
	t.Parallel()

	raw := tartest.New().File("app/server.js", "console.log(1)").Bytes()

	t.Run("declared_diffid_accepted", func(t *testing.T) {
		t.Parallel()
		idx := indexTar(t, raw)
		assert.Equal(t, tartest.DiffID(raw), idx.DiffID)
	})

	t.Run("diffid_mismatch_fails", func(t *testing.T) {
		t.Parallel()
		// Flip one byte of file content: the tar still parses, so only
		// the DiffID check can catch it.
		tampered := bytes.Replace(raw, []byte("console.log(1)"), []byte("console.log(2)"), 1)
		require.NotEqual(t, raw, tampered)

		_, err := analyze.IndexLayer(t.Context(), analyze.LayerSource{
			Reader:    bytes.NewReader(tampered),
			MediaType: analyze.MediaTypeOCILayerTar,
			DiffID:    tartest.DiffID(raw),
		})
		require.ErrorIs(t, err, analyze.ErrDiffIDMismatch)
	})

	t.Run("trailing_padding_is_part_of_the_diffid", func(t *testing.T) {
		t.Parallel()
		// GNU tar pads archives out to its blocking factor. Those bytes
		// are in the DiffID even though archive/tar stops before them,
		// so the indexer must drain the stream after the last member.
		padded := append(append([]byte{}, raw...), make([]byte, 10240)...)
		idx, err := analyze.IndexLayer(t.Context(), analyze.LayerSource{
			Reader:    bytes.NewReader(padded),
			MediaType: analyze.MediaTypeOCILayerTar,
			DiffID:    tartest.DiffID(padded),
		})
		require.NoError(t, err)
		assert.Equal(t, tartest.DiffID(padded), idx.DiffID)
		assert.NotEqual(t, tartest.DiffID(raw), idx.DiffID)
	})

	t.Run("no_declared_diffid_computes_only", func(t *testing.T) {
		t.Parallel()
		idx, err := analyze.IndexLayer(t.Context(), analyze.LayerSource{
			Reader:    bytes.NewReader(raw),
			MediaType: analyze.MediaTypeOCILayerTar,
		})
		require.NoError(t, err)
		assert.Equal(t, tartest.DiffID(raw), idx.DiffID)
	})

	t.Run("malformed_declared_diffid_rejected", func(t *testing.T) {
		t.Parallel()
		_, err := analyze.IndexLayer(t.Context(), analyze.LayerSource{
			Reader:    bytes.NewReader(raw),
			MediaType: analyze.MediaTypeOCILayerTar,
			DiffID:    "sha256:short",
		})
		require.Error(t, err)
	})

	t.Run("truncated_stream_fails", func(t *testing.T) {
		t.Parallel()
		_, err := analyze.IndexLayer(t.Context(), analyze.LayerSource{
			Reader:    bytes.NewReader(raw[:len(raw)/2]),
			MediaType: analyze.MediaTypeOCILayerTar,
			DiffID:    tartest.DiffID(raw),
		})
		require.Error(t, err)
	})
}

func TestIndexLayerMediaTypes(t *testing.T) {
	t.Parallel()

	b := tartest.New().
		Dir("app").
		File("app/server.js", "console.log(1)", tartest.Owner(1000, 1000)).
		Symlink("app/current", "server.js").
		Whiteout("app/old.js")
	raw := b.Bytes()
	want := indexTar(t, raw)

	cases := []struct {
		name      string
		mediaType string
		body      []byte
	}{
		{"none_explicit", analyze.MediaTypeOCILayerTar, raw},
		{"none_docker", analyze.MediaTypeDockerLayerTar, raw},
		{"none_empty_media_type", "", raw},
		{"gzip_oci", analyze.MediaTypeOCILayerGzip, b.Gzip()},
		{"gzip_docker", analyze.MediaTypeDockerLayerGzip, b.Gzip()},
		{"gzip_with_parameters", analyze.MediaTypeOCILayerGzip + "; charset=utf-8", b.Gzip()},
		{"zstd_oci", analyze.MediaTypeOCILayerZstd, b.Zstd()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			idx, err := analyze.IndexLayer(t.Context(), analyze.LayerSource{
				Reader:    bytes.NewReader(tc.body),
				MediaType: tc.mediaType,
				DiffID:    tartest.DiffID(raw),
			})
			require.NoError(t, err)
			// Identical bytes through any transport produce an
			// identical index, DiffID and changeset digest.
			assert.Equal(t, want, idx)
		})
	}

	t.Run("unknown_media_type_rejected", func(t *testing.T) {
		t.Parallel()
		_, err := analyze.IndexLayer(t.Context(), analyze.LayerSource{
			Reader:    bytes.NewReader(raw),
			MediaType: "application/vnd.in-toto+json",
		})
		require.ErrorIs(t, err, analyze.ErrUnknownMediaType)
	})

	t.Run("corrupt_gzip_rejected", func(t *testing.T) {
		t.Parallel()
		_, err := analyze.IndexLayer(t.Context(), analyze.LayerSource{
			Reader:    bytes.NewReader([]byte("not gzip at all")),
			MediaType: analyze.MediaTypeOCILayerGzip,
		})
		require.Error(t, err)
	})
}

func TestIndexLayerProgressCounter(t *testing.T) {
	t.Parallel()

	b := tartest.New().File("app/data.bin", strings.Repeat("q", 100000))
	body := b.Gzip()
	var counter analyze.ByteCounter

	_, err := analyze.IndexLayer(t.Context(), analyze.LayerSource{
		Reader:    bytes.NewReader(body),
		MediaType: analyze.MediaTypeOCILayerGzip,
		DiffID:    b.DiffID(),
		Progress:  &counter,
	})
	require.NoError(t, err)
	// Progress counts *compressed* bytes, which is what a registry pull
	// can compare against the manifest's layer size.
	assert.Equal(t, int64(len(body)), counter.Load())
}

func TestIndexLayerCancellation(t *testing.T) {
	t.Parallel()

	raw := tartest.New().File("a", "a").File("b", "b").Bytes()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := analyze.IndexLayer(ctx, analyze.LayerSource{
		Reader:    bytes.NewReader(raw),
		MediaType: analyze.MediaTypeOCILayerTar,
		DiffID:    tartest.DiffID(raw),
	})
	require.ErrorIs(t, err, context.Canceled)
}

func TestIndexLayerNilReader(t *testing.T) {
	t.Parallel()
	_, err := analyze.IndexLayer(t.Context(), analyze.LayerSource{MediaType: analyze.MediaTypeOCILayerTar})
	require.Error(t, err)
}

// The 25 GiB guarantee is structural: this feeds the indexer a tar far larger
// than any sane test buffer, generated on the fly and never materialized, and
// asserts that it is consumed in one pass with metadata-sized memory.
func TestIndexLayerStreamsWithoutBuffering(t *testing.T) {
	if testing.Short() {
		t.Skip("streams 1 GiB of synthetic tar")
	}
	// Deliberately not parallel: the allocation assertion below measures a
	// process-wide counter.

	const fileSize = 1 << 30   // 1 GiB of content in a single member
	const smallFiles = 100_000 // §4.6's pathological entry count

	reader := tartest.Pipe(func(tw *tar.Writer) error {
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg, Name: "big.bin", Mode: 0o644, Size: fileSize,
		}); err != nil {
			return err
		}
		if _, err := io.Copy(tw, tartest.NewFiller(fileSize, 'Z')); err != nil {
			return err
		}
		for i := range smallFiles {
			body := fmt.Sprintf("payload-%d", i)
			if err := tw.WriteHeader(&tar.Header{
				Typeflag: tar.TypeReg,
				Name:     fmt.Sprintf("small/%06d.txt", i),
				Mode:     0o644,
				Size:     int64(len(body)),
			}); err != nil {
				return err
			}
			if _, err := io.WriteString(tw, body); err != nil {
				return err
			}
		}
		return nil
	})

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	idx, err := analyze.IndexLayer(t.Context(), analyze.LayerSource{
		Reader:    reader,
		MediaType: analyze.MediaTypeOCILayerTar,
	})
	runtime.ReadMemStats(&after)
	require.NoError(t, err)

	// Two independent claims.
	//
	// 1. Content is never buffered: a single retained copy of the layer
	//    would put a gigabyte through the allocator on its own.
	allocated := after.TotalAlloc - before.TotalAlloc
	assert.Less(t, allocated, uint64(fileSize)/4,
		"indexing a %d-byte layer allocated %d bytes: content is being buffered", fileSize, allocated)
	//
	// 2. What is actually retained is metadata: measured against
	//    ARCHITECTURE §4.6's ~200 B/entry budget, with slack for Go's map
	//    and slice growth.
	runtime.GC()
	var resident runtime.MemStats
	runtime.ReadMemStats(&resident)
	perEntry := resident.HeapAlloc / uint64(smallFiles+1)
	assert.Less(t, perEntry, uint64(600),
		"%d bytes resident for %d entries (%d B/entry)", resident.HeapAlloc, smallFiles+1, perEntry)
	t.Logf("1 GiB / %d entries: %d bytes allocated in total, %d bytes resident (%d B/entry)",
		smallFiles+1, allocated, resident.HeapAlloc, perEntry)

	require.Len(t, idx.Entries, smallFiles+1)
	assert.Equal(t, int64(fileSize)+idxSmallBytes(smallFiles), idx.ContentBytes)

	big := entryByPath(t, idx, "/big.bin")
	assert.Equal(t, int64(fileSize), big.Size)
	// Independently computed sha256 of 1 GiB of 'Z'.
	assert.Equal(t, fillerDigest(t, fileSize, 'Z'), big.ContentSHA)
}

func idxSmallBytes(n int) int64 {
	var total int64
	for i := range n {
		total += int64(len(fmt.Sprintf("payload-%d", i)))
	}
	return total
}

func fillerDigest(t *testing.T, n int64, pattern byte) domain.Digest {
	t.Helper()
	h := sha256.New()
	_, err := io.Copy(h, tartest.NewFiller(n, pattern))
	require.NoError(t, err)
	return domain.DigestFromHash(h)
}
