package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/berkaycubuk/fabrika/internal/model"
)

func doBody(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Host = "localhost" // pass the same-origin/loopback guard
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRetryBatch(t *testing.T) {
	s, h := newTestServerWithStore(t)

	// Create tasks in different states.
	failed := &model.Task{Title: "failing", Status: model.TaskFailed}
	blocked := &model.Task{Title: "blocked", Status: model.TaskBlocked}
	closed := &model.Task{Title: "closed", Status: model.TaskClosed}
	ready := &model.Task{Title: "ready", Status: model.TaskReady}
	review := &model.Task{Title: "review", Status: model.TaskReview}

	for _, task := range []*model.Task{failed, blocked, closed, ready, review} {
		if err := s.Tasks.Create(task); err != nil {
			t.Fatalf("create task %q: %v", task.Title, err)
		}
		if task != ready {
			if err := s.Tasks.UpdateStatus(task.ID, task.Status); err != nil {
				t.Fatalf("update status for %q: %v", task.Title, err)
			}
		}
	}

	// Retry batch: mix of retryable and non-retryable.
	ids := []string{failed.ID, ready.ID, blocked.ID, review.ID, closed.ID}
	rec := doBody(t, h, "POST", "/api/tasks/retry-batch", map[string]any{
		"ids": ids,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("retry-batch: %d %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Results []batchResult `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != len(ids) {
		t.Fatalf("expected %d results, got %d", len(ids), len(resp.Results))
	}

	// Verify order and correctness.
	for i, r := range resp.Results {
		if r.ID != ids[i] {
			t.Errorf("result[%d] id = %q, want %q", i, r.ID, ids[i])
		}
		switch r.ID {
		case failed.ID, blocked.ID, closed.ID:
			if !r.OK {
				t.Errorf("%s should be retryable, got error=%q", ids[i], r.Err)
			}
		case ready.ID, review.ID:
			if r.OK {
				t.Errorf("%s should not be retryable", ids[i])
			}
			if r.Err == "" {
				t.Errorf("%s should have error message", ids[i])
			}
		}
	}

	// Verify status transitions: retryable tasks should now be ready.
	for _, task := range []*model.Task{failed, blocked, closed} {
		got, err := s.Tasks.Get(task.ID)
		if err != nil {
			t.Fatalf("get task %q: %v", task.Title, err)
		}
		if got.Status != model.TaskReady {
			t.Errorf("%s status = %q, want ready", task.Title, got.Status)
		}
	}
}

func TestAcceptBatchValidation(t *testing.T) {
	h := newTestServer(t)

	// Empty body — missing ids.
	rec := doBody(t, h, "POST", "/api/tasks/accept-batch", map[string]any{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty body: %d %s", rec.Code, rec.Body.String())
	}

	// Empty ids array.
	rec = doBody(t, h, "POST", "/api/tasks/accept-batch", map[string]any{
		"ids": []string{},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty ids: %d %s", rec.Code, rec.Body.String())
	}

	// Unknown id — should return 200 with ok:false per-id.
	rec = doBody(t, h, "POST", "/api/tasks/accept-batch", map[string]any{
		"ids": []string{"nonexistent"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("unknown id: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Results []batchResult `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.Results))
	}
	if resp.Results[0].OK {
		t.Fatal("unknown ID should not be ok")
	}
}
