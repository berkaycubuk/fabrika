package store

import (
	"testing"

	"github.com/berkaycubuk/fabrika/internal/model"
)

func TestTaskActivityCapTrim(t *testing.T) {
	s := openTest(t)

	task := &model.Task{Title: "trim-test"}
	if err := s.Tasks.Create(task); err != nil {
		t.Fatalf("Create task: %v", err)
	}

	// Insert taskActivityCap+10 rows.
	total := taskActivityCap + 10
	for i := 0; i < total; i++ {
		a := model.PlanActivity{Type: "step", Summary: "row", Ts: int64(i)}
		if err := s.TaskActivity.Append(task.ID, a); err != nil {
			t.Fatalf("Append(%d): %v", i, err)
		}
	}

	rows, err := s.TaskActivity.List(task.ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != taskActivityCap {
		t.Fatalf("expected %d rows after trim, got %d", taskActivityCap, len(rows))
	}
	// The oldest 10 rows (Ts 0..9) should have been dropped; first surviving Ts=10.
	if rows[0].Ts != 10 {
		t.Fatalf("oldest surviving Ts = %d, want 10", rows[0].Ts)
	}
}

func TestTaskActivityOldestFirst(t *testing.T) {
	s := openTest(t)

	task := &model.Task{Title: "order-test"}
	if err := s.Tasks.Create(task); err != nil {
		t.Fatalf("Create task: %v", err)
	}

	for i := int64(0); i < 5; i++ {
		if err := s.TaskActivity.Append(task.ID, model.PlanActivity{Type: "t", Summary: "s", Ts: i}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	rows, err := s.TaskActivity.List(task.ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("expected 5 rows, got %d", len(rows))
	}
	for i, r := range rows {
		if r.Ts != int64(i) {
			t.Fatalf("row[%d].Ts = %d, want %d (not oldest-first)", i, r.Ts, i)
		}
	}
}

func TestTaskActivityPerTaskIsolation(t *testing.T) {
	s := openTest(t)

	taskA := &model.Task{Title: "task-a"}
	taskB := &model.Task{Title: "task-b"}
	if err := s.Tasks.Create(taskA); err != nil {
		t.Fatalf("Create taskA: %v", err)
	}
	if err := s.Tasks.Create(taskB); err != nil {
		t.Fatalf("Create taskB: %v", err)
	}

	if err := s.TaskActivity.Append(taskA.ID, model.PlanActivity{Type: "a", Summary: "for A", Ts: 1}); err != nil {
		t.Fatalf("Append A: %v", err)
	}
	if err := s.TaskActivity.Append(taskB.ID, model.PlanActivity{Type: "b", Summary: "for B", Ts: 2}); err != nil {
		t.Fatalf("Append B: %v", err)
	}
	if err := s.TaskActivity.Append(taskB.ID, model.PlanActivity{Type: "b", Summary: "for B 2", Ts: 3}); err != nil {
		t.Fatalf("Append B2: %v", err)
	}

	rowsA, err := s.TaskActivity.List(taskA.ID)
	if err != nil {
		t.Fatalf("List A: %v", err)
	}
	if len(rowsA) != 1 || rowsA[0].Summary != "for A" {
		t.Fatalf("task A isolation failed: %+v", rowsA)
	}

	rowsB, err := s.TaskActivity.List(taskB.ID)
	if err != nil {
		t.Fatalf("List B: %v", err)
	}
	if len(rowsB) != 2 {
		t.Fatalf("task B expected 2 rows, got %d", len(rowsB))
	}
}

func TestTaskActivityDeleteByTask(t *testing.T) {
	s := openTest(t)

	task := &model.Task{Title: "del-test"}
	if err := s.Tasks.Create(task); err != nil {
		t.Fatalf("Create task: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := s.TaskActivity.Append(task.ID, model.PlanActivity{Type: "t", Summary: "s", Ts: int64(i)}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	// DeleteByTask on a non-existent task is not an error.
	if err := s.TaskActivity.DeleteByTask("no-such-task"); err != nil {
		t.Fatalf("DeleteByTask (no-op): %v", err)
	}

	if err := s.TaskActivity.DeleteByTask(task.ID); err != nil {
		t.Fatalf("DeleteByTask: %v", err)
	}

	rows, err := s.TaskActivity.List(task.ID)
	if err != nil {
		t.Fatalf("List after delete: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows after DeleteByTask, got %d", len(rows))
	}
}
