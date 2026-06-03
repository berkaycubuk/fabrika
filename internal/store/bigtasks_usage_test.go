package store

import (
	"testing"

	"github.com/berkaycubuk/fabrika/internal/model"
)

func TestBigTaskUsageRoundTrip(t *testing.T) {
	s := openTest(t)

	bt := &model.BigTask{Title: "Ship login", Intent: "y", PlannerAgentID: "planner-1"}
	if err := s.BigTasks.Create(bt); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := s.BigTasks.SetUsage(bt.ID, model.Usage{InputTokens: 200, OutputTokens: 60, TotalTokens: 260}); err != nil {
		t.Fatalf("SetUsage: %v", err)
	}

	totals, err := s.BigTasks.PlanningTokensByAgent()
	if err != nil {
		t.Fatalf("PlanningTokensByAgent: %v", err)
	}
	if got := totals["planner-1"]; got != (model.Usage{InputTokens: 200, OutputTokens: 60, TotalTokens: 260}) {
		t.Fatalf("planner-1 usage = %+v", got)
	}
}

func TestPlanningTokensByAgent(t *testing.T) {
	s := openTest(t)

	bigtasks := []*model.BigTask{
		{Title: "a", Intent: "x", PlannerAgentID: "planner-1"},
		{Title: "b", Intent: "x", PlannerAgentID: "planner-1"},
		{Title: "c", Intent: "x", PlannerAgentID: "planner-2"},
		// Empty planner_agent_id row should be skipped.
		{Title: "d", Intent: "x", PlannerAgentID: ""},
	}
	usages := []model.Usage{
		{InputTokens: 100, OutputTokens: 10, TotalTokens: 110},
		{InputTokens: 50, OutputTokens: 5, TotalTokens: 55},
		{InputTokens: 7, OutputTokens: 3, TotalTokens: 10},
		{InputTokens: 999, OutputTokens: 999, TotalTokens: 999},
	}
	for i, bt := range bigtasks {
		if err := s.BigTasks.Create(bt); err != nil {
			t.Fatalf("Create bigtask %d: %v", i, err)
		}
		if err := s.BigTasks.SetUsage(bt.ID, usages[i]); err != nil {
			t.Fatalf("SetUsage %d: %v", i, err)
		}
	}

	totals, err := s.BigTasks.PlanningTokensByAgent()
	if err != nil {
		t.Fatalf("PlanningTokensByAgent: %v", err)
	}

	if _, ok := totals[""]; ok {
		t.Fatal("empty planner_agent_id should be skipped")
	}
	if got := totals["planner-1"]; got != (model.Usage{InputTokens: 150, OutputTokens: 15, TotalTokens: 165}) {
		t.Fatalf("planner-1 totals = %+v", got)
	}
	if got := totals["planner-2"]; got != (model.Usage{InputTokens: 7, OutputTokens: 3, TotalTokens: 10}) {
		t.Fatalf("planner-2 totals = %+v", got)
	}
}
