package analyze_test

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/analyze"
	"github.com/ericsuh/layerlens/internal/domain"
)

// diffOf squashes both sides and diffs them, which is how the server will use
// these two functions.
func diffOf(left, right []domain.LayerIndex) *domain.DiffNode {
	return analyze.Diff(analyze.Squash(left), analyze.Squash(right))
}

// diffLines renders a diff tree as one sorted line per path, in the order the
// children are actually stored (so the ordering contract is visible), e.g.
//
//	"/app/x.txt modified L=3 R=5"
func diffLines(root *domain.DiffNode) []string {
	var out []string
	var walk func(prefix string, n *domain.DiffNode)
	walk = func(prefix string, n *domain.DiffNode) {
		for _, c := range n.Children {
			p := prefix + "/" + c.Name
			out = append(out, fmt.Sprintf("%s %s %s", p, c.Status, sideSummary(c)))
			walk(p, c)
		}
	}
	walk("", root)
	return out
}

func sideSummary(n *domain.DiffNode) string {
	side := func(m *domain.SideMeta) string {
		if m == nil {
			return "-"
		}
		return fmt.Sprintf("%s:%d", m.Kind, m.Size)
	}
	return "L=" + side(n.Left) + " R=" + side(n.Right)
}

// diffAt finds a node by "/"-rooted path in a diff tree, or nil.
func diffAt(root *domain.DiffNode, p string) *domain.DiffNode {
	cur := root
	for _, seg := range splitPath(p) {
		var next *domain.DiffNode
		for _, c := range cur.Children {
			if c.Name == seg {
				next = c
				break
			}
		}
		if next == nil {
			return nil
		}
		cur = next
	}
	return cur
}

// childNames lists a node's children in stored order.
func childNames(n *domain.DiffNode) []string {
	out := make([]string, 0, len(n.Children))
	for _, c := range n.Children {
		out = append(out, c.Name)
	}
	return out
}

func body(n int) string { return strings.Repeat("x", n) }

func TestDiffOneSidedSubtrees(t *testing.T) {
	t.Parallel()

	side := []domain.LayerIndex{layer(
		dirEntry("/opt"),
		dirEntry("/opt/tool"),
		fileEntry("/opt/tool/bin", body(10)),
		fileEntry("/opt/tool/lib", body(4)),
		symlinkEntry("/opt/tool/latest", "bin"),
	)}

	t.Run("added_subtree_marks_recursively", func(t *testing.T) {
		t.Parallel()
		d := diffOf(nil, side)
		assert.Equal(t, domain.StatusModified, d.Status, "the root gained children")
		assert.Equal(t, []string{
			"/opt added L=- R=dir:0",
			"/opt/tool added L=- R=dir:0",
			"/opt/tool/bin added L=- R=file:10",
			"/opt/tool/latest added L=- R=symlink:0",
			"/opt/tool/lib added L=- R=file:4",
		}, diffLines(d))

		// Agg is filled at every level of a one-sided subtree: there is
		// no later pass that could repair it.
		tool := diffAt(d, "/opt/tool")
		require.NotNil(t, tool)
		assert.Equal(t, domain.Agg{
			RightBytes: 14, RightFiles: 2,
			AddedBytes: 14, AddedFiles: 2,
		}, tool.Agg)
		assert.Equal(t, tool.Agg, diffAt(d, "/opt").Agg)
		assert.Equal(t, tool.Agg, d.Agg)
		assert.Nil(t, diffAt(d, "/opt/tool/bin").Left)
	})

	t.Run("removed_subtree", func(t *testing.T) {
		t.Parallel()
		d := diffOf(side, nil)
		assert.Equal(t, []string{
			"/opt removed L=dir:0 R=-",
			"/opt/tool removed L=dir:0 R=-",
			"/opt/tool/bin removed L=file:10 R=-",
			"/opt/tool/latest removed L=symlink:0 R=-",
			"/opt/tool/lib removed L=file:4 R=-",
		}, diffLines(d))
		assert.Equal(t, domain.Agg{
			LeftBytes: 14, LeftFiles: 2,
			RemovedBytes: 14, RemovedFiles: 2,
		}, d.Agg)
		assert.Nil(t, diffAt(d, "/opt/tool/bin").Right)
	})

	t.Run("both_sides_empty", func(t *testing.T) {
		t.Parallel()
		d := diffOf(nil, nil)
		require.NotNil(t, d)
		assert.Equal(t, domain.StatusUnchanged, d.Status)
		assert.Empty(t, d.Children)
		assert.Equal(t, domain.Agg{}, d.Agg)
	})
}

