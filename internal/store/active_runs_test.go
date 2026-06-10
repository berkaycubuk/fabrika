package store

import "testing"

func TestActiveRunsRoundTrip(t *testing.T) {
	s := openTest(t)

	if err := s.ActiveRuns.Record("task-1", 4242, "agent-a"); err != nil {
		t.Fatalf("Record: %v", err)
	}
	// Re-recording the same task must replace, not duplicate.
	if err := s.ActiveRuns.Record("task-1", 5151, "agent-b"); err != nil {
		t.Fatalf("Record (upsert): %v", err)
	}
	if err := s.ActiveRuns.Record("task-2", 6060, ""); err != nil {
		t.Fatalf("Record task-2: %v", err)
	}

	runs, err := s.ActiveRuns.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("List: want 2 rows, got %d", len(runs))
	}
	byID := map[string]int{}
	agents := map[string]string{}
	for _, r := range runs {
		byID[r.TaskID] = r.PGID
		agents[r.TaskID] = r.AgentID
	}
	if byID["task-1"] != 5151 || agents["task-1"] != "agent-b" {
		t.Fatalf("upsert did not replace: pgid=%d agent=%q", byID["task-1"], agents["task-1"])
	}
	if byID["task-2"] != 6060 {
		t.Fatalf("task-2 pgid: want 6060, got %d", byID["task-2"])
	}

	// Delete is idempotent.
	if err := s.ActiveRuns.Delete("task-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := s.ActiveRuns.Delete("task-1"); err != nil {
		t.Fatalf("Delete (missing row): %v", err)
	}
	runs, _ = s.ActiveRuns.List()
	if len(runs) != 1 {
		t.Fatalf("after Delete: want 1 row, got %d", len(runs))
	}

	// Clear empties the table and is safe to repeat.
	if err := s.ActiveRuns.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if err := s.ActiveRuns.Clear(); err != nil {
		t.Fatalf("Clear (empty): %v", err)
	}
	runs, _ = s.ActiveRuns.List()
	if len(runs) != 0 {
		t.Fatalf("after Clear: want 0 rows, got %d", len(runs))
	}
}
