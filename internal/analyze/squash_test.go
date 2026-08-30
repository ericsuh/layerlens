package analyze_test

import (
	"fmt"
	"path"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/analyze"
	"github.com/ericsuh/layerlens/internal/analyze/tartest"
	"github.com/ericsuh/layerlens/internal/domain"
)

// --- helpers shared by the squash, diff and aggregation tests ---------------

// entryOpt mutates a changeset entry a test is building.
type entryOpt func(*domain.Entry)

func withMode(m uint32) entryOpt { return func(e *domain.Entry) { e.Mode = m } }
func withOwner(uid, gid int) entryOpt {
	return func(e *domain.Entry) { e.UID, e.GID = uid, gid }
}
func withMtime(t int64) entryOpt { return func(e *domain.Entry) { e.MtimeUnix = t } }
func withXattr(k, v string) entryOpt {
	return func(e *domain.Entry) {
		if e.Xattrs == nil {
			e.Xattrs = map[string]string{}
		}
		e.Xattrs[k] = v
	}
}

func apply(e domain.Entry, opts []entryOpt) domain.Entry {
	for _, o := range opts {
		o(&e)
	}
	return e
}

// fileEntry builds a regular-file entry whose size and content hash follow
// from content, the way the indexer would have produced them.
func fileEntry(p, content string, opts ...entryOpt) domain.Entry {
	return apply(domain.Entry{
		Path:       p,
		Kind:       domain.KindFile,
		Mode:       0o644,
		Size:       int64(len(content)),
		ContentSHA: tartest.SHA256(content),
	}, opts)
}

func dirEntry(p string, opts ...entryOpt) domain.Entry {
	return apply(domain.Entry{Path: p, Kind: domain.KindDir, Mode: 0o755}, opts)
}

func symlinkEntry(p, target string, opts ...entryOpt) domain.Entry {
	return apply(domain.Entry{Path: p, Kind: domain.KindSymlink, Mode: 0o777, LinkTarget: target}, opts)
}

func hardlinkEntry(p, target string, opts ...entryOpt) domain.Entry {
	return apply(domain.Entry{Path: p, Kind: domain.KindHardlink, Mode: 0o644, LinkTarget: target}, opts)
}

func deviceEntry(p string, major, minor int64, opts ...entryOpt) domain.Entry {
	return apply(domain.Entry{
		Path: p, Kind: domain.KindDevice, Mode: 0o600, Devmajor: major, Devminor: minor,
	}, opts)
}

func fifoEntry(p string, opts ...entryOpt) domain.Entry {
	return apply(domain.Entry{Path: p, Kind: domain.KindFifo, Mode: 0o600}, opts)
}

func whiteoutEntry(p string) domain.Entry {
	return domain.Entry{Path: p, Kind: domain.KindWhiteout}
}

func opaqueEntry(p string) domain.Entry {
	return domain.Entry{Path: p, Kind: domain.KindOpaque}
}

// layer wraps entries into a LayerIndex *without sorting them*. Squash must
// not depend on entry order — that is the whole point of its two-pass
// structure — so tests get to choose deliberately adverse orders.
func layer(entries ...domain.Entry) domain.LayerIndex {
	return domain.LayerIndex{
		SchemaVersion: domain.LayerIndexSchemaVersion,
		Entries:       entries,
	}
}

// indexTarLayer runs a tar through the real indexer, so a test can assert on
// the indexer→squash path end to end.
func indexTarLayer(t *testing.T, b *tartest.Builder) domain.LayerIndex {
	t.Helper()
	return *indexTar(t, b.Bytes())
}

// treeLines renders a squashed tree as one sorted line per path:
//
//	"/usr/bin/sh symlink -> /bin/sh"
//	"/etc/hosts file 12"
//	"/var dir(implicit)"
//
// It is deliberately human-readable: the golden expectations in these tests
// are meant to be verified by eye, not by re-running the implementation.
func treeLines(root *domain.Node) []string {
	var out []string
	var walk func(prefix string, n *domain.Node)
	walk = func(prefix string, n *domain.Node) {
		for _, c := range n.Children {
			p := prefix + "/" + c.Name
			out = append(out, describeNode(p, c))
			walk(p, c)
		}
	}
	walk("", root)
	sort.Strings(out)
	return out
}

