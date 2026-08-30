package safehttp

import (
	"errors"
	"io"
	"testing"
	"time"
)

// eofReader yields its payload once and then reports a textbook clean EOF —
// the tidy hang-up a well-behaved upstream produces when the watchdog cancels
// the request and the server's handler returns normally, closing a chunked
// body with a proper terminator.
type eofReader struct {
	payload []byte
	done    bool
}

func (r *eofReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	n := copy(p, r.payload)
	return n, io.EOF
}

func (r *eofReader) Close() error { return nil }

// Once the watchdog has tripped, the body has no legitimate end: the transfer
// was killed deliberately and whatever arrived is short. This is asserted at
// the guard rather than over a socket because which terminal outcome the
// transport surfaces — an error or a clean EOF — is a race decided by machine
// load, and only the EOF half is interesting. Excluding io.EOF here used to
// report a truncated body as a complete one, which for a manifest or config
// (neither of which is digest-verified downstream, unlike a layer blob) means
// silently parsing a short response.
func TestStallGuardReportsTruncationOnACleanEOF(t *testing.T) {
	t.Parallel()

	// A window long enough that the watchdog cannot fire on its own: the trip
	// below is set explicitly, so the assertion is about the Read path only
	// and carries no timing dependence at all.
	guard := newStallGuard(&eofReader{payload: []byte("partial")}, func() {}, time.Hour, 1<<20)
	t.Cleanup(func() { _ = guard.Close() })
	guard.tripped.Store(true)

	n, err := guard.Read(make([]byte, 64))
	if n != len("partial") {
		t.Fatalf("read %d bytes, want %d", n, len("partial"))
	}
	if !errors.Is(err, ErrStalled) {
		t.Fatalf("a clean EOF after a trip must surface as ErrStalled, got %v", err)
	}
}

// The mirror image: an untripped guard must pass a genuine EOF straight
// through, or every successful transfer would end in a spurious error.
func TestStallGuardPassesACleanEOFWhenUntripped(t *testing.T) {
	t.Parallel()

	guard := newStallGuard(&eofReader{payload: []byte("whole")}, func() {}, time.Hour, 1<<20)
	t.Cleanup(func() { _ = guard.Close() })

	_, err := guard.Read(make([]byte, 64))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("an untripped guard must report io.EOF unchanged, got %v", err)
	}
}
