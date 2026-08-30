package analyze_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/analyze"
	"github.com/ericsuh/layerlens/internal/domain"
)

// baseEntries is a small, fully populated changeset. Each test mutates exactly
// one field of one entry so that a digest difference can only be attributed to
// that field.
func baseEntries() []domain.Entry {
	return []domain.Entry{
		{
			Path:       "/app/server.js",
			Kind:       domain.KindFile,
			Mode:       0o644,
			UID:        1000,
			GID:        1000,
			Uname:      "node",
			Gname:      "node",
			Size:       11,
			MtimeUnix:  1700000000,
			ContentSHA: domain.MustDigest("sha256:" + "11" + "0000000000000000000000000000000000000000000000000000000000000000"[2:]),
			Xattrs:     map[string]string{"security.capability": "cap"},
		},
		{
			Path:      "/app",
			Kind:      domain.KindDir,
			Mode:      0o755,
			UID:       0,
			GID:       0,
			Uname:     "root",
			Gname:     "root",
			MtimeUnix: 1700000001,
		},
		{
			Path:       "/app/link",
			Kind:       domain.KindSymlink,
			Mode:       0o777,
			LinkTarget: "server.js",
			MtimeUnix:  1700000002,
		},
		{
			Path:      "/dev/null",
			Kind:      domain.KindDevice,
			Mode:      0o666,
			Devmajor:  1,
			Devminor:  3,
			MtimeUnix: 1700000003,
		},
	}
}

// mutate returns a copy of baseEntries with fn applied to entry i.
func mutate(i int, fn func(*domain.Entry)) []domain.Entry {
	es := baseEntries()
	fn(&es[i])
	return es
}

func TestChangesetDigestFieldSelection(t *testing.T) {
	t.Parallel()

	baseline := analyze.ChangesetDigest(baseEntries())
	require.NoError(t, baseline.Validate())

	tests := []struct {
		name    string
		entries []domain.Entry
		// wantEqual is true when the mutation must NOT change the digest.
		wantEqual bool
	}{
		{
			// The product thesis: mtime churn is exactly what makes
			// two byte-identical rebuilds produce different DiffIDs,
			// and it must not make two changesets differ.
			name: "mtime_only_diff_equal",
			entries: func() []domain.Entry {
				es := baseEntries()
				for i := range es {
					es[i].MtimeUnix += 86400
				}
				return es
			}(),
			wantEqual: true,
		},
		{
			name:    "mode_diff_not_equal",
			entries: mutate(0, func(e *domain.Entry) { e.Mode = 0o755 }),
		},
		{
			name:    "setuid_bit_diff_not_equal",
			entries: mutate(0, func(e *domain.Entry) { e.Mode = 0o4644 }),
		},
		{
			// RESEARCH Q9 reversed Q5 here: Docker's build cache
			// includes uid/gid, so layerlens does too.
			name:    "uid_diff_not_equal",
			entries: mutate(0, func(e *domain.Entry) { e.UID = 1001 }),
		},
		{
			name:    "gid_diff_not_equal",
			entries: mutate(0, func(e *domain.Entry) { e.GID = 1001 }),
		},
		{
			name:    "uname_diff_not_equal",
			entries: mutate(0, func(e *domain.Entry) { e.Uname = "app" }),
		},
		{
			name:    "gname_diff_not_equal",
			entries: mutate(0, func(e *domain.Entry) { e.Gname = "app" }),
		},
		{
			name:    "xattr_value_diff_not_equal",
			entries: mutate(0, func(e *domain.Entry) { e.Xattrs = map[string]string{"security.capability": "other"} }),
		},
		{
			name:    "xattr_added_not_equal",
			entries: mutate(1, func(e *domain.Entry) { e.Xattrs = map[string]string{"user.note": "x"} }),
		},
		{
			name:    "content_sha_diff_not_equal",
			entries: mutate(0, func(e *domain.Entry) { e.ContentSHA = rep('f') }),
		},
		{
			name:    "size_diff_not_equal",
			entries: mutate(0, func(e *domain.Entry) { e.Size = 12 }),
		},
		{
			name:    "symlink_target_diff_not_equal",
			entries: mutate(2, func(e *domain.Entry) { e.LinkTarget = "other.js" }),
		},
		{
			name:    "devmajor_diff_not_equal",
			entries: mutate(3, func(e *domain.Entry) { e.Devmajor = 5 }),
		},
		{
			name:    "devminor_diff_not_equal",
			entries: mutate(3, func(e *domain.Entry) { e.Devminor = 5 }),
		},
		{
			name:    "path_diff_not_equal",
			entries: mutate(0, func(e *domain.Entry) { e.Path = "/app/server2.js" }),
		},
		{
			name:    "kind_diff_not_equal",
			entries: mutate(1, func(e *domain.Entry) { e.Kind = domain.KindFile }),
		},
		{
			name: "whiteout_vs_file_not_equal",
			entries: mutate(0, func(e *domain.Entry) {
				*e = domain.Entry{Path: "/app/server.js", Kind: domain.KindWhiteout}
			}),
		},
		{
			name: "opaque_vs_dir_not_equal",
			entries: mutate(1, func(e *domain.Entry) {
				*e = domain.Entry{Path: "/app", Kind: domain.KindOpaque}
			}),
		},
		{
			name: "removed_entry_not_equal",
			entries: func() []domain.Entry {
				return baseEntries()[:3]
			}(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := analyze.ChangesetDigest(tc.entries)
			if tc.wantEqual {
				assert.Equal(t, baseline, got)
				return
			}
			assert.NotEqual(t, baseline, got)
		})
	}
}

