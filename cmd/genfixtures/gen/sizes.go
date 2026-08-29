package gen

import (
	"fmt"
	"hash/fnv"
	"io"
)

// Byte-size helpers. Fixture sizes are chosen to give the UI realistic
// *proportions* — a fat node binary, a node_modules that dominates the app,
// a .git pack that dwarfs the source it sits next to — at roughly a twentieth
// of a real image's absolute scale (ARCHITECTURE §10.8 explicitly accepts toy
// sizes). Bodies are mostly NUL padding, so these numbers cost the repository
// almost nothing.
const (
	kib int64 = 1 << 10
	mib int64 = 1 << 20
)

// spread returns a stable size in [lo, hi] derived from seed.
//
// It exists so that a group of thirty synthetic files can have varied sizes
// without a random number generator anywhere near the generator: the value is
// a pure function of the seed string, so it survives refactors, reorderings
// and Go releases.
func spread(seed string, lo, hi int64) int64 {
	if hi <= lo {
		return lo
	}
	h := fnv.New64a()
	_, _ = io.WriteString(h, seed)
	return lo + int64(h.Sum64()%uint64(hi-lo+1)) //nolint:gosec // range is small and positive
}

// numbered formats a zero-padded index for generated path families, so that
// lexical order and numeric order agree in the UI.
func numbered(format string, i int) string { return fmt.Sprintf(format, i) }
