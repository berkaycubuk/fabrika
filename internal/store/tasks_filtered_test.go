package store

import (
	"testing"

	"github.com/berkaycubuk/fabrika/internal/model"
)

func TestListFiltered(t *testing.T) {
	s := openTest(t)

	// Create a mix of tasks across statuses.
	const n = 6
	for i := 0; i < n; i++ {
		task := &model.Task{Title: "t", Status: model.TaskReady}
		if err := s.Tasks.Create(task); err != nil {
			t.Fatalf("Create ready %d: %v", i, err)
		}
	}
	for i := 0; i < 3; i++ {
		task := &model.Task{Title: "t", Status: model.TaskRunning}
		if err := s.Tasks.Create(task); err != nil {
			t.Fatalf("Create running %d: %v", i, err)
		}
	}
	const total = n + 3

	// Limit alone caps the count.
	got, err := s.Tasks.ListFiltered(TaskFilter{Limit: 4})
	if err != nil {
		t.Fatalf("ListFiltered limit: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("limit 4 returned %d rows, want 4", len(got))
	}

	// Limit combined with a status filter: cap applies after filtering and all
	// matched rows carry the filtered status.
	got, err = s.Tasks.ListFiltered(TaskFilter{Statuses: []string{model.TaskReady}, Limit: 2})
	if err != nil {
		t.Fatalf("ListFiltered status+limit: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("status+limit returned %d rows, want 2", len(got))
	}
	for _, task := range got {
		if task.Status != model.TaskReady {
			t.Fatalf("filtered row has status %q, want %q", task.Status, model.TaskReady)
		}
	}

	// Limit absent (<=0) returns all matching rows.
	got, err = s.Tasks.ListFiltered(TaskFilter{})
	if err != nil {
		t.Fatalf("ListFiltered no-limit: %v", err)
	}
	if len(got) != total {
		t.Fatalf("no-limit returned %d rows, want %d", len(got), total)
	}

	got, err = s.Tasks.ListFiltered(TaskFilter{Limit: 0})
	if err != nil {
		t.Fatalf("ListFiltered limit 0: %v", err)
	}
	if len(got) != total {
		t.Fatalf("limit 0 returned %d rows, want %d", len(got), total)
	}
}
