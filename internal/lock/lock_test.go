package lock_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/berkaycubuk/fabrika/internal/lock"
)

func TestAcquireRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")

	l, err := lock.Acquire(path)
	if err != nil {
		t.Fatalf("first Acquire failed: %v", err)
	}

	// Second Acquire on the same path must fail.
	_, err = lock.Acquire(path)
	if err == nil {
		t.Fatal("second Acquire should have failed while lock is held")
	}
	if !strings.Contains(err.Error(), "another fabrika instance") {
		t.Fatalf("unexpected error message: %v", err)
	}

	// After Release the path must be acquirable again.
	if err := l.Release(); err != nil {
		t.Fatalf("Release failed: %v", err)
	}

	l2, err := lock.Acquire(path)
	if err != nil {
		t.Fatalf("Acquire after Release failed: %v", err)
	}
	if err := l2.Release(); err != nil {
		t.Fatalf("second Release failed: %v", err)
	}
}
