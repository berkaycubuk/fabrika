package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/berkaycubuk/fabrika/internal/config"
	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/berkaycubuk/fabrika/internal/store"
)

// newTestServerStore is like newTestServer but also returns the store so tests
// can seed terminal task states the API alone can't reach (merged, flagged).
func newTestServerStore(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "g"), filepath.Join(dir, "p"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	srv := NewServer(s, &config.Config{}, dir, nil, "")
	srv.Start(context.Background())
	return srv.Handler(), s
}

func TestAuditQueueAndAck(t *testing.T) {
	h, s := newTestServerStore(t)

	// Seed an auto-merged, audit-flagged task and a plain merged one.
	flagged := &model.Task{Title: "sampled", Status: model.TaskMerged, AutoMerged: true, AuditFlagged: true}
	if err := s.Tasks.Create(flagged); err != nil {
		t.Fatal(err)
	}
	plain := &model.Task{Title: "quiet", Status: model.TaskMerged, AutoMerged: true}
	if err := s.Tasks.Create(plain); err != nil {
		t.Fatal(err)
	}

	rec := do(t, h, "GET", "/api/audits", nil)
	var items []reviewItem
	json.Unmarshal(rec.Body.Bytes(), &items)
	if len(items) != 1 || items[0].Task.ID != flagged.ID {
		t.Fatalf("audits = %d items, want only the flagged task", len(items))
	}

	// Ack clears it from the queue.
	if rec := do(t, h, "POST", "/api/tasks/"+flagged.ID+"/audit-ok", nil); rec.Code != 200 {
		t.Fatalf("audit-ok: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, "GET", "/api/audits", nil)
	json.Unmarshal(rec.Body.Bytes(), &items)
	if len(items) != 0 {
		t.Fatalf("audits should be empty after ack, got %d", len(items))
	}
}

func TestRevertFeedsChangeFailureRate(t *testing.T) {
	h, s := newTestServerStore(t)
	for i := 0; i < 4; i++ {
		if err := s.Tasks.Create(&model.Task{Title: "m", Status: model.TaskMerged, AutoMerged: true}); err != nil {
			t.Fatal(err)
		}
	}
	var merged []model.Task
	all, _ := s.Tasks.List()
	merged = all

	// Revert one of the four merges.
	rec := do(t, h, "POST", "/api/tasks/"+merged[0].ID+"/revert", nil)
	if rec.Code != 200 {
		t.Fatalf("revert: %d %s", rec.Code, rec.Body.String())
	}

	rec = do(t, h, "GET", "/api/metrics", nil)
	var m Metrics
	json.Unmarshal(rec.Body.Bytes(), &m)
	if m.Merged != 4 || m.Reverted != 1 {
		t.Fatalf("merged=%d reverted=%d, want 4 and 1", m.Merged, m.Reverted)
	}
	if m.ChangeFailRate < 0.24 || m.ChangeFailRate > 0.26 {
		t.Fatalf("changeFailRate = %v, want ~0.25", m.ChangeFailRate)
	}
	if m.AutoMergeShare != 1 {
		t.Fatalf("autoMergeShare = %v, want 1 (all auto)", m.AutoMergeShare)
	}

	// Reverting a non-merged task is a conflict.
	ready := &model.Task{Title: "ready"}
	s.Tasks.Create(ready)
	if rec := do(t, h, "POST", "/api/tasks/"+ready.ID+"/revert", nil); rec.Code != http.StatusConflict {
		t.Fatalf("revert of ready task: %d, want 409", rec.Code)
	}
}
