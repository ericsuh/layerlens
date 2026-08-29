//go:build race

package analyze_test

// raceEnabled reports whether the race detector is active. The detector adds
// substantial per-allocation bookkeeping, so the streaming allocation budget
// in TestIndexLayerStreamsWithoutBuffering cannot hold under it. Tests that
// measure allocations skip themselves rather than fail a plain
// `go test -race ./...`.
const raceEnabled = true
