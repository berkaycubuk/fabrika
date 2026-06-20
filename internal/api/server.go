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
	"time"

	"github.com/berkaycubuk/fabrika/internal/config"
	"github.com/berkaycubuk/fabrika/internal/engine"
	"github.com/berkaycubuk/fabrika/internal/git"
	"github.com/berkaycubuk/fabrika/internal/relay"
	"github.com/berkaycubuk/fabrika/internal/store"
)

// Server holds the dependencies shared across handlers.
type Server struct {
	store       *store.Store
	cfg         *config.Config
	hub         *Hub
	web         fs.FS  // embedded static UI assets (may be nil in tests)
	repoRoot    string // project root; uploads live under <repoRoot>/.fabrika/uploads
	engine      *engine.Engine
	relay       *relay.Manager
	notifier    *relay.Notifier
	version     string
	BuildCommit string // set after construction to the build-time commit hash
}

// NewServer constructs a Server and its engine. cfg + repoRoot configure the
// dispatch loop; web is the embedded UI filesystem (nil disables static
// serving). The engine emits UI events through the WebSocket hub.
func NewServer(s *store.Store, cfg *config.Config, repoRoot string, web fs.FS, version string) *Server {
	srv := &Server{store: s, cfg: cfg, hub: newHub(), web: web, repoRoot: repoRoot, version: version}
	srv.engine = engine.New(s, cfg, repoRoot, func(t string, p any) {
		srv.hub.Broadcast(Event{Type: t, Payload: p})
	})
	subscribe := func(buf int) (<-chan relay.Event, func()) {
		ch, cancel := srv.hub.Subscribe(buf)
		out := make(chan relay.Event, buf)
		go func() {
			defer close(out)
			for e := range ch {
				out <- relay.Event{Type: e.Type, Payload: e.Payload}
			}
		}()
		return out, cancel
	}
	srv.relay = relay.NewManager(relay.Options{
		Store:       s,
		Subscribe:   subscribe,
		ProjectRoot: repoRoot,
		ProjectName: cfg.Project.Name,
	})
	srv.notifier = relay.NewNotifier(s, srv.relay, subscribe)
	return srv
}

// Start launches the engine dispatch loop, the relay tunnel (if enabled), and
// the push notifier until ctx is cancelled.
func (s *Server) Start(ctx context.Context) {
	s.engine.Start(ctx)
	s.relay.Start(ctx)
	s.notifier.Start(ctx)
}

// Stop drains in-flight engine goroutines with the given timeout.
// Returns true if all goroutines finished within the timeout.
func (s *Server) Stop(timeout time.Duration) bool {
	return s.engine.Stop(timeout)
}

