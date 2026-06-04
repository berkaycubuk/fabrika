// Package api exposes Fabrika's REST + WebSocket surface (SPECS.md §11) over the
// store. Agents, tasks, reviews, settings, the Phase 1 scheduling surface
// (assign, steer, metrics), and the Phase 2 planner surface (plans, approve, and
// the decision queue) are all wired.
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
	store    *store.Store
	cfg      *config.Config
	hub      *Hub
	web      fs.FS  // embedded static UI assets (may be nil in tests)
	repoRoot string // project root; uploads live under <repoRoot>/.fabrika/uploads
	engine   *engine.Engine
	version  string
}

// NewServer constructs a Server and its engine. cfg + repoRoot configure the
// dispatch loop; web is the embedded UI filesystem (nil disables static
// serving). The engine emits UI events through the WebSocket hub.
func NewServer(s *store.Store, cfg *config.Config, repoRoot string, web fs.FS, version string) *Server {
	srv := &Server{store: s, cfg: cfg, hub: newHub(), web: web, repoRoot: repoRoot, version: version}
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
	mux.HandleFunc("DELETE /api/tasks/{id}", s.deleteTask)
	mux.HandleFunc("GET /api/tasks/{id}/comments", s.listComments)
	mux.HandleFunc("POST /api/tasks/{id}/comments", s.createComment)
	mux.HandleFunc("POST /api/uploads", s.createUpload)
	mux.HandleFunc("GET /api/uploads/{name}", s.getUpload)
	mux.HandleFunc("GET /api/bigtasks", s.listBigTasks)
	mux.HandleFunc("POST /api/bigtasks", s.createBigTask)
	mux.HandleFunc("DELETE /api/bigtasks/{id}", s.deleteBigTask)
	mux.HandleFunc("POST /api/bigtasks/{id}/replan", s.replanBigTask)

	// --- Accept queue (live loop) ---
	mux.HandleFunc("GET /api/reviews", s.listReviews)
	mux.HandleFunc("POST /api/tasks/{id}/accept", s.acceptTask)
	mux.HandleFunc("POST /api/tasks/{id}/reject", s.rejectTask)
	mux.HandleFunc("POST /api/tasks/{id}/retry", s.retryTask)

	// --- Audit queue (Phase 3: post-merge sampling of auto-merged work) ---
	mux.HandleFunc("GET /api/audits", s.listAudits)
	mux.HandleFunc("POST /api/tasks/{id}/audit-ok", s.ackAudit)
	mux.HandleFunc("POST /api/tasks/{id}/revert", s.revertTask)

	// --- Scheduling / steering (Phase 1) ---
	mux.HandleFunc("POST /api/tasks/{id}/assign", s.assignTask)
	mux.HandleFunc("POST /api/steer", s.steer)
	mux.HandleFunc("GET /api/metrics", s.getMetrics)

	// --- Ship: push the integration branch to its remote ---
	mux.HandleFunc("GET /api/push/status", s.pushStatus)
	mux.HandleFunc("POST /api/push", s.pushMain)

	// --- Planner: plans + decisions (Phase 2) ---
	mux.HandleFunc("GET /api/plans", s.listPlans)
	mux.HandleFunc("GET /api/plans/{id}", s.getPlan)
	mux.HandleFunc("POST /api/plans/{id}/approve", s.approvePlan)
	mux.HandleFunc("POST /api/plans/{id}/reject", s.rejectPlan)
	mux.HandleFunc("GET /api/decisions", s.listDecisions)
	mux.HandleFunc("POST /api/decisions/{id}/answer", s.answerDecision)

	// --- Settings (global store) ---
	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.putSettings)

	// --- Version ---
	mux.HandleFunc("GET /api/version", s.getVersion)

	// --- WebSocket events ---
	mux.HandleFunc("GET /api/events", s.hub.serveWS)

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

func (s *Server) getVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": s.version})
}

// mapStoreErr translates store errors into HTTP responses.
func mapStoreErr(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	writeErr(w, http.StatusInternalServerError, err.Error())
}

// mapEngineErr translates engine/store action errors into HTTP responses,
// mapping a NotFound to 404 and any other error to 409 Conflict (the action
// was rejected by the current state, not an internal fault).
func mapEngineErr(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	writeErr(w, http.StatusConflict, err.Error())
}
