package analyze

import (
	"crypto/sha256"
	"fmt"

	"github.com/ericsuh/layerlens/internal/domain"
)

// ChainIDs computes the ChainID of every prefix of diffIDs (DECISIONS A4,
// OCI image-spec v1.1.1 config.md):
//
//	ChainID(L0)       = DiffID(L0)
//	ChainID(L0..Ln)   = sha256(ChainID(L0..Ln-1) + " " + DiffID(Ln))
//
// operating on the canonical "sha256:<hex>" string forms. A ChainID identifies
// a layer *in the context of everything below it*, which is exactly what the
// local layer store keys on — so equal ChainID prefixes are what "shares a
// layer cache" means. Because ChainID is a fold over the DiffID prefix, equal
// DiffID prefixes imply equal ChainID prefixes and vice versa, which is why
// the trunk can be computed as the longest common prefix of the diff_ids.
func ChainIDs(diffIDs []domain.Digest) ([]domain.Digest, error) {
	out := make([]domain.Digest, len(diffIDs))
	var prev domain.Digest
	for i, d := range diffIDs {
		if err := d.Validate(); err != nil {
			return nil, fmt.Errorf("diff_ids[%d]: %w", i, err)
		}
		if i == 0 {
			prev = d
		} else {
			sum := sha256.Sum256([]byte(string(prev) + " " + string(d)))
			prev = domain.DigestFromBytes(sum[:])
		}
		out[i] = prev
	}
	return out, nil
}
