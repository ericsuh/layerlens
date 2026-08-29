//go:build !windows

package cachestore

import (
	"fmt"
	"os"
	"syscall"
)

// fileLock is the exclusive advisory lock that enforces "exactly one server
// process per cache root" (ARCHITECTURE §1.3, §5).
//
// flock is per open-file-description, not per process, so a second Open of the
// same data directory fails even from inside the same process — which is what
// makes the guarantee testable without spawning a child.
type fileLock struct {
	f *os.File
}

// acquireLock takes LOCK_EX|LOCK_NB on path, creating it if needed. It never
// blocks: a data directory in use is a configuration error the operator has to
// see immediately, not something to wait out.
func acquireLock(path string) (*fileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, filePerm) //nolint:gosec // path is derived from --data-dir, not from a request
	if err != nil {
		return nil, fmt.Errorf("cachestore: open lock file %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf(
			"cachestore: data directory %s is locked by another layerlens process: %w",
			path, err)
	}
	return &fileLock{f: f}, nil
}

// Close releases the lock. The unlock is explicit rather than relying on the
// close: it keeps the release ordered before any error the close might report.
func (l *fileLock) Close() error {
	if l == nil || l.f == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	closeErr := l.f.Close()
	l.f = nil
	if unlockErr != nil {
		return fmt.Errorf("cachestore: release lock: %w", unlockErr)
	}
	return closeErr
}
