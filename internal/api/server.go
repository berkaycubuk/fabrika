// Package api exposes Fabrika's REST + WebSocket surface (SPECS.md §11) over the
// store. In this phase the agents/tasks/settings endpoints are fully wired;
// planner/decision/review/steer/metrics endpoints are present but return 501 so
// the surface is discoverable while the live loop is built out.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"

	"github.com/berkaycubuk/fabrika/internal/config"
	"github.com/berkaycubuk/fabrika/internal/engine"
	"github.com/berkaycubuk/fabrika/internal/store"
)

// Server holds the dependencies shared across handlers.
type Server struct {
	store  *store.Store
	hub    *Hub
	web    fs.FS // embedded static UI assets (may be nil in tests)
	engine *engine.Engine
}

// NewServer constructs a Server and its engine. cfg + repoRoot configure the
// dispatch loop; web is the embedded UI filesystem (nil disables static
// serving). The engine emits UI events through the WebSocket hub.
func NewServer(s *store.Store, cfg *config.Config, repoRoot string, web fs.FS) *Server {
	srv := &Server{store: s, hub: newHub(), web: web}
	srv.engine = engine.New(s, cfg, repoRoot, func(t string, p any) {
		srv.hub.Broadcast(Event{Type: t, Payload: p})
	})
	return srv
}

// Start launches the engine dispatch loop until ctx is cancelled.
func (s *Server) Start(ctx context.Context) {
	s.engine.Start(ctx)
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

	// --- Accept queue (live loop) ---
	mux.HandleFunc("GET /api/reviews", s.listReviews)
	mux.HandleFunc("POST /api/tasks/{id}/accept", s.acceptTask)
	mux.HandleFunc("POST /api/tasks/{id}/reject", s.rejectTask)

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
