// Package api exposes Fabrika's REST + WebSocket surface (SPECS.md §11) over the
// store. In this phase the agents/tasks/settings endpoints are fully wired;
// planner/decision/review/steer/metrics endpoints are present but return 501 so
// the surface is discoverable while the live loop is built out.
package api

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"

	"github.com/berkaycubuk/fabrika/internal/store"
)

// Server holds the dependencies shared across handlers.
type Server struct {
	store *store.Store
	hub   *Hub
	web   fs.FS // embedded static UI assets (may be nil in tests)
}

// NewServer constructs a Server. web is the embedded UI filesystem rooted at the
// built assets; pass nil to disable static serving.
func NewServer(s *store.Store, web fs.FS) *Server {
	return &Server{store: s, hub: newHub(), web: web}
}

// Handler builds the full HTTP routing tree.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// --- Agents (global store) ---
	mux.HandleFunc("GET /api/agents", s.listAgents)
	mux.HandleFunc("POST /api/agents", s.createAgent)
	mux.HandleFunc("PUT /api/agents/{id}", s.updateAgent)
	mux.HandleFunc("DELETE /api/agents/{id}", s.deleteAgent)
	mux.HandleFunc("POST /api/agents/{id}/enable", s.enableAgent)
	mux.HandleFunc("POST /api/agents/{id}/disable", s.disableAgent)

	// --- Tasks + BigTasks (per-project store) ---
	mux.HandleFunc("GET /api/tasks", s.listTasks)
	mux.HandleFunc("POST /api/tasks", s.createTask)
	mux.HandleFunc("GET /api/tasks/{id}", s.getTask)
	mux.HandleFunc("POST /api/bigtasks", s.createBigTask)

	// --- Settings (global store) ---
	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.putSettings)

	// --- WebSocket events ---
	mux.HandleFunc("GET /api/events", s.hub.serveWS)

	// --- Deferred surface (present but not yet implemented) ---
	for _, p := range []string{
		"GET /api/plans/{id}",
		"POST /api/plans/{id}/approve",
		"POST /api/plans/{id}/reject",
		"GET /api/decisions",
		"POST /api/decisions/{id}/answer",
		"GET /api/reviews",
		"POST /api/tasks/{id}/accept",
		"POST /api/tasks/{id}/reject",
		"POST /api/tasks/{id}/assign",
		"POST /api/steer",
		"GET /api/metrics",
	} {
		mux.HandleFunc(p, notImplemented)
	}

	// --- Static UI (SPA) ---
	if s.web != nil {
		mux.Handle("/", s.spaHandler())
	}

	return logRequests(mux)
}

// --- JSON helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// mapStoreErr translates store errors into HTTP responses.
func mapStoreErr(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	writeErr(w, http.StatusInternalServerError, err.Error())
}

func notImplemented(w http.ResponseWriter, r *http.Request) {
	writeErr(w, http.StatusNotImplemented, "not implemented in this phase")
}
