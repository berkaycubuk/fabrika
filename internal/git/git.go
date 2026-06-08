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
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// CoAuthorTrailer is the canonical co-author trailer line Fabrika appends to
// commits it creates on an agent's behalf. The casing matches GitHub's
// recognized "Co-authored-by:" trailer so the attribution renders in the UI.
const CoAuthorTrailer = "Co-authored-by: fabrika <noreply@fabrika-ai.com>"

// WithCoAuthor returns msg with the fabrika co-author trailer appended in a
// properly-formatted trailer block: a blank line separates the body from the
// trailers. It is idempotent — if the trailer is already present, msg is
// returned unchanged. This is co-author attribution only; it never alters the
// author or committer identity of the commit.
func WithCoAuthor(msg string) string {
	if strings.Contains(msg, CoAuthorTrailer) {
		return msg
	}
	trimmed := strings.TrimRight(msg, "\n")
	if trimmed == "" {
		return CoAuthorTrailer
	}
	return trimmed + "\n\n" + CoAuthorTrailer
}

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

// HasCommits reports whether HEAD resolves to a commit. A freshly `git init`'d
// repo with no commits still passes is-inside-work-tree, but has no HEAD — which
// breaks CurrentBranch and worktree creation. Callers preflight with this to
// give an actionable error instead of a raw "ambiguous argument 'HEAD'".
func (r *Repo) HasCommits(ctx context.Context) bool {
	_, err := r.run(ctx, "rev-parse", "--verify", "--quiet", "HEAD")
	return err == nil
}

// CurrentBranch returns the checked-out branch name.
func (r *Repo) CurrentBranch(ctx context.Context) (string, error) {
	out, err := r.run(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	return strings.TrimSpace(out), err
}

// RevParse resolves ref to its full commit SHA. Passing "HEAD" returns the
// current commit's SHA.
func (r *Repo) RevParse(ctx context.Context, ref string) (string, error) {
	out, err := r.run(ctx, "rev-parse", ref)
	return strings.TrimSpace(out), err
}

// AddWorktree creates a new worktree at path on a fresh branch off base.
func (r *Repo) AddWorktree(ctx context.Context, path, branch, base string) error {
	_, err := r.run(ctx, "worktree", "add", "-b", branch, path, base)
	return err
}

// AddWorktreeDetached creates a new worktree at path in detached HEAD state
// at ref. RemoveWorktree is the corresponding cleanup.
func (r *Repo) AddWorktreeDetached(ctx context.Context, path, ref string) error {
	_, err := r.run(ctx, "worktree", "add", "--detach", path, ref)
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

// RevList returns the commit SHAs in range rng (e.g. "prevsha..sha" or a bare
// SHA). It runs `git rev-list` in the repo root, one SHA per line. An empty
// rng returns an empty slice with no error.
func (r *Repo) RevList(ctx context.Context, rng string) ([]string, error) {
	if rng == "" {
		return nil, nil
	}
	out, err := r.run(ctx, "rev-list", rng)
	if err != nil {
		return nil, err
	}
	var shas []string
	for _, line := range strings.Split(out, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			shas = append(shas, s)
		}
	}
	return shas, nil
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

// Merge merges branch into base. On conflict it aborts the merge so the repo
// is never left in a conflicted half-merged state — the user's only recovery
// from that would be the git CLI, which the web UI must never require. The
// returned error carries git's conflict output; the caller surfaces it.
func (r *Repo) Merge(ctx context.Context, base, branch string) error {
	if _, err := r.run(ctx, "checkout", base); err != nil {
		return err
	}
	if _, err := r.run(ctx, "merge", "--no-ff", branch); err != nil {
		if _, aerr := r.run(ctx, "merge", "--abort"); aerr != nil {
			return fmt.Errorf("merge %s into %s: %w (and abort failed: %v)", branch, base, err, aerr)
		}
		return fmt.Errorf("merge %s into %s: %w", branch, base, err)
	}
	return nil
}

// Remotes lists the configured remote names (one per line from `git remote`).
func (r *Repo) Remotes(ctx context.Context) ([]string, error) {
	out, err := r.run(ctx, "remote")
	if err != nil {
		return nil, err
	}
	var remotes []string
	for _, line := range strings.Split(out, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			remotes = append(remotes, s)
		}
	}
	return remotes, nil
}

// Ahead reports how many commits branch carries that remote/branch does not —
// the work waiting to be pushed. It reads the local remote-tracking ref (the
// state as of the last fetch/push; no network). When that ref doesn't exist yet
// (branch never pushed), every commit on branch counts as ahead.
func (r *Repo) Ahead(ctx context.Context, remote, branch string) (int, error) {
	rng := remote + "/" + branch + ".." + branch
	if _, err := r.run(ctx, "rev-parse", "--verify", "--quiet", remote+"/"+branch); err != nil {
		rng = branch
	}
	out, err := r.run(ctx, "rev-list", "--count", rng)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// Pushed reports whether sha has already been pushed to remote/branch. It
// reads the local remote-tracking ref only — no network round-trip. If that
// ref does not exist (branch never pushed), it returns (false, nil) without
// error. A commit is considered pushed when it is an ancestor of the tip of
// the remote ref, which includes the tip itself.
func (r *Repo) Pushed(ctx context.Context, remote, branch, sha string) (bool, error) {
	remoteRef := remote + "/" + branch
	if _, err := r.run(ctx, "rev-parse", "--verify", "--quiet", remoteRef); err != nil {
		return false, nil
	}
	cmd := exec.CommandContext(ctx, "git", "merge-base", "--is-ancestor", sha, remoteRef)
	cmd.Dir = r.Root
	if _, _, err := runCmd(cmd); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("git merge-base --is-ancestor %s %s: %w", sha, remoteRef, err)
	}
	return true, nil
}

// Push pushes branch to remote, setting upstream tracking (-u). It pushes the
// ref by name, so the result is independent of which branch is checked out.
// git writes its human-readable summary ("To <url> ... main -> main" or
// "Everything up-to-date") to stderr, which this returns so callers can surface
// what happened. A rejected (non-fast-forward) push surfaces as an error.
func (r *Repo) Push(ctx context.Context, remote, branch string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "push", "-u", remote, branch)
	cmd.Dir = r.Root
	stdout, stderr, err := runCmd(cmd)
	if err != nil {
		return "", fmt.Errorf("git push %s %s: %w: %s",
			remote, branch, err, strings.TrimSpace(stderr))
	}
	if summary := strings.TrimSpace(stderr); summary != "" {
		return summary, nil
	}
	return strings.TrimSpace(stdout), nil
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
	if _, err := runIn(ctx, worktreeDir, "commit", "-m", WithCoAuthor(msg)); err != nil {
		return false, err
	}
	return true, nil
}

