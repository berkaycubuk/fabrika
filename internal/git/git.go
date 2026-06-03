// Package git provides thin wrappers over the system `git` CLI for the
// operations Fabrika needs: isolating each task on its own worktree/branch,
// reading the branch diff (the "PR"), and merging accepted work back. Shelling
// out keeps full worktree support and matches how agents operate in the repo.
//
// This is plumbing for the deferred live loop: the functions are usable and
// tested, but no scheduler invokes them yet. See SPECS.md §7, §9.
package git

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Repo is a handle to a git repository at Root.
type Repo struct {
	Root string
}

// Open returns a Repo handle. It verifies Root is inside a git work tree.
func Open(ctx context.Context, root string) (*Repo, error) {
	r := &Repo{Root: root}
	out, err := r.run(ctx, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return nil, fmt.Errorf("not a git repo at %s: %w", root, err)
	}
	if strings.TrimSpace(out) != "true" {
		return nil, fmt.Errorf("%s is not inside a git work tree", root)
	}
	return r, nil
}

// CurrentBranch returns the checked-out branch name.
func (r *Repo) CurrentBranch(ctx context.Context) (string, error) {
	out, err := r.run(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	return strings.TrimSpace(out), err
}

// AddWorktree creates a new worktree at path on a fresh branch off base.
func (r *Repo) AddWorktree(ctx context.Context, path, branch, base string) error {
	_, err := r.run(ctx, "worktree", "add", "-b", branch, path, base)
	return err
}

// RemoveWorktree removes a worktree (force, since it may contain agent output).
func (r *Repo) RemoveWorktree(ctx context.Context, path string) error {
	_, err := r.run(ctx, "worktree", "remove", "--force", path)
	return err
}

// DeleteBranch force-deletes a branch. Used to clear a stale branch before
// re-creating a worktree for a retried/resumed task.
func (r *Repo) DeleteBranch(ctx context.Context, branch string) error {
	_, err := r.run(ctx, "branch", "-D", branch)
	return err
}

// Diff returns the unified diff of branch relative to base (base...branch).
func (r *Repo) Diff(ctx context.Context, base, branch string) (string, error) {
	return r.run(ctx, "diff", base+"..."+branch)
}

// ChangedFiles lists files changed on branch relative to base.
func (r *Repo) ChangedFiles(ctx context.Context, base, branch string) ([]string, error) {
	out, err := r.run(ctx, "diff", "--name-only", base+"..."+branch)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			files = append(files, s)
		}
	}
	return files, nil
}

// Merge merges branch into base. On conflict it returns an error; the caller
// escalates that as a Decision (SPECS §9).
func (r *Repo) Merge(ctx context.Context, base, branch string) error {
	if _, err := r.run(ctx, "checkout", base); err != nil {
		return err
	}
	if _, err := r.run(ctx, "merge", "--no-ff", branch); err != nil {
		// Leave the conflicted state for the caller to inspect/abort.
		return fmt.Errorf("merge %s into %s: %w", branch, base, err)
	}
	return nil
}

// AddAllAndCommit stages everything in a worktree and commits it on the
// worktree's branch. It reports whether a commit was actually made (false when
// the tree was already clean) so callers can tell apart "agent did nothing".
// This guards the loop against agents that leave changes uncommitted.
func (r *Repo) AddAllAndCommit(ctx context.Context, worktreeDir, msg string) (bool, error) {
	if _, err := runIn(ctx, worktreeDir, "add", "-A"); err != nil {
		return false, err
	}
	// `diff --cached --quiet` exits non-zero when there are staged changes.
	if _, err := runIn(ctx, worktreeDir, "diff", "--cached", "--quiet"); err == nil {
		return false, nil // nothing staged -> clean tree
	}
	if _, err := runIn(ctx, worktreeDir, "commit", "-m", msg); err != nil {
		return false, err
	}
	return true, nil
}

// run executes a git command in the repo root and returns combined stdout.
func (r *Repo) run(ctx context.Context, args ...string) (string, error) {
	return runIn(ctx, r.Root, args...)
}

// runIn executes a git command in dir and returns combined stdout.
func runIn(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("git %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