func describeNode(p string, n *domain.Node) string {
	switch n.Kind {
	case domain.KindDir:
		if n.Implicit {
			return fmt.Sprintf("%s dir(implicit) %04o", p, n.Mode)
		}
		return fmt.Sprintf("%s dir %04o", p, n.Mode)
	case domain.KindFile:
		return fmt.Sprintf("%s file %04o %d", p, n.Mode, n.Size)
	case domain.KindSymlink, domain.KindHardlink:
		return fmt.Sprintf("%s %s -> %s", p, n.Kind, n.LinkTarget)
	case domain.KindDevice:
		return fmt.Sprintf("%s device %d:%d", p, n.Devmajor, n.Devminor)
	default:
		return fmt.Sprintf("%s %s", p, n.Kind)
	}
}

// nodeAt looks a path up in a squashed tree, or nil when it is absent.
func nodeAt(root *domain.Node, p string) *domain.Node {
	cur := root
	for _, seg := range splitPath(p) {
		if cur.Children == nil {
			return nil
		}
		cur = cur.Children[seg]
		if cur == nil {
			return nil
		}
	}
	return cur
}

func splitPath(p string) []string {
	var segs []string
	for p != "/" && p != "" && p != "." {
		segs = append([]string{path.Base(p)}, segs...)
		p = path.Dir(p)
	}
	return segs
}

// --- tests ------------------------------------------------------------------

func TestSquashEmpty(t *testing.T) {
	t.Parallel()

	root := analyze.Squash(nil)
	require.NotNil(t, root)
	assert.Equal(t, domain.KindDir, root.Kind)
	assert.Empty(t, root.Children)
	assert.True(t, root.Implicit, "the synthetic root must never read as a modification")
}

func TestSquashWhiteouts(t *testing.T) {
	t.Parallel()

	base := layer(
		dirEntry("/app"),
		fileEntry("/app/keep.txt", "keep"),
		fileEntry("/app/old.txt", "old"),
	)

	t.Run("whiteout_deletes_lower_file", func(t *testing.T) {
		t.Parallel()
		root := analyze.Squash([]domain.LayerIndex{base, layer(whiteoutEntry("/app/old.txt"))})
		assert.Equal(t, []string{
			"/app dir 0755",
			"/app/keep.txt file 0644 4",
		}, treeLines(root))
	})

	t.Run("whiteout_deletes_lower_subtree", func(t *testing.T) {
		t.Parallel()
		lower := layer(
			dirEntry("/app"),
			dirEntry("/app/vendor"),
			fileEntry("/app/vendor/lib.so", "lib"),
			dirEntry("/app/vendor/deep"),
			fileEntry("/app/vendor/deep/x", "x"),
			fileEntry("/app/main", "main"),
		)
		root := analyze.Squash([]domain.LayerIndex{lower, layer(whiteoutEntry("/app/vendor"))})
		assert.Equal(t, []string{
			"/app dir 0755",
			"/app/main file 0644 4",
		}, treeLines(root))
	})

	t.Run("whiteout_nonexistent_noop", func(t *testing.T) {
		t.Parallel()
		root := analyze.Squash([]domain.LayerIndex{base, layer(
			whiteoutEntry("/app/never-existed"),
			whiteoutEntry("/nowhere/at/all"),
		)})
		assert.Equal(t, []string{
			"/app dir 0755",
			"/app/keep.txt file 0644 4",
			"/app/old.txt file 0644 3",
		}, treeLines(root))
	})

	t.Run("whiteout_and_recreate_same_layer", func(t *testing.T) {
		t.Parallel()
		// The layer deletes the lower /app/old.txt and ships its own.
		// Pass 1 removes the lower version, pass 2 lands this layer's,
		// so the layer's own file survives (ARCHITECTURE §4.2).
		top := layer(
			whiteoutEntry("/app/old.txt"),
			fileEntry("/app/old.txt", "brand new"),
		)
		root := analyze.Squash([]domain.LayerIndex{base, top})
		require.NotNil(t, nodeAt(root, "/app/old.txt"))
		assert.Equal(t, int64(len("brand new")), nodeAt(root, "/app/old.txt").Size)
	})

	t.Run("whiteout_order_within_layer_is_irrelevant", func(t *testing.T) {
		t.Parallel()
		// The same changeset in both possible orders: the whiteout
		// before the file it deletes, and after it. A single-pass
		// implementation would produce different trees.
		before := layer(
			whiteoutEntry("/app/old.txt"),
			fileEntry("/app/old.txt", "brand new"),
			fileEntry("/app/extra.txt", "extra"),
		)
		after := layer(
			fileEntry("/app/extra.txt", "extra"),
			fileEntry("/app/old.txt", "brand new"),
			whiteoutEntry("/app/old.txt"),
		)
		want := treeLines(analyze.Squash([]domain.LayerIndex{base, before}))
		got := treeLines(analyze.Squash([]domain.LayerIndex{base, after}))
		assert.Equal(t, want, got)
		assert.Contains(t, got, "/app/old.txt file 0644 9")
	})

	t.Run("whiteout_from_indexed_tar_in_both_orders", func(t *testing.T) {
		t.Parallel()
		// Same guarantee, driven through the real indexer: tar member
		// order must not change the squashed result.
		whFirst := indexTarLayer(t, tartest.New().
			Whiteout("app/old.txt").
			File("app/old.txt", "brand new").
			File("app/extra.txt", "extra"))
		whLast := indexTarLayer(t, tartest.New().
			File("app/extra.txt", "extra").
			File("app/old.txt", "brand new").
			Whiteout("app/old.txt"))

		want := treeLines(analyze.Squash([]domain.LayerIndex{base, whFirst}))
		got := treeLines(analyze.Squash([]domain.LayerIndex{base, whLast}))
		assert.Equal(t, want, got)
		assert.Contains(t, got, "/app/old.txt file 0644 9")
	})
}

