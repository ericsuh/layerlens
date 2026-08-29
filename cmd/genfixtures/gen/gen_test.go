package gen

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/validate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/analyze"
	"github.com/ericsuh/layerlens/internal/domain"
)

// ---------------------------------------------------------------------------
// Determinism
// ---------------------------------------------------------------------------

// Determinism is the entire premise of vendoring generated fixtures: a
// reviewer must be able to run the generator and see git say nothing. Two
// independent builds are compared byte for byte, which catches map iteration
// order, clock reads, gzip header stamping and anything else that would make
// the committed tree unreproducible.
func TestGeneratorIsDeterministic(t *testing.T) {
	t.Parallel()

	first := t.TempDir()
	second := t.TempDir()
	_, err := Build(first)
	require.NoError(t, err)
	_, err = Build(second)
	require.NoError(t, err)

	assertTreesEqual(t, first, second)
}

// A second build over an existing output directory must converge on the same
// bytes rather than accumulating orphans from the first — WritePair wipes each
// pair directory precisely so that a renamed or resized fixture cannot leave a
// stale blob behind that `git status` would then report forever.
func TestRebuildInPlaceIsIdempotent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_, err := Build(dir)
	require.NoError(t, err)
	before := snapshot(t, dir)

	// Plant an orphan the way a deleted fixture entry would.
	orphan := filepath.Join(dir, "example", "blobs", "sha256",
		"0000000000000000000000000000000000000000000000000000000000000000")
	require.NoError(t, os.WriteFile(orphan, []byte("stale"), 0o600))

	_, err = Build(dir)
	require.NoError(t, err)
	assert.Equal(t, before, snapshot(t, dir))
}

// The committed fixtures must be exactly what the current generator produces.
// This is the "mise run genfixtures leaves no git diff" acceptance criterion,
// asserted without shelling out to git.
func TestCommittedFixturesAreUpToDate(t *testing.T) {
	t.Parallel()

	fresh := t.TempDir()
	_, err := Build(fresh)
	require.NoError(t, err)
	assertTreesEqual(t, fresh, fixturesRoot(t))
}

// The fixtures live in the repository, so their size is a real cost. The cap
// is deliberately far below the phase budget: if a future edit blows past it,
// that is a design decision to make on purpose, not to discover in a clone.
func TestCommittedFixturesAreSmall(t *testing.T) {
	t.Parallel()

	total, err := dirSize(fixturesRoot(t))
	require.NoError(t, err)
	t.Logf("vendored fixtures: %d bytes", total)
	assert.Less(t, total, int64(4<<20), "vendored fixtures should stay a few MiB at most")
}

func assertTreesEqual(t *testing.T, a, b string) {
	t.Helper()
	sa, sb := snapshot(t, a), snapshot(t, b)

	namesA := sortedKeys(sa)
	namesB := sortedKeys(sb)
	require.Equal(t, namesA, namesB, "the two trees hold different files")
	for _, name := range namesA {
		if !bytes.Equal(sa[name], sb[name]) {
			t.Fatalf("%s differs between the two trees (%d vs %d bytes)", name, len(sa[name]), len(sb[name]))
		}
	}
}

// snapshot reads a directory tree into memory, keyed by slash-separated
// relative path.
func snapshot(t *testing.T, root string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path) //nolint:gosec // test-local walk of a temp or repo dir
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = b
		return nil
	})
	require.NoError(t, err)
	return out
}