// TestDiffAggregationArithmetic checks every aggregate against hand-computed
// values on a small fixed tree, including a directory that merely *contains*
// changes (/a) next to ones that were themselves added (/d) or removed (/b).
func TestDiffAggregationArithmetic(t *testing.T) {
	t.Parallel()

	left := []domain.LayerIndex{layer(
		dirEntry("/a"),
		fileEntry("/a/keep.txt", body(10)),
		fileEntry("/a/mod.txt", body(5)),
		fileEntry("/a/gone.txt", body(7)),
		dirEntry("/b"),
		fileEntry("/b/only-left.txt", body(3)),
		dirEntry("/c"),
		fileEntry("/c/x.txt", body(4)),
	)}
	right := []domain.LayerIndex{layer(
		dirEntry("/a"),
		fileEntry("/a/keep.txt", body(10)),
		fileEntry("/a/mod.txt", body(8)),
		fileEntry("/a/new.txt", body(6)),
		dirEntry("/c"),
		fileEntry("/c/x.txt", body(4)),
		dirEntry("/d"),
		fileEntry("/d/new1.txt", body(2)),
		dirEntry("/d/sub"),
		fileEntry("/d/sub/new2.txt", body(9)),
	)}

	d := diffOf(left, right)

	t.Run("dir_containing_changes", func(t *testing.T) {
		a := diffAt(d, "/a")
		require.NotNil(t, a)
		assert.Equal(t, domain.StatusModified, a.Status)
		assert.Equal(t, domain.Agg{
			LeftBytes: 22, RightBytes: 24,
			LeftFiles: 3, RightFiles: 3,
			AddedBytes: 6, RemovedBytes: 7,
			ModifiedBytesLeft: 5, ModifiedBytesRight: 8,
			AddedFiles: 1, RemovedFiles: 1, ModifiedFiles: 1,
		}, a.Agg)
	})

	t.Run("removed_dir", func(t *testing.T) {
		b := diffAt(d, "/b")
		require.NotNil(t, b)
		assert.Equal(t, domain.StatusRemoved, b.Status)
		assert.Equal(t, domain.Agg{
			LeftBytes: 3, LeftFiles: 1,
			RemovedBytes: 3, RemovedFiles: 1,
		}, b.Agg)
	})

	t.Run("unchanged_dir", func(t *testing.T) {
		c := diffAt(d, "/c")
		require.NotNil(t, c)
		assert.Equal(t, domain.StatusUnchanged, c.Status)
		assert.Equal(t, domain.Agg{
			LeftBytes: 4, RightBytes: 4, LeftFiles: 1, RightFiles: 1,
		}, c.Agg)
	})

	t.Run("added_dir_with_nested_dir", func(t *testing.T) {
		dd := diffAt(d, "/d")
		require.NotNil(t, dd)
		assert.Equal(t, domain.StatusAdded, dd.Status)
		assert.Equal(t, domain.Agg{
			RightBytes: 11, RightFiles: 2,
			AddedBytes: 11, AddedFiles: 2,
		}, dd.Agg)
		assert.Equal(t, domain.Agg{
			RightBytes: 9, RightFiles: 1, AddedBytes: 9, AddedFiles: 1,
		}, diffAt(d, "/d/sub").Agg)
	})

	t.Run("root_totals", func(t *testing.T) {
		assert.Equal(t, domain.StatusModified, d.Status)
		assert.Equal(t, domain.Agg{
			LeftBytes: 29, RightBytes: 39,
			LeftFiles: 5, RightFiles: 6,
			AddedBytes: 17, RemovedBytes: 10,
			ModifiedBytesLeft: 5, ModifiedBytesRight: 8,
			AddedFiles: 3, RemovedFiles: 2, ModifiedFiles: 1,
		}, d.Agg)
	})

	t.Run("modified_file_counts_both_sides_bytes", func(t *testing.T) {
		m := diffAt(d, "/a/mod.txt")
		require.NotNil(t, m)
		assert.Equal(t, domain.StatusModified, m.Status)
		assert.Equal(t, domain.Agg{
			LeftBytes: 5, RightBytes: 8, LeftFiles: 1, RightFiles: 1,
			ModifiedBytesLeft: 5, ModifiedBytesRight: 8, ModifiedFiles: 1,
		}, m.Agg, "a modified file counts on both sides' byte totals")
	})
}

