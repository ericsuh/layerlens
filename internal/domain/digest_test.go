package domain_test

import (
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/domain"
)

const validHex = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

func TestDigestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		ok    bool
	}{
		{name: "canonical", input: "sha256:" + validHex, ok: true},
		{name: "empty", input: ""},
		{name: "no_prefix", input: validHex},
		{name: "wrong_algorithm", input: "sha512:" + validHex},
		{name: "uppercase_hex", input: "sha256:" + strings.ToUpper(validHex)},
		{name: "too_short", input: "sha256:" + validHex[:63]},
		{name: "too_long", input: "sha256:" + validHex + "a"},
		{name: "non_hex", input: "sha256:" + strings.Repeat("z", 64)},
		// A digest becomes a cache path component, so anything that
		// could escape a directory must fail before filepath.Join
		// (ARCHITECTURE §7.3).
		{name: "traversal_shaped", input: "sha256:../../../etc/passwd" + strings.Repeat("a", 41)},
		{name: "separator_inside", input: "sha256:" + validHex[:32] + "/" + validHex[33:]},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := domain.Digest(tc.input)
			assert.Equal(t, tc.ok, d.IsValid())

			parsed, err := domain.ParseDigest(tc.input)
			if !tc.ok {
				require.Error(t, err)
				assert.Empty(t, parsed)
				assert.Empty(t, d.Hex(), "a malformed digest must never yield a path component")
				assert.Empty(t, d.Short())
				return
			}
			require.NoError(t, err)
			assert.Equal(t, domain.Digest(tc.input), parsed)
			assert.Equal(t, validHex, d.Hex())
			assert.Equal(t, validHex[:12], d.Short())
		})
	}
}

func TestDigestFromHash(t *testing.T) {
	t.Parallel()

	h := sha256.New()
	_, err := h.Write([]byte("abc"))
	require.NoError(t, err)

	d := domain.DigestFromHash(h)
	require.NoError(t, d.Validate())
	assert.Equal(t, domain.MustDigest("sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"), d)

	sum := sha256.Sum256([]byte("abc"))
	assert.Equal(t, d, domain.DigestFromBytes(sum[:]))
}

func TestMustDigestPanics(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { domain.MustDigest("not-a-digest") })
	assert.NotPanics(t, func() { domain.MustDigest("sha256:" + validHex) })
}
