package store

import (
	"path/filepath"
	"testing"

	"github.com/berkaycubuk/fabrika/internal/model"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "global"), filepath.Join(dir, "project"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestAgentRoundTrip(t *testing.T) {
	s := openTest(t)

	a := &model.Agent{
		Name:        "Claude Code",
		Command:     "claude --prompt {prompt_file} --cwd {worktree} --model {model}",
		Model:       "claude-sonnet-4-6",
		Roles:       []string{model.RoleImplementer, model.RolePlanner},
		Tags:        []string{"go", "frontend"},
		Concurrency: 2,
		Timeout:     "20m",
		MaxAttempts: 3,
		Enabled:     true,
	}
	if err := s.Agents.Create(a); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if a.ID == "" {
		t.Fatal("Create should assign an ID")
	}

	got, err := s.Agents.Get(a.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != a.Name || got.Command != a.Command {
		t.Fatalf("mismatch: %+v", got)
	}
	if got.Model != "claude-sonnet-4-6" {
		t.Fatalf("model not round-tripped: %q", got.Model)
	}
	if len(got.Roles) != 2 || got.Roles[0] != model.RoleImplementer {
		t.Fatalf("roles = %v", got.Roles)
	}
	if len(got.Tags) != 2 || !got.Enabled || got.Concurrency != 2 {
		t.Fatalf("fields mismatch: %+v", got)
	}

	// Disable then verify.
	if err := s.Agents.SetEnabled(a.ID, false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	got, _ = s.Agents.Get(a.ID)
	if got.Enabled {
		t.Fatal("agent should be disabled")
	}

	// Update.
	a.Name = "Claude Code v2"
	a.Model = "claude-opus-4-8"
	a.Enabled = true
	if err := s.Agents.Update(a); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ = s.Agents.Get(a.ID)
	if got.Name != "Claude Code v2" || !got.Enabled || got.Model != "claude-opus-4-8" {
		t.Fatalf("update not applied: %+v", got)
	}

	list, err := s.Agents.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %v (err %v)", list, err)
	}

	if err := s.Agents.Delete(a.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Agents.Get(a.ID); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestBigTaskErrorRoundTrip(t *testing.T) {
	s := openTest(t)

	bt := &model.BigTask{Title: "Ship login", Intent: "y"}
	if err := s.BigTasks.Create(bt); err != nil {
		t.Fatalf("create: %v", err)
	}

	// A failure is recorded as status 'error' + a reason, and survives a reload.
	if err := s.BigTasks.SetError(bt.ID, "repo has no commits yet"); err != nil {
		t.Fatalf("set error: %v", err)
	}
	got, err := s.BigTasks.Get(bt.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != model.BigTaskError || got.Error != "repo has no commits yet" {
		t.Fatalf("after SetError = %q / %q", got.Status, got.Error)
	}

	// A successful transition supersedes the failure: the reason is cleared.
	if err := s.BigTasks.UpdateStatus(bt.ID, model.BigTaskPlanned); err != nil {
		t.Fatalf("update status: %v", err)
	}
	got, _ = s.BigTasks.Get(bt.ID)
	if got.Status != model.BigTaskPlanned || got.Error != "" {
		t.Fatalf("after UpdateStatus = %q / %q (error should be cleared)", got.Status, got.Error)
	}
}

func TestTaskRoundTrip(t *testing.T) {
	s := openTest(t)

	task := &model.Task{
		Title: "Add health endpoint",
		Spec:  "Expose GET /healthz returning 200",
		Acceptance: model.Contract{
			VerifyCmds:  []string{"go test ./..."},
			LockedGlobs: []string{"**/*_test.go"},
		},
		DependsOn:  []string{"x"},
		TouchPaths: []string{"internal/api"},
		Tags:       []string{"go"},
	}
	if err := s.Tasks.Create(task); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if task.Status != model.TaskReady || task.RiskTier != model.RiskLow {
		t.Fatalf("defaults not applied: %+v", task)
	}

	got, err := s.Tasks.Get(task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != task.Title || got.Spec != task.Spec {
		t.Fatalf("mismatch: %+v", got)
	}
	if len(got.Acceptance.VerifyCmds) != 1 || got.Acceptance.VerifyCmds[0] != "go test ./..." {
		t.Fatalf("acceptance not preserved: %+v", got.Acceptance)
	}
	if len(got.Acceptance.LockedGlobs) != 1 {
		t.Fatalf("locked globs not preserved: %+v", got.Acceptance)
	}
	if len(got.TouchPaths) != 1 || got.TouchPaths[0] != "internal/api" {
		t.Fatalf("touch paths = %v", got.TouchPaths)
	}

	if err := s.Tasks.UpdateStatus(task.ID, model.TaskRunning); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	got, _ = s.Tasks.Get(task.ID)
	if got.Status != model.TaskRunning {
		t.Fatalf("status = %q", got.Status)
	}
}

func TestAttemptRoundTrip(t *testing.T) {
	s := openTest(t)

	task := &model.Task{Title: "t"}
	if err := s.Tasks.Create(task); err != nil {
		t.Fatalf("Create task: %v", err)
	}

	if _, err := s.Attempts.LatestForTask(task.ID); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	a1 := &model.Attempt{
		TaskID:  task.ID,
		AgentID: "agent-1",
		Result:  model.ResultFail,
		Evidence: model.Evidence{
			Stages: map[string]model.StageResult{"build": {Pass: false, Output: "boom", ExitCode: 1}},
			Diff:   "diff --git a b",
		},
		Log: "first try",
	}
	if err := s.Attempts.Create(a1); err != nil {
		t.Fatalf("Create attempt: %v", err)
	}
	a2 := &model.Attempt{TaskID: task.ID, AgentID: "agent-1", Result: model.ResultPass}
	if err := s.Attempts.Create(a2); err != nil {
		t.Fatalf("Create attempt 2: %v", err)
	}

	latest, err := s.Attempts.LatestForTask(task.ID)
	if err != nil {
		t.Fatalf("LatestForTask: %v", err)
	}
	if latest.Result != model.ResultPass {
		t.Fatalf("latest result = %q, want pass", latest.Result)
	}

	all, err := s.Attempts.ListForTask(task.ID)
	if err != nil || len(all) != 2 {
		t.Fatalf("ListForTask = %d (err %v)", len(all), err)
	}
	// Evidence preserved on the first attempt.
	if all[1].Evidence.Stages["build"].Output != "boom" {
		t.Fatalf("evidence not preserved: %+v", all[1].Evidence)
	}
}

func TestTaskSetRun(t *testing.T) {
	s := openTest(t)
	task := &model.Task{Title: "t"}
	if err := s.Tasks.Create(task); err != nil {
		t.Fatal(err)
	}
	if err := s.Tasks.SetRun(task.ID, "agent-9", "fabrika/task-abc", model.TaskRunning); err != nil {
		t.Fatalf("SetRun: %v", err)
	}
	got, _ := s.Tasks.Get(task.ID)
	if got.AgentID != "agent-9" || got.Branch != "fabrika/task-abc" || got.Status != model.TaskRunning {
		t.Fatalf("SetRun not applied: %+v", got)
	}
}

func TestBigTaskDelete(t *testing.T) {
	s := openTest(t)

	if err := s.BigTasks.Delete("nope"); err != ErrNotFound {
		t.Fatalf("Delete missing = %v, want ErrNotFound", err)
	}

	bt := &model.BigTask{Title: "t", Intent: "y"}
	if err := s.BigTasks.Create(bt); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.BigTasks.Delete(bt.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.BigTasks.Get(bt.ID); err != ErrNotFound {
		t.Fatalf("Get after Delete = %v, want ErrNotFound", err)
	}
}

func TestDeleteByBigTask(t *testing.T) {
	s := openTest(t)

	bt := &model.BigTask{Title: "Ship login", Intent: "y"}
	if err := s.BigTasks.Create(bt); err != nil {
		t.Fatalf("Create big task: %v", err)
	}
	plan := &model.Plan{BigTaskID: bt.ID}
	if err := s.Plans.Create(plan); err != nil {
		t.Fatalf("Create plan: %v", err)
	}
	task := &model.Task{BigTaskID: bt.ID, Title: "do it"}
	if err := s.Tasks.Create(task); err != nil {
		t.Fatalf("Create task: %v", err)
	}
	dec := &model.Decision{PlanID: plan.ID, Question: "which?", Options: []string{"a", "b"}}
	if err := s.Decisions.Create(dec); err != nil {
		t.Fatalf("Create decision: %v", err)
	}

	// Deleting an unrelated big task removes nothing and is not an error.
	if err := s.Decisions.DeleteByBigTask("other"); err != nil {
		t.Fatalf("DeleteByBigTask decisions (no-op): %v", err)
	}
	if err := s.Plans.DeleteByBigTask("other"); err != nil {
		t.Fatalf("DeleteByBigTask plans (no-op): %v", err)
	}
	if err := s.Tasks.DeleteByBigTask("other"); err != nil {
		t.Fatalf("DeleteByBigTask tasks (no-op): %v", err)
	}
	if _, err := s.Plans.Get(plan.ID); err != nil {
		t.Fatalf("plan should survive unrelated delete: %v", err)
	}

	// Now delete everything under the big task.
	if err := s.Decisions.DeleteByBigTask(bt.ID); err != nil {
		t.Fatalf("DeleteByBigTask decisions: %v", err)
	}
	if err := s.Plans.DeleteByBigTask(bt.ID); err != nil {
		t.Fatalf("DeleteByBigTask plans: %v", err)
	}
	if err := s.Tasks.DeleteByBigTask(bt.ID); err != nil {
		t.Fatalf("DeleteByBigTask tasks: %v", err)
	}
	if err := s.BigTasks.Delete(bt.ID); err != nil {
		t.Fatalf("Delete big task: %v", err)
	}

	if _, err := s.Decisions.Get(dec.ID); err != ErrNotFound {
		t.Fatalf("decision Get = %v, want ErrNotFound", err)
	}
	if _, err := s.Plans.Get(plan.ID); err != ErrNotFound {
		t.Fatalf("plan Get = %v, want ErrNotFound", err)
	}
	if _, err := s.Tasks.Get(task.ID); err != ErrNotFound {
		t.Fatalf("task Get = %v, want ErrNotFound", err)
	}
	if _, err := s.BigTasks.Get(bt.ID); err != ErrNotFound {
		t.Fatalf("big task Get = %v, want ErrNotFound", err)
	}
	if list, err := s.Tasks.ListByBigTask(bt.ID); err != nil || len(list) != 0 {
		t.Fatalf("ListByBigTask = %v (err %v), want empty", list, err)
	}
	if list, err := s.Decisions.ListForPlan(plan.ID); err != nil || len(list) != 0 {
		t.Fatalf("ListForPlan = %v (err %v), want empty", list, err)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	s := openTest(t)

	v, err := s.Settings.Get("missing")
	if err != nil || v != "" {
		t.Fatalf("unset key = %q (err %v)", v, err)
	}
	if err := s.Settings.Set("wip_cap", "4"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Settings.Set("wip_cap", "8"); err != nil {
		t.Fatalf("Set (upsert): %v", err)
	}
	v, _ = s.Settings.Get("wip_cap")
	if v != "8" {
		t.Fatalf("wip_cap = %q, want 8", v)
	}
	all, _ := s.Settings.All()
	if all["wip_cap"] != "8" {
		t.Fatalf("All = %v", all)
	}
}
