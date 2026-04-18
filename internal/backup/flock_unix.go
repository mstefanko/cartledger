//go:build !windows

package backup

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// fileLock wraps an *os.File holding a syscall.Flock() advisory lock. Release
// must be called exactly once — it unlocks, closes, and removes the lock file.
type fileLock struct {
	f    *os.File
	path string
}

// acquireFileLock attempts a non-blocking exclusive flock on path. Returns
// nil (no lock) + errLockBusy if the lock is held by another process, or
// a wrapped non-nil error for any other failure.
//
// The file is created with O_CREATE | O_RDWR — we never truncate, so multiple
// racing openers see a stable fd to flock. Content is irrelevant; we use the
// descriptor itself as the lock.
func acquireFileLock(path string) (*fileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lockfile: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errLockBusy
		}
		return nil, fmt.Errorf("flock: %w", err)
	}
	return &fileLock{f: f, path: path}, nil
}

// Release unlocks, closes, and removes the lock file. Best-effort: errors
// unlocking or removing are ignored (the OS releases the flock on close, and
// a leftover lockfile is harmless — the next acquire will re-flock it).
func (l *fileLock) Release() {
	if l == nil || l.f == nil {
		return
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	_ = l.f.Close()
	// Leave the file on disk so the next acquire can flock it immediately
	// without a mkdir race. Removing it adds nothing but a race window.
}