// Handler builds the full HTTP routing tree.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// --- Conventions (global store) ---
	mux.HandleFunc("GET /api/conventions", s.listConventions)
	mux.HandleFunc("POST /api/conventions", s.createConvention)
	mux.HandleFunc("DELETE /api/conventions/{id}", s.deleteConvention)
	mux.HandleFunc("POST /api/conventions/{id}/approve", s.approveConvention)
	mux.HandleFunc("POST /api/conventions/{id}/reject", s.rejectConvention)

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
	mux.HandleFunc("GET /api/tasks/{id}/history", s.listTaskHistory)
	mux.HandleFunc("POST /api/tasks/{id}/comments", s.createComment)
	mux.HandleFunc("GET /api/bigtasks/{id}/comments", s.listBigTaskComments)
	mux.HandleFunc("POST /api/bigtasks/{id}/comments", s.createBigTaskComment)
	mux.HandleFunc("POST /api/uploads", s.createUpload)
	mux.HandleFunc("GET /api/uploads/{name}", s.getUpload)
	mux.HandleFunc("GET /api/bigtasks", s.listBigTasks)
	mux.HandleFunc("GET /api/bigtasks/{id}", s.getBigTask)
	mux.HandleFunc("GET /api/bigtasks/{id}/activity", s.getBigTaskActivity)
	mux.HandleFunc("POST /api/bigtasks", s.createBigTask)
	mux.HandleFunc("POST /api/bigtasks/reorder", s.reorderBigTasks)
	mux.HandleFunc("DELETE /api/bigtasks/{id}", s.deleteBigTask)
	mux.HandleFunc("POST /api/bigtasks/{id}/plan", s.promoteBigTask)
	mux.HandleFunc("POST /api/bigtasks/{id}/replan", s.replanBigTask)
	mux.HandleFunc("POST /api/bigtasks/{id}/stop", s.stopBigTask)

	// --- Accept queue (live loop) ---
	mux.HandleFunc("GET /api/reviews", s.listReviews)
	mux.HandleFunc("POST /api/tasks/{id}/accept", s.acceptTask)
	mux.HandleFunc("POST /api/tasks/{id}/reject", s.rejectTask)
	mux.HandleFunc("POST /api/tasks/{id}/retry", s.retryTask)
	mux.HandleFunc("POST /api/tasks/{id}/request-changes", s.requestChanges)
	mux.HandleFunc("POST /api/tasks/accept-batch", s.acceptBatch)
	mux.HandleFunc("POST /api/tasks/retry-batch", s.retryBatch)

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
	// Relay-facing alias: the phone PWA ships the branch via this path and
	// expects structured {pushed, branch, remote} rather than a free-text summary.
	mux.HandleFunc("POST /api/git/push", s.gitPush)

	// --- Releases (Phase 4) ---
	mux.HandleFunc("GET /api/releases", s.listReleases)
	mux.HandleFunc("POST /api/releases/ship", s.shipRelease)
	mux.HandleFunc("GET /api/releases/unshipped", s.unshippedReleases)
	mux.HandleFunc("GET /api/releases/{id}", s.getRelease)
	mux.HandleFunc("POST /api/releases/{id}/rollback", s.rollbackRelease)

	// --- Attention: unified judgment feed ---
	mux.HandleFunc("GET /api/attention", s.getAttention)

	// --- Interactive chat sessions ---
	mux.HandleFunc("GET /api/sessions", s.listSessions)
	mux.HandleFunc("POST /api/sessions", s.createSession)
	mux.HandleFunc("GET /api/sessions/{id}", s.getSession)
	mux.HandleFunc("POST /api/sessions/{id}/messages", s.sendSessionMessage)
	mux.HandleFunc("POST /api/sessions/{id}/finish", s.finishSession)
	mux.HandleFunc("POST /api/sessions/{id}/discard", s.discardSession)

	// --- Crons ---
	mux.HandleFunc("GET /api/crons", s.listCrons)
	mux.HandleFunc("POST /api/crons", s.createCron)
	mux.HandleFunc("PUT /api/crons/{id}", s.updateCron)
	mux.HandleFunc("DELETE /api/crons/{id}", s.deleteCron)
	mux.HandleFunc("POST /api/crons/{id}/enable", s.enableCron)
	mux.HandleFunc("POST /api/crons/{id}/disable", s.disableCron)
	mux.HandleFunc("POST /api/crons/{id}/run", s.runCron)

	// --- Planner: plans + decisions (Phase 2) ---
	mux.HandleFunc("GET /api/plans", s.listPlans)
	mux.HandleFunc("GET /api/plans/{id}", s.getPlan)
	mux.HandleFunc("POST /api/plans/{id}/approve", s.approvePlan)
	mux.HandleFunc("POST /api/plans/{id}/reject", s.rejectPlan)
	mux.HandleFunc("POST /api/plans/{id}/revise", s.revisePlan)
	mux.HandleFunc("GET /api/decisions", s.listDecisions)
	mux.HandleFunc("POST /api/decisions/{id}/answer", s.answerDecision)

	// --- Settings (global store) ---
	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.putSettings)

	// --- Config (per-repo fabrika.toml manifest) ---
	mux.HandleFunc("GET /api/config", s.getConfig)
	mux.HandleFunc("PUT /api/config", s.putConfig)

	// --- Relay (phone access through a fabrika-portal server) ---
	mux.HandleFunc("GET /api/relay", s.getRelay)
	mux.HandleFunc("PUT /api/relay", s.putRelay)
	mux.HandleFunc("POST /api/relay/pair", s.pairRelay)
	mux.HandleFunc("DELETE /api/relay/devices/{id}", s.deleteRelayDevice)

	// --- Version ---
	mux.HandleFunc("GET /api/version", s.getVersion)

	// --- WebSocket events ---
	mux.HandleFunc("GET /api/events", s.hub.serveWS)

	// --- Static UI (SPA) ---
	if s.web != nil {
		mux.Handle("/", s.spaHandler())
	}

	inner := logRequests(recoverPanics(mux))
	// The relay RPC bridge replays phone requests through this tree directly: it
	// authenticates devices via the Noise handshake + method allowlist and synth-
	// esizes in-process requests (non-loopback Host), so it must bypass the
	// browser-facing origin guard below.
	s.relay.SetHandler(inner)
	// The public listener enforces a same-origin / loopback-Host policy to block
	// CSRF and DNS-rebinding from any page the user's browser visits.
	return guardOrigin(inner)
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
	project := ""
	if s.cfg != nil {
		project = s.cfg.Project.Name
	}
	behindHead := 0
	repo, err := git.Open(context.Background(), s.repoRoot)
	if err == nil {
		behindHead, _ = repo.BehindHead(context.Background(), s.BuildCommit)
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": s.version, "project": project, "behindHead": behindHead})
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
