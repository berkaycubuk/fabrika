package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/berkaycubuk/fabrika/internal/model"
)

// TestListTasksFiltering covers the additive query-parameter filtering on
// GET /api/tasks: single value, comma-separated OR list, AND across params,
// and an unmatched value yielding an empty array (still HTTP 200).
func TestListTasksFiltering(t *testing.T) {
	s, h := newTestServerWithStore(t)

	seed := []model.Task{
		{Title: "a", Status: model.TaskReady, AgentID: "agent-1", RiskTier: "low"},
		{Title: "b", Status: model.TaskReview, AgentID: "agent-1", RiskTier: "high"},
		{Title: "c", Status: model.TaskFailed, AgentID: "agent-2", RiskTier: "high"},
		{Title: "d", Status: model.TaskReview, AgentID: "agent-2", RiskTier: "low"},
	}
	for i := range seed {
		if err := s.Tasks.Create(&seed[i]); err != nil {
			t.Fatalf("create %q: %v", seed[i].Title, err)
		}
	}

	get := func(t *testing.T, path string) []model.Task {
		t.Helper()
		rec := do(t, h, "GET", path, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body.String())
		}
		var tasks []model.Task
		if err := json.Unmarshal(rec.Body.Bytes(), &tasks); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return tasks
	}

	titles := func(tasks []model.Task) map[string]bool {
		m := map[string]bool{}
		for _, tk := range tasks {
			m[tk.Title] = true
		}
		return m
	}

	// No params -> all tasks.
	if got := get(t, "/api/tasks"); len(got) != 4 {
		t.Fatalf("no params: got %d tasks, want 4", len(got))
	}

	// Single value.
	got := get(t, "/api/tasks?status=review")
	if g := titles(got); len(g) != 2 || !g["b"] || !g["d"] {
		t.Fatalf("status=review: got %v, want {b,d}", g)
	}

	// Comma list (OR within param).
	got = get(t, "/api/tasks?status=review,failed")
	if g := titles(got); len(g) != 3 || !g["b"] || !g["c"] || !g["d"] {
		t.Fatalf("status=review,failed: got %v, want {b,c,d}", g)
	}

	// AND across params: status in {review,failed} AND agentId=agent-2.
	got = get(t, "/api/tasks?status=review,failed&agentId=agent-2")
	if g := titles(got); len(g) != 2 || !g["c"] || !g["d"] {
		t.Fatalf("status=review,failed&agentId=agent-2: got %v, want {c,d}", g)
	}

	// Three-param AND narrowing to a single task.
	got = get(t, "/api/tasks?status=review&agentId=agent-1&riskTier=high")
	if g := titles(got); len(g) != 1 || !g["b"] {
		t.Fatalf("triple AND: got %v, want {b}", g)
	}

	// Unmatched value -> empty array, HTTP 200.
	got = get(t, "/api/tasks?status=nonexistent")
	if len(got) != 0 {
		t.Fatalf("unmatched: got %d tasks, want 0", len(got))
	}

	// Empty param value applies no constraint.
	if got := get(t, "/api/tasks?status="); len(got) != 4 {
		t.Fatalf("empty status param: got %d tasks, want 4", len(got))
	}
}

// TestListTasksFilterByBigTaskID covers the bigTaskId query parameter: a single
// value selects only that big task's child tasks, a comma list ORs across big
// tasks, it ANDs with other params, and an empty param leaves the set untouched.
func TestListTasksFilterByBigTaskID(t *testing.T) {
	s, h := newTestServerWithStore(t)

	seed := []model.Task{
		{Title: "a", Status: model.TaskReady, BigTaskID: "bt-1"},
		{Title: "b", Status: model.TaskReview, BigTaskID: "bt-1"},
		{Title: "c", Status: model.TaskReady, BigTaskID: "bt-2"},
		{Title: "d", Status: model.TaskReady, BigTaskID: ""},
	}
	for i := range seed {
		if err := s.Tasks.Create(&seed[i]); err != nil {
			t.Fatalf("create %q: %v", seed[i].Title, err)
		}
	}

	get := func(t *testing.T, path string) []model.Task {
		t.Helper()
		rec := do(t, h, "GET", path, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body.String())
		}
		var tasks []model.Task
		if err := json.Unmarshal(rec.Body.Bytes(), &tasks); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return tasks
	}
	titles := func(tasks []model.Task) map[string]bool {
		m := map[string]bool{}
		for _, tk := range tasks {
			m[tk.Title] = true
		}
		return m
	}

	// Single big task -> only its children.
	got := get(t, "/api/tasks?bigTaskId=bt-1")
	if g := titles(got); len(g) != 2 || !g["a"] || !g["b"] {
		t.Fatalf("bigTaskId=bt-1: got %v, want {a,b}", g)
	}

	// Comma list ORs across big tasks.
	got = get(t, "/api/tasks?bigTaskId=bt-1,bt-2")
	if g := titles(got); len(g) != 3 || !g["a"] || !g["b"] || !g["c"] {
		t.Fatalf("bigTaskId=bt-1,bt-2: got %v, want {a,b,c}", g)
	}

	// ANDs with other params.
	got = get(t, "/api/tasks?bigTaskId=bt-1&status=review")
	if g := titles(got); len(g) != 1 || !g["b"] {
		t.Fatalf("bigTaskId=bt-1&status=review: got %v, want {b}", g)
	}

	// Empty param applies no constraint.
	if got := get(t, "/api/tasks?bigTaskId="); len(got) != 4 {
		t.Fatalf("empty bigTaskId param: got %d tasks, want 4", len(got))
	}
}