func TestDiffModificationPredicate(t *testing.T) {
	t.Parallel()

	// Each case is the same path on both sides, differing in exactly one
	// field of the tarsum-v1 set (or in mtime, which is not in it).
	base := fileEntry("/f", "content", withMode(0o644), withOwner(0, 0))

	tests := []struct {
		name  string
		right domain.Entry
		want  domain.DiffStatus
	}{
		{
			name:  "mtime_only_change_is_unchanged",
			right: fileEntry("/f", "content", withMtime(1234567890)),
			want:  domain.StatusUnchanged,
		},
		{
			name:  "content_change_is_modified",
			right: fileEntry("/f", "different"),
			want:  domain.StatusModified,
		},
		{
			name:  "mode_change_is_modified",
			right: fileEntry("/f", "content", withMode(0o600)),
			want:  domain.StatusModified,
		},
		{
			name:  "uid_change_is_modified",
			right: fileEntry("/f", "content", withOwner(1000, 0)),
			want:  domain.StatusModified,
		},
		{
			name:  "gid_change_is_modified",
			right: fileEntry("/f", "content", withOwner(0, 1000)),
			want:  domain.StatusModified,
		},
		{
			name: "uname_change_is_modified",
			right: apply(fileEntry("/f", "content"), []entryOpt{
				func(e *domain.Entry) { e.Uname = "app" },
			}),
			want: domain.StatusModified,
		},
		{
			name:  "xattr_change_is_modified",
			right: fileEntry("/f", "content", withXattr("security.capability", "cap")),
			want:  domain.StatusModified,
		},
		{
			name:  "kind_change_is_modified",
			right: symlinkEntry("/f", "elsewhere"),
			want:  domain.StatusModified,
		},
		{
			name:  "identical_is_unchanged",
			right: fileEntry("/f", "content"),
			want:  domain.StatusUnchanged,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := diffOf([]domain.LayerIndex{layer(base)}, []domain.LayerIndex{layer(tc.right)})
			f := diffAt(d, "/f")
			require.NotNil(t, f)
			assert.Equal(t, tc.want, f.Status)

			// The tree predicate and the changeset digest are the
			// same field set by construction; assert they agree.
			sameDigest := analyze.ChangesetDigest([]domain.Entry{base}) ==
				analyze.ChangesetDigest([]domain.Entry{tc.right})
			assert.Equal(t, tc.want == domain.StatusUnchanged, sameDigest,
				"the diff predicate and the changeset digest must never disagree")
		})
	}
}

func TestDiffSymlinkTargetChange(t *testing.T) {
	t.Parallel()

	d := diffOf(
		[]domain.LayerIndex{layer(symlinkEntry("/bin/sh", "busybox"))},
		[]domain.LayerIndex{layer(symlinkEntry("/bin/sh", "dash"))},
	)
	assert.Equal(t, domain.StatusModified, diffAt(d, "/bin/sh").Status)
	assert.Equal(t, "dash", diffAt(d, "/bin/sh").Right.LinkTarget)
}