func TestSquashOpaque(t *testing.T) {
	t.Parallel()

	lower := layer(
		dirEntry("/var"),
		dirEntry("/var/cache", withMode(0o700)),
		fileEntry("/var/cache/a", "aaa"),
		dirEntry("/var/cache/sub"),
		fileEntry("/var/cache/sub/b", "bb"),
		fileEntry("/var/keep", "keep"),
	)

	t.Run("opaque_clears_lower_children_dir_survives", func(t *testing.T) {
		t.Parallel()
		root := analyze.Squash([]domain.LayerIndex{lower, layer(opaqueEntry("/var/cache"))})
		assert.Equal(t, []string{
			"/var dir 0755",
			"/var/cache dir 0700",
			"/var/keep file 0644 4",
		}, treeLines(root))
		assert.False(t, nodeAt(root, "/var/cache").Implicit,
			"the surviving dir keeps the lower layer's explicit metadata")
	})

	t.Run("opaque_applied_before_own_entries_regardless_of_tar_order", func(t *testing.T) {
		t.Parallel()
		// The opaque marker is deliberately LAST in the changeset,
		// after the entries the layer ships inside that directory. The
		// two-pass structure is the only thing that keeps them alive.
		top := layer(
			fileEntry("/var/cache/fresh", "fresh"),
			dirEntry("/var/cache/newsub"),
			fileEntry("/var/cache/newsub/c", "c"),
			opaqueEntry("/var/cache"),
		)
		root := analyze.Squash([]domain.LayerIndex{lower, top})
		assert.Equal(t, []string{
			"/var dir 0755",
			"/var/cache dir 0700",
			"/var/cache/fresh file 0644 5",
			"/var/cache/newsub dir 0755",
			"/var/cache/newsub/c file 0644 1",
			"/var/keep file 0644 4",
		}, treeLines(root))
	})

	t.Run("opaque_from_indexed_tar_marker_last", func(t *testing.T) {
		t.Parallel()
		// The overlay representation of an opaque dir the layer also
		// re-creates: the dir member, its contents, then the marker.
		top := indexTarLayer(t, tartest.New().
			Dir("var/cache", tartest.Mode(0o750)).
			File("var/cache/fresh", "fresh").
			Opaque("var/cache"))
		root := analyze.Squash([]domain.LayerIndex{lower, top})
		assert.Equal(t, []string{
			"/var dir 0755",
			"/var/cache dir 0750",
			"/var/cache/fresh file 0644 5",
			"/var/keep file 0644 4",
		}, treeLines(root))
	})

	t.Run("opaque_on_absent_dir_noop", func(t *testing.T) {
		t.Parallel()
		root := analyze.Squash([]domain.LayerIndex{lower, layer(
			opaqueEntry("/opt/nothing"),
			opaqueEntry("/var/keep"), // present, but not a directory
		)})
		assert.Equal(t, []string{
			"/var dir 0755",
			"/var/cache dir 0700",
			"/var/cache/a file 0644 3",
			"/var/cache/sub dir 0755",
			"/var/cache/sub/b file 0644 2",
			"/var/keep file 0644 4",
		}, treeLines(root))
	})

	t.Run("opaque_plus_explicit_whiteout_same_dir", func(t *testing.T) {
		t.Parallel()
		// Both markers only ever delete, so their relative order cannot
		// matter; the layer's own entry in the same directory survives.
		top := layer(
			opaqueEntry("/var/cache"),
			whiteoutEntry("/var/cache/a"),
			fileEntry("/var/cache/new", "new"),
		)
		root := analyze.Squash([]domain.LayerIndex{lower, top})
		assert.Equal(t, []string{
			"/var dir 0755",
			"/var/cache dir 0700",
			"/var/cache/new file 0644 3",
			"/var/keep file 0644 4",
		}, treeLines(root))
	})

	t.Run("opaque_at_root", func(t *testing.T) {
		t.Parallel()
		top := layer(
			opaqueEntry("/"),
			fileEntry("/only", "only"),
		)
		root := analyze.Squash([]domain.LayerIndex{lower, top})
		assert.Equal(t, []string{"/only file 0644 4"}, treeLines(root))
	})
}

