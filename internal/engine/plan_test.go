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

func TestPlanDecisionAnswerPropagatesToTasks(t *testing.T) {
	eng, st, _ := setup(t)

	plan := `{"tasks":[` +
		`{"title":"Schema","spec":"tables"},` +
		`{"title":"API","spec":"endpoints"}` +
		`],"decisions":[` +
		`{"question":"Which DB?","options":["sqlite","pg"]},` +
		`{"question":"Auth scheme?","options":["jwt","session"]}` +
		`]}`
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

	p, err := st.Plans.GetByBigTask(bt.ID)
	if err != nil {
		t.Fatalf("plan not created: %v", err)
	}
	ds, _ := st.Decisions.ListForPlan(p.ID)
	if len(ds) != 2 {
		t.Fatalf("expected 2 plan decisions, got %d", len(ds))
	}

	// Answer before approval: every planned task's spec carries the resolution.
	if err := eng.AnswerDecision(ds[0].ID, "sqlite", false); err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}
	tasks, _ := st.Tasks.ListByBigTask(bt.ID)
	for _, tk := range tasks {
		if !strings.Contains(tk.Spec, ds[0].Question) || !strings.Contains(tk.Spec, "sqlite") {
			t.Fatalf("task %q spec missing pre-approval resolution: %q", tk.Title, tk.Spec)
		}
	}

	// Answer after approval: ready (not yet dispatched) tasks get it too.
	if err := eng.ApprovePlan(p.ID); err != nil {
		t.Fatalf("ApprovePlan: %v", err)
	}
	if err := eng.AnswerDecision(ds[1].ID, "jwt", false); err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}
	tasks, _ = st.Tasks.ListByBigTask(bt.ID)
	for _, tk := range tasks {
		if !strings.Contains(tk.Spec, ds[1].Question) || !strings.Contains(tk.Spec, "jwt") {
			t.Fatalf("task %q spec missing post-approval resolution: %q", tk.Title, tk.Spec)
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

// A plan whose held-out check references a file that neither exists, is
// authored in heldOutFiles, nor is in touchPaths is rejected, and the planner
// gets ONE repair attempt with the violations fed back. Here the second
// attempt authors the file, so the plan lands.
func TestPlanRetryRepairsUnsatisfiableHeldOut(t *testing.T) {
	eng, st, _ := setup(t)

	bad := `{"tasks":[{"title":"T","spec":"s","acceptance":{` +
		`"heldOut":["node --test test/heldout/x.heldout.test.ts"]}}]}`
	good := `{"tasks":[{"title":"T","spec":"s","acceptance":{` +
		`"heldOut":["node --test test/heldout/x.heldout.test.ts"],` +
		`"heldOutFiles":{"test/heldout/x.heldout.test.ts":"// hidden"}}}]}`
	ag := &model.Agent{
		Name: "planner",
		Command: "if [ -f .second ]; then printf '%s' '" + good + "' > fabrika_plan.json; " +
			"else touch .second; printf '%s' '" + bad + "' > fabrika_plan.json; fi",
		Roles:   []string{model.RolePlanner},
		Enabled: true,
	}
	if err := st.Agents.Create(ag); err != nil {
		t.Fatal(err)
	}
	bt := &model.BigTask{Title: "ship", Intent: "do"}
	if err := st.BigTasks.Create(bt); err != nil {
		t.Fatal(err)
	}

	eng.planBigTask(*bt)

	got, _ := st.BigTasks.Get(bt.ID)
	if got.Status != model.BigTaskPlanned {
		t.Fatalf("bigtask status = %q (error %q), want planned after repair", got.Status, got.Error)
	}
	tasks, _ := st.Tasks.ListByBigTask(bt.ID)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if _, ok := tasks[0].Acceptance.HeldOutFiles["test/heldout/x.heldout.test.ts"]; !ok {
		t.Fatalf("repaired plan should carry heldOutFiles, got %+v", tasks[0].Acceptance)
	}
}

// If the repair attempt still references a held-out file it never authored,
// the big task fails at PLAN time with an actionable reason — no doomed task
// is ever persisted for an implementer to burn tokens on.
func TestPlanRejectedWhenHeldOutStaysUnsatisfiable(t *testing.T) {
	eng, st, _ := setup(t)

	bad := `{"tasks":[{"title":"T","spec":"s","acceptance":{` +
		`"heldOut":["node --test test/heldout/x.heldout.test.ts"]}}]}`
	ag := &model.Agent{
		Name:    "planner",
		Command: "printf '%s' '" + bad + "' > fabrika_plan.json",
		Roles:   []string{model.RolePlanner},
		Enabled: true,
	}
	if err := st.Agents.Create(ag); err != nil {
		t.Fatal(err)
	}
	bt := &model.BigTask{Title: "ship", Intent: "do"}
	if err := st.BigTasks.Create(bt); err != nil {
		t.Fatal(err)
	}

	eng.planBigTask(*bt)

	got, _ := st.BigTasks.Get(bt.ID)
	if got.Status != "error" {
		t.Fatalf("bigtask status = %q, want error", got.Status)
	}
	if !strings.Contains(got.Error, "test/heldout/x.heldout.test.ts") {
		t.Fatalf("error not actionable: %q", got.Error)
	}
	tasks, _ := st.Tasks.ListByBigTask(bt.ID)
	if len(tasks) != 0 {
		t.Fatalf("rejected plan must persist no tasks, got %d", len(tasks))
	}
}

// Backstop for tasks that already carry an unsatisfiable held-out contract
// (e.g. persisted before validation existed): dispatch fails them as a plan
// defect BEFORE running the implementer, so no agent tokens are spent on work
// that can only ever gate red.
func TestDispatchFailsFastOnUnsatisfiableHeldOut(t *testing.T) {
	eng, st, _ := setup(t)
	registerAgent(t, st, "echo AGENT_RAN")

	task := &model.Task{
		Title: "doomed",
		Acceptance: model.Contract{
			HeldOut:     []string{"node --test test/heldout/missing.heldout.test.ts"},
			LockedGlobs: []string{"test/heldout/**"},
		},
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
	att, err := st.Attempts.LatestForTask(task.ID)
	if err != nil {
		t.Fatalf("attempt: %v", err)
	}
	if att.Evidence.Stages["contract"].Pass {
		t.Fatalf("expected failing contract stage: %+v", att.Evidence.Stages)
	}
	if !strings.Contains(att.Log, "plan defect") || !strings.Contains(att.Log, "missing.heldout.test.ts") {
		t.Fatalf("attempt log not actionable: %q", att.Log)
	}
	if strings.Contains(att.Log, "AGENT_RAN") {
		t.Fatal("implementer ran despite an unsatisfiable contract")
	}
}
