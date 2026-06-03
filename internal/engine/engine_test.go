package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
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

// gitOut runs git and returns its combined output, failing the test on error.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
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

func TestDispatchRecordsAgentUsage(t *testing.T) {
	eng, st, _ := setup(t)
	// Agent does work and reports its token usage via the fabrika_USAGE marker;
	// the engine must thread that into the persisted attempt.
	registerAgent(t, st, "printf 'done' > out.txt && "+
		`echo 'fabrika_USAGE: {"inputTokens":120,"outputTokens":45,"totalTokens":165}'`)

	task := &model.Task{Title: "report usage", Spec: "create out.txt"}
	if err := st.Tasks.Create(task); err != nil {
		t.Fatal(err)
	}
	if !eng.dispatchOnce() {
		t.Fatal("expected a task to be dispatched")
	}

	att, err := st.Attempts.LatestForTask(task.ID)
	if err != nil {
		t.Fatalf("attempt: %v", err)
	}
	want := model.Usage{InputTokens: 120, OutputTokens: 45, TotalTokens: 165}
	if att.Usage != want {
		t.Fatalf("usage = %+v, want %+v", att.Usage, want)
	}
}

func TestDispatchNormalizesCommitTrailers(t *testing.T) {
	eng, st, repo := setup(t)
	// The agent makes its own commit carrying a foreign co-author trailer; the
	// engine must rewrite the branch so each commit instead carries the fabrika
	// trailer and no foreign attribution before the diff/gate run.
	registerAgent(t, st, "printf 'done' > out.txt && "+
		"git add . && "+
		"git commit -qm 'agent work\n\nCo-Authored-By: SomeAgent <a@b.c>'")

	task := &model.Task{Title: "normalize trailers", Spec: "create out.txt"}
	if err := st.Tasks.Create(task); err != nil {
		t.Fatal(err)
	}

	if !eng.dispatchOnce() {
		t.Fatal("expected a task to be dispatched")
	}

	got, _ := st.Tasks.Get(task.ID)
	if got.Branch == "" {
		t.Fatal("task should have a branch after dispatch")
	}

	const fabrikaTrailer = "Co-authored-by: fabrika <fabrika@berkaycubuk.com>"
	hashes := strings.Fields(gitOut(t, repo, "rev-list", "main.."+got.Branch))
	if len(hashes) == 0 {
		t.Fatal("expected at least one commit on the branch")
	}
	for _, h := range hashes {
		body := gitOut(t, repo, "log", "-1", "--format=%B", h)
		if n := strings.Count(body, fabrikaTrailer); n != 1 {
			t.Fatalf("commit %s carries fabrika trailer %d times, want 1:\n%s", h, n, body)
		}
		if strings.Contains(body, "SomeAgent") {
			t.Fatalf("commit %s still contains foreign co-author:\n%s", h, body)
		}
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

func TestPickRemote(t *testing.T) {
	if _, err := pickRemote(nil); err == nil {
		t.Fatal("expected error for no remotes")
	}
	if r, err := pickRemote([]string{"upstream"}); err != nil || r != "upstream" {
		t.Fatalf("single remote: got %q, %v", r, err)
	}
	if r, err := pickRemote([]string{"upstream", "origin"}); err != nil || r != "origin" {
		t.Fatalf("multi remote: prefer origin, got %q, %v", r, err)
	}
	if _, err := pickRemote([]string{"a", "b"}); err == nil {
		t.Fatal("expected error for ambiguous remotes")
	}
}

func TestEvidenceArtifactIngest(t *testing.T) {
	eng, st, repo := setup(t)
	// Agent produces a screenshot and points at it via the evidence marker.
	registerAgent(t, st, "printf 'shot' > shot.png && "+
		"echo 'fabrika_EVIDENCE: shot.png | login screen'")

	task := &model.Task{Title: "with evidence", Spec: "s"}
	if err := st.Tasks.Create(task); err != nil {
		t.Fatal(err)
	}
	if !eng.dispatchOnce() {
		t.Fatal("expected a task to be dispatched")
	}

	att, err := st.Attempts.LatestForTask(task.ID)
	if err != nil {
		t.Fatalf("attempt: %v", err)
	}
	if len(att.Evidence.Artifacts) != 1 {
		t.Fatalf("artifacts = %v, want 1", att.Evidence.Artifacts)
	}
	url := att.Evidence.Artifacts[0]
	if !regexp.MustCompile(`^/api/uploads/[a-f0-9-]{36}\.png$`).MatchString(url) {
		t.Fatalf("artifact url = %q", url)
	}
	// The file was copied out of the worktree into the project uploads dir.
	name := strings.TrimPrefix(url, "/api/uploads/")
	data, err := os.ReadFile(filepath.Join(repo, ".fabrika", "uploads", name))
	if err != nil || string(data) != "shot" {
		t.Fatalf("stored artifact: %q, %v", data, err)
	}
	// The run also surfaces the artifact as one agent comment with the caption.
	comments, err := st.Comments.ListForTask(task.ID)
	if err != nil {
		t.Fatalf("comments: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("comments = %+v, want 1", comments)
	}
	c := comments[0]
	if c.AuthorType != "agent" || len(c.Attachments) != 1 || c.Attachments[0] != url ||
		!strings.Contains(c.Body, "login screen") {
		t.Fatalf("evidence comment = %+v", c)
	}
}

func TestEvidenceBadRefsSkipped(t *testing.T) {
	eng, st, _ := setup(t)
	// Escaping paths (even with allowed extensions), disallowed types, and
	// missing files are all skipped without failing the run.
	registerAgent(t, st, "printf 'x' > ../../../escape.png && printf 'y' > run.sh && "+
		"echo 'fabrika_EVIDENCE: ../../../escape.png' && "+
		"echo 'fabrika_EVIDENCE: run.sh' && "+
		"echo 'fabrika_EVIDENCE: missing.png'")

	task := &model.Task{Title: "bad evidence", Spec: "s"}
	if err := st.Tasks.Create(task); err != nil {
		t.Fatal(err)
	}
	if !eng.dispatchOnce() {
		t.Fatal("expected a task to be dispatched")
	}

	got, _ := st.Tasks.Get(task.ID)
	if got.Status != model.TaskReview {
		t.Fatalf("status = %q, want review (bad evidence must not fail the run)", got.Status)
	}
	att, err := st.Attempts.LatestForTask(task.ID)
	if err != nil {
		t.Fatalf("attempt: %v", err)
	}
	if len(att.Evidence.Artifacts) != 0 {
		t.Fatalf("artifacts = %v, want none", att.Evidence.Artifacts)
	}
	comments, _ := st.Comments.ListForTask(task.ID)
	if len(comments) != 0 {
		t.Fatalf("comments = %+v, want none", comments)
	}
}
