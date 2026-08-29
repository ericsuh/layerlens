package analyze

import "github.com/ericsuh/layerlens/internal/domain"

// TrunkLCP returns the length k of the shared trunk: the longest common prefix
// of the two images' rootfs.diff_ids (ARCHITECTURE §4.5, DECISIONS A4).
//
// DiffIDs — never compressed manifest digests. The same layer content
// recompressed produces a different blob digest but the same DiffID, and it is
// the DiffID chain the local layer store keys on, so this is the only
// comparison that answers "do these two images actually share cached layers?".
// Because ChainID is a fold over the DiffID prefix, an equal DiffID prefix is
// exactly an equal ChainID prefix.
//
// Every degenerate case is meaningful and none is an error:
//
//   - k == 0: the images share nothing, and both branches are whole images;
//   - k == len(a) or k == len(b): one image is a strict prefix of the other,
//     so that image's branch is empty (it is entirely trunk);
//   - k == len(a) == len(b): the images have identical layer stacks and both
//     branches are empty.
//
// A malformed (or empty) DiffID never matches, so a partially-populated record
// cannot fabricate sharing.
func TrunkLCP(a, b []domain.Layer) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if !a[i].DiffID.IsValid() || a[i].DiffID != b[i].DiffID {
			return i
		}
	}
	return n
}