// NormalizeCommitTrailers rewrites every commit unique to branch (the range
// base..branch) so that each carries exactly one fabrika co-author trailer and
// no other co-author trailers. Any pre-existing "Co-authored-by:" trailers
// (matched case-insensitively, so "Co-Authored-By:" too) are stripped first,
// then the fabrika trailer (see [CoAuthorTrailer]) is appended in its own
// trailer block. Each commit's subject and body are otherwise preserved.
//
// Only the branch's own range is rewritten: commits reachable from base are
// never touched, and base itself is left unchanged. It is a no-op when the
// range is empty.
func (r *Repo) NormalizeCommitTrailers(ctx context.Context, base, branch string) error {
	rng := base + ".." + branch

	// Skip cleanly when the branch adds nothing on top of base — filter-branch
	// errors out ("Found nothing to rewrite") on an empty range otherwise.
	count, err := r.run(ctx, "rev-list", "--count", rng)
	if err != nil {
		return err
	}
	if strings.TrimSpace(count) == "0" {
		return nil
	}

	// awk message filter (portable across BSD/GNU awk): drop every
	// co-authored-by line regardless of casing, trim trailing blank lines, then
	// re-attach exactly one fabrika trailer in its own block. Mirrors the
	// formatting of [WithCoAuthor]. The program contains no single quotes, so it
	// embeds safely inside the single-quoted shell argument git eval's.
	const awkProg = `tolower($0) ~ /^co-authored-by:/ {next} {out[++n]=$0} ` +
		`END {while(n>0 && out[n] ~ /^[ \t]*$/)n--; for(i=1;i<=n;i++)print out[i]; if(n>0)print ""; print trailer}`
	msgFilter := `awk -v trailer="` + CoAuthorTrailer + `" '` + awkProg + `'`

	// --force lets repeated runs reuse the refs/original backup; the trailing
	// "base..branch" rev-range scopes the rewrite to (and updates only) branch.
	cmd := exec.CommandContext(ctx, "git",
		"filter-branch", "--force", "--msg-filter", msgFilter, "--", rng)
	cmd.Dir = r.Root
	cmd.Env = append(os.Environ(), "FILTER_BRANCH_SQUELCH_WARNING=1")
	if _, stderr, err := runCmd(cmd); err != nil {
		return fmt.Errorf("normalize trailers on %s: %w: %s",
			rng, err, strings.TrimSpace(stderr))
	}
	return nil
}

// run executes a git command in the repo root and returns combined stdout.
func (r *Repo) run(ctx context.Context, args ...string) (string, error) {
	return runIn(ctx, r.Root, args...)
}

// runIn executes a git command in dir and returns combined stdout.
func runIn(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	stdout, stderr, err := runCmd(cmd)
	if err != nil {
		return stdout, fmt.Errorf("git %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(stderr))
	}
	return stdout, nil
}

// runCmd runs an already-configured git command, capturing both output streams.
// It wires up the stdout/stderr buffers, runs the command, and returns the
// buffered output alongside the raw error from Run so callers can wrap it with
// command-specific context.
func runCmd(cmd *exec.Cmd) (stdout, stderr string, err error) {
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return outBuf.String(), errBuf.String(), err
}
