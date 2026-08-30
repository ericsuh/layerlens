package imgref_test

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/imgref"
)

// vectorFile is shared with the SPA's client-side mirror
// (web/src/lib/refcheck.test.ts). Both implementations are driven by the same
// table so the inline "✓ allowed" verdict the user sees while typing cannot
// drift from the verdict the server will actually give.
const vectorFile = "../../testdata/refs.json"

type vectors struct {
	Accept []struct {
		Input      string `json:"input"`
		Registry   string `json:"registry"`
		Repository string `json:"repository"`
		Tag        string `json:"tag"`
		Digest     string `json:"digest"`
	} `json:"accept"`
	Reject []struct {
		Input    string `json:"input"`
		Reason   string `json:"reason"`
		Registry string `json:"registry"`
	} `json:"reject"`
}

func loadVectors(t *testing.T) vectors {
	t.Helper()
	raw, err := os.ReadFile(vectorFile)
	require.NoError(t, err)
	var v vectors
	require.NoError(t, json.Unmarshal(raw, &v))
	require.NotEmpty(t, v.Accept)
	require.NotEmpty(t, v.Reject)
	return v
}

func TestParseSharedVectors(t *testing.T) {
	v := loadVectors(t)
	list := imgref.Default()

	for _, tc := range v.Accept {
		t.Run("accept/"+tc.Input, func(t *testing.T) {
			got, err := list.Parse(tc.Input)
			require.NoError(t, err)
			assert.Equal(t, tc.Registry, got.Registry)
			assert.Equal(t, tc.Repository, got.Repository)
			assert.Equal(t, tc.Tag, got.Tag)
			assert.Equal(t, tc.Digest, string(got.Digest))
		})
	}

	for _, tc := range v.Reject {
		t.Run("reject/"+tc.Input, func(t *testing.T) {
			_, err := list.Parse(tc.Input)
			require.Error(t, err)
			var notAllowed *imgref.ErrRegistryNotAllowed
			switch tc.Reason {
			case "not_allowed":
				require.ErrorAs(t, err, &notAllowed)
				assert.Equal(t, tc.Registry, notAllowed.Registry)
			case "invalid":
				assert.True(t, errors.Is(err, imgref.ErrInvalidReference),
					"expected ErrInvalidReference, got %v", err)
			default:
				t.Fatalf("unknown reject reason %q", tc.Reason)
			}
		})
	}
}

// The substring attacks get their own named test as well as living in the
// shared vectors: they are the reason the matcher works on labels, and a
// regression here is a live SSRF, not a cosmetic bug.
func TestAllowsLabelBoundaries(t *testing.T) {
	list := imgref.Default()
	allowed := []string{
		"ghcr.io", "gcr.io", "us.gcr.io", "eu.gcr.io", "asia.gcr.io",
		"us-docker.pkg.dev", "europe-west1-docker.pkg.dev",
		"public.ecr.aws", "123456789012.dkr.ecr.eu-west-1.amazonaws.com",
		"myreg.azurecr.io", "docker.io", "index.docker.io", "registry-1.docker.io",
		// A trailing dot is the same name to DNS.
		"ghcr.io.",
		"GHCR.IO",
	}
	for _, host := range allowed {
		assert.True(t, list.Allows(host), "expected %q to be allowed", host)
	}

	refused := []string{
		"evilgcr.io", "xgcr.io", "gcr.io.evil.com", "notghcr.io",
		"ghcr.io.evil.com", "x.azurecr.io.evil.com", "azurecr.io",
		"myazurecr.io", "pkg.dev", "notpkg.dev", "xpkg.dev",
		"amazonaws.com", "dkr.ecr.us-east-1.amazonaws.com",
		"123.dkr.ecr.us-east-1.amazonaws.com.evil.net",
		"docker.io.evil.com", "evil.com", "localhost", "127.0.0.1",
		"", ".", "..", "ghcr..io", ".ghcr.io",
		// A wildcard must consume whole labels, never the empty string.
		"gcr.io..", "public.ecr.aws.evil.com",
		// One trailing root dot is a spelling of the same name; two is an
		// empty label, and normalization must not quietly eat both.
		"ghcr.io..", "ghcr.io...",
	}
	for _, host := range refused {
		assert.False(t, list.Allows(host), "expected %q to be refused", host)
	}
}

