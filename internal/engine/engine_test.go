package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/berkaycubuk/fabrika/internal/config"
	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/berkaycubuk/fabrika/internal/store"
)

// gitEnv returns a deterministic identity so commits work in CI/sandboxes.
func gitEnv() []string {
	return append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// setup creates a repo with one commit and an engine wired to a temp store.
func setup(t *testing.T) (*Engine, *store.Store, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	gitRun(t, repo, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-qm", "init")

	stDir := t.TempDir()
	st, err := store.Open(filepath.Join(stDir, "g"), filepath.Join(repo, ".fabrika"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	// Identity for the engine's auto-commit, which shells out to git.
	t.Setenv("GIT_AUTHOR_NAME", "test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")

	eng := New(st, &config.Config{}, repo, nil)
	eng.ctx = context.Background()
	return eng, st, repo
}

// registerAgent adds an enabled implementer whose command writes a file into the
// worktree — simulating an agent that produces work.
func registerAgent(t *testing.T, st *store.Store, command string) {
	t.Helper()
	a := &model.Agent{
		Name:    "fake",
		Command: command,
		Roles:   []string{model.RoleImplementer},
		Enabled: true,
	}
	if err := st.Agents.Create(a); err != nil {
		t.Fatalf("create agent: %v", err)
	}
}

func TestDispatchProducesGreenReview(t *testing.T) {
	eng, st, repo := setup(t)
	// Agent writes a file in the worktree; gate has no verbs -> vacuously green.
	registerAgent(t, st, "printf 'done' > out.txt")

	task := &model.Task{Title: "make out.txt", Spec: "create out.txt"}
	if err := st.Tasks.Create(task); err != nil {
		t.Fatal(err)
	}

	if !eng.dispatchOnce() {
		t.Fatal("expected a task to be dispatched")
	}

	got, _ := st.Tasks.Get(task.ID)
	if got.Status != model.TaskReview {
		t.Fatalf("status = %q, want review", got.Status)
	}
	att, err := st.Attempts.LatestForTask(task.ID)
	if err != nil {
		t.Fatalf("attempt: %v", err)
	}
	if att.Result != model.ResultPass {
		t.Fatalf("result = %q, want pass", att.Result)
	}
	if att.Evidence.Diff == "" {
		t.Fatal("expected a non-empty diff capturing the agent's work")
	}

	// Accept -> merged, and the file lands on main.
	if err := eng.Accept(task.ID); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	got, _ = st.Tasks.Get(task.ID)
	if got.Status != model.TaskMerged {
		t.Fatalf("status = %q, want merged", got.Status)
	}
	if _, err := os.Stat(filepath.Join(repo, "out.txt")); err != nil {
		t.Fatalf("out.txt should exist on main after merge: %v", err)
	}
}

func TestGateFailureSurfacesAsFailed(t *testing.T) {
	eng, st, _ := setup(t)
	registerAgent(t, st, "printf 'x' > out.txt")

	task := &model.Task{
		Title:      "failing verify",
		Acceptance: model.Contract{VerifyCmds: []string{"exit 1"}},
	}
	if err := st.Tasks.Create(task); err != nil {
		t.Fatal(err)
	}

	if !eng.dispatchOnce() {
		t.Fatal("expected dispatch")
	}
	got, _ := st.Tasks.Get(task.ID)
	if got.Status != model.TaskFailed {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	// Accept must be refused for a non-green task.
	if err := eng.Accept(task.ID); err == nil {
		t.Fatal("Accept should fail for a failed task")
	}
}

func TestRetryRequeuesFailedTask(t *testing.T) {
	eng, st, _ := setup(t)
	registerAgent(t, st, "printf 'x' > out.txt")

	task := &model.Task{
		Title:      "failing verify",
		Acceptance: model.Contract{VerifyCmds: []string{"exit 1"}},
	}
	if err := st.Tasks.Create(task); err != nil {
		t.Fatal(err)
	}
	if !eng.dispatchOnce() {
		t.Fatal("expected dispatch")
	}
	if got, _ := st.Tasks.Get(task.ID); got.Status != model.TaskFailed {
		t.Fatalf("status = %q, want failed", got.Status)
	}

	// Retry returns the task to ready for a fresh attempt.
	if err := eng.Retry(task.ID); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	got, _ := st.Tasks.Get(task.ID)
	if got.Status != model.TaskReady {
		t.Fatalf("status = %q, want ready after retry", got.Status)
	}
	// The failed attempt is preserved as history.
	if _, err := st.Attempts.LatestForTask(task.ID); err != nil {
		t.Fatalf("prior attempt should be kept: %v", err)
	}
	// A re-dispatch picks it up again (it fails the same way, but it ran).
	if !eng.dispatchOnce() {
		t.Fatal("expected the re-queued task to dispatch")
	}

	// Retry is refused for a merged task and for an unknown id.
	merged := &model.Task{Title: "done"}
	if err := st.Tasks.Create(merged); err != nil {
		t.Fatal(err)
	}
	if err := st.Tasks.UpdateStatus(merged.ID, model.TaskMerged); err != nil {
		t.Fatal(err)
	}
	if err := eng.Retry(merged.ID); err == nil {
		t.Fatal("Retry should be refused for a merged task")
	}
	if err := eng.Retry("nonexistent"); err == nil {
		t.Fatal("Retry should fail for an unknown task")
	}
}

func TestEscalationBlocks(t *testing.T) {
	eng, st, _ := setup(t)
	registerAgent(t, st, `echo 'fabrika_DECISION: {"question":"which db?"}'`)

	task := &model.Task{Title: "ambiguous"}
	if err := st.Tasks.Create(task); err != nil {
		t.Fatal(err)
	}
	if !eng.dispatchOnce() {
		t.Fatal("expected dispatch")
	}
	got, _ := st.Tasks.Get(task.ID)
	if got.Status != model.TaskBlocked {
		t.Fatalf("status = %q, want blocked", got.Status)
	}
	att, _ := st.Attempts.LatestForTask(task.ID)
	if att.Result != model.ResultEscalated {
		t.Fatalf("result = %q, want escalated", att.Result)
	}
}

func TestNoAgentLeavesTaskReady(t *testing.T) {
	eng, st, _ := setup(t)
	task := &model.Task{Title: "no agent"}
	if err := st.Tasks.Create(task); err != nil {
		t.Fatal(err)
	}
	if eng.dispatchOnce() {
		t.Fatal("should not dispatch without an enabled agent")
	}
	got, _ := st.Tasks.Get(task.ID)
	if got.Status != model.TaskReady {
		t.Fatalf("status = %q, want ready", got.Status)
	}
}

func TestRejectClosesAndCleansUp(t *testing.T) {
	eng, st, repo := setup(t)
	registerAgent(t, st, "printf 'x' > out.txt")
	task := &model.Task{Title: "to reject"}
	if err := st.Tasks.Create(task); err != nil {
		t.Fatal(err)
	}
	eng.dispatchOnce()

	if err := eng.Reject(task.ID, "not what I wanted"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	got, _ := st.Tasks.Get(task.ID)
	if got.Status != model.TaskClosed {
		t.Fatalf("status = %q, want closed", got.Status)
	}
	// Worktree removed.
	if _, err := os.Stat(filepath.Join(repo, ".fabrika", "worktrees", task.ID)); !os.IsNotExist(err) {
		t.Fatal("worktree should be removed after reject")
	}
}

// Guard against the loop hanging if ctx is already cancelled.
func TestLoopStopsOnContextCancel(t *testing.T) {
	eng, _, _ := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { eng.ctx = ctx; eng.loop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not stop on cancelled context")
	}
}