func TestDiffDirectoryStatus(t *testing.T) {
	t.Parallel()

	t.Run("dir_status_modified_iff_descendant_or_own_meta_changed", func(t *testing.T) {
		t.Parallel()
		left := []domain.LayerIndex{layer(
			dirEntry("/same"), fileEntry("/same/f", "f"),
			dirEntry("/childchanged"), fileEntry("/childchanged/f", "one"),
			dirEntry("/metachanged", withMode(0o755)), fileEntry("/metachanged/f", "f"),
		)}
		right := []domain.LayerIndex{layer(
			dirEntry("/same"), fileEntry("/same/f", "f"),
			dirEntry("/childchanged"), fileEntry("/childchanged/f", "two"),
			dirEntry("/metachanged", withMode(0o700)), fileEntry("/metachanged/f", "f"),
		)}
		d := diffOf(left, right)

		assert.Equal(t, domain.StatusUnchanged, diffAt(d, "/same").Status)
		assert.Equal(t, domain.StatusModified, diffAt(d, "/childchanged").Status,
			"a dir is modified when a descendant changed")
		assert.Equal(t, domain.StatusModified, diffAt(d, "/metachanged").Status,
			"a dir is modified when its own metadata changed")
		assert.Equal(t, domain.StatusUnchanged, diffAt(d, "/metachanged/f").Status,
			"the dir's mode change must not spill onto its children")
	})

	t.Run("deep_descendant_change_propagates_to_every_ancestor", func(t *testing.T) {
		t.Parallel()
		left := []domain.LayerIndex{layer(fileEntry("/a/b/c/d.txt", "one"))}
		right := []domain.LayerIndex{layer(fileEntry("/a/b/c/d.txt", "two"))}
		d := diffOf(left, right)
		for _, p := range []string{"/a", "/a/b", "/a/b/c", "/a/b/c/d.txt"} {
			assert.Equal(t, domain.StatusModified, diffAt(d, p).Status, p)
		}
		assert.Equal(t, domain.StatusModified, d.Status)
	})

	t.Run("implicit_dir_mode_never_flags_modified", func(t *testing.T) {
		t.Parallel()
		// Left ships an explicit /usr/share with an unusual mode; right
		// ships only the file, so /usr/share is synthesized at 0755.
		// The synthetic mode is ours, not the image's, and must not read
		// as a change.
		left := []domain.LayerIndex{layer(
			dirEntry("/usr", withMode(0o755)),
			dirEntry("/usr/share", withMode(0o700), withOwner(7, 7)),
			fileEntry("/usr/share/doc", "doc"),
		)}
		right := []domain.LayerIndex{layer(fileEntry("/usr/share/doc", "doc"))}

		d := diffOf(left, right)
		assert.Equal(t, domain.StatusUnchanged, diffAt(d, "/usr/share").Status)
		assert.Equal(t, domain.StatusUnchanged, diffAt(d, "/usr").Status)
		assert.Equal(t, domain.StatusUnchanged, d.Status)
	})

	t.Run("dir_owner_change_is_modified", func(t *testing.T) {
		t.Parallel()
		// Both sides explicit: a chowned directory is a real change and
		// the tarsum-v1 field set includes uid/gid.
		left := []domain.LayerIndex{layer(dirEntry("/srv"), fileEntry("/srv/f", "f"))}
		right := []domain.LayerIndex{layer(dirEntry("/srv", withOwner(1000, 1000)), fileEntry("/srv/f", "f"))}
		d := diffOf(left, right)
		assert.Equal(t, domain.StatusModified, diffAt(d, "/srv").Status)
	})

	t.Run("dir_replaced_by_file_is_a_leaf", func(t *testing.T) {
		t.Parallel()
		left := []domain.LayerIndex{layer(dirEntry("/x"), fileEntry("/x/inner", "inner"))}
		right := []domain.LayerIndex{layer(fileEntry("/x", "now a file"))}
		d := diffOf(left, right)

		x := diffAt(d, "/x")
		require.NotNil(t, x)
		assert.Equal(t, domain.StatusModified, x.Status)
		assert.Empty(t, x.Children, "a type change hides the subtree below it (§4.3)")
		assert.Equal(t, domain.KindDir, x.Left.Kind)
		assert.Equal(t, domain.KindFile, x.Right.Kind)
	})
}

func TestDiffNonFileKinds(t *testing.T) {
	t.Parallel()

	// Non-regular files are rows with a status, but they contribute no
	// bytes and are not counted as files.
	right := []domain.LayerIndex{layer(
		dirEntry("/dev"),
		symlinkEntry("/dev/stdin", "/proc/self/fd/0"),
		deviceEntry("/dev/null", 1, 3),
		fifoEntry("/dev/initctl"),
		fileEntry("/dev/real", body(6)),
		hardlinkEntry("/dev/link", "/dev/real"),
	)}
	d := diffOf(nil, right)

	assert.Equal(t, []string{
		"/dev added L=- R=dir:0",
		"/dev/initctl added L=- R=fifo:0",
		"/dev/link added L=- R=hardlink:0",
		"/dev/null added L=- R=device:0",
		"/dev/real added L=- R=file:6",
		"/dev/stdin added L=- R=symlink:0",
	}, diffLines(d), "every non-file kind still appears as a row")

	assert.Equal(t, domain.Agg{
		RightBytes: 6, RightFiles: 1, AddedBytes: 6, AddedFiles: 1,
	}, diffAt(d, "/dev").Agg, "only the regular file contributes bytes and counts")
}

