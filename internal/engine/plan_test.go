package engine

import (
	"strings"
	"testing"

	"github.com/berkaycubuk/fabrika/internal/model"
)

func TestPlanBigTaskProducesProposedPlan(t *testing.T) {
	eng, st, _ := setup(t)

	plan := `{"tasks":[` +
		`{"title":"Schema","spec":"tables","acceptance":{"verifyCmds":["true"]}},` +
		`{"title":"API","spec":"endpoints","dependsOn":["Schema"]}` +
		`],"decisions":[{"question":"Which DB?","options":["sqlite","pg"]}]}`
	ag := &model.Agent{
		Name:    "planner",
		Command: "printf '%s' '" + plan + "' > fabrika_plan.json",
		Roles:   []string{model.RolePlanner},
		Enabled: true,
	}
	if err := st.Agents.Create(ag); err != nil {
		t.Fatal(err)
	}

	bt := &model.BigTask{Title: "ship feature", Intent: "do the thing"}
	if err := st.BigTasks.Create(bt); err != nil {
		t.Fatal(err)
	}

	eng.planBigTask(*bt)

	// A proposed plan with two planned tasks + one open decision should exist.
	p, err := st.Plans.GetByBigTask(bt.ID)
	if err != nil {
		t.Fatalf("plan not created: %v", err)
	}
	if p.Status != model.PlanProposed {
		t.Fatalf("plan status = %q", p.Status)
	}
	tasks, _ := st.Tasks.ListByBigTask(bt.ID)
	if len(tasks) != 2 {
		t.Fatalf("expected 2 planned tasks, got %d", len(tasks))
	}
	for _, tk := range tasks {
		if tk.Status != model.TaskPlanned {
			t.Fatalf("task %q status = %q, want planned", tk.Title, tk.Status)
		}
	}
	decisions, _ := st.Decisions.ListForPlan(p.ID)
	if len(decisions) != 1 {
		t.Fatalf("expected 1 plan decision, got %d", len(decisions))
	}
	got, _ := st.BigTasks.Get(bt.ID)
	if got.Status != model.BigTaskPlanned {
		t.Fatalf("bigtask status = %q, want planned", got.Status)
	}

	// Approving the plan promotes planned tasks to ready.
	if err := eng.ApprovePlan(p.ID); err != nil {
		t.Fatalf("ApprovePlan: %v", err)
	}
	tasks, _ = st.Tasks.ListByBigTask(bt.ID)
	for _, tk := range tasks {
		if tk.Status != model.TaskReady {
			t.Fatalf("after approve, task %q = %q, want ready", tk.Title, tk.Status)
		}
	}
}

func TestEscalationCreatesDecisionAndAnswerResumes(t *testing.T) {
	eng, st, _ := setup(t)
	registerAgent(t, st, `echo 'fabrika_DECISION: {"question":"which db?","options":["a","b"]}'`)

	task := &model.Task{Title: "ambiguous", Spec: "original spec"}
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
	ds, _ := st.Decisions.ListForTask(task.ID)
	if len(ds) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(ds))
	}
	if ds[0].Question != "which db?" || ds[0].Status != model.DecisionOpen {
		t.Fatalf("decision = %+v", ds[0])
	}

	// Answer + promote -> task resumes to ready, spec carries the resolution,
	// and a convention is created.
	if err := eng.AnswerDecision(ds[0].ID, "use sqlite", true); err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}
	got, _ = st.Tasks.Get(task.ID)
	if got.Status != model.TaskReady {
		t.Fatalf("after answer, status = %q, want ready", got.Status)
	}
	if !strings.Contains(got.Spec, "use sqlite") || !strings.Contains(got.Spec, "original spec") {
		t.Fatalf("resumed spec missing resolution: %q", got.Spec)
	}
	convs, _ := st.Conventions.List()
	if len(convs) != 1 {
		t.Fatalf("expected 1 promoted convention, got %d", len(convs))
	}
	// Decision is now answered (out of the open queue).
	open, _ := st.Decisions.ListOpen()
	if len(open) != 0 {
		t.Fatalf("expected empty open queue, got %d", len(open))
	}
}

func TestLockedGlobViolationFailsTask(t *testing.T) {
	eng, st, _ := setup(t)
	// Agent edits a protected file it was told not to touch.
	registerAgent(t, st, "printf 'tampered' > secret_test.go")

	task := &model.Task{
		Title:      "tamperer",
		Acceptance: model.Contract{LockedGlobs: []string{"**/*_test.go"}},
	}
	if err := st.Tasks.Create(task); err != nil {
		t.Fatal(err)
	}
	if !eng.dispatchOnce() {
		t.Fatal("expected dispatch")
	}
	got, _ := st.Tasks.Get(task.ID)
	if got.Status != model.TaskFailed {
		t.Fatalf("status = %q, want failed (locked glob touched)", got.Status)
	}
	att, _ := st.Attempts.LatestForTask(task.ID)
	if att.Result != model.ResultFail {
		t.Fatalf("result = %q, want fail", att.Result)
	}
	if att.Evidence.Stages["locked"].Pass {
		t.Fatalf("expected a failing locked stage: %+v", att.Evidence.Stages)
	}
}

func TestHeldOutChecksRunInGate(t *testing.T) {
	eng, st, _ := setup(t)
	registerAgent(t, st, "printf 'x' > out.txt")

	// Held-out check fails even though there are no visible verify commands.
	task := &model.Task{
		Title:      "held out fails",
		Acceptance: model.Contract{HeldOut: []string{"exit 3"}},
	}
	if err := st.Tasks.Create(task); err != nil {
		t.Fatal(err)
	}
	if !eng.dispatchOnce() {
		t.Fatal("expected dispatch")
	}
	got, _ := st.Tasks.Get(task.ID)
	if got.Status != model.TaskFailed {
		t.Fatalf("status = %q, want failed (held-out check failed)", got.Status)
	}
}
