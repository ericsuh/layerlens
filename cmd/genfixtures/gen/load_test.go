package gen

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/analyze"
	"github.com/ericsuh/layerlens/internal/domain"
)

// The fixtures are only worth anything if the *real* analyzer agrees with what
// the generator believes it built. Everything below therefore goes through the
// phase 002/003 code — analyze.IndexLayer, ChainIDs, ApplyHistory, Squash,
// Diff, TrunkLCP, CouldBeSharedEdges — and never re-implements a rule it is
// checking. The only bespoke code here is the OCI-layout reader, which is
// phase 006's job and does not exist yet.

// loadedImage is one fixture image after the analyzer has seen it.
type loadedImage struct {
	Ref            string
	ID             domain.Digest
	ManifestDigest domain.Digest
	Config         configDoc
	// Layers are the domain records the server would serve.
	Layers []domain.Layer
	// Indexes are the per-layer changesets, in rootfs order.
	Indexes []domain.LayerIndex
}

// squash folds this image's layers 0..n-1 into a cumulative tree.
func (img *loadedImage) squash(n int) *domain.Node {
	return analyze.Squash(img.Indexes[:n])
}

// loadLayout reads an OCI layout directory and streams every layer of every
// image through analyze.IndexLayer, verifying each blob against the DiffID its
// config declares.
func loadLayout(t *testing.T, dir string) map[string]*loadedImage {
	t.Helper()

	var idx indexDoc
	readJSON(t, filepath.Join(dir, "index.json"), &idx)

	var layout layoutDoc
	readJSON(t, filepath.Join(dir, "oci-layout"), &layout)
	require.Equal(t, ociLayoutVersion, layout.ImageLayoutVersion)

	out := make(map[string]*loadedImage, len(idx.Manifests))
	for _, md := range idx.Manifests {
		ref := md.Annotations[RefNameAnnotation]
		require.NotEmpty(t, ref, "manifest %s carries no ref-name annotation", md.Digest)

		var mfst manifestDoc
		readJSON(t, blobPath(t, dir, md.Digest), &mfst)

		var cfg configDoc
		readJSON(t, blobPath(t, dir, mfst.Config.Digest), &cfg)

		img := &loadedImage{
			Ref:            ref,
			ID:             mfst.Config.Digest,
			ManifestDigest: md.Digest,
			Config:         cfg,
		}
		require.Len(t, cfg.RootFS.DiffIDs, len(mfst.Layers),
			"%s: manifest layers and config diff_ids disagree", ref)

		diffIDs := make([]domain.Digest, 0, len(mfst.Layers))
		for i, ld := range mfst.Layers {
			f, err := os.Open(blobPath(t, dir, ld.Digest))
			require.NoError(t, err)
			index, err := analyze.IndexLayer(context.Background(), analyze.LayerSource{
				Reader:    f,
				MediaType: ld.MediaType,
				// Passing the declared DiffID makes IndexLayer
				// verify it: a generator that mislabelled a
				// layer fails here rather than silently.
				DiffID: cfg.RootFS.DiffIDs[i],
			})
			require.NoError(t, f.Close())
			require.NoError(t, err, "%s: layer %d (%s)", ref, i, ld.Digest)

			img.Indexes = append(img.Indexes, *index)
			diffIDs = append(diffIDs, index.DiffID)
			img.Layers = append(img.Layers, domain.Layer{
				Index:            i,
				DiffID:           index.DiffID,
				CompressedDigest: ld.Digest,
				CompressedSize:   ld.Size,
				ContentBytes:     index.ContentBytes,
				EntryCount:       len(index.Entries),
				ChangesetDigest:  index.ChangesetDigest,
			})
		}

		chains, err := analyze.ChainIDs(diffIDs)
		require.NoError(t, err)
		for i := range img.Layers {
			img.Layers[i].ChainID = chains[i]
		}
		analyze.ApplyHistory(img.Layers, historyOf(cfg))
		out[ref] = img
	}
	return out
}

// historyOf projects the config's history onto the domain mirror MapHistory
// consumes.
func historyOf(cfg configDoc) []domain.HistoryEntry {
	hs := make([]domain.HistoryEntry, 0, len(cfg.History))
	for _, h := range cfg.History {
		hs = append(hs, domain.HistoryEntry{
			CreatedBy:  h.CreatedBy,
			Comment:    h.Comment,
			EmptyLayer: h.EmptyLayer,
		})
	}
	return hs
}

func blobPath(t *testing.T, dir string, d domain.Digest) string {
	t.Helper()
	require.NoError(t, d.Validate())
	return filepath.Join(dir, "blobs", "sha256", d.Hex())
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test-local, generator-written path
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, v), "decoding %s", path)
}

// fixturesRoot is the committed fixtures/ tree, which is what the property
// tests read: they validate the artifact that ships, not a fresh build of it.
func fixturesRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "fixtures"))
	require.NoError(t, err)
	require.DirExists(t, root, "run `mise run genfixtures` to create the vendored fixtures")
	return root
}

// pairCache memoizes the (slow) indexing pass per pair so that eight property
// tests do not stream the same 150 MiB eight times.
var pairCache sync.Map

func loadPair(t *testing.T, name string) map[string]*loadedImage {
	t.Helper()
	if v, ok := pairCache.Load(name); ok {
		return v.(map[string]*loadedImage)
	}
	loaded := loadLayout(t, filepath.Join(fixturesRoot(t), name))
	pairCache.Store(name, loaded)
	return loaded
}

// image fetches one image of a pair by reference.
func image(t *testing.T, pair, ref string) *loadedImage {
	t.Helper()
	img, ok := loadPair(t, pair)[ref]
	require.True(t, ok, "pair %s has no image %s", pair, ref)
	return img
}

// child walks a diff tree by path, so assertions can name "/app/debug.log"
// instead of indexing into slices.
func child(t *testing.T, root *domain.DiffNode, path string) *domain.DiffNode {
	t.Helper()
	n := findChild(root, path)
	require.NotNil(t, n, "diff tree has no %s", path)
	return n
}

func findChild(root *domain.DiffNode, path string) *domain.DiffNode {
	cur := root
	for _, seg := range splitPath(path) {
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

// node walks a squashed tree by path.
func node(root *domain.Node, path string) *domain.Node {
	cur := root
	for _, seg := range splitPath(path) {
		if cur.Children == nil {
			return nil
		}
		next, ok := cur.Children[seg]
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}

func splitPath(p string) []string {
	var segs []string
	cur := ""
	for _, r := range p {
		if r == '/' {
			if cur != "" {
				segs = append(segs, cur)
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		segs = append(segs, cur)
	}
	return segs
}
