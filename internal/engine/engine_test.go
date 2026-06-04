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
	if err := eng.Accept(task.ID, false); err != nil {
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
	// Accept must be refused for a non-green task — and the error should point
	// the human at the force escape hatch.
	err := eng.Accept(task.ID, false)
	if err == nil {
		t.Fatal("Accept should fail for a failed task")
	}
	if !strings.Contains(err.Error(), "force") {
		t.Fatalf("error should mention force, got: %v", err)
	}
}

func TestForceAcceptMergesFailedTask(t *testing.T) {
	eng, st, repo := setup(t)
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

	// Force-accept merges the red work and cleans up, exactly like a green accept.
	if err := eng.Accept(task.ID, true); err != nil {
		t.Fatalf("force Accept: %v", err)
	}
	got, _ = st.Tasks.Get(task.ID)
	if got.Status != model.TaskMerged {
		t.Fatalf("status = %q, want merged", got.Status)
	}
	if _, err := os.Stat(filepath.Join(repo, "out.txt")); err != nil {
		t.Fatalf("out.txt should exist on main after force merge: %v", err)
	}
	if _, err := os.Stat(eng.worktreePath(task.ID)); !os.IsNotExist(err) {
		t.Fatalf("worktree should be removed after force merge, stat err = %v", err)
	}
}

func TestDeleteTaskOnlyClosed(t *testing.T) {
	eng, st, _ := setup(t)

	task := &model.Task{Title: "kicked back", Status: model.TaskClosed}
	if err := st.Tasks.Create(task); err != nil {
		t.Fatal(err)
	}
	// Attempt + comment history should go with the task.
	if err := st.Attempts.Create(&model.Attempt{TaskID: task.ID, Result: model.ResultFail}); err != nil {
		t.Fatal(err)
	}
	if err := st.Comments.Create(&model.Comment{TaskID: task.ID, AuthorType: "user", Body: "redo"}); err != nil {
		t.Fatal(err)
	}

	// A non-closed task is refused.
	ready := &model.Task{Title: "still queued"}
	if err := st.Tasks.Create(ready); err != nil {
		t.Fatal(err)
	}
	if err := eng.DeleteTask(ready.ID); err == nil {
		t.Fatal("DeleteTask should refuse a ready task")
	}

	if err := eng.DeleteTask(task.ID); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	if _, err := st.Tasks.Get(task.ID); err == nil {
		t.Fatal("task should be gone")
	}
	if atts, _ := st.Attempts.ListForTask(task.ID); len(atts) != 0 {
		t.Fatalf("attempts should be gone, got %d", len(atts))
	}
	if cs, _ := st.Comments.ListForTask(task.ID); len(cs) != 0 {
		t.Fatalf("comments should be gone, got %d", len(cs))
	}
}

func TestAutoRetryRequeuesWithinBudget(t *testing.T) {
	eng, st, _ := setup(t)
	// Two-attempt budget; verify is red on the first run (no marker yet) and
	// green on the second, simulating a failure the agent can correct.
	a := &model.Agent{
		Name:        "fake",
		Command:     "printf 'x' > out.txt",
		Roles:       []string{model.RoleImplementer},
		Enabled:     true,
		MaxAttempts: 2,
	}
	if err := st.Agents.Create(a); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "marker")
	task := &model.Task{
		Title:      "flaky verify",
		Acceptance: model.Contract{VerifyCmds: []string{"test -f " + marker + " || { touch " + marker + "; exit 1; }"}},
	}
	if err := st.Tasks.Create(task); err != nil {
		t.Fatal(err)
	}

	// First run fails the gate but stays within budget -> auto-requeued.
	if !eng.dispatchOnce() {
		t.Fatal("expected dispatch")
	}
	got, _ := st.Tasks.Get(task.ID)
	if got.Status != model.TaskReady {
		t.Fatalf("status = %q, want ready (auto-retry)", got.Status)
	}
	att, err := st.Attempts.LatestForTask(task.ID)
	if err != nil {
		t.Fatalf("attempt: %v", err)
	}
	if att.Result != model.ResultFail {
		t.Fatalf("result = %q, want fail", att.Result)
	}

	// Second run passes -> review, with both attempts kept as history.
	if !eng.dispatchOnce() {
		t.Fatal("expected auto-retry dispatch")
	}
	got, _ = st.Tasks.Get(task.ID)
	if got.Status != model.TaskReview {
		t.Fatalf("status = %q, want review after retry", got.Status)
	}
	atts, err := st.Attempts.ListForTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(atts) != 2 {
		t.Fatalf("attempts = %d, want 2", len(atts))
	}
}

