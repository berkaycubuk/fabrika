package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/berkaycubuk/fabrika/internal/config"
	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/berkaycubuk/fabrika/internal/store"
)

// initRepo makes dir a git repo with one commit, so createBigTask's preflight
// (which requires a resolvable HEAD for worktrees) passes.
func initRepo(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "init")
}

func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	_, h := newTestServerWithStore(t)
	return h
}

func newTestServerWithStore(t *testing.T) (*store.Store, http.Handler) {
	t.Helper()
	dir := t.TempDir()
	initRepo(t, dir)
	s, err := store.Open(filepath.Join(dir, "g"), filepath.Join(dir, "p"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	srv := NewServer(s, &config.Config{}, dir, nil)
	srv.Start(context.Background())
	return s, srv.Handler()
}

func do(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAgentCRUDOverHTTP(t *testing.T) {
	h := newTestServer(t)

	// Empty list.
	rec := do(t, h, "GET", "/api/agents", nil)
	if rec.Code != 200 || rec.Body.String() != "[]\n" {
		t.Fatalf("empty list: %d %q", rec.Code, rec.Body.String())
	}

	// Create.
	rec = do(t, h, "POST", "/api/agents", model.Agent{
		Name:    "Claude Code",
		Command: "claude {prompt_file} {worktree}",
		Roles:   []string{"implementer"},
		Enabled: true,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created model.Agent
	json.Unmarshal(rec.Body.Bytes(), &created)
	if created.ID == "" {
		t.Fatal("expected assigned ID")
	}

	// Validation: missing command.
	rec = do(t, h, "POST", "/api/agents", model.Agent{Name: "x"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}

	// Disable.
	rec = do(t, h, "POST", "/api/agents/"+created.ID+"/disable", nil)
	if rec.Code != 200 {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body.String())
	}
	var disabled model.Agent
	json.Unmarshal(rec.Body.Bytes(), &disabled)
	if disabled.Enabled {
		t.Fatal("agent should be disabled")
	}

	// Delete.
	rec = do(t, h, "DELETE", "/api/agents/"+created.ID, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	rec = do(t, h, "DELETE", "/api/agents/"+created.ID, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on second delete, got %d", rec.Code)
	}
}

func TestTaskCreateOverHTTP(t *testing.T) {
	h := newTestServer(t)

	rec := do(t, h, "POST", "/api/tasks", model.Task{
		Title: "Add healthz",
		Spec:  "GET /healthz -> 200",
		Acceptance: model.Contract{
			VerifyCmds: []string{"go test ./..."},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create task: %d %s", rec.Code, rec.Body.String())
	}

	rec = do(t, h, "GET", "/api/tasks", nil)
	var tasks []model.Task
	json.Unmarshal(rec.Body.Bytes(), &tasks)
	if len(tasks) != 1 || tasks[0].Title != "Add healthz" {
		t.Fatalf("list tasks = %+v", tasks)
	}
	if tasks[0].Status != model.TaskReady {
		t.Fatalf("default status = %q", tasks[0].Status)
	}
}

func TestBigTaskCreatesPassthroughTask(t *testing.T) {
	h := newTestServer(t)

	rec := do(t, h, "POST", "/api/bigtasks", model.BigTask{
		Title:  "Ship login",
		Intent: "Users can log in with email",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create bigtask: %d %s", rec.Code, rec.Body.String())
	}

	rec = do(t, h, "GET", "/api/tasks", nil)
	var tasks []model.Task
	json.Unmarshal(rec.Body.Bytes(), &tasks)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 passthrough task, got %d", len(tasks))
	}
	if tasks[0].Title != "Ship login" {
		t.Fatalf("passthrough task title = %q", tasks[0].Title)
	}

	// The big task must advance out of draft — its task is already ready, so
	// leaving it in draft makes it a dead-end in the Define UI.
	rec = do(t, h, "GET", "/api/bigtasks", nil)
	var bts []model.BigTask
	json.Unmarshal(rec.Body.Bytes(), &bts)
	if len(bts) != 1 || bts[0].Status != model.BigTaskRunning {
		t.Fatalf("passthrough big task status = %+v, want running", bts)
	}
}

func TestBigTaskWithPlannerSkipsPassthrough(t *testing.T) {
	h := newTestServer(t)

	// Register an enabled planner agent.
	rec := do(t, h, "POST", "/api/agents", model.Agent{
		Name: "Planner", Command: "true", Roles: []string{model.RolePlanner}, Enabled: true,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create planner: %d %s", rec.Code, rec.Body.String())
	}

	// Defining a big task routes to the (async) planner, not the passthrough — so
	// no task is materialized synchronously the way the no-planner path does.
	rec = do(t, h, "POST", "/api/bigtasks", model.BigTask{Title: "X", Intent: "y"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create bigtask: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, "GET", "/api/tasks", nil)
	var tasks []model.Task
	json.Unmarshal(rec.Body.Bytes(), &tasks)
	if len(tasks) != 0 {
		t.Fatalf("expected no passthrough task when a planner exists, got %d", len(tasks))
	}
}

// TestBigTaskPreflightRejectsRepoWithoutCommits is the regression for the
// silent failure: a repo with no commits (unresolvable HEAD) used to pass
// is-inside-work-tree, then fail deep in the planner and revert to draft with
// no UI feedback. createBigTask now preflights and returns a clear 400.
func TestBigTaskPreflightRejectsRepoWithoutCommits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q", "-b", "main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	s, err := store.Open(filepath.Join(dir, "g"), filepath.Join(dir, "p"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	srv := NewServer(s, &config.Config{}, dir, nil)
	srv.Start(context.Background())
	h := srv.Handler()

	rec := do(t, h, "POST", "/api/bigtasks", model.BigTask{Title: "X", Intent: "y"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for repo without commits, got %d %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("no commits")) {
		t.Fatalf("error should mention missing commits, got %s", rec.Body.String())
	}
	// Nothing should have been persisted.
	rec = do(t, h, "GET", "/api/bigtasks", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "[]\n" {
		t.Fatalf("bigtasks should be empty: %d %s", rec.Code, rec.Body.String())
	}
}

// TestListBigTasks confirms defined big tasks are listable (the Define UI reads
// this to show planning status / failures).
func TestListBigTasks(t *testing.T) {
	h := newTestServer(t)

	rec := do(t, h, "POST", "/api/bigtasks", model.BigTask{Title: "Ship login", Intent: "y"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create bigtask: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, "GET", "/api/bigtasks", nil)
	var bts []model.BigTask
	json.Unmarshal(rec.Body.Bytes(), &bts)
	if len(bts) != 1 || bts[0].Title != "Ship login" {
		t.Fatalf("list bigtasks = %+v", bts)
	}
}

func TestPlanAndDecisionEndpointsLive(t *testing.T) {
	h := newTestServer(t)

	// Empty decision queue and plan list are arrays, not 501s.
	rec := do(t, h, "GET", "/api/decisions", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "[]\n" {
		t.Fatalf("decisions: %d %q", rec.Code, rec.Body.String())
	}
	rec = do(t, h, "GET", "/api/plans", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "[]\n" {
		t.Fatalf("plans: %d %q", rec.Code, rec.Body.String())
	}

	// Answering a missing decision is a 404.
	rec = do(t, h, "POST", "/api/decisions/nope/answer", map[string]any{"answer": "x"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("answer missing decision: %d", rec.Code)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	h := newTestServer(t)
	rec := do(t, h, "GET", "/api/metrics", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics: %d %s", rec.Code, rec.Body.String())
	}
	var m Metrics
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode metrics: %v", err)
	}
	if m.Agents == nil {
		t.Fatal("agents should be a (possibly empty) array, not null")
	}
}

func TestPlannerPlannedCountsAsShipped(t *testing.T) {
	h, s := newTestServerStore(t)

	rec := do(t, h, "POST", "/api/agents", model.Agent{
		Name: "Planner", Command: "true", Roles: []string{model.RolePlanner}, Enabled: true,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create planner: %d %s", rec.Code, rec.Body.String())
	}
	var agent model.Agent
	if err := json.Unmarshal(rec.Body.Bytes(), &agent); err != nil {
		t.Fatal(err)
	}

	if err := s.BigTasks.Create(&model.BigTask{
		Title:          "Ship login",
		Intent:         "Users can log in",
		PlannerAgentID: agent.ID,
		Status:         model.BigTaskPlanned,
	}); err != nil {
		t.Fatal(err)
	}

	rec = do(t, h, "GET", "/api/metrics", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics: %d %s", rec.Code, rec.Body.String())
	}
	var m Metrics
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}

	var found bool
	for _, a := range m.Agents {
		if a.AgentID == agent.ID {
			found = true
			if a.Planned != 1 {
				t.Fatalf("agent planned = %d, want 1", a.Planned)
			}
		}
	}
	if !found {
		t.Fatal("planner agent not found in metrics")
	}
	if m.Merged != 0 {
		t.Fatalf("global merged = %d, want 0 (big tasks don't count as code shipment)", m.Merged)
	}
}

func TestMetricsTokenTotals(t *testing.T) {
	s, h := newTestServerWithStore(t)

	// Register two agents.
	rec := do(t, h, "POST", "/api/agents", model.Agent{
		Name: "Agent One", Command: "true", Enabled: true,
	})
	_ = json.Unmarshal(rec.Body.Bytes(), new(model.Agent))
	rec = do(t, h, "POST", "/api/agents", model.Agent{
		Name: "Agent Two", Command: "true", Enabled: true,
	})
	var a2 model.Agent
	json.Unmarshal(rec.Body.Bytes(), &a2)

	// Seed tasks + attempts with usage.
	t1 := &model.Task{Title: "t1"}
	if err := s.Tasks.Create(t1); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := s.Attempts.Create(&model.Attempt{
		TaskID: t1.ID, AgentID: a2.ID, Result: model.ResultPass,
		Usage: model.Usage{InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
	}); err != nil {
		t.Fatalf("create attempt: %v", err)
	}

	// Hit metrics.
	rec = do(t, h, "GET", "/api/metrics", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics: %d %s", rec.Code, rec.Body.String())
	}
	var m Metrics
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode metrics: %v", err)
	}

	// Board-wide total.
	if m.TotalTokens != 150 {
		t.Fatalf("board totalTokens = %d, want 150", m.TotalTokens)
	}

	// Find Agent Two in the response — its token totals should be non-zero.
	var gotA2 *AgentMetrics
	for i := range m.Agents {
		if m.Agents[i].AgentID == a2.ID {
			gotA2 = &m.Agents[i]
			break
		}
	}
	if gotA2 == nil {
		t.Fatal("Agent Two missing from metrics")
	}
	if gotA2.InputTokens != 100 {
		t.Fatalf("agent2 inputTokens = %d, want 100", gotA2.InputTokens)
	}
	if gotA2.OutputTokens != 50 {
		t.Fatalf("agent2 outputTokens = %d, want 50", gotA2.OutputTokens)
	}
	if gotA2.TotalTokens != 150 {
		t.Fatalf("agent2 totalTokens = %d, want 150", gotA2.TotalTokens)
	}

	// Agent One has no attempts — tokens should be zero.
	for i := range m.Agents {
		if m.Agents[i].AgentID != a2.ID {
			if m.Agents[i].InputTokens != 0 || m.Agents[i].OutputTokens != 0 || m.Agents[i].TotalTokens != 0 {
				t.Fatalf("agent %s tokens should all be zero, got %d/%d/%d",
					m.Agents[i].Name, m.Agents[i].InputTokens, m.Agents[i].OutputTokens, m.Agents[i].TotalTokens)
			}
		}
	}
}

func TestSettingsRoundTripOverHTTP(t *testing.T) {
	h := newTestServer(t)
	rec := do(t, h, "PUT", "/api/settings", map[string]string{"wip_cap": "4"})
	if rec.Code != 200 {
		t.Fatalf("put settings: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, "GET", "/api/settings", nil)
	var got map[string]string
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got["wip_cap"] != "4" {
		t.Fatalf("settings = %v", got)
	}
}