func TestSquashTypeChanges(t *testing.T) {
	t.Parallel()

	t.Run("file_replaces_dir_drops_subtree", func(t *testing.T) {
		t.Parallel()
		lower := layer(
			dirEntry("/opt"),
			dirEntry("/opt/thing"),
			fileEntry("/opt/thing/inner", "inner"),
			dirEntry("/opt/thing/deeper"),
			fileEntry("/opt/thing/deeper/x", "x"),
		)
		root := analyze.Squash([]domain.LayerIndex{lower, layer(fileEntry("/opt/thing", "now a file"))})
		assert.Equal(t, []string{
			"/opt dir 0755",
			"/opt/thing file 0644 10",
		}, treeLines(root))
	})

	t.Run("dir_replaces_file_fresh_subtree", func(t *testing.T) {
		t.Parallel()
		lower := layer(dirEntry("/opt"), fileEntry("/opt/thing", "a file"))
		root := analyze.Squash([]domain.LayerIndex{lower, layer(
			dirEntry("/opt/thing", withMode(0o750)),
			fileEntry("/opt/thing/inner", "inner"),
		)})
		assert.Equal(t, []string{
			"/opt dir 0755",
			"/opt/thing dir 0750",
			"/opt/thing/inner file 0644 5",
		}, treeLines(root))
	})

	t.Run("symlink_replaces_dir", func(t *testing.T) {
		t.Parallel()
		lower := layer(dirEntry("/lib"), fileEntry("/lib/libc.so", "libc"))
		root := analyze.Squash([]domain.LayerIndex{lower, layer(symlinkEntry("/lib", "usr/lib"))})
		assert.Equal(t, []string{"/lib symlink -> usr/lib"}, treeLines(root))
	})
}

