package ingest

import "testing"

// SetSaveBufferLimit lowers the docker-save metadata buffering threshold for
// the duration of a test, so the streaming path can be exercised against a
// small fixture instead of a nine-megabyte one. Restored on cleanup.
func SetSaveBufferLimit(t *testing.T, member int64) {
	t.Helper()
	previous := maxSmallMember
	maxSmallMember = member
	t.Cleanup(func() { maxSmallMember = previous })
}
