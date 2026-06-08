package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/berkaycubuk/fabrika/internal/model"
)

func TestConventionCRUDOverHTTP(t *testing.T) {
	h := newTestServer(t)

	// Empty list -> 200, JSON array (not null).
	rec := do(t, h, "GET", "/api/conventions", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "[]\n" {
		t.Fatalf("empty list: %d %q", rec.Code, rec.Body.String())
	}

	// Reject empty rule with 400.
	rec = do(t, h, "POST", "/api/conventions", model.Convention{Rule: ""})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty rule, got %d", rec.Code)
	}

	// Create -> 201 with assigned ID.
	rec = do(t, h, "POST", "/api/conventions", model.Convention{Rule: "Always use tabs"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created model.Convention
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected assigned ID")
	}
	if created.Rule != "Always use tabs" {
		t.Fatalf("rule = %q, want %q", created.Rule, "Always use tabs")
	}

	// List now contains the created convention.
	rec = do(t, h, "GET", "/api/conventions", nil)
	var list []model.Convention
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("list = %+v, want one item with ID %s", list, created.ID)
	}

	// Delete -> 204.
	rec = do(t, h, "DELETE", "/api/conventions/"+created.ID, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}

	// Delete again -> 404.
	rec = do(t, h, "DELETE", "/api/conventions/"+created.ID, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on second delete, got %d", rec.Code)
	}

	// Delete non-existent -> 404.
	rec = do(t, h, "DELETE", "/api/conventions/nope", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-existent, got %d", rec.Code)
	}

	// List empty again after delete.
	rec = do(t, h, "GET", "/api/conventions", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "[]\n" {
		t.Fatalf("list after delete: %d %q", rec.Code, rec.Body.String())
	}
}
