//go:build unix

package filelock

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// ErrAlreadyLocked is returned by Acquire when another process (or open file
// description) currently holds the lock.
var ErrAlreadyLocked = errors.New("filelock: already locked")

type Lock struct {
	f *os.File
}

// Acquire opens path (creating it if needed) and takes a non-blocking
// exclusive flock. If the lock is held elsewhere, it returns ErrAlreadyLocked.
func Acquire(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock %q: %w", path, err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrAlreadyLocked
		}
		return nil, fmt.Errorf("flock %q: %w", path, err)
	}
	return &Lock{f: f}, nil
}

// Close releases the lock and closes the underlying file.
func (l *Lock) Close() error {
	unlockErr := unix.Flock(int(l.f.Fd()), unix.LOCK_UN)
	closeErr := l.f.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
