package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/berkaycubuk/fabrika/internal/config"
	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/berkaycubuk/fabrika/internal/store"
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

	// An already-approved plan needs no judgment and must stay out of the feed.
	bt2 := &model.BigTask{Title: "already decided"}
	if err := s.BigTasks.Create(bt2); err != nil {
		t.Fatal(err)
	}
	if err := s.Plans.Create(&model.Plan{BigTaskID: bt2.ID, Status: model.PlanApproved}); err != nil {
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

// newTestServerHub builds a server and returns it directly so tests can reach
// the event hub to broadcast and inspect the cursor.
func newTestServerHub(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "g"), filepath.Join(dir, "p"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	srv := NewServer(s, &config.Config{}, dir, nil, "")
	srv.Start(context.Background())
	return srv
}

func TestAttentionCursorAndLongPoll(t *testing.T) {
	srv := newTestServerHub(t)
	h := srv.Handler()

	// The plain snapshot carries the current cursor (0 with no events yet).
	rec := do(t, h, "GET", "/api/attention", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("attention: %d %s", rec.Code, rec.Body.String())
	}
	var resp attentionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Cursor != 0 {
		t.Fatalf("cursor = %d, want 0", resp.Cursor)
	}

	// A broadcast advances the cursor.
	srv.hub.Broadcast(Event{Type: "test.event"})
	if got := srv.hub.Seq(); got != 1 {
		t.Fatalf("seq after broadcast = %d, want 1", got)
	}

	// since < current seq returns immediately even with wait set.
	start := time.Now()
	rec = do(t, h, "GET", "/api/attention?since=0&wait=30", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("attention: %d %s", rec.Code, rec.Body.String())
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("expected immediate return, blocked for %s", elapsed)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Cursor != 1 {
		t.Fatalf("cursor = %d, want 1", resp.Cursor)
	}

	// since == current seq blocks until a broadcast wakes the long-poll.
	done := make(chan attentionResponse, 1)
	go func() {
		r := do(t, h, "GET", "/api/attention?since=1&wait=30", nil)
		var out attentionResponse
		_ = json.Unmarshal(r.Body.Bytes(), &out)
		done <- out
	}()

	// Give the request time to enter the blocking select before broadcasting.
	time.Sleep(100 * time.Millisecond)
	srv.hub.Broadcast(Event{Type: "test.wakeup"})

	select {
	case out := <-done:
		if out.Cursor != 2 {
			t.Fatalf("woken cursor = %d, want 2", out.Cursor)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("long-poll did not wake on broadcast")
	}

	// wait timeout elapses and returns the snapshot when no event arrives.
	start = time.Now()
	rec = do(t, h, "GET", "/api/attention?since=2&wait=1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("attention: %d %s", rec.Code, rec.Body.String())
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Fatalf("expected to block ~1s, returned after %s", elapsed)
	}
}