func sortedKeys(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ---------------------------------------------------------------------------
// OCI validity
// ---------------------------------------------------------------------------

// Every layout must be readable and correct by go-containerregistry's own
// validator — the same library the ingest path will use in phase 006 — so that
// "valid OCI layout" is a checked claim rather than a hand-written JSON file
// that happens to look right.
func TestLayoutsAreValidOCI(t *testing.T) {
	t.Parallel()

	for _, pair := range Pairs() {
		t.Run(pair.Name, func(t *testing.T) {
			t.Parallel()

			idx, err := layout.ImageIndexFromPath(filepath.Join(fixturesRoot(t), pair.Name))
			require.NoError(t, err)
			manifest, err := idx.IndexManifest()
			require.NoError(t, err)
			require.Len(t, manifest.Manifests, len(pair.Images))

			for i, desc := range manifest.Manifests {
				require.NotNil(t, desc.Platform, "manifest %d has no platform", i)
				assert.Equal(t, OS, desc.Platform.OS)
				assert.Equal(t, Architecture, desc.Platform.Architecture)
				assert.Equal(t, pair.Images[i].Ref, desc.Annotations[RefNameAnnotation])

				img, err := idx.Image(desc.Digest)
				require.NoError(t, err)
				require.NoError(t, validate.Image(img), "%s failed gcr validation", pair.Images[i].Ref)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The golden pair
// ---------------------------------------------------------------------------

// Layer indexes within the example images. The trunk ends at the COPY step,
// which is the whole point of the fixture.
const (
	exTrunkLen  = 5 // base, apt deps, node, yarn, WORKDIR
	exCopyIdx   = 5
	exNpmIdx    = 6
	exFfmpegIdx = 7
	exLayers    = 8
)

// The shared trunk is exactly the five pre-COPY layers, and it is computed
// from DiffIDs, so it is the same trunk Docker's layer store would find.
func TestExamplePairTrunk(t *testing.T) {
	t.Parallel()

	v1 := image(t, "example", "example:v1")
	v2 := image(t, "example", "example:v2")

	require.Len(t, v1.Layers, exLayers)
	require.Len(t, v2.Layers, exLayers)
	assert.Equal(t, exTrunkLen, analyze.TrunkLCP(v1.Layers, v2.Layers))
	assert.NotEqual(t, v1.Layers[exCopyIdx].DiffID, v2.Layers[exCopyIdx].DiffID,
		"the COPY layers must fork the images")
}

// The load-bearing property of the entire fixture set.
//
// The two `RUN npm install` layers install identical files at different times.
// Under Docker's *layer store* rule (byte-exact over the uncompressed tar,
// mtime included) they are different layers, so their DiffIDs differ and
// Docker pulls both. Under Docker's *build cache* rule (tarsum v1, mtime
// excluded) they are the same work, so their normalized changeset digests
// match. That gap is what the dotted "could be shared" edge draws, and it is
// what the demo exists to explain — so it gets an explicit, standalone test.
func TestExampleNpmLayersDifferOnlyByMtime(t *testing.T) {
	t.Parallel()

	a := image(t, "example", "example:v1").Layers[exNpmIdx]
	b := image(t, "example", "example:v2").Layers[exNpmIdx]

	assert.NotEqual(t, a.DiffID, b.DiffID,
		"npm layers must have different DiffIDs — otherwise there is no cache-invalidation story to tell")
	assert.Equal(t, a.ChangesetDigest, b.ChangesetDigest,
		"npm layers must be equivalent under Docker's build-cache rule")
	assert.Equal(t, a.ContentBytes, b.ContentBytes)
	assert.Equal(t, a.EntryCount, b.EntryCount)

	// And the difference really is only the timestamps: every other field
	// of every entry matches, entry for entry.
	ea := image(t, "example", "example:v1").Indexes[exNpmIdx].Entries
	eb := image(t, "example", "example:v2").Indexes[exNpmIdx].Entries
	require.Equal(t, len(ea), len(eb))
	skewed := 0
	for i := range ea {
		if ea[i].MtimeUnix != eb[i].MtimeUnix {
			skewed++
		}
		x, y := ea[i], eb[i]
		x.MtimeUnix, y.MtimeUnix = 0, 0
		require.Equal(t, x, y, "entry %d (%s) differs beyond its mtime", i, ea[i].Path)
	}
	assert.Equal(t, len(ea), skewed, "every npm entry should carry a skewed mtime")
}

// The pair produces exactly the two dotted edges the demo describes, and both
// flavours of DiffIDEqual are represented: false for the rebuilt npm layer,
// true for the ffmpeg layer whose bytes reproduced exactly.
func TestExamplePairEdges(t *testing.T) {
	t.Parallel()

	v1 := image(t, "example", "example:v1")
	v2 := image(t, "example", "example:v2")
	k := analyze.TrunkLCP(v1.Layers, v2.Layers)
	edges := analyze.CouldBeSharedEdges(v1.Layers, v2.Layers, k)

	require.Len(t, edges, 2)
	assert.Equal(t, exNpmIdx, edges[0].LeftIndex)
	assert.Equal(t, exNpmIdx, edges[0].RightIndex)
	assert.False(t, edges[0].DiffIDEqual,
		"the npm edge is the demo's whole point: same content, different tar bytes")

	assert.Equal(t, exFfmpegIdx, edges[1].LeftIndex)
	assert.Equal(t, exFfmpegIdx, edges[1].RightIndex)
	assert.True(t, edges[1].DiffIDEqual,
		"the ffmpeg layers reproduce byte-for-byte, so their edge is the contrasting case")

	// The COPY layers have genuinely different contents and must not be
	// linked; a false edge there would tell the user the opposite of the
	// truth.
	for _, e := range edges {
		assert.NotEqual(t, exCopyIdx, e.LeftIndex)
		assert.NotEqual(t, exCopyIdx, e.RightIndex)
	}
}

// The `.dockerignore` mistake itself: at the fork point, v2 carries junk that
// v1 does not, v1 still carries sources v2 deleted, and two files were edited.
// The expected byte totals are derived from the fixture specs rather than
// hard-coded, so the assertion stays true when a fixture size is tuned but
// fails loudly if an entry is added to the wrong side.
func TestExampleCopyLayerDiff(t *testing.T) {
	t.Parallel()

	v1 := image(t, "example", "example:v1")
	v2 := image(t, "example", "example:v2")
	d := analyze.Diff(v1.squash(exCopyIdx+1), v2.squash(exCopyIdx+1))

	addedBytes, addedFiles := onlyIn(copyLayerV2(), copyLayerV1())
	removedBytes, removedFiles := onlyIn(copyLayerV1(), copyLayerV2())

	app := child(t, d, "/app")
	assert.Equal(t, domain.StatusModified, app.Status)
	assert.Equal(t, addedBytes, app.Agg.AddedBytes)
	assert.Equal(t, addedFiles, app.Agg.AddedFiles)
	assert.Equal(t, removedBytes, app.Agg.RemovedBytes)
	assert.Equal(t, removedFiles, app.Agg.RemovedFiles)
	assert.Equal(t, int64(2), app.Agg.ModifiedFiles, "main.js and src/util.js were edited")

	// The named rows the golden workflow and the e2e test both look for.
	assert.Equal(t, domain.StatusAdded, child(t, d, "/app/debug.log").Status)
	assert.Equal(t, domain.StatusAdded, child(t, d, "/app/.env").Status)
	assert.Equal(t, domain.StatusAdded, child(t, d, "/app/.git").Status)
	assert.Equal(t, domain.StatusModified, child(t, d, "/app/main.js").Status)
	assert.Equal(t, domain.StatusRemoved, child(t, d, "/app/src/old-util.js").Status)
	assert.Equal(t, domain.StatusRemoved, child(t, d, "/app/src/legacy").Status)
	assert.Equal(t, domain.StatusUnchanged, child(t, d, "/app/package-lock.json").Status)

	// The .git pack is both the largest addition and the one whose parent
	// directories arrive as implicit nodes — a real .git tar omits the
	// fan-out directory headers, and the squasher has to synthesize them.
	assert.Equal(t, domain.StatusAdded, child(t, d, "/app/.git/objects/pack/pack-8c4f21ab.pack").Status)
	assert.Positive(t, child(t, d, "/app/.git").Agg.AddedBytes)

	// Nothing below the trunk moved: the images share every pre-COPY layer.
	assert.Equal(t, domain.StatusUnchanged, child(t, d, "/usr/local/bin/node").Status)
	assert.Equal(t, domain.StatusUnchanged, child(t, d, "/bin/bash").Status)
}

// onlyIn sums the regular files present in a but not in b.
func onlyIn(a, b []EntrySpec) (bytes int64, files int64) {
	inB := map[string]bool{}
	for _, e := range b {
		inB[e.Path] = true
	}
	for _, e := range a {
		if e.Kind != domain.KindFile || inB[e.Path] {
			continue
		}
		bytes += e.Size
		files++
	}
	return bytes, files
}

// The apt/ffmpeg layer's `rm -rf /var/lib/{apt,dpkg,cache,log}/` must reach the
// analyzer as real whiteout entries and must actually delete the base image's
// state when the layers are squashed. Without this the filesystem view has no
// deletions to show.
func TestExampleFfmpegWhiteoutsDelete(t *testing.T) {
	t.Parallel()

	v2 := image(t, "example", "example:v2")

	whiteouts := map[string]bool{}
	for _, e := range v2.Indexes[exFfmpegIdx].Entries {
		if e.Kind == domain.KindWhiteout {
			whiteouts[e.Path] = true
		}
	}
	for _, target := range ffmpegWhiteoutTargets {
		assert.True(t, whiteouts[target], "ffmpeg layer should whiteout %s", target)
	}

	before := v2.squash(exFfmpegIdx)
	after := v2.squash(exFfmpegIdx + 1)
	for _, target := range ffmpegWhiteoutTargets {
		assert.NotNil(t, node(before, target), "%s should exist before the cleanup layer", target)
		assert.Nil(t, node(after, target), "%s should be gone after the cleanup layer", target)
	}
	// Deleting /var/lib/apt must not take /var/lib with it.
	assert.NotNil(t, node(after, "/var/lib"))
	assert.NotNil(t, node(after, "/usr/bin/ffmpeg"))

	// Selecting the pre-cleanup layer on the left and the cleanup layer on
	// the right is how the UI surfaces those deletions as removed rows.
	d := analyze.Diff(image(t, "example", "example:v1").squash(exFfmpegIdx), after)
	assert.Equal(t, domain.StatusRemoved, child(t, d, "/var/lib/apt").Status)
	assert.Equal(t, domain.StatusRemoved, child(t, d, "/var/lib/dpkg").Status)
	assert.Positive(t, child(t, d, "/var/lib").Agg.RemovedBytes)
}

// Symlinks survive the round trip as links, never as resolved targets: the
// npm .bin entries and the node runtime's own aliases are the shapes the tree
// view has to render.
func TestExampleSymlinksAndKinds(t *testing.T) {
	t.Parallel()

	tree := image(t, "example", "example:v2").squash(exLayers)

	nodejs := node(tree, "/usr/local/bin/nodejs")
	require.NotNil(t, nodejs)
	assert.Equal(t, domain.KindSymlink, nodejs.Kind)
	assert.Equal(t, "/usr/local/bin/node", nodejs.LinkTarget)

	ejs := node(tree, "/app/node_modules/.bin/ejs")
	require.NotNil(t, ejs)
	assert.Equal(t, domain.KindSymlink, ejs.Kind)
	assert.Equal(t, "../ejs/index.js", ejs.LinkTarget)

	// The deep path DESIGN §11 asks the demo data to contain.
	assert.NotNil(t, node(tree, "/app/node_modules/@babel/plugin-transform-runtime/lib/get-runtime-path/index.js"))
}

// The example configs interleave empty_layer history entries (CMD, two ENVs)
// with the layer-producing steps, so analyze.MapHistory has to skip them to
// keep instructions aligned with diff_ids. A misalignment here would attribute
// one layer's bytes to another layer's instruction everywhere in the UI.
func TestExampleHistoryMapping(t *testing.T) {
	t.Parallel()

	for _, ref := range []string{"example:v1", "example:v2"} {
		img := image(t, "example", ref)
		history := historyOf(img.Config)

		empties := 0
		for _, h := range history {
			if h.EmptyLayer {
				empties++
			}
		}
		assert.Equal(t, 4, empties, "%s should exercise empty_layer entries", ref)

		raw, ok := analyze.MapHistory(history, len(img.Layers))
		require.True(t, ok, "%s: history must map cleanly onto diff_ids", ref)
		require.Len(t, raw, exLayers)
		assert.Equal(t, cbBaseAdd, raw[0])
		assert.Equal(t, cbWorkdir, raw[exTrunkLen-1])
		assert.Equal(t, cbNpm, raw[exNpmIdx])

		for _, l := range img.Layers {
			assert.True(t, l.InstructionKnown, "%s: layer %d", ref, l.Index)
		}
		assert.Equal(t, "WORKDIR /app", img.Layers[exTrunkLen-1].Instruction)
		assert.Equal(t, "COPY . .", img.Layers[exCopyIdx].Instruction)
		assert.Equal(t, "RUN npm install", img.Layers[exNpmIdx].Instruction)
		assert.Equal(t,
			"RUN apt-get update -y && apt-get install -y ffmpeg && rm -rf /var/lib/{apt,dpkg,cache,log}/",
			img.Layers[exFfmpegIdx].Instruction)
	}
}

// Selecting the same trunk point on both sides is the degenerate comparison
// the UI must still render: every row unchanged, every aggregate balanced.
func TestExampleTrunkPointIsAllUnchanged(t *testing.T) {
	t.Parallel()

	v1 := image(t, "example", "example:v1")
	v2 := image(t, "example", "example:v2")
	d := analyze.Diff(v1.squash(exTrunkLen), v2.squash(exTrunkLen))

	assert.Equal(t, domain.StatusUnchanged, d.Status)
	assert.Zero(t, d.Agg.AddedFiles)
	assert.Zero(t, d.Agg.RemovedFiles)
	assert.Zero(t, d.Agg.ModifiedFiles)
	assert.Equal(t, d.Agg.LeftBytes, d.Agg.RightBytes)
	assert.Positive(t, d.Agg.LeftBytes)
}

// ---------------------------------------------------------------------------
// Degenerate pairs
// ---------------------------------------------------------------------------

func TestPrefixPairProperties(t *testing.T) {
	t.Parallel()

	base := image(t, "prefix", "prefix:base")
	ext := image(t, "prefix", "prefix:extended")

	require.Len(t, base.Layers, 3)
	require.Len(t, ext.Layers, 5)
	k := analyze.TrunkLCP(base.Layers, ext.Layers)
	assert.Equal(t, len(base.Layers), k, "base must be a strict prefix of extended")
	// One branch is empty, so there is nothing to draw an edge between.
	assert.Empty(t, analyze.CouldBeSharedEdges(base.Layers, ext.Layers, k))

	d := analyze.Diff(base.squash(len(base.Layers)), ext.squash(len(ext.Layers)))
	assert.Equal(t, domain.StatusAdded, child(t, d, "/etc/server").Status)
	assert.Equal(t, domain.StatusModified, child(t, d, "/etc/passwd").Status)
	assert.Equal(t, domain.StatusUnchanged, child(t, d, "/usr/bin/server").Status)
}

func TestDisjointPairProperties(t *testing.T) {
	t.Parallel()

	a := image(t, "disjoint", "disjoint:a")
	b := image(t, "disjoint", "disjoint:b")

	k := analyze.TrunkLCP(a.Layers, b.Layers)
	assert.Zero(t, k, "the disjoint pair must share nothing")
	assert.Empty(t, analyze.CouldBeSharedEdges(a.Layers, b.Layers, k),
		"disjoint images have no equivalent changesets either")

	d := analyze.Diff(a.squash(len(a.Layers)), b.squash(len(b.Layers)))
	assert.Equal(t, domain.StatusRemoved, child(t, d, "/srv").Status)
	assert.Equal(t, domain.StatusAdded, child(t, d, "/usr/local/bin/worker").Status)
}

// ---------------------------------------------------------------------------
// Edge cases
// ---------------------------------------------------------------------------

func TestEdgecasePairSquashSemantics(t *testing.T) {
	t.Parallel()

	opq := image(t, "edgecase", "edgecase:opaque")
	tree := opq.squash(len(opq.Layers))

	// Opaque marker: the lower children are hidden, the directory node
	// survives, and the layer's own member inside it still lands.
	cache := node(tree, "/data/cache")
	require.NotNil(t, cache)
	assert.Equal(t, domain.KindDir, cache.Kind)
	assert.Nil(t, node(tree, "/data/cache/a.bin"))
	assert.Nil(t, node(tree, "/data/cache/b.bin"))
	assert.NotNil(t, node(tree, "/data/cache/new.bin"))
	// Opacity is scoped to the one directory.
	assert.NotNil(t, node(tree, "/data/keep.txt"))

	// dir -> file: the shadowed subtree is gone.
	cfg := node(tree, "/etc/config")
	require.NotNil(t, cfg)
	assert.Equal(t, domain.KindFile, cfg.Kind)
	assert.Nil(t, node(tree, "/etc/config/base.conf"))

	// file -> dir: a fresh subtree.
	plugins := node(tree, "/opt/tool/plugins")
	require.NotNil(t, plugins)
	assert.Equal(t, domain.KindDir, plugins.Kind)
	assert.NotNil(t, node(tree, "/opt/tool/plugins/codec.so"))

	// Symlink retarget.
	cur := node(tree, "/opt/current")
	require.NotNil(t, cur)
	assert.Equal(t, "/opt/tool-v2", cur.LinkTarget)

	// Hardlinks: bytes counted once at the target, and the link whose
	// target was whiteouted is left dangling rather than repaired.
	alias := node(tree, "/usr/bin/alias")
	require.NotNil(t, alias)
	assert.Equal(t, domain.KindHardlink, alias.Kind)
	assert.Equal(t, "/usr/bin/real", alias.LinkTarget)
	assert.Zero(t, alias.Size)
	dangle := node(tree, "/srv/dangle")
	require.NotNil(t, dangle)
	assert.Nil(t, node(tree, "/srv/dangle-target.txt"))

	// Non-file kinds and xattrs survive indexing and squashing.
	dev := node(tree, "/dev/null")
	require.NotNil(t, dev)
	assert.Equal(t, domain.KindDevice, dev.Kind)
	assert.Equal(t, int64(1), dev.Devmajor)
	assert.Equal(t, int64(3), dev.Devminor)
	assert.Equal(t, domain.KindFifo, node(tree, "/run/initctl").Kind)
	assert.Equal(t, "\x01\x00\x00\x02\x00 \x00\x00",
		node(tree, "/usr/bin/ping").Xattrs["security.capability"])
	owned := node(tree, "/srv/owned.txt")
	require.NotNil(t, owned)
	assert.Equal(t, 1000, owned.UID)
	assert.Equal(t, "app", owned.Uname)
}

// The mode-only case, stated exactly as ARCHITECTURE §9.2 puts it: the file is
// *modified* in the tree, and because mode participates in the tarsum-v1 field
// set, the two layers are not equivalent under Docker's build-cache rule
// either — so no dotted edge is drawn anywhere in this pair.
func TestEdgecaseModeOnlyChange(t *testing.T) {
	t.Parallel()

	opq := image(t, "edgecase", "edgecase:opaque")
	pln := image(t, "edgecase", "edgecase:plain")

	k := analyze.TrunkLCP(opq.Layers, pln.Layers)
	assert.Equal(t, 1, k, "the two images share only their base layer")
	assert.Empty(t, analyze.CouldBeSharedEdges(opq.Layers, pln.Layers, k),
		"a mode difference is a real difference under Docker's build-cache rule")

	d := analyze.Diff(pln.squash(len(pln.Layers)), opq.squash(len(opq.Layers)))
	mode := child(t, d, "/srv/mode.sh")
	assert.Equal(t, domain.StatusModified, mode.Status)
	require.NotNil(t, mode.Left)
	require.NotNil(t, mode.Right)
	assert.Equal(t, uint32(0o644), mode.Left.Mode)
	assert.Equal(t, uint32(0o755), mode.Right.Mode)
	// Only the bits changed: identical content, identical size.
	assert.Equal(t, mode.Left.ContentSHA, mode.Right.ContentSHA)
	assert.Equal(t, mode.Left.Size, mode.Right.Size)

	// The rest of the pair's structural differences show up as expected.
	assert.Equal(t, domain.StatusRemoved, child(t, d, "/data/cache/a.bin").Status)
	assert.Equal(t, domain.StatusModified, child(t, d, "/etc/config").Status)
	assert.Equal(t, domain.StatusModified, child(t, d, "/opt/current").Status)
}

// ---------------------------------------------------------------------------
// Wide directory
// ---------------------------------------------------------------------------

func TestWidePairPagination(t *testing.T) {
	t.Parallel()

	v1 := image(t, "wide", "wide:v1")
	v2 := image(t, "wide", "wide:v2")

	for _, img := range []*loadedImage{v1, v2} {
		dir := node(img.squash(len(img.Layers)), WideDirPath)
		require.NotNil(t, dir, "%s has no %s", img.Ref, WideDirPath)
		assert.Len(t, dir.Children, WideDirChildren, "%s", img.Ref)
	}

	k := analyze.TrunkLCP(v1.Layers, v2.Layers)
	assert.Equal(t, 1, k)

	d := analyze.Diff(v1.squash(len(v1.Layers)), v2.squash(len(v2.Layers)))
	shards := child(t, d, WideDirPath)
	assert.Len(t, shards.Children, WideDirChildren)
	assert.Equal(t, int64(len(wideModified)), shards.Agg.ModifiedFiles)
	assert.Zero(t, shards.Agg.AddedFiles)
	assert.Zero(t, shards.Agg.RemovedFiles)
	for _, i := range wideModified {
		assert.Equal(t, domain.StatusModified, child(t, d, wideShardPath(i)).Status)
	}
}

// ---------------------------------------------------------------------------
// Cross-cutting invariants
// ---------------------------------------------------------------------------

// Every fixture layer must be internally consistent: its declared DiffID
// verifies (loadLayout would have failed otherwise), it has at least one
// entry, and its changeset digest is well formed. An empty changeset would
// silently drop a layer out of the edge computation.
func TestAllLayersAreWellFormed(t *testing.T) {
	t.Parallel()

	for _, pair := range Pairs() {
		for _, spec := range pair.Images {
			img := image(t, pair.Name, spec.Ref)
			for i, l := range img.Layers {
				assert.NoError(t, l.DiffID.Validate(), "%s layer %d", spec.Ref, i)
				assert.NoError(t, l.ChainID.Validate(), "%s layer %d", spec.Ref, i)
				assert.NoError(t, l.ChangesetDigest.Validate(), "%s layer %d", spec.Ref, i)
				assert.Positive(t, l.EntryCount, "%s layer %d is empty", spec.Ref, i)
				assert.Empty(t, img.Indexes[i].Warnings, "%s layer %d", spec.Ref, i)
			}
		}
	}
}

// Duplicate tar members are the one fixture mistake that would build cleanly
// and then quietly change what the property tests assert against, so the
// writer refuses them outright.
func TestDuplicateMembersAreRejected(t *testing.T) {
	t.Parallel()

	_, err := buildLayer(&LayerSpec{Entries: []EntrySpec{
		File("/a/b.txt", 10),
		File("/a/b.txt", 20),
	}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate tar member")
}

// A body is a pure function of its seed and length, which is what makes the
// whole tree reproducible and what makes "modified" in the UI mean something.
func TestFileBodiesAreSeededAndCompressible(t *testing.T) {
	t.Parallel()

	var a, b, c bytes.Buffer
	require.NoError(t, writeBody(&a, "/app/main.js", 128))
	require.NoError(t, writeBody(&b, "/app/main.js", 128))
	require.NoError(t, writeBody(&c, "/app/other.js", 128))

	assert.Equal(t, a.Bytes(), b.Bytes())
	assert.NotEqual(t, a.Bytes(), c.Bytes())
	assert.Len(t, a.Bytes(), 128)

	// A body shorter than its banner is truncated, not overrun.
	var short bytes.Buffer
	require.NoError(t, writeBody(&short, "/some/very/long/path/name.txt", 4))
	assert.Len(t, short.Bytes(), 4)
}
