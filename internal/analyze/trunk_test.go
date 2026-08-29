package analyze_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ericsuh/layerlens/internal/analyze"
	"github.com/ericsuh/layerlens/internal/analyze/tartest"
	"github.com/ericsuh/layerlens/internal/domain"
)

// stack builds an image's layer list from short DiffID nicknames, so a test
// reads as the layer stack it is about ("a, b, c" vs "a, b, x").
func stack(nicknames ...string) []domain.Layer {
	out := make([]domain.Layer, len(nicknames))
	for i, n := range nicknames {
		out[i] = domain.Layer{
			Index:           i,
			DiffID:          tartest.SHA256("diff:" + n),
			ChangesetDigest: tartest.SHA256("changeset:" + n),
			EntryCount:      1,
		}
	}
	return out
}

func TestTrunkLCP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a, b []domain.Layer
		want int
	}{
		{
			name: "normal_fork",
			a:    stack("base", "deps", "appA", "cmdA"),
			b:    stack("base", "deps", "appB"),
			want: 2,
		},
		{
			name: "zero_shared_layers",
			a:    stack("alpine", "appA"),
			b:    stack("debian", "appB"),
			want: 0,
		},
		{
			name: "strict_prefix_left_shorter",
			a:    stack("base", "deps"),
			b:    stack("base", "deps", "app"),
			want: 2,
		},
		{
			name: "strict_prefix_right_shorter",
			a:    stack("base", "deps", "app"),
			b:    stack("base", "deps"),
			want: 2,
		},
		{
			name: "identical_images",
			a:    stack("base", "deps", "app"),
			b:    stack("base", "deps", "app"),
			want: 3,
		},
		{
			name: "single_layer_images_equal",
			a:    stack("only"),
			b:    stack("only"),
			want: 1,
		},
		{
			name: "single_layer_images_different",
			a:    stack("one"),
			b:    stack("two"),
			want: 0,
		},
		{
			name: "empty_left",
			a:    nil,
			b:    stack("base"),
			want: 0,
		},
		{
			name: "both_empty",
			want: 0,
		},
		{
			name: "divergence_in_the_middle_stops_the_trunk",
			// A later coincidental match must NOT extend the trunk:
			// the trunk is a prefix, not a set intersection.
			a:    stack("base", "x", "shared-tail"),
			b:    stack("base", "y", "shared-tail"),
			want: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, analyze.TrunkLCP(tc.a, tc.b))
			assert.Equal(t, tc.want, analyze.TrunkLCP(tc.b, tc.a), "the trunk is symmetric")
		})
	}
}

func TestTrunkLCPIgnoresNonDiffIDIdentities(t *testing.T) {
	t.Parallel()

	// Same compressed blob digests and same changeset digests, different
	// DiffIDs: not a shared trunk. Recompressing a layer changes the blob
	// digest without changing what the layer store caches, and equal
	// changeset digests are only "could have been the same layer".
	a := stack("left")
	b := stack("right")
	a[0].CompressedDigest = tartest.SHA256("same blob")
	b[0].CompressedDigest = tartest.SHA256("same blob")
	b[0].ChangesetDigest = a[0].ChangesetDigest

	assert.Equal(t, 0, analyze.TrunkLCP(a, b))
}

func TestTrunkLCPMalformedDiffIDNeverMatches(t *testing.T) {
	t.Parallel()

	// A half-populated record must not fabricate sharing.
	a := stack("base", "app")
	b := stack("base", "app")
	a[0].DiffID = ""
	b[0].DiffID = ""

	assert.Equal(t, 0, analyze.TrunkLCP(a, b))
}
