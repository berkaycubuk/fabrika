package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/berkaycubuk/fabrika/internal/model"
)

// TestListTasksLimit covers the optional `limit` query parameter on
// GET /api/tasks: limit alone caps the count, limit combined with a status
// filter caps the filtered set, and an absent limit returns the full list.
func TestListTasksLimit(t *testing.T) {
	s, h := newTestServerWithStore(t)

	seed := []model.Task{
		{Title: "a", Status: model.TaskReady},
		{Title: "b", Status: model.TaskReview},
		{Title: "c", Status: model.TaskReview},
		{Title: "d", Status: model.TaskReview},
		{Title: "e", Status: model.TaskFailed},
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

	// Limit alone caps the count.
	if got := get(t, "/api/tasks?limit=2"); len(got) != 2 {
		t.Fatalf("limit=2: got %d tasks, want 2", len(got))
	}

	// Limit combined with a status filter caps the filtered set (3 review tasks).
	if got := get(t, "/api/tasks?status=review&limit=2"); len(got) != 2 {
		t.Fatalf("status=review&limit=2: got %d tasks, want 2", len(got))
	}

	// Limit larger than the matching set returns all matches.
	if got := get(t, "/api/tasks?status=review&limit=10"); len(got) != 3 {
		t.Fatalf("status=review&limit=10: got %d tasks, want 3", len(got))
	}

	// Limit absent -> full list.
	if got := get(t, "/api/tasks"); len(got) != 5 {
		t.Fatalf("no limit: got %d tasks, want 5", len(got))
	}

	// Non-positive / unparseable limit -> no constraint (full list).
	if got := get(t, "/api/tasks?limit=0"); len(got) != 5 {
		t.Fatalf("limit=0: got %d tasks, want 5", len(got))
	}
	if got := get(t, "/api/tasks?limit=-3"); len(got) != 5 {
		t.Fatalf("limit=-3: got %d tasks, want 5", len(got))
	}
	if got := get(t, "/api/tasks?limit=abc"); len(got) != 5 {
		t.Fatalf("limit=abc: got %d tasks, want 5", len(got))
	}
}