func TestAutoRetryExhaustsBudgetThenFails(t *testing.T) {
	eng, st, _ := setup(t)
	a := &model.Agent{
		Name:        "fake",
		Command:     "printf 'x' > out.txt",
		Roles:       []string{model.RoleImplementer},
		Enabled:     true,
		MaxAttempts: 2,
	}
	if err := st.Agents.Create(a); err != nil {
		t.Fatal(err)
	}
	task := &model.Task{
		Title:      "always-red verify",
		Acceptance: model.Contract{VerifyCmds: []string{"exit 1"}},
	}
	if err := st.Tasks.Create(task); err != nil {
		t.Fatal(err)
	}

	// Attempt 1: fails, auto-requeued. Attempt 2: fails, budget spent -> failed.
	if !eng.dispatchOnce() {
		t.Fatal("expected dispatch")
	}
	if got, _ := st.Tasks.Get(task.ID); got.Status != model.TaskReady {
		t.Fatalf("status = %q, want ready after first failure", got.Status)
	}
	if !eng.dispatchOnce() {
		t.Fatal("expected auto-retry dispatch")
	}
	got, _ := st.Tasks.Get(task.ID)
	if got.Status != model.TaskFailed {
		t.Fatalf("status = %q, want failed after budget exhausted", got.Status)
	}
	atts, err := st.Attempts.ListForTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(atts) != 2 {
		t.Fatalf("attempts = %d, want 2", len(atts))
	}
}