func TestDiffChildOrdering(t *testing.T) {
	t.Parallel()

	t.Run("child_ordering_dirs_first_then_files_by_name", func(t *testing.T) {
		t.Parallel()
		right := []domain.LayerIndex{layer(
			fileEntry("/root/zeta.txt", "z"),
			fileEntry("/root/alpha.txt", "a"),
			dirEntry("/root/zulu"),
			dirEntry("/root/bravo"),
			symlinkEntry("/root/mike", "zulu"),
		)}
		d := diffOf(nil, right)
		assert.Equal(t,
			[]string{"bravo", "zulu", "alpha.txt", "mike", "zeta.txt"},
			childNames(diffAt(d, "/root")))
	})

	t.Run("ordering_follows_the_right_side_on_a_type_change", func(t *testing.T) {
		t.Parallel()
		left := []domain.LayerIndex{layer(dirEntry("/r"), dirEntry("/r/m"), fileEntry("/r/m/x", "x"), fileEntry("/r/a", "a"))}
		right := []domain.LayerIndex{layer(dirEntry("/r"), fileEntry("/r/m", "now a file"), fileEntry("/r/a", "a"))}
		d := diffOf(left, right)
		// "m" is a directory on the left but a file on the right; the
		// "after" state is what the user is looking at.
		assert.Equal(t, []string{"a", "m"}, childNames(diffAt(d, "/r")))
	})
}

func TestDiffResultIsSelfContained(t *testing.T) {
	t.Parallel()

	// §4.6 budgets the side trees as transient: the diff must copy what it
	// needs so both input trees can be dropped the moment Diff returns.
	leftTree := analyze.Squash([]domain.LayerIndex{layer(fileEntry("/f", body(4)))})
	rightTree := analyze.Squash([]domain.LayerIndex{layer(fileEntry("/f", body(9)))})
	d := analyze.Diff(leftTree, rightTree)

	before := *diffAt(d, "/f")
	leftTree.Children["f"].Size = 999999
	leftTree.Children["f"].Mode = 0o000
	rightTree.Children["f"].ContentSHA = ""
	delete(rightTree.Children, "f")

	after := diffAt(d, "/f")
	require.NotNil(t, after)
	assert.Equal(t, before.Left.Size, after.Left.Size)
	assert.Equal(t, before.Left.Mode, after.Left.Mode)
	assert.Equal(t, before.Right.ContentSHA, after.Right.ContentSHA)
	assert.Equal(t, int64(4), after.Agg.LeftBytes)
}

// TestDiffAggInvariantFuzz is the property test: on randomly generated tree
// pairs, every node's Agg equals its own contribution plus the sum of its
// children's, directories contribute nothing of their own, and the byte
// deltas reconcile. Seeded, so a failure is reproducible.
func TestDiffAggInvariantFuzz(t *testing.T) {
	t.Parallel()

	const seed = 20260829
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec // deterministic test input, not security

	for i := 0; i < 200; i++ {
		left := randomStack(rng)
		right := randomStack(rng)
		d := diffOf(left, right)
		require.NotNil(t, d)
		checkAggInvariant(t, d, "")
		if t.Failed() {
			t.Fatalf("agg invariant broken on iteration %d (seed %d)", i, seed)
		}
	}
}

