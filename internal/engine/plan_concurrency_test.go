package engine

import (
	"testing"
	"time"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/berkaycubuk/fabrika/internal/store"
)

// validPlan is a minimal planner output: one planned task with a trivially-green
// acceptance check, enough for persistPlan to flip the big task to `planned`.
const validPlan = `{"tasks":[{"title":"t","spec":"s","acceptance":{"verifyCmds":["true"]}}]}`

// makePlanner registers an enabled planner whose command BLOCKS (sleep) before
// emitting a valid plan, so concurrent planning runs overlap long enough to be
// observed in the DB. Mirrors the blocking-command idea from scheduler_test.go.
func makePlanner(t *testing.T, st *store.Store, name string, concurrency int) string {
	t.Helper()
	a := &model.Agent{
		Name:        name,
		Command:     "sleep 0.5; printf '%s' '" + validPlan + "' > fabrika_plan.json",
		Roles:       []string{model.RolePlanner},
		Concurrency: concurrency,
		Enabled:     true,
	}
	if err := st.Agents.Create(a); err != nil {
		t.Fatalf("create planner: %v", err)
	}
	return a.ID
}

func mkBigTask(t *testing.T, st *store.Store, title string) string {
	t.Helper()
	bt := &model.BigTask{Title: title, Intent: "do " + title}
	if err := st.BigTasks.Create(bt); err != nil {
		t.Fatalf("create bigtask: %v", err)
	}
	return bt.ID
}

// TestPlanningConcurrencyCapOne proves a planner with Concurrency 1 never runs
// two planning runs at once: the second big task waits in `draft` until the
// first is planned, and both eventually reach `planned` in FIFO (oldest-first)
// order.
func TestPlanningConcurrencyCapOne(t *testing.T) {
	eng, st, _ := setup(t)
	makePlanner(t, st, "planner", 1)

	// Created oldest-first: alpha before bravo. FIFO must plan alpha first.
	alpha := mkBigTask(t, st, "alpha")
	bravo := mkBigTask(t, st, "bravo")

	eng.Start(t.Context())
	eng.Wake()

	deadline := time.After(20 * time.Second)
	for {
		var aSt, bSt string
		planning := 0
		bts, _ := st.BigTasks.List()
		for _, bt := range bts {
			if bt.Status == model.BigTaskPlanning {
				planning++
			}
			switch bt.ID {
			case alpha:
				aSt = bt.Status
			case bravo:
				bSt = bt.Status
			}
		}
		// The cap: at most one big task is ever in `planning` at a time.
		if planning > 1 {
			t.Fatalf("planner Concurrency 1 ran %d planning runs at once", planning)
		}
		// FIFO: bravo must not begin planning until alpha has finished (planned),
		// and while alpha is unplanned bravo must still be `draft`, not `planning`.
		if bSt == model.BigTaskPlanning && aSt != model.BigTaskPlanned {
			t.Fatalf("bravo started planning before alpha finished (alpha=%q): FIFO violated", aSt)
		}
		if aSt == model.BigTaskPlanned && bSt == model.BigTaskPlanned {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("big tasks did not both reach planned (alpha=%q bravo=%q)", aSt, bSt)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestPlanningConcurrencyCapTwo proves a planner with Concurrency 2 runs exactly
// two planning runs concurrently — never three — with the third big task waiting
// in `draft`, and that all three eventually reach `planned`.
func TestPlanningConcurrencyCapTwo(t *testing.T) {
	eng, st, _ := setup(t)
	makePlanner(t, st, "planner", 2)

	mkBigTask(t, st, "a")
	mkBigTask(t, st, "b")
	mkBigTask(t, st, "c")

	eng.Start(t.Context())
	eng.Wake()

	maxConcurrent := 0
	deadline := time.After(20 * time.Second)
	for {
		planning, planned := 0, 0
		bts, _ := st.BigTasks.List()
		for _, bt := range bts {
			switch bt.Status {
			case model.BigTaskPlanning:
				planning++
			case model.BigTaskPlanned:
				planned++
			}
		}
		if planning > 2 {
			t.Fatalf("planner Concurrency 2 ran %d planning runs at once", planning)
		}
		if planning > maxConcurrent {
			maxConcurrent = planning
		}
		if planned == 3 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("not all 3 big tasks planned (planned=%d)", planned)
		case <-time.After(5 * time.Millisecond):
		}
	}
	// The two free slots must actually be used in parallel, not serialized.
	if maxConcurrent < 2 {
		t.Fatalf("expected to observe 2 concurrent planning runs, max seen = %d", maxConcurrent)
	}
}
