package git

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// ClearStaleLocks removes leftover git index.lock files that wedge all future
// git operations in the repo. A kill during a commit/merge can strand a lock;
// the engine's single-instance flock guarantees no other fabrika is mid-operation
// at boot, so any lock older than maxAge is necessarily stale and safe to clear.
//
// It scans <root>/.git/index.lock and <root>/.git/worktrees/*/index.lock (linked
// worktree gitdirs), removing only those whose modtime is older than maxAge so a
// freshly-created lock is preserved. It returns the absolute paths actually
// removed. A missing .git dir, a .git that is a file (linked-worktree checkout),
// or no matching locks is a no-op returning an empty slice and nil error. An
// error removing one file is collected but does not stop the others.
func ClearStaleLocks(root string, maxAge time.Duration) ([]string, error) {
	gitDir := filepath.Join(root, ".git")
	info, err := os.Stat(gitDir)
	if err != nil || !info.IsDir() {
		// Missing .git, or .git is a file (linked-worktree checkout): no-op.
		return nil, nil
	}

	candidates := []string{filepath.Join(gitDir, "index.lock")}

	worktreesDir := filepath.Join(gitDir, "worktrees")
	if entries, err := os.ReadDir(worktreesDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				candidates = append(candidates, filepath.Join(worktreesDir, e.Name(), "index.lock"))
			}
		}
	}

	cutoff := time.Now().Add(-maxAge)
	var removed []string
	var errs []error
	for _, path := range candidates {
		fi, err := os.Stat(path)
		if err != nil {
			continue // not present (or unreadable): skip
		}
		if fi.ModTime().After(cutoff) {
			continue // modified within maxAge: preserve
		}
		abs, aerr := filepath.Abs(path)
		if aerr != nil {
			abs = path
		}
		if rerr := os.Remove(path); rerr != nil {
			errs = append(errs, rerr)
			continue
		}
		removed = append(removed, abs)
	}

	return removed, errors.Join(errs...)
}
