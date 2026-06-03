package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
