package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/berkaycubuk/fabrika/internal/model"
)

func TestAttentionEndpoint(t *testing.T) {
	h, s := newTestServerStore(t)

	// Seed a review task with an attempt.
	reviewTask := &model.Task{Title: "review me", Status: model.TaskReview}
	if err := s.Tasks.Create(reviewTask); err != nil {
		t.Fatal(err)
	}
	if err := s.Attempts.Create(&model.Attempt{
		TaskID:  reviewTask.ID,
		AgentID: "agent-1",
		Result:  model.ResultPass,
	}); err != nil {
		t.Fatal(err)
	}

	// Seed an audit task (merged, flagged, not reverted) with an attempt.
	auditTask := &model.Task{
		Title:        "audit me",
		Status:       model.TaskMerged,
		AutoMerged:   true,
		AuditFlagged: true,
	}
	if err := s.Tasks.Create(auditTask); err != nil {
		t.Fatal(err)
	}
	if err := s.Attempts.Create(&model.Attempt{
		TaskID:  auditTask.ID,
		AgentID: "agent-2",
		Result:  model.ResultPass,
	}); err != nil {
		t.Fatal(err)
	}

	// Seed an open decision.
	decision := &model.Decision{
		Question: "which framework?",
		Options:  []string{"React", "Vue"},
		Status:   model.DecisionOpen,
	}
	if err := s.Decisions.Create(decision); err != nil {
		t.Fatal(err)
	}

	// Seed a plan with its big task.
	bt := &model.BigTask{Title: "plan this"}
	if err := s.BigTasks.Create(bt); err != nil {
		t.Fatal(err)
	}
	plan := &model.Plan{BigTaskID: bt.ID, Status: model.PlanProposed}
	if err := s.Plans.Create(plan); err != nil {
		t.Fatal(err)
	}

	// Hit the attention endpoint.
	rec := do(t, h, "GET", "/api/attention", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("attention: %d %s", rec.Code, rec.Body.String())
	}

	var resp attentionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode attention: %v", err)
	}

	if len(resp.Reviews) != 1 {
		t.Fatalf("reviews = %d, want 1", len(resp.Reviews))
	}
	if resp.Reviews[0].Task.ID != reviewTask.ID {
		t.Fatalf("review task = %s, want %s", resp.Reviews[0].Task.ID, reviewTask.ID)
	}

	if len(resp.Audits) != 1 {
		t.Fatalf("audits = %d, want 1", len(resp.Audits))
	}
	if resp.Audits[0].Task.ID != auditTask.ID {
		t.Fatalf("audit task = %s, want %s", resp.Audits[0].Task.ID, auditTask.ID)
	}

	if len(resp.Decisions) != 1 {
		t.Fatalf("decisions = %d, want 1", len(resp.Decisions))
	}
	if resp.Decisions[0].ID != decision.ID {
		t.Fatalf("decision = %s, want %s", resp.Decisions[0].ID, decision.ID)
	}

	if len(resp.Plans) != 1 {
		t.Fatalf("plans = %d, want 1", len(resp.Plans))
	}
	if resp.Plans[0].ID != plan.ID {
		t.Fatalf("plan = %s, want %s", resp.Plans[0].ID, plan.ID)
	}
}
