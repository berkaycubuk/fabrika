package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/berkaycubuk/fabrika/internal/config"
	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/berkaycubuk/fabrika/internal/store"
)

// registerReviewer adds an enabled reviewer-role agent that emits a fixed verdict.
func registerReviewer(t *testing.T, st *store.Store, command string) {
	t.Helper()
	a := &model.Agent{Name: "reviewer", Command: command, Roles: []string{model.RoleReviewer}, Enabled: true}
	if err := st.Agents.Create(a); err != nil {
		t.Fatalf("create reviewer: %v", err)
	}
}

func autoMergeCfg() *config.Config {
	return &config.Config{Autonomy: config.Autonomy{AutoMerge: []string{"low"}}}
}

func waitStatus(t *testing.T, st *store.Store, id, want string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if got, err := st.Tasks.Get(id); err == nil && got.Status == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, _ := st.Tasks.Get(id)
	t.Fatalf("task status = %q, want %q within %s", got.Status, want, d)
}

func TestAutoMergeLowRisk(t *testing.T) {
	eng, st, repo := setup(t)
	eng.cfg = autoMergeCfg()
	registerAgent(t, st, "printf done > out.txt")

	task := &model.Task{Title: "auto", RiskTier: model.RiskLow}
	if err := st.Tasks.Create(task); err != nil {
		t.Fatal(err)
	}
	eng.dispatchOnce()

	got, _ := st.Tasks.Get(task.ID)
	if got.Status != model.TaskMerged {
		t.Fatalf("status = %q, want merged", got.Status)
	}
	if !got.AutoMerged {
		t.Fatal("expected AutoMerged = true")
	}
	if got.AuditFlagged {
		t.Fatal("audit_rate is 0, task should not be flagged for audit")
	}
	if _, err := os.Stat(filepath.Join(repo, "out.txt")); err != nil {
		t.Fatalf("auto-merged file should be on main: %v", err)
	}
}

func TestAutoMergeBlockedByReviewer(t *testing.T) {
	eng, st, _ := setup(t)
	eng.cfg = autoMergeCfg()
	registerAgent(t, st, "printf done > out.txt")
	registerReviewer(t, st, `echo 'fabrika_REVIEW: {"approve": false, "notes": "needs a test"}'`)

	task := &model.Task{Title: "reviewed", RiskTier: model.RiskLow}
	if err := st.Tasks.Create(task); err != nil {
		t.Fatal(err)
	}
	eng.dispatchOnce()

	got, _ := st.Tasks.Get(task.ID)
	if got.Status != model.TaskReview {
		t.Fatalf("status = %q, want review (reviewer rejected)", got.Status)
	}
	att, _ := st.Attempts.LatestForTask(task.ID)
	rev, ok := att.Evidence.Stages["review"]
	if !ok || rev.Pass {
		t.Fatalf("expected a failing review stage, got %+v", att.Evidence.Stages["review"])
	}
}

func TestAutoMergeWithReviewerApproval(t *testing.T) {
	eng, st, _ := setup(t)
	eng.cfg = autoMergeCfg()
	registerAgent(t, st, "printf done > out.txt")
	registerReviewer(t, st, `echo 'fabrika_REVIEW: {"approve": true, "notes": "lgtm"}'`)

	task := &model.Task{Title: "approved", RiskTier: model.RiskLow}
	if err := st.Tasks.Create(task); err != nil {
		t.Fatal(err)
	}
	eng.dispatchOnce()

	got, _ := st.Tasks.Get(task.ID)
	if got.Status != model.TaskMerged || !got.AutoMerged {
		t.Fatalf("status = %q autoMerged = %v, want merged+auto", got.Status, got.AutoMerged)
	}
}

// staleConflictPair dispatches two tasks that edit the same region of README
// (via the given agent command), then merges the first so the second is left
// stale and conflicting against main. Returns the two task IDs. The agent
// command runs for both the initial work and the later resolution.
func staleConflictPair(t *testing.T, eng *Engine, st *store.Store, agentCmd string) (string, string) {
	t.Helper()
	registerAgent(t, st, agentCmd)

	t1 := &model.Task{Title: "first"}
	if err := st.Tasks.Create(t1); err != nil {
		t.Fatal(err)
	}
	if !eng.dispatchOnce() {
		t.Fatal("dispatch t1")
	}
	t2 := &model.Task{Title: "second"}
	if err := st.Tasks.Create(t2); err != nil {
		t.Fatal(err)
	}
	if !eng.dispatchOnce() {
		t.Fatal("dispatch t2")
	}
	// Merge t1 -> main advances; t2 (forked from the old main) is now stale.
	if err := eng.Accept(t1.ID, false); err != nil {
		t.Fatalf("accept t1: %v", err)
	}
	return t1.ID, t2.ID
}

// TestAcceptResolvesConflict: a human merging a stale, conflicting task triggers
// an agent auto-resolution that, when the agent clears the markers and the gate
// passes, merges the task without further human action.
func TestAcceptResolvesConflict(t *testing.T) {
	eng, st, repo := setup(t)
	// One command serves both phases: initial work appends the (unique) worktree
	// path so the two branches diverge and conflict; on resolution it sees the
	// merge markers and rewrites README to a clean, marker-free file.
	_, t2 := staleConflictPair(t, eng, st,
		"if grep -q '<<<<<<<' README.md; then printf 'resolved\\n' > README.md; else pwd >> README.md; fi")

	// Human clicks merge: conflict detected -> resolution dispatched (not an error).
	err := eng.Accept(t2, false)
	if !eng.IsResolutionStarted(err) {
		t.Fatalf("Accept = %v, want resolution-started signal", err)
	}
	if got, _ := st.Tasks.Get(t2); got.Status != model.TaskRunning {
		t.Fatalf("t2 status = %q, want running (resolving)", got.Status)
	}

	// Resolution runs async -> ends merged, with the resolved content on main.
	waitStatus(t, st, t2, model.TaskMerged, 10*time.Second)
	data, err := os.ReadFile(filepath.Join(repo, "README.md"))
	if err != nil || string(data) != "resolved\n" {
		t.Fatalf("README on main = %q (err %v), want resolved", data, err)
	}
	if eng.isParkedConflict(t2) {
		t.Fatal("a merged task should not stay parked")
	}
}