func TestSquashDirectories(t *testing.T) {
	t.Parallel()

	t.Run("implicit_parent_dirs_created", func(t *testing.T) {
		t.Parallel()
		// A tar that ships only the leaf: every parent is synthesized.
		root := analyze.Squash([]domain.LayerIndex{layer(fileEntry("/usr/share/doc/readme", "hi"))})
		assert.Equal(t, []string{
			"/usr dir(implicit) 0755",
			"/usr/share dir(implicit) 0755",
			"/usr/share/doc dir(implicit) 0755",
			"/usr/share/doc/readme file 0644 2",
		}, treeLines(root))
	})

	t.Run("explicit_dir_clears_implicit_flag", func(t *testing.T) {
		t.Parallel()
		root := analyze.Squash([]domain.LayerIndex{
			layer(fileEntry("/usr/share/doc/readme", "hi")),
			layer(dirEntry("/usr/share", withMode(0o701))),
		})
		share := nodeAt(root, "/usr/share")
		require.NotNil(t, share)
		assert.False(t, share.Implicit)
		assert.Equal(t, uint32(0o701), share.Mode)
		assert.True(t, nodeAt(root, "/usr").Implicit, "unrelated implicit parents stay implicit")
		assert.NotNil(t, nodeAt(root, "/usr/share/doc/readme"), "restating a dir keeps its children")
	})

	t.Run("restated_dir_keeps_children_updates_meta", func(t *testing.T) {
		t.Parallel()
		root := analyze.Squash([]domain.LayerIndex{
			layer(dirEntry("/etc", withMode(0o755)), fileEntry("/etc/hosts", "127.0.0.1")),
			layer(dirEntry("/etc", withMode(0o700), withOwner(1000, 1000))),
		})
		etc := nodeAt(root, "/etc")
		require.NotNil(t, etc)
		assert.Equal(t, uint32(0o700), etc.Mode)
		assert.Equal(t, 1000, etc.UID)
		assert.Equal(t, []string{
			"/etc dir 0700",
			"/etc/hosts file 0644 9",
		}, treeLines(root))
	})

	t.Run("file_shadowed_by_implicit_parent", func(t *testing.T) {
		t.Parallel()
		// A later layer claims a path below something that is not a
		// directory: the non-directory is replaced by an implicit dir
		// rather than the entry being dropped.
		root := analyze.Squash([]domain.LayerIndex{
			layer(fileEntry("/a", "file")),
			layer(fileEntry("/a/b", "child")),
		})
		assert.Equal(t, []string{
			"/a dir(implicit) 0755",
			"/a/b file 0644 5",
		}, treeLines(root))
	})
}

func TestSquashLinks(t *testing.T) {
	t.Parallel()

	t.Run("hardlink_size_counted_once", func(t *testing.T) {
		t.Parallel()
		idx := indexTarLayer(t, tartest.New().
			Dir("bin").
			File("bin/busybox", "0123456789").
			Hardlink("bin/sh", "./bin/busybox").
			Hardlink("bin/ls", "./bin/busybox"))
		root := analyze.Squash([]domain.LayerIndex{idx})

		assert.Equal(t, []string{
			"/bin dir 0755",
			"/bin/busybox file 0644 10",
			"/bin/ls hardlink -> /bin/busybox",
			"/bin/sh hardlink -> /bin/busybox",
		}, treeLines(root))

		var total int64
		for _, c := range nodeAt(root, "/bin").Children {
			total += c.Size
		}
		assert.Equal(t, int64(10), total, "the linked content counts once, at the target")
	})

	t.Run("dangling_hardlink_after_target_whiteout", func(t *testing.T) {
		t.Parallel()
		lower := layer(
			dirEntry("/bin"),
			fileEntry("/bin/busybox", "0123456789"),
			hardlinkEntry("/bin/sh", "/bin/busybox"),
		)
		root := analyze.Squash([]domain.LayerIndex{lower, layer(whiteoutEntry("/bin/busybox"))})

		// The link is displayed exactly as the tar declared it. We never
		// resolve links, so we never invent a repair the filesystem
		// itself would not perform.
		assert.Equal(t, []string{
			"/bin dir 0755",
			"/bin/sh hardlink -> /bin/busybox",
		}, treeLines(root))
		assert.Zero(t, nodeAt(root, "/bin/sh").Size)
	})
}

func TestSquashDuplicatePathsNeverReachSquash(t *testing.T) {
	t.Parallel()

	// The indexer resolves duplicate paths (last-in-tar wins) before the
	// squasher ever sees them, so squashing has no duplicate case to get
	// wrong.
	idx := indexTarLayer(t, tartest.New().
		File("app/dup.txt", "first", tartest.Mode(0o600)).
		File("app/dup.txt", "second!", tartest.Mode(0o755)))

	seen := map[string]int{}
	for _, e := range idx.Entries {
		seen[e.Path]++
	}
	assert.Equal(t, map[string]int{"/app/dup.txt": 1}, seen)

	root := analyze.Squash([]domain.LayerIndex{idx})
	assert.Equal(t, []string{
		"/app dir(implicit) 0755",
		"/app/dup.txt file 0755 7",
	}, treeLines(root))
}

