// Package lock provides an advisory single-instance file lock backed by
// syscall.Flock (available on both darwin and linux).
package lock

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
)

// Lock holds an acquired advisory file lock.
type Lock struct {
	f *os.File
}

// Acquire opens (creating if needed) the file at lockPath and takes an
// exclusive non-blocking flock on it. If another process already holds the
// lock, a human-readable error is returned and the fd is closed.
// Each call opens its own fd, so two Acquire calls on the same path within one
// process are also refused.
func Acquire(lockPath string) (*Lock, error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("lock %s: %w", lockPath, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another fabrika instance is already running for this project (%s)", lockPath)
	}
	// Write pid for operator debugging; errors are non-fatal.
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	return &Lock{f: f}, nil
}

// Release unlocks and closes the file. Safe to call exactly once; the path
// becomes acquirable again immediately after.
func (l *Lock) Release() error {
	if err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN); err != nil {
		l.f.Close()
		return err
	}
	return l.f.Close()
}
