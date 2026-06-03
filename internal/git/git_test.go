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
	const want = "Co-authored-by: fabrika <fabrika@berkaycubuk.com>"
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
