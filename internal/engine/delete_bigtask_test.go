package engine

import (
	"testing"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/berkaycubuk/fabrika/internal/store"
)

const plannerPlan = `{"tasks":[{"title":"task1","spec":"spec1"},{"title":"task2","spec":"spec2"}],"decisions":[{"question":"Which DB?","options":["sqlite","pg"]}]}`

func registerPlanner(t *testing.T, st *store.Store) {
	t.Helper()
	ag := &model.Agent{
		Name:    "planner",
		Command: "printf '%s' '" + plannerPlan + "' > fabrika_plan.json",
		Roles:   []string{model.RolePlanner},
		Enabled: true,
	}
	if err := st.Agents.Create(ag); err != nil {
		t.Fatalf("create planner agent: %v", err)
	}
}

func TestRejectPlanDeletesEverything(t *testing.T) {
	eng, st, _ := setup(t)
	registerPlanner(t, st)

	bt := &model.BigTask{Title: "feature", Intent: "build it"}
	if err := st.BigTasks.Create(bt); err != nil {
		t.Fatal(err)
	}
	eng.planBigTask(*bt)

	p, err := st.Plans.GetByBigTask(bt.ID)
	if err != nil {
		t.Fatalf("plan not created: %v", err)
	}
	planID := p.ID

	tasks, _ := st.Tasks.ListByBigTask(bt.ID)
	if len(tasks) == 0 {
		t.Fatal("expected planned tasks")
	}
	taskIDs := make([]string, len(tasks))
	for i, tk := range tasks {
		taskIDs[i] = tk.ID
	}

	decisions, _ := st.Decisions.ListForPlan(planID)
	if len(decisions) == 0 {
		t.Fatal("expected plan decisions")
	}
	decisionIDs := make([]string, len(decisions))
	for i, d := range decisions {
		decisionIDs[i] = d.ID
	}

	if err := eng.RejectPlan(planID); err != nil {
		t.Fatalf("RejectPlan: %v", err)
	}

	// Plan must be gone.
	if _, err := st.Plans.Get(planID); err != store.ErrNotFound {
		t.Fatalf("plan still exists after rejection: %v", err)
	}
	// All planned tasks must be gone.
	for _, id := range taskIDs {
		if _, err := st.Tasks.Get(id); err != store.ErrNotFound {
			t.Fatalf("task %s still exists after rejection: %v", id, err)
		}
	}
	// All plan decisions must be gone.
	for _, id := range decisionIDs {
		if _, err := st.Decisions.Get(id); err != store.ErrNotFound {
			t.Fatalf("decision %s still exists after rejection: %v", id, err)
		}
	}
	// Big task itself must be gone.
	if _, err := st.BigTasks.Get(bt.ID); err != store.ErrNotFound {
		t.Fatalf("big task still exists after rejection: %v", err)
	}
}

func TestRejectPlanNoRePlan(t *testing.T) {
	eng, st, _ := setup(t)
	registerPlanner(t, st)

	bt := &model.BigTask{Title: "feature", Intent: "build it"}
	if err := st.BigTasks.Create(bt); err != nil {
		t.Fatal(err)
	}
	eng.planBigTask(*bt)

	p, err := st.Plans.GetByBigTask(bt.ID)
	if err != nil {
		t.Fatalf("plan not created: %v", err)
	}
	if err := eng.RejectPlan(p.ID); err != nil {
		t.Fatalf("RejectPlan: %v", err)
	}

	// A planning pass must not produce a new plan for the deleted big task.
	eng.dispatchPlanning()

	bts, _ := st.BigTasks.List()
	for _, b := range bts {
		if b.ID == bt.ID {
			t.Fatalf("big task was recreated after rejection (status=%q)", b.Status)
		}
	}
	plans, _ := st.Plans.List()
	for _, pl := range plans {
		if pl.BigTaskID == bt.ID {
			t.Fatalf("a new plan was created for the rejected big task")
		}
	}
}

func TestDeleteBigTaskRefusesRunningTask(t *testing.T) {
	eng, st, _ := setup(t)

	bt := &model.BigTask{Title: "in-flight", Intent: "running"}
	if err := st.BigTasks.Create(bt); err != nil {
		t.Fatal(err)
	}
	task := &model.Task{Title: "running task", BigTaskID: bt.ID}
	if err := st.Tasks.Create(task); err != nil {
		t.Fatal(err)
	}
	if err := st.Tasks.UpdateStatus(task.ID, model.TaskRunning); err != nil {
		t.Fatal(err)
	}

	if err := eng.DeleteBigTask(bt.ID); err == nil {
		t.Fatal("expected error when deleting big task with running tasks")
	}

	// Nothing must have been deleted.
	if _, err := st.BigTasks.Get(bt.ID); err != nil {
		t.Fatalf("big task must still exist: %v", err)
	}
	if _, err := st.Tasks.Get(task.ID); err != nil {
		t.Fatalf("task must still exist: %v", err)
	}
}
