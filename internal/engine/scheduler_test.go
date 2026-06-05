package engine

import (
	"testing"
	"time"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/berkaycubuk/fabrika/internal/store"
)

// makeAgent creates an enabled implementer with the given concurrency + tags and
// returns its ID. (registerAgent in engine_test.go always uses concurrency 1.)
func makeAgent(t *testing.T, st *store.Store, name string, concurrency int, tags ...string) string {
	t.Helper()
	a := &model.Agent{
		Name:        name,
		Command:     "printf 'done' > out-" + name + ".txt",
		Roles:       []string{model.RoleImplementer},
		Tags:        tags,
		Concurrency: concurrency,
		Enabled:     true,
	}
	if err := st.Agents.Create(a); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return a.ID
}

func mkTask(t *testing.T, st *store.Store, title string, mod func(*model.Task)) string {
	t.Helper()
	task := &model.Task{Title: title}
	if mod != nil {
		mod(task)
	}
	if err := st.Tasks.Create(task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	return task.ID
}

// claimID is a test helper: claim one task and report (taskID, agentID, ok)
// without running it, so scheduler placement can be asserted deterministically.
func (e *Engine) claimID() (string, string, bool) {
	task, ag, _, _, ok := e.claim()
	return task.ID, ag.ID, ok
}

func TestParallelDispatchAcrossAgents(t *testing.T) {
	eng, st, _ := setup(t)
	a1 := makeAgent(t, st, "alpha", 1)
	a2 := makeAgent(t, st, "bravo", 1)
	mkTask(t, st, "t1", nil)
	mkTask(t, st, "t2", nil)

	_, g1, ok1 := eng.claimID()
	_, g2, ok2 := eng.claimID()
	if !ok1 || !ok2 {
		t.Fatalf("expected both tasks claimed, got %v %v", ok1, ok2)
	}
	if g1 == g2 {
		t.Fatalf("both tasks went to the same agent %q; expected spread across %s/%s", g1, a1, a2)
	}
	// No third task -> nothing left to claim.
	if _, _, ok3 := eng.claimID(); ok3 {
		t.Fatal("claimed a third task that does not exist")
	}
}

func TestConcurrencyLimitHoldsSecondTask(t *testing.T) {
	eng, st, _ := setup(t)
	makeAgent(t, st, "solo", 1)
	mkTask(t, st, "t1", nil)
	mkTask(t, st, "t2", nil)

	if _, _, ok := eng.claimID(); !ok {
		t.Fatal("first task should claim")
	}
	if _, _, ok := eng.claimID(); ok {
		t.Fatal("second task should wait: the only agent's single slot is busy")
	}
}

func TestConcurrencyTwoRunsBoth(t *testing.T) {
	eng, st, _ := setup(t)
	makeAgent(t, st, "wide", 2)
	mkTask(t, st, "t1", nil)
	mkTask(t, st, "t2", nil)

	if _, _, ok := eng.claimID(); !ok {
		t.Fatal("first claim")
	}
	if _, _, ok := eng.claimID(); !ok {
		t.Fatal("second task should claim: agent has 2 slots")
	}
}

func TestWIPCapLimitsGlobally(t *testing.T) {
	eng, st, _ := setup(t)
	if err := st.Settings.Set(settingWIPCap, "1"); err != nil {
		t.Fatal(err)
	}
	makeAgent(t, st, "a", 5)
	makeAgent(t, st, "b", 5)
	mkTask(t, st, "t1", nil)
	mkTask(t, st, "t2", nil)

	if _, _, ok := eng.claimID(); !ok {
		t.Fatal("first claim")
	}
	if _, _, ok := eng.claimID(); ok {
		t.Fatal("WIP cap of 1 should block the second task despite free agent slots")
	}
}

func TestTouchPathCollisionAvoided(t *testing.T) {
	eng, st, _ := setup(t)
	makeAgent(t, st, "wide", 2) // 2 slots, so collision (not slots) is the limiter
	mkTask(t, st, "t1", func(t *model.Task) { t.TouchPaths = []string{"src/api"} })
	mkTask(t, st, "t2", func(t *model.Task) { t.TouchPaths = []string{"src/api/users.go"} })

	if _, _, ok := eng.claimID(); !ok {
		t.Fatal("first claim")
	}
	if _, _, ok := eng.claimID(); ok {
		t.Fatal("second task collides on src/api and should wait")
	}
}

func TestNonCollidingPathsRunTogether(t *testing.T) {
	eng, st, _ := setup(t)
	makeAgent(t, st, "wide", 2)
	mkTask(t, st, "t1", func(t *model.Task) { t.TouchPaths = []string{"src/api"} })
	mkTask(t, st, "t2", func(t *model.Task) { t.TouchPaths = []string{"src/web"} })

	if _, _, ok := eng.claimID(); !ok {
		t.Fatal("first claim")
	}
	if _, _, ok := eng.claimID(); !ok {
		t.Fatal("disjoint paths should run concurrently")
	}
}

func TestDependencyGating(t *testing.T) {
	eng, st, _ := setup(t)
	makeAgent(t, st, "wide", 5)
	aID := mkTask(t, st, "base", nil)
	mkTask(t, st, "dependent", func(t *model.Task) { t.DependsOn = []string{aID} })

	// Only the base task is claimable; the dependent waits for it to merge.
	id1, _, ok := eng.claimID()
	if !ok || id1 != aID {
		t.Fatalf("expected base task claimed first, got id=%q ok=%v", id1, ok)
	}
	if _, _, ok := eng.claimID(); ok {
		t.Fatal("dependent task ran before its dependency merged")
	}

	// Once the base merges and frees its slot, the dependent becomes claimable.
	if err := st.Tasks.UpdateStatus(aID, model.TaskMerged); err != nil {
		t.Fatal(err)
	}
	eng.markDone(aID)
	if _, _, ok := eng.claimID(); !ok {
		t.Fatal("dependent should claim after its dependency merged")
	}
}

// TestLoopDrainsManyTasksInParallel exercises the live async path end to end:
// Start the loop, drop several tasks across two multi-slot agents, and confirm
// they all reach review without manual dispatchOnce calls.
func TestLoopDrainsManyTasksInParallel(t *testing.T) {
	eng, st, _ := setup(t)
	makeAgent(t, st, "a", 2)
	makeAgent(t, st, "b", 2)

	const n = 5
	ids := make([]string, n)
	for i := range ids {
		ids[i] = mkTask(t, st, "task", nil)
	}

	eng.Start(t.Context())
	eng.Wake()

	deadline := time.After(15 * time.Second)
	for {
		done := 0
		for _, id := range ids {
			tk, err := st.Tasks.Get(id)
			if err != nil {
				t.Fatal(err)
			}
			if tk.Status == model.TaskReview {
				done++
			}
		}
		if done == n {
			return // all tasks shipped to the review queue
		}
		select {
		case <-deadline:
			t.Fatalf("only %d/%d tasks reached review before timeout", done, n)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func TestPinnedAgentWaitsWhenBusy(t *testing.T) {
	eng, st, _ := setup(t)
	makeAgent(t, st, "free-pool", 5)         // plenty of generic capacity
	pin := makeAgent(t, st, "specialist", 1) // single slot, both tasks pinned to it
	mkTask(t, st, "t1", func(t *model.Task) { t.PreferredAgentID = pin })
	mkTask(t, st, "t2", func(t *model.Task) { t.PreferredAgentID = pin })

	id1, g1, ok := eng.claimID()
	if !ok || g1 != pin {
		t.Fatalf("first pinned task should run on specialist, got id=%q agent=%q", id1, g1)
	}
	if _, _, ok := eng.claimID(); ok {
		t.Fatal("second pinned task must wait for the specialist, not spill to the pool")
	}
}
