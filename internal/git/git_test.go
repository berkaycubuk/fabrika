package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initRepo creates a throwaway git repo with one commit and returns its root.
func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "init")
	return dir
}

func TestPushToBareRemote(t *testing.T) {
	dir := initRepo(t)
	ctx := context.Background()
	repo, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}

	// No remote yet -> Push should fail and Remotes should be empty.
	if rs, err := repo.Remotes(ctx); err != nil || len(rs) != 0 {
		t.Fatalf("expected no remotes, got %v, %v", rs, err)
	}

	// Create a bare repo to act as "origin" and wire it up.
	bare := filepath.Join(t.TempDir(), "origin.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v\n%s", err, out)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("remote", "add", "origin", bare)

	if rs, err := repo.Remotes(ctx); err != nil || len(rs) != 1 || rs[0] != "origin" {
		t.Fatalf("expected [origin], got %v, %v", rs, err)
	}

	// Never pushed: the whole branch (1 commit) counts as ahead.
	if n, err := repo.Ahead(ctx, "origin", "main"); err != nil || n != 1 {
		t.Fatalf("ahead before push = %d, %v; want 1", n, err)
	}

	if _, err := repo.Push(ctx, "origin", "main"); err != nil {
		t.Fatalf("push: %v", err)
	}

	// Pushed: nothing left to ship.
	if n, err := repo.Ahead(ctx, "origin", "main"); err != nil || n != 0 {
		t.Fatalf("ahead after push = %d, %v; want 0", n, err)
	}

	// The bare remote now has the main branch at our HEAD.
	got, err := exec.Command("git", "--git-dir", bare, "rev-parse", "main").Output()
	if err != nil {
		t.Fatalf("rev-parse on remote: %v", err)
	}
	want, err := repo.run(ctx, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != strings.TrimSpace(want) {
		t.Fatalf("remote main = %s, want %s", got, want)
	}

	// A second push with no new commits succeeds (up-to-date).
	if _, err := repo.Push(ctx, "origin", "main"); err != nil {
		t.Fatalf("idempotent push: %v", err)
	}

	// A new local commit reads as ahead again; pushing clears it.
	if err := os.WriteFile(filepath.Join(dir, "more.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "-m", "more")
	if n, err := repo.Ahead(ctx, "origin", "main"); err != nil || n != 1 {
		t.Fatalf("ahead after new commit = %d, %v; want 1", n, err)
	}
	if _, err := repo.Push(ctx, "origin", "main"); err != nil {
		t.Fatalf("push new commit: %v", err)
	}
	if n, err := repo.Ahead(ctx, "origin", "main"); err != nil || n != 0 {
		t.Fatalf("ahead after second push = %d, %v; want 0", n, err)
	}
}

func TestWorktreeAndDiff(t *testing.T) {
	ctx := context.Background()
	root := initRepo(t)

	r, err := Open(ctx, root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if b, _ := r.CurrentBranch(ctx); b != "main" {
		t.Fatalf("branch = %q, want main", b)
	}

	wt := filepath.Join(t.TempDir(), "wt")
	if err := r.AddWorktree(ctx, wt, "task/feature", "main"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}

	// Make a change on the branch via a commit in the worktree.
	if err := os.WriteFile(filepath.Join(wt, "feature.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commit := exec.Command("git", "commit", "-aqm", "add feature")
	commit.Dir = wt
	commit.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	// Stage the new file first.
	add := exec.Command("git", "add", ".")
	add.Dir = wt
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	files, err := r.ChangedFiles(ctx, "main", "task/feature")
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	if len(files) != 1 || files[0] != "feature.txt" {
		t.Fatalf("changed files = %v", files)
	}

	diff, err := r.Diff(ctx, "main", "task/feature")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if diff == "" {
		t.Fatal("expected non-empty diff")
	}

	if err := r.RemoveWorktree(ctx, wt); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
}

func TestCoAuthorTrailer(t *testing.T) {
	const want = "Co-authored-by: fabrika <noreply@fabrika-ai.com>"
	if CoAuthorTrailer != want {
		t.Fatalf("CoAuthorTrailer = %q, want %q", CoAuthorTrailer, want)
	}
}

func TestWithCoAuthor(t *testing.T) {
	msg := "capture agent work"
	got := WithCoAuthor(msg)
	want := msg + "\n\n" + CoAuthorTrailer
	if got != want {
		t.Fatalf("WithCoAuthor = %q, want %q", got, want)
	}
	// Idempotent: applying again must not duplicate the trailer.
	again := WithCoAuthor(got)
	if again != got {
		t.Fatalf("WithCoAuthor not idempotent: %q", again)
	}
	if n := strings.Count(again, CoAuthorTrailer); n != 1 {
		t.Fatalf("trailer appears %d times, want 1", n)
	}
}

func TestNormalizeCommitTrailers(t *testing.T) {
	ctx := context.Background()
	root := initRepo(t)

	env := append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}

	// Record the base commit so we can prove it is left untouched.
	baseHash := strings.TrimSpace(git("rev-parse", "main"))

	// Branch off and make two commits; the second carries a foreign co-author
	// trailer that the agent must strip.
	git("checkout", "-q", "-b", "feature")
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "first change")

	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "second change\n\nCo-Authored-By: SomeAgent <a@b.c>")
	git("checkout", "-q", "main")

	r, err := Open(ctx, root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := r.NormalizeCommitTrailers(ctx, "main", "feature"); err != nil {
		t.Fatalf("NormalizeCommitTrailers: %v", err)
	}

	// Every commit unique to the branch must carry the fabrika trailer exactly
	// once and no foreign co-author line.
	hashes := strings.Fields(git("rev-list", "main..feature"))
	if len(hashes) != 2 {
		t.Fatalf("branch range has %d commits, want 2", len(hashes))
	}
	for _, h := range hashes {
		body := git("log", "-1", "--format=%B", h)
		if n := strings.Count(body, CoAuthorTrailer); n != 1 {
			t.Fatalf("commit %s has fabrika trailer %d times, want 1:\n%s", h, n, body)
		}
		if strings.Contains(body, "SomeAgent") {
			t.Fatalf("commit %s still contains foreign co-author:\n%s", h, body)
		}
		// No co-author line other than the single fabrika one.
		var coAuthors int
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "co-authored-by:") {
				coAuthors++
			}
		}
		if coAuthors != 1 {
			t.Fatalf("commit %s has %d co-author lines, want 1:\n%s", h, coAuthors, body)
		}
	}

	// Subjects must be preserved (rev-list lists newest first).
	if subj := strings.TrimSpace(git("log", "-1", "--format=%s", "feature")); subj != "second change" {
		t.Fatalf("tip subject = %q, want %q", subj, "second change")
	}

	// The base commit must be byte-for-byte unchanged.
	if got := strings.TrimSpace(git("rev-parse", "main")); got != baseHash {
		t.Fatalf("base commit changed: %s -> %s", baseHash, got)
	}
	if body := git("log", "-1", "--format=%B", "main"); strings.Contains(body, CoAuthorTrailer) {
		t.Fatalf("base commit gained a fabrika trailer:\n%s", body)
	}
}

func TestNormalizeCommitTrailersEmptyRange(t *testing.T) {
	ctx := context.Background()
	root := initRepo(t)

	cmd := exec.Command("git", "branch", "feature")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git branch: %v\n%s", err, out)
	}

	r, err := Open(ctx, root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Branch points at base: empty range must no-op without error.
	if err := r.NormalizeCommitTrailers(ctx, "main", "feature"); err != nil {
		t.Fatalf("NormalizeCommitTrailers (empty range): %v", err)
	}
}

func TestAddAllAndCommit(t *testing.T) {
	ctx := context.Background()
	root := initRepo(t)
	r, err := Open(ctx, root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	wt := filepath.Join(t.TempDir(), "wt")
	if err := r.AddWorktree(ctx, wt, "task/auto", "main"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}

	// Clean tree -> no commit.
	committed, err := r.AddAllAndCommit(ctx, wt, "noop")
	if err != nil {
		t.Fatalf("AddAllAndCommit (clean): %v", err)
	}
	if committed {
		t.Fatal("expected no commit on clean tree")
	}

	// Uncommitted change -> commit happens and shows in the diff.
	if err := os.WriteFile(filepath.Join(wt, "new.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	committed, err = r.AddAllAndCommit(ctx, wt, "capture agent work")
	if err != nil {
		t.Fatalf("AddAllAndCommit (dirty): %v", err)
	}
	if !committed {
		t.Fatal("expected a commit for the new file")
	}

	files, _ := r.ChangedFiles(ctx, "main", "task/auto")
	if len(files) != 1 || files[0] != "new.txt" {
		t.Fatalf("changed files = %v", files)
	}

	// The commit AddAllAndCommit created must carry the fabrika trailer.
	logCmd := exec.Command("git", "log", "-1", "--format=%B")
	logCmd.Dir = wt
	body, err := logCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v\n%s", err, body)
	}
	if !strings.Contains(string(body), CoAuthorTrailer) {
		t.Fatalf("commit body missing co-author trailer:\n%s", body)
	}

	_ = r.RemoveWorktree(ctx, wt)
}

// A conflicting merge must leave the repo clean (merge aborted), never in a
// half-merged state the web UI can't recover from.
func TestMergeConflictAborts(t *testing.T) {
	dir := initRepo(t)
	ctx := context.Background()
	r, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// Branch edits README one way; main edits it another -> guaranteed conflict.
	run("checkout", "-q", "-b", "task/conflict")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("branch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("commit", "-aqm", "branch edit")
	run("checkout", "-q", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("commit", "-aqm", "main edit")

	if err := r.Merge(ctx, "main", "task/conflict"); err == nil {
		t.Fatal("expected merge conflict error")
	}

	// No MERGE_HEAD and a clean status -> the merge was aborted.
	if _, err := os.Stat(filepath.Join(dir, ".git", "MERGE_HEAD")); !os.IsNotExist(err) {
		t.Fatalf("MERGE_HEAD should not exist after abort, stat err = %v", err)
	}
	status := exec.Command("git", "status", "--porcelain")
	status.Dir = dir
	out, err := status.CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("repo should be clean after aborted merge, status:\n%s", out)
	}
}

// gitRunner returns a helper that runs git in dir with a deterministic identity.
func gitRunner(t *testing.T, dir string) func(args ...string) {
	t.Helper()
	return func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func TestSyncBranchFromBase(t *testing.T) {
	ctx := context.Background()

	// Non-overlapping changes: main advances on file A, branch edited file B.
	// Sync should merge cleanly and report the branch updated.
	t.Run("clean", func(t *testing.T) {
		dir := initRepo(t)
		r, err := Open(ctx, dir)
		if err != nil {
			t.Fatal(err)
		}
		wt := filepath.Join(t.TempDir(), "wt")
		if err := r.AddWorktree(ctx, wt, "task/clean", "main"); err != nil {
			t.Fatal(err)
		}
		// Branch edits a new file.
		runBr := gitRunner(t, wt)
		if err := os.WriteFile(filepath.Join(wt, "branch.txt"), []byte("b\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runBr("add", "branch.txt")
		runBr("commit", "-qm", "branch work")
		// main advances on an unrelated file.
		runMain := gitRunner(t, dir)
		if err := os.WriteFile(filepath.Join(dir, "main.txt"), []byte("m\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runMain("add", "main.txt")
		runMain("commit", "-qm", "main work")

		updated, conflicts, err := r.SyncBranchFromBase(ctx, wt, "main")
		if err != nil || len(conflicts) != 0 || !updated {
			t.Fatalf("clean sync: updated=%v conflicts=%v err=%v", updated, conflicts, err)
		}
		// Branch now contains main's commit.
		if _, err := os.Stat(filepath.Join(wt, "main.txt")); err != nil {
			t.Fatalf("branch should contain main.txt after sync: %v", err)
		}
		// A second sync is a no-op (base already an ancestor).
		updated, _, err = r.SyncBranchFromBase(ctx, wt, "main")
		if err != nil || updated {
			t.Fatalf("second sync should be a no-op: updated=%v err=%v", updated, err)
		}
	})

	// Overlapping edit to the same file: the original failure. Sync must abort,
	// return the conflicted path, and leave the worktree clean (no err).
	t.Run("conflict", func(t *testing.T) {
		dir := initRepo(t)
		r, err := Open(ctx, dir)
		if err != nil {
			t.Fatal(err)
		}
		wt := filepath.Join(t.TempDir(), "wt")
		if err := r.AddWorktree(ctx, wt, "task/conflict", "main"); err != nil {
			t.Fatal(err)
		}
		runBr := gitRunner(t, wt)
		if err := os.WriteFile(filepath.Join(wt, "README.md"), []byte("branch\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runBr("commit", "-aqm", "branch edit")
		runMain := gitRunner(t, dir)
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runMain("commit", "-aqm", "main edit")

		updated, conflicts, err := r.SyncBranchFromBase(ctx, wt, "main")
		if err != nil {
			t.Fatalf("conflict should not be a hard error: %v", err)
		}
		if updated {
			t.Fatal("conflicting sync should not report updated")
		}
		if len(conflicts) != 1 || conflicts[0] != "README.md" {
			t.Fatalf("expected README.md conflict, got %v", conflicts)
		}
		// Worktree is clean -> the merge was aborted.
		status := exec.Command("git", "status", "--porcelain")
		status.Dir = wt
		out, _ := status.CombinedOutput()
		if strings.TrimSpace(string(out)) != "" {
			t.Fatalf("worktree should be clean after aborted sync:\n%s", out)
		}
	})
}

// pushedSetFixture builds a repo with a bare origin where two commits are
// pushed and a third exists only locally. Returns the repo handle and the
// three SHAs: [0],[1] pushed, [2] unpushed.
func pushedSetFixture(t *testing.T) (*Repo, [3]string) {
	t.Helper()
	ctx := context.Background()
	dir := initRepo(t)
	repo, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}

	bare := filepath.Join(t.TempDir(), "origin.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v\n%s", err, out)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("remote", "add", "origin", bare)

	var shas [3]string
	commit := func(name string) string {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", ".")
		run("commit", "-q", "-m", name)
		sha, err := repo.RevParse(ctx, "HEAD")
		if err != nil {
			t.Fatal(err)
		}
		return sha
	}
	shas[0] = commit("a.txt")
	shas[1] = commit("b.txt")
	if _, err := repo.Push(ctx, "origin", "main"); err != nil {
		t.Fatalf("push: %v", err)
	}
	shas[2] = commit("c.txt") // local only
	return repo, shas
}

func TestPushedSet(t *testing.T) {
	ctx := context.Background()
	repo, shas := pushedSetFixture(t)

	bogus := strings.Repeat("0", 40)
	got, err := repo.PushedSet(ctx, "origin", "main", []string{shas[0], shas[1], shas[2], bogus})
	if err != nil {
		t.Fatalf("PushedSet: %v", err)
	}
	want := map[string]bool{shas[0]: true, shas[1]: true, shas[2]: false, bogus: false}
	for sha, w := range want {
		if got[sha] != w {
			t.Errorf("PushedSet[%s] = %v, want %v", sha, got[sha], w)
		}
	}

	// A branch that was never pushed has no remote-tracking ref: every sha
	// reports unpushed, without error.
	got, err = repo.PushedSet(ctx, "origin", "nope", []string{shas[0]})
	if err != nil {
		t.Fatalf("PushedSet (missing ref): %v", err)
	}
	if got[shas[0]] {
		t.Error("sha reported pushed against a nonexistent remote ref")
	}
}

// TestPushedSetConstantGitCalls locks down the fix for the kanban board's
// multi-second refresh: annotating N merged tasks must not cost N git
// subprocesses. A counting `git` shim on PATH records every invocation while
// PushedSet checks 50 SHAs; the count must stay constant (rev-parse + rev-list).
func TestPushedSetConstantGitCalls(t *testing.T) {
	ctx := context.Background()
	repo, shas := pushedSetFixture(t)

	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	binDir := t.TempDir()
	countFile := filepath.Join(binDir, "count")
	shim := "#!/bin/sh\necho x >> " + countFile + "\nexec " + realGit + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(binDir, "git"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	many := make([]string, 0, 50)
	for i := 0; i < 50; i++ {
		many = append(many, shas[i%3])
	}
	if _, err := repo.PushedSet(ctx, "origin", "main", many); err != nil {
		t.Fatalf("PushedSet: %v", err)
	}

	data, err := os.ReadFile(countFile)
	if err != nil {
		t.Fatalf("shim never ran: %v", err)
	}
	if n := strings.Count(string(data), "x"); n > 2 {
		t.Fatalf("PushedSet(50 shas) spawned %d git processes, want <= 2", n)
	}
}
