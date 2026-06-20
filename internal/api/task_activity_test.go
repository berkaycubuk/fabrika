package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/berkaycubuk/fabrika/internal/model"
)

func TestGetTaskActivity_UnknownID(t *testing.T) {
	s, h := newTestServerWithStore(t)
	_ = s
	rec := do(t, h, http.MethodGet, "/api/tasks/does-not-exist/activity", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var out []model.PlanActivity
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("want empty array, got %v", out)
	}
}

func TestGetTaskActivity_WithEntries(t *testing.T) {
	s, h := newTestServerWithStore(t)

	taskID := "test-task-1"
	entries := []model.PlanActivity{
		{Type: "tool_use", Summary: "ran build", Ts: 1750420800},
		{Type: "text", Summary: "thinking about it", Ts: 1750420860},
	}
	for _, e := range entries {
		if err := s.TaskActivity.Append(taskID, e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	rec := do(t, h, http.MethodGet, "/api/tasks/"+taskID+"/activity", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var out []model.PlanActivity
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != len(entries) {
		t.Fatalf("want %d entries, got %d", len(entries), len(out))
	}
	if out[0].Summary != entries[0].Summary {
		t.Fatalf("first entry mismatch: got %q", out[0].Summary)
	}
}
