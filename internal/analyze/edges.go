package analyze

import "github.com/ericsuh/layerlens/internal/domain"

// CouldBeSharedEdge links one post-fork layer of the left image to one of the
// right image whose changeset is equivalent under Docker's build-cache rule
// (equal normalized changeset digests, §3.1).
//
// This is emphatically NOT a cache hit. The two layers have different DiffIDs
// (or they would be on the trunk), so Docker's layer store holds them as
// separate blobs and pulls both. What the edge says is narrower and more
// useful: these layers *could* have been one layer — the only differences
// between them are ones Docker's own build cache ignores, in practice almost
// always mtimes. The API field is `couldBeShared` and the word "shared" is
// reserved for the trunk.
type CouldBeSharedEdge struct {
	// LeftIndex and RightIndex are absolute layer indexes within their
	// images (positions in rootfs.diff_ids), not branch offsets.
	LeftIndex  int `json:"leftIndex"`
	RightIndex int `json:"rightIndex"`
	// DiffIDEqual distinguishes "byte-identical layer tar at a different
	// position in the stack" (true) from "same content, different tar bytes"
	// (false). Neither is a cache hit.
	DiffIDEqual bool `json:"diffIdEqual"`
	// ChangesetDigest is the digest both layers share, for diagnostics.
	ChangesetDigest domain.Digest `json:"changesetDigest,omitempty"`
}

// CouldBeSharedEdges computes the dotted edges between the two images' branch
// layers, i.e. the layers at index k and beyond, where k is the trunk length
// from TrunkLCP (ARCHITECTURE §4.5).
//
// Trunk layers are excluded because they are already genuinely shared; drawing
// a "could be shared" edge over them would be noise at best and a lie at
// worst. Layers with an empty changeset are excluded too: every pair of no-op
// layers has the same digest, so including them would connect unrelated
// metadata-only instructions to each other.
//
// All matching pairs are emitted (m×n when a digest genuinely repeats within
// an image), ordered by right index and then left index so the payload is
// deterministic. O(len(a) + len(b)) via a hash multimap.
func CouldBeSharedEdges(a, b []domain.Layer, k int) []CouldBeSharedEdge {
	if k < 0 {
		k = 0
	}
	if k >= len(a) || k >= len(b) {
		// One branch is empty: nothing to connect.
		return nil
	}

	byDigest := make(map[domain.Digest][]int)
	for i := k; i < len(a); i++ {
		if !eligible(&a[i]) {
			continue
		}
		byDigest[a[i].ChangesetDigest] = append(byDigest[a[i].ChangesetDigest], i)
	}
	if len(byDigest) == 0 {
		return nil
	}

	var edges []CouldBeSharedEdge
	for j := k; j < len(b); j++ {
		if !eligible(&b[j]) {
			continue
		}
		for _, i := range byDigest[b[j].ChangesetDigest] {
			edges = append(edges, CouldBeSharedEdge{
				LeftIndex:       i,
				RightIndex:      j,
				DiffIDEqual:     a[i].DiffID.IsValid() && a[i].DiffID == b[j].DiffID,
				ChangesetDigest: b[j].ChangesetDigest,
			})
		}
	}
	return edges
}

// eligible reports whether a layer may participate in an edge: it must have a
// well-formed changeset digest and at least one changeset entry.
func eligible(l *domain.Layer) bool {
	return l.EntryCount > 0 && l.ChangesetDigest.IsValid()
}