// TestResolutionFailureParks: when the agent fails to clear the conflict markers,
// the task lands back in review and is parked so the sweep won't re-resolve it
// every tick. The park clears when the task leaves review (Retry).
func TestResolutionFailureParks(t *testing.T) {
	eng, st, _ := setup(t)
	// "pwd >>" appends without removing the merge markers -> resolution fails.
	_, t2 := staleConflictPair(t, eng, st, "pwd >> README.md")

	if err := st.Settings.Set(settingAutoMode, "on"); err != nil {
		t.Fatal(err)
	}
	// First sweep starts a resolution (nothing merged yet).
	if n := eng.sweepAutoMerge(); n != 0 {
		t.Fatalf("first sweep merged %d, want 0 (resolution dispatched)", n)
	}
	// Resolution fails -> back to review, parked.
	waitStatus(t, st, t2, model.TaskReview, 10*time.Second)
	if !eng.isParkedConflict(t2) {
		t.Fatal("a failed resolution should park the task")
	}

	// Subsequent sweeps must not start another resolution (stays in review).
	if n := eng.sweepAutoMerge(); n != 0 {
		t.Fatalf("second sweep merged %d, want 0", n)
	}
	if got, _ := st.Tasks.Get(t2); got.Status != model.TaskReview {
		t.Fatalf("t2 status = %q, want review (not re-resolving)", got.Status)
	}

	// Retry releases the park so a fresh rebuild can try again.
	eng.setStatusBy(t2, model.TaskReady, "human", "retry")
	if eng.isParkedConflict(t2) {
		t.Fatal("park should clear when the task leaves review")
	}
}

func TestAuditSamplingFlags(t *testing.T) {
	eng, st, _ := setup(t)
	eng.cfg = autoMergeCfg()
	eng.sample = func(float64) bool { return true } // deterministically sample
	registerAgent(t, st, "printf done > out.txt")

	task := &model.Task{Title: "sampled", RiskTier: model.RiskLow}
	if err := st.Tasks.Create(task); err != nil {
		t.Fatal(err)
	}
	eng.dispatchOnce()

	got, _ := st.Tasks.Get(task.ID)
	if got.Status != model.TaskMerged || !got.AutoMerged || !got.AuditFlagged {
		t.Fatalf("got status=%q auto=%v audit=%v, want merged+auto+audit", got.Status, got.AutoMerged, got.AuditFlagged)
	}
}

// An agent that touches an undeclared high-risk path must not auto-merge under a
// low declared tier — the effective tier escalates it to the human.
func TestEffectiveTierBlocksUndeclaredHighRisk(t *testing.T) {
	eng, st, _ := setup(t)
	eng.cfg = &config.Config{
		Risk:     config.Risk{High: []string{"secret/**"}},
		Autonomy: config.Autonomy{AutoMerge: []string{"low"}},
	}
	registerAgent(t, st, "mkdir -p secret && printf x > secret/key.txt")

	task := &model.Task{Title: "sneaky", RiskTier: model.RiskLow}
	if err := st.Tasks.Create(task); err != nil {
		t.Fatal(err)
	}
	eng.dispatchOnce()

	got, _ := st.Tasks.Get(task.ID)
	if got.Status != model.TaskReview {
		t.Fatalf("status = %q, want review (high-risk path escalates)", got.Status)
	}
	if got.AutoMerged {
		t.Fatal("high-risk change must not auto-merge")
	}
}

func TestRevertMarksChangeFailure(t *testing.T) {
	eng, st, _ := setup(t)
	eng.cfg = autoMergeCfg()
	registerAgent(t, st, "printf done > out.txt")

	task := &model.Task{Title: "to revert", RiskTier: model.RiskLow}
	if err := st.Tasks.Create(task); err != nil {
		t.Fatal(err)
	}
	eng.dispatchOnce()
	if got, _ := st.Tasks.Get(task.ID); got.Status != model.TaskMerged {
		t.Fatalf("precondition: want merged, got %q", got.Status)
	}

	if err := eng.Revert(task.ID); err != nil {
		t.Fatalf("Revert: %v", err)
	}
	got, _ := st.Tasks.Get(task.ID)
	if !got.Reverted {
		t.Fatal("expected Reverted = true")
	}
	// Revert is only valid on merged tasks.
	if err := eng.Revert("nonexistent"); err == nil {
		t.Error("Revert of unknown task should error")
	}
}

// Reject of an in-flight task cancels its subprocess and finalizes it closed.
func TestStopInFlightTask(t *testing.T) {
	eng, st, repo := setup(t)
	registerAgent(t, st, "sleep 30; printf done > out.txt")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(ctx)

	task := &model.Task{Title: "long runner", RiskTier: model.RiskLow}
	if err := st.Tasks.Create(task); err != nil {
		t.Fatal(err)
	}
	eng.Wake()

	waitStatus(t, st, task.ID, model.TaskRunning, 3*time.Second)
	if err := eng.Reject(task.ID, "changed my mind"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	waitStatus(t, st, task.ID, model.TaskClosed, 5*time.Second)

	if _, err := os.Stat(filepath.Join(repo, "out.txt")); !os.IsNotExist(err) {
		t.Fatal("stopped task should not have produced out.txt on main")
	}
}