// TestSquashSixLayerWorkedExample is the committed worked example: six layers
// exercising every §4.2 rule at once, with the expected tree written out in
// full so a human can check it by hand.
func TestSquashSixLayerWorkedExample(t *testing.T) {
	t.Parallel()

	root := analyze.Squash(sixLayerStack())

	assert.Equal(t, []string{
		"/app dir 0755",
		// L4 made /app/cache opaque and re-created it: only L4's own
		// entry is inside it.
		"/app/cache dir 0700",
		"/app/cache/warm file 0644 4",
		// /app/config was a directory in L1 and is a file from L3 on:
		// the type change hid the whole subtree.
		"/app/config file 0644 8",
		"/app/data dir 0755",
		// /app/data/keep survived every whiteout; /app/data/gone did not.
		"/app/data/keep file 0644 4",
		// L6 whiteouts the link's *target* but keeps the link itself.
		"/app/data/link hardlink -> /app/data/tmp",
		"/bin dir(implicit) 0755",
		"/bin/busybox file 0755 6",
		"/bin/sh symlink -> busybox",
		"/dev dir(implicit) 0755",
		"/dev/null device 1:3",
		"/var dir 0755",
		"/var/run fifo",
	}, treeLines(root))
}

// sixLayerStack is the worked example's input, kept next to the expectation.
func sixLayerStack() []domain.LayerIndex {
	return []domain.LayerIndex{
		// L1 — a base with a config directory and some data.
		layer(
			dirEntry("/app"),
			dirEntry("/app/config"),
			fileEntry("/app/config/settings.ini", "old settings"),
			dirEntry("/app/data"),
			fileEntry("/app/data/keep", "keep"),
			fileEntry("/app/data/gone", "gone"),
			dirEntry("/var"),
			fifoEntry("/var/run"),
		),
		// L2 — implicit parents only (no /bin or /dev members).
		layer(
			fileEntry("/bin/busybox", "BUSYBX", withMode(0o755)),
			symlinkEntry("/bin/sh", "busybox"),
			deviceEntry("/dev/null", 1, 3),
		),
		// L3 — /app/config becomes a file: the subtree below it vanishes.
		// It also seeds a cache directory for L4 to make opaque.
		layer(
			fileEntry("/app/config", "settings"),
			whiteoutEntry("/app/data/gone"),
			dirEntry("/app/cache"),
			fileEntry("/app/cache/stale", "stale"),
		),
		// L4 — re-creates the cache directory with new metadata and one
		// file, and makes it opaque. The marker is written LAST, after
		// the entries it must not delete: the lower "stale" goes, the
		// layer's own "warm" stays.
		layer(
			fileEntry("/app/cache/warm", "warm"),
			dirEntry("/app/cache", withMode(0o700)),
			opaqueEntry("/app/cache"),
		),
		// L5 — a hardlink, and a whiteout of a path that never existed
		// (a no-op, not an error).
		layer(
			fileEntry("/app/data/tmp", "tmpfile"),
			hardlinkEntry("/app/data/link", "/app/data/tmp"),
			whiteoutEntry("/app/never"),
		),
		// L6 — whiteouts the hardlink's target: the link is kept,
		// dangling, exactly as declared. Links are never resolved.
		layer(
			whiteoutEntry("/app/data/tmp"),
		),
	}
}

// TestSquasherMatchesSquash: the incremental API and the slice API are the
// same fold. Squash's callers (and its own tests) keep working precisely
// because Squash is now expressed in terms of the squasher rather than
// duplicating it.
func TestSquasherMatchesSquash(t *testing.T) {
	t.Parallel()

	indexes := []domain.LayerIndex{
		layer(dirEntry("/app"), fileEntry("/app/a", "one"), fileEntry("/app/b", "two")),
		layer(whiteoutEntry("/app/a"), fileEntry("/app/c", "three")),
		layer(opaqueEntry("/app"), fileEntry("/app/d", "four")),
		layer(fileEntry("/app/e/deep", "five")),
	}

	incremental := analyze.NewSquasher()
	for i := range indexes {
		incremental.Apply(indexes[i].Entries)
	}

	assert.Equal(t, treeLines(analyze.Squash(indexes)), treeLines(incremental.Tree()))

	// Applying nothing is the legal empty filesystem at layer point 0, and
	// it matches Squash(nil) rather than being a special case.
	assert.Equal(t, treeLines(analyze.Squash(nil)), treeLines(analyze.NewSquasher().Tree()))
}
