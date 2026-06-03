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
		Command:     "claude --prompt {prompt_file} --cwd {worktree}",
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
	a.Enabled = true
	if err := s.Agents.Update(a); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ = s.Agents.Get(a.ID)
	if got.Name != "Claude Code v2" || !got.Enabled {
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
