package store

import (
	"testing"

	"github.com/berkaycubuk/fabrika/internal/model"
)

func TestAttemptUsageRoundTrip(t *testing.T) {
	s := openTest(t)

	task := &model.Task{Title: "t"}
	if err := s.Tasks.Create(task); err != nil {
		t.Fatalf("Create task: %v", err)
	}

	a := &model.Attempt{
		TaskID:  task.ID,
		AgentID: "agent-1",
		Result:  model.ResultPass,
		Usage:   model.Usage{InputTokens: 120, OutputTokens: 45, TotalTokens: 165},
	}
	if err := s.Attempts.Create(a); err != nil {
		t.Fatalf("Create attempt: %v", err)
	}

	got, err := s.Attempts.LatestForTask(task.ID)
	if err != nil {
		t.Fatalf("LatestForTask: %v", err)
	}
	if got.Usage != (model.Usage{InputTokens: 120, OutputTokens: 45, TotalTokens: 165}) {
		t.Fatalf("usage not round-tripped: %+v", got.Usage)
	}
}

func TestTokensByAgent(t *testing.T) {
	s := openTest(t)

	task := &model.Task{Title: "t"}
	if err := s.Tasks.Create(task); err != nil {
		t.Fatalf("Create task: %v", err)
	}

	attempts := []*model.Attempt{
		{TaskID: task.ID, AgentID: "agent-1", Result: model.ResultFail, Usage: model.Usage{InputTokens: 100, OutputTokens: 10, TotalTokens: 110}},
		{TaskID: task.ID, AgentID: "agent-1", Result: model.ResultPass, Usage: model.Usage{InputTokens: 50, OutputTokens: 5, TotalTokens: 55}},
		{TaskID: task.ID, AgentID: "agent-2", Result: model.ResultPass, Usage: model.Usage{InputTokens: 7, OutputTokens: 3, TotalTokens: 10}},
		// Empty agent_id row should be skipped.
		{TaskID: task.ID, AgentID: "", Result: model.ResultFail, Usage: model.Usage{InputTokens: 999, OutputTokens: 999, TotalTokens: 999}},
	}
	for i, a := range attempts {
		if err := s.Attempts.Create(a); err != nil {
			t.Fatalf("Create attempt %d: %v", i, err)
		}
	}

	totals, err := s.Attempts.TokensByAgent()
	if err != nil {
		t.Fatalf("TokensByAgent: %v", err)
	}

	if _, ok := totals[""]; ok {
		t.Fatal("empty agent_id should be skipped")
	}
	if got := totals["agent-1"]; got != (model.Usage{InputTokens: 150, OutputTokens: 15, TotalTokens: 165}) {
		t.Fatalf("agent-1 totals = %+v", got)
	}
	if got := totals["agent-2"]; got != (model.Usage{InputTokens: 7, OutputTokens: 3, TotalTokens: 10}) {
		t.Fatalf("agent-2 totals = %+v", got)
	}
}