// A "." or ".." path segment is refused on the registry path, exactly as it
// already was on the Docker path. `ghcr.io/../../secret` otherwise produced a
// literal `GET /v2/../../secret/manifests/v1` and a token scope of
// `repository:../../secret:pull` — an arbitrary anonymous GET path on a
// registry the operator vetted for something else.
func TestParseRefusesTraversalSegments(t *testing.T) {
	list := imgref.Default()
	for _, raw := range []string{
		"ghcr.io/../../secret",
		"ghcr.io/./secret",
		"ghcr.io/org/../secret",
		"ghcr.io//secret",
		"ghcr.io/org/..",
		"../../secret",
		"./alpine",
	} {
		t.Run(raw, func(t *testing.T) {
			_, err := list.Parse(raw)
			require.Error(t, err)
			assert.ErrorIs(t, err, imgref.ErrInvalidReference)
		})
	}
	// The rule must not cost a legitimate reference whose path merely
	// contains dots.
	for _, raw := range []string{"ghcr.io/org/my.img:v1", "ghcr.io/org.name/img"} {
		_, err := list.Parse(raw)
		assert.NoError(t, err, raw)
	}
}

// A single trailing root dot is the same name to DNS; a second one is an empty
// label. Parse used to trim one and Allows a second, so "ghcr.io.." was
// accepted and then carried as the registry "ghcr.io." — a second idempotency
// key for one image, and a non-canonical TLS ServerName.
func TestParseNormalizesTrailingDotsExactlyOnce(t *testing.T) {
	list := imgref.Default()

	ref, err := list.Parse("ghcr.io./o/i")
	require.NoError(t, err)
	assert.Equal(t, "ghcr.io", ref.Registry)
	assert.Equal(t, "ghcr.io/o/i:latest", imgref.Canonical(ref))

	for _, raw := range []string{"ghcr.io../o/i", "ghcr.io.../o/i", "ghcr..io/o/i", ".ghcr.io/o/i"} {
		t.Run(raw, func(t *testing.T) {
			_, err := list.Parse(raw)
			require.Error(t, err, "expected %q to be refused", raw)
		})
	}
}

func TestCanonical(t *testing.T) {
	list := imgref.Default()
	for _, tc := range []struct{ in, want string }{
		{"alpine:3.20", "index.docker.io/library/alpine:3.20"},
		{"alpine", "index.docker.io/library/alpine:latest"},
		{"ghcr.io/org/img:tag", "ghcr.io/org/img:tag"},
		{
			"ghcr.io/org/img@sha256:1111111111111111111111111111111111111111111111111111111111111111",
			"ghcr.io/org/img@sha256:1111111111111111111111111111111111111111111111111111111111111111",
		},
	} {
		ref, err := list.Parse(tc.in)
		require.NoError(t, err)
		assert.Equal(t, tc.want, imgref.Canonical(ref))
	}
}

func TestPatternsAreReportedForDisplay(t *testing.T) {
	list := imgref.Default()
	assert.Equal(t, imgref.DefaultPatterns, list.Patterns())
	// The copy must be defensive: the API hands this slice to a JSON
	// encoder and the UI renders it.
	got := list.Patterns()
	got[0] = "mutated"
	assert.Equal(t, imgref.DefaultPatterns, list.Patterns())
}

func TestNarrowerAllowlist(t *testing.T) {
	list := imgref.NewAllowlist([]string{"ghcr.io"})
	_, err := list.Parse("ghcr.io/org/img")
	require.NoError(t, err)
	_, err = list.Parse("alpine:3.20")
	var notAllowed *imgref.ErrRegistryNotAllowed
	require.ErrorAs(t, err, &notAllowed)
	assert.Equal(t, "index.docker.io", notAllowed.Registry)
}
