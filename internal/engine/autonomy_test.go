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

// TestAutoMergeSweepParksConflict reproduces the tight-loop bug: a review task
// whose branch conflicts with main can never auto-merge, so the sweep must park
// it after one attempt instead of re-Accepting it on every tick. Two tasks edit
// the same file; the first merges (advancing main), the second is left stale and
// conflicting.
func TestAutoMergeSweepParksConflict(t *testing.T) {
	eng, st, _ := setup(t)
	// Each worktree path is unique, so appending it makes the two branches edit
	// the same region of README with different content -> a guaranteed conflict
	// once the first has merged.
	registerAgent(t, st, "pwd >> README.md")

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

	// Merge t1 -> main advances; t2's branch (forked from the old main) is now stale.
	if err := eng.Accept(t1.ID, false); err != nil {
		t.Fatalf("accept t1: %v", err)
	}

	if err := st.Settings.Set(settingAutoMode, "on"); err != nil {
		t.Fatal(err)
	}

	// First sweep: t2 conflicts, so nothing merges and t2 is parked, still in review.
	if n := eng.sweepAutoMerge(); n != 0 {
		t.Fatalf("first sweep merged %d, want 0 (t2 conflicts)", n)
	}
	if !eng.isParkedConflict(t2.ID) {
		t.Fatal("t2 should be parked after a conflicting auto-merge")
	}
	if got, _ := st.Tasks.Get(t2.ID); got.Status != model.TaskReview {
		t.Fatalf("t2 status = %q, want review", got.Status)
	}

	// Second sweep: parked -> skipped, not re-attempted.
	if n := eng.sweepAutoMerge(); n != 0 {
		t.Fatalf("second sweep merged %d, want 0", n)
	}
	if !eng.isParkedConflict(t2.ID) {
		t.Fatal("t2 should remain parked")
	}

	// Moving t2 out of review (Retry) releases the park so a rebuilt branch can try again.
	eng.setStatusBy(t2.ID, model.TaskReady, "human", "retry")
	if eng.isParkedConflict(t2.ID) {
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
