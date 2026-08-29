package analyze_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/analyze"
	"github.com/ericsuh/layerlens/internal/domain"
)

func rep(c byte) domain.Digest {
	b := make([]byte, 64)
	for i := range b {
		b[i] = c
	}
	return domain.MustDigest(domain.DigestPrefix + string(b))
}

func TestChainIDs(t *testing.T) {
	t.Parallel()

	diffIDs := []domain.Digest{rep('a'), rep('b'), rep('c')}

	// Known answers, computed independently of this package:
	//   ChainID(L0)    = DiffID(L0)
	//   ChainID(L0..1) = sha256("sha256:aaa… sha256:bbb…")
	//   ChainID(L0..2) = sha256("<ChainID(L0..1)> sha256:ccc…")
	want := []domain.Digest{
		rep('a'),
		domain.MustDigest("sha256:ccd722928bd92476ba1745586fed6e45a102504185ad88cd89e01ff116fd146c"),
		domain.MustDigest("sha256:c1377126441fb2f5ec2c21ae2a60255331d639e830f0ee1b40a36e52d4c40588"),
	}

	got, err := analyze.ChainIDs(diffIDs)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestChainIDsEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		got, err := analyze.ChainIDs(nil)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("single_layer_is_its_diff_id", func(t *testing.T) {
		t.Parallel()
		got, err := analyze.ChainIDs([]domain.Digest{rep('d')})
		require.NoError(t, err)
		assert.Equal(t, []domain.Digest{rep('d')}, got)
	})

	t.Run("invalid_digest_rejected", func(t *testing.T) {
		t.Parallel()
		_, err := analyze.ChainIDs([]domain.Digest{rep('a'), "sha256:nothex"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "diff_ids[1]")
	})
}

// The trunk is computed as the longest common prefix of the diff_ids arrays,
// which is only sound because ChainID is a fold over that prefix: equal DiffID
// prefixes must imply equal ChainID prefixes and nothing beyond.
func TestChainIDPrefixEquivalence(t *testing.T) {
	t.Parallel()

	left, err := analyze.ChainIDs([]domain.Digest{rep('a'), rep('b'), rep('c')})
	require.NoError(t, err)
	right, err := analyze.ChainIDs([]domain.Digest{rep('a'), rep('b'), rep('d')})
	require.NoError(t, err)

	assert.Equal(t, left[:2], right[:2], "shared DiffID prefix ⇒ shared ChainID prefix")
	assert.NotEqual(t, left[2], right[2], "diverging DiffID ⇒ diverging ChainID")

	// The same DiffID at a different depth is NOT shared storage: that is
	// exactly the "could-be-shared" case, not a trunk case.
	shifted, err := analyze.ChainIDs([]domain.Digest{rep('9'), rep('a'), rep('b')})
	require.NoError(t, err)
	assert.NotEqual(t, left[1], shifted[2])
}