func TestChangesetDigestOrderIndependence(t *testing.T) {
	t.Parallel()

	forward := baseEntries()
	reversed := baseEntries()
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	shuffled := []domain.Entry{forward[2], forward[0], forward[3], forward[1]}

	want := analyze.ChangesetDigest(forward)
	assert.Equal(t, want, analyze.ChangesetDigest(reversed))
	assert.Equal(t, want, analyze.ChangesetDigest(shuffled))

	// The input slice must not be reordered underneath the caller.
	assert.Equal(t, baseEntries(), forward)
}

func TestChangesetDigestEmpty(t *testing.T) {
	t.Parallel()

	empty := analyze.ChangesetDigest(nil)
	require.NoError(t, empty.Validate())
	assert.Equal(t, empty, analyze.ChangesetDigest([]domain.Entry{}))
	assert.NotEqual(t, empty, analyze.ChangesetDigest(baseEntries()))
}

// The scheme-version byte exists so the definition of the digest can change
// without silently colliding with digests computed under the old definition.
func TestChangesetDigestSchemeVersionByte(t *testing.T) {
	t.Parallel()

	assert.Equal(t, byte(1), analyze.ChangesetSchemeVersion)

	// Recompute the same field stream under a different version byte and
	// confirm every digest moves. Done here rather than by mutating a
	// package-level variable so the production constant stays constant.
	for _, entries := range [][]domain.Entry{nil, baseEntries()} {
		v1 := analyze.ChangesetDigest(entries)
		v2 := analyze.ChangesetDigestWithVersion(analyze.ChangesetSchemeVersion+1, entries)
		assert.NotEqual(t, v1, v2)
	}
}

// Length-prefixing every field means no run of values can be re-read as a
// different run of values: two entries whose fields concatenate identically
// must still hash differently.
func TestChangesetDigestFieldsAreUnambiguous(t *testing.T) {
	t.Parallel()

	a := []domain.Entry{{Path: "/ab", Kind: domain.KindSymlink, LinkTarget: "c"}}
	b := []domain.Entry{{Path: "/a", Kind: domain.KindSymlink, LinkTarget: "bc"}}
	assert.NotEqual(t, analyze.ChangesetDigest(a), analyze.ChangesetDigest(b))
}

