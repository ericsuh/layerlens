//go:build race

package server_test

// raceEnabled reports whether the race detector is active. The detector's
// per-allocation bookkeeping makes heap measurements meaningless, so the tests
// that measure them skip rather than fail a plain `go test -race ./...`.
const raceEnabled = true
