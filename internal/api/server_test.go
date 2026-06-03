package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/berkaycubuk/fabrika/internal/config"
	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/berkaycubuk/fabrika/internal/store"
)

func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "g"), filepath.Join(dir, "p"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	srv := NewServer(s, &config.Config{}, dir, nil)
	srv.Start(context.Background())
	return srv.Handler()
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