func checkAggInvariant(t *testing.T, n *domain.DiffNode, path string) {
	t.Helper()

	var want domain.Agg
	// Own contribution: regular files only, per §4.4.
	if n.Left != nil && n.Left.Kind == domain.KindFile {
		want.LeftBytes += n.Left.Size
		want.LeftFiles++
	}
	if n.Right != nil && n.Right.Kind == domain.KindFile {
		want.RightBytes += n.Right.Size
		want.RightFiles++
	}
	switch n.Status {
	case domain.StatusAdded:
		want.AddedBytes = want.RightBytes
		want.AddedFiles = want.RightFiles
	case domain.StatusRemoved:
		want.RemovedBytes = want.LeftBytes
		want.RemovedFiles = want.LeftFiles
	case domain.StatusModified:
		want.ModifiedBytesLeft = want.LeftBytes
		want.ModifiedBytesRight = want.RightBytes
		if want.LeftFiles+want.RightFiles > 0 {
			want.ModifiedFiles = 1
		}
	case domain.StatusUnchanged:
	}

	// A path that is a directory on every side it exists on contributes
	// nothing of its own, which is what makes "Agg == Σ children" the
	// directory invariant the API relies on.
	bothDirs := (n.Left == nil || n.Left.Kind == domain.KindDir) &&
		(n.Right == nil || n.Right.Kind == domain.KindDir)
	if bothDirs {
		require.Equal(t, domain.Agg{}, want, "%s: directories contribute no bytes of their own", path)
	}

	for _, c := range n.Children {
		checkAggInvariant(t, c, path+"/"+c.Name)
		addInto(&want, &c.Agg)
	}
	require.Equal(t, want, n.Agg, "%s: Agg must equal own contribution + Σ children", path)

	// The byte deltas must reconcile: unchanged bytes cancel out, so
	// right - left is exactly what was added and grown minus what was
	// removed and shrunk.
	require.Equal(t,
		n.Agg.RightBytes-n.Agg.LeftBytes,
		n.Agg.AddedBytes+n.Agg.ModifiedBytesRight-n.Agg.RemovedBytes-n.Agg.ModifiedBytesLeft,
		"%s: byte deltas must reconcile", path)
}

func addInto(dst *domain.Agg, src *domain.Agg) {
	dst.LeftBytes += src.LeftBytes
	dst.RightBytes += src.RightBytes
	dst.LeftFiles += src.LeftFiles
	dst.RightFiles += src.RightFiles
	dst.AddedBytes += src.AddedBytes
	dst.RemovedBytes += src.RemovedBytes
	dst.ModifiedBytesLeft += src.ModifiedBytesLeft
	dst.ModifiedBytesRight += src.ModifiedBytesRight
	dst.AddedFiles += src.AddedFiles
	dst.RemovedFiles += src.RemovedFiles
	dst.ModifiedFiles += src.ModifiedFiles
}

// randomStack builds one to three random layers over a small shared path
// vocabulary, so the two sides overlap often enough to produce every status,
// and so whiteouts, opaque markers and type changes all occur.
func randomStack(rng *rand.Rand) []domain.LayerIndex {
	dirs := []string{"/a", "/a/b", "/a/b/c", "/d", "/d/e", "/f"}
	names := []string{"one", "two", "three"}

	n := 1 + rng.Intn(3)
	stack := make([]domain.LayerIndex, 0, n)
	for i := 0; i < n; i++ {
		var entries []domain.Entry
		for j := 0; j < 1+rng.Intn(8); j++ {
			dir := dirs[rng.Intn(len(dirs))]
			name := names[rng.Intn(len(names))]
			p := dir + "/" + name
			switch rng.Intn(8) {
			case 0:
				entries = append(entries, whiteoutEntry(p))
			case 1:
				entries = append(entries, opaqueEntry(dir))
			case 2:
				entries = append(entries, symlinkEntry(p, "target"))
			case 3:
				entries = append(entries, dirEntry(p, withMode(uint32(0o700+rng.Intn(78)))))
			case 4:
				entries = append(entries, hardlinkEntry(p, dir+"/one"))
			case 5:
				entries = append(entries, dirEntry(dir))
			default:
				entries = append(entries, fileEntry(p, body(rng.Intn(64))))
			}
		}
		// The indexer guarantees one filesystem entry per path; mimic
		// that so the fuzz exercises legal changesets only.
		stack = append(stack, layer(dedupeEntries(entries)...))
	}
	return stack
}

// dedupeEntries keeps the last filesystem entry per path and at most one
// marker of each kind per path, exactly like the indexer's bookkeeping.
func dedupeEntries(entries []domain.Entry) []domain.Entry {
	type key struct {
		path   string
		marker domain.EntryKind
	}
	seen := map[key]domain.Entry{}
	for _, e := range entries {
		k := key{path: e.Path}
		if e.Kind == domain.KindWhiteout || e.Kind == domain.KindOpaque {
			k.marker = e.Kind
		}
		seen[k] = e
	}
	out := make([]domain.Entry, 0, len(seen))
	for _, e := range seen {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}