func TestLastFailureSummaryIncludesPriorFailures(t *testing.T) {
	eng, st, _ := setup(t)
	task := &model.Task{Title: "multi-fail"}
	if err := st.Tasks.Create(task); err != nil {
		t.Fatal(err)
	}
	older := &model.Attempt{
		TaskID: task.ID, Result: model.ResultFail,
		Evidence: model.Evidence{Stages: map[string]model.StageResult{
			"build": {Pass: false, Output: "syntax error: smart quotes"},
		}},
	}
	newer := &model.Attempt{
		TaskID: task.ID, Result: model.ResultFail,
		Evidence: model.Evidence{Stages: map[string]model.StageResult{
			"verify": {Pass: false, Output: "TypeError: cannot read undefined"},
		}},
	}
	for _, a := range []*model.Attempt{older, newer} {
		if err := st.Attempts.Create(a); err != nil {
			t.Fatal(err)
		}
	}

	sum := eng.lastFailureSummary(task.ID)
	for _, want := range []string{
		`stage "verify" failed`, "TypeError: cannot read undefined",
		"Earlier attempts failed too", `stage "build" failed: syntax error: smart quotes`,
	} {
		if !strings.Contains(sum, want) {
			t.Fatalf("summary missing %q:\n%s", want, sum)
		}
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

func TestRecoverOrphansRequeuesStrandedWork(t *testing.T) {
	eng, st, repo := setup(t)

	// Simulate a previous process that dispatched a task and died: status
	// `running` in the DB, branch + worktree on disk, nothing in memory.
	task := &model.Task{Title: "orphan"}
	if err := st.Tasks.Create(task); err != nil {
		t.Fatal(err)
	}
	branch := "fabrika/task-orphan"
	wt := eng.worktreePath(task.ID)
	gitRun(t, repo, "worktree", "add", "-q", "-b", branch, wt)
	if err := st.Tasks.SetRun(task.ID, "agent-x", branch, model.TaskRunning); err != nil {
		t.Fatal(err)
	}

	// A task in a terminal state must stay put.
	merged := &model.Task{Title: "done"}
	if err := st.Tasks.Create(merged); err != nil {
		t.Fatal(err)
	}
	if err := st.Tasks.UpdateStatus(merged.ID, model.TaskMerged); err != nil {
		t.Fatal(err)
	}

	// A big task stranded mid-planning goes back to draft for a fresh claim.
	bt := &model.BigTask{Title: "big"}
	if err := st.BigTasks.Create(bt); err != nil {
		t.Fatal(err)
	}
	if err := st.BigTasks.UpdateStatus(bt.ID, model.BigTaskPlanning); err != nil {
		t.Fatal(err)
	}

	eng.recoverOrphans()

	if got, _ := st.Tasks.Get(task.ID); got.Status != model.TaskReady {
		t.Fatalf("orphan status = %q, want ready", got.Status)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("stale worktree should be removed, stat err = %v", err)
	}
	if out := gitOut(t, repo, "branch", "--list", branch); strings.TrimSpace(out) != "" {
		t.Fatalf("stale branch should be deleted, got %q", out)
	}
	if got, _ := st.Tasks.Get(merged.ID); got.Status != model.TaskMerged {
		t.Fatalf("merged task status = %q, want merged", got.Status)
	}
	if got, _ := st.BigTasks.Get(bt.ID); got.Status != model.BigTaskDraft {
		t.Fatalf("bigtask status = %q, want draft", got.Status)
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

func TestWriteHeldOutFiles(t *testing.T) {
	wt := t.TempDir()
	files := map[string]string{
		"web/test/heldout/x.test.ts": "import \"node:test\"",
		"./flat.txt":                 "flat",
	}
	if err := writeHeldOutFiles(wt, files); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(wt, "web", "test", "heldout", "x.test.ts"))
	if err != nil || string(b) != "import \"node:test\"" {
		t.Fatalf("nested file = %q, err = %v", b, err)
	}
	if b, _ := os.ReadFile(filepath.Join(wt, "flat.txt")); string(b) != "flat" {
		t.Fatalf("flat file = %q", b)
	}

	// Overwrites an implementer-supplied copy with the trusted contents.
	if err := writeHeldOutFiles(wt, map[string]string{"flat.txt": "trusted"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(wt, "flat.txt")); string(b) != "trusted" {
		t.Fatalf("flat file after overwrite = %q", b)
	}

	// Paths escaping the worktree are rejected.
	for _, bad := range []string{"../escape.txt", "/abs.txt", "a/../../b.txt", ""} {
		if err := writeHeldOutFiles(wt, map[string]string{bad: "x"}); err == nil {
			t.Fatalf("path %q accepted, want error", bad)
		}
	}

	// No held-out files is a no-op.
	if err := writeHeldOutFiles(wt, nil); err != nil {
		t.Fatal(err)
	}
}

// A human can steer a retry by commenting on the task: the next run's prompt
// carries the comment as guidance plus a summary of the previous failure. The
// fake agent echoes its own prompt (minus the fabrika_ marker instruction
// lines, which the engine would otherwise parse as real markers) so the
// attempt log proves what it was told.
func TestRetryCarriesHumanGuidanceAndLastFailure(t *testing.T) {
	eng, st, _ := setup(t)
	a := &model.Agent{
		Name:    "fake",
		Command: "grep -v fabrika_ {prompt_file}",
		Roles:   []string{model.RoleImplementer},
		Enabled: true,
	}
	if err := st.Agents.Create(a); err != nil {
		t.Fatal(err)
	}

	// First run fails (held-out check is red).
	task := &model.Task{
		Title:      "steerable",
		Spec:       "do the thing",
		Acceptance: model.Contract{HeldOut: []string{"echo boom-marker && exit 3"}},
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

	// Human leaves guidance and retries.
	c := &model.Comment{TaskID: task.ID, AuthorType: "user", Body: "try mocking the clock instead"}
	if err := st.Comments.Create(c); err != nil {
		t.Fatal(err)
	}
	if err := eng.Retry(task.ID); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if !eng.dispatchOnce() {
		t.Fatal("expected retry dispatch")
	}

	att, err := st.Attempts.LatestForTask(task.ID)
	if err != nil {
		t.Fatalf("attempt: %v", err)
	}
	for _, want := range []string{
		"Guidance from the human",
		"try mocking the clock instead",
		"Previous attempt failed",
		"boom-marker",
	} {
		if !strings.Contains(att.Log, want) {
			t.Fatalf("retry prompt missing %q:\n%s", want, att.Log)
		}
	}
}