func TestFilterXattrs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		pax  map[string]string
		want map[string]string
	}{
		{name: "nil", pax: nil, want: nil},
		{
			name: "non_xattr_records_ignored",
			pax:  map[string]string{"path": "/very/long/name", "mtime": "1700000000.0"},
			want: nil,
		},
		{
			name: "buildkit_filter",
			pax: map[string]string{
				"SCHILY.xattr.security.capability":     "\x01\x00",
				"SCHILY.xattr.security.selinux":        "system_u:object_r:bin_t:s0",
				"SCHILY.xattr.system.posix_acl_access": "acl",
				"SCHILY.xattr.user.comment":            "hello",
				"SCHILY.xattr.trusted.overlay.opaque":  "y",
			},
			want: map[string]string{
				"security.capability":    "\x01\x00",
				"user.comment":           "hello",
				"trusted.overlay.opaque": "y",
			},
		},
		{
			name: "empty_attribute_name_ignored",
			pax:  map[string]string{"SCHILY.xattr.": "x"},
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, analyze.FilterXattrs(tc.pax))
		})
	}
}

// TestChangesetDigestOrderIndependenceWithMarkers is the case four distinct
// paths cannot catch: one path holding BOTH a filesystem object and its
// whiteout marker — the standard overlay representation of "delete x, then
// recreate x in this same layer". Sorting by path alone leaves their relative
// order to the sort's internal choices, so the digest of a layer would depend
// on the order the entries happened to arrive in, contradicting the doc
// comment and the §3.1 claim that the digest is a property of the changeset.
func TestChangesetDigestOrderIndependenceWithMarkers(t *testing.T) {
	t.Parallel()

	object := domain.Entry{Path: "/var/cache/x", Kind: domain.KindFile, Mode: 0o644, Size: 3,
		ContentSHA: domain.MustDigest("sha256:" + strings.Repeat("ab", 32))}
	marker := domain.Entry{Path: "/var/cache/x", Kind: domain.KindWhiteout}
	dir := domain.Entry{Path: "/var/cache", Kind: domain.KindDir, Mode: 0o755}
	opaque := domain.Entry{Path: "/var/cache", Kind: domain.KindOpaque}

	orders := [][]domain.Entry{
		{dir, opaque, object, marker},
		{marker, object, opaque, dir},
		{opaque, dir, marker, object},
		{object, dir, marker, opaque},
	}
	want := analyze.ChangesetDigest(orders[0])
	for i, entries := range orders[1:] {
		assert.Equal(t, want, analyze.ChangesetDigest(entries),
			"ordering %d of the same changeset must hash the same", i+1)
	}

	// And the two entries at one path are still distinguished: dropping
	// the marker must change the digest, or the sort key would be hiding
	// real content.
	assert.NotEqual(t, want, analyze.ChangesetDigest([]domain.Entry{dir, opaque, object}))
}

// TestChangesetDigestIgnoresDirectorySizeAndContent is the §3.2 guarantee made
// structural: the digest and the diff tree's modified predicate share one
// field projection, and that projection zeroes a directory's size and content
// hash. A tar's directory `size` field is meaningless, so a writer that sets it
// must not be able to make the digest disagree with the predicate — which
// suppresses the difference either way.
func TestChangesetDigestIgnoresDirectorySizeAndContent(t *testing.T) {
	t.Parallel()

	plain := domain.Entry{Path: "/app", Kind: domain.KindDir, Mode: 0o755}
	noisy := plain
	noisy.Size = 4096
	noisy.ContentSHA = domain.MustDigest("sha256:" + strings.Repeat("cd", 32))

	assert.Equal(t, analyze.ChangesetDigest([]domain.Entry{plain}),
		analyze.ChangesetDigest([]domain.Entry{noisy}),
		"a directory has neither a size nor a content hash, on either side of the guarantee")

	// The same two fields on a regular file are still load-bearing.
	file := domain.Entry{Path: "/app", Kind: domain.KindFile, Mode: 0o755}
	sized := file
	sized.Size = 4096
	assert.NotEqual(t, analyze.ChangesetDigest([]domain.Entry{file}),
		analyze.ChangesetDigest([]domain.Entry{sized}))
}
