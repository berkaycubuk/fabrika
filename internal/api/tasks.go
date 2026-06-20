package api

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/berkaycubuk/fabrika/internal/planner"
	"github.com/berkaycubuk/fabrika/internal/store"
)

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.store.Tasks.List()
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	if tasks == nil {
		tasks = []model.Task{}
	}
	tasks = filterTasks(tasks, r.URL.Query())
	tasks = s.engine.PushAnnotate(r.Context(), tasks)
	writeJSON(w, http.StatusOK, tasks)
}

// filterTasks applies additive query-parameter filtering to the task slice.
// Each supported param (status, agentId, riskTier, bigTaskId) accepts a single
// value or a comma-separated list (OR within a param); different params combine
// with AND. An absent or empty param applies no constraint on its field.
func filterTasks(tasks []model.Task, q url.Values) []model.Task {
	matches := func(values, field string) bool {
		values = strings.TrimSpace(values)
		if values == "" {
			return true
		}
		for _, v := range strings.Split(values, ",") {
			if strings.TrimSpace(v) == field {
				return true
			}
		}
		return false
	}
	out := []model.Task{}
	for _, t := range tasks {
		if matches(q.Get("status"), t.Status) &&
			matches(q.Get("agentId"), t.AgentID) &&
			matches(q.Get("riskTier"), t.RiskTier) &&
			matches(q.Get("bigTaskId"), t.BigTaskID) {
			out = append(out, t)
		}
	}
	return out
}

// taskDetail bundles a task with its attempt history for the detail view.
type taskDetail struct {
	Task     model.Task      `json:"task"`
	Attempts []model.Attempt `json:"attempts"`
}

func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t, err := s.store.Tasks.Get(id)
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	attempts, err := s.store.Attempts.ListForTask(id)
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	if attempts == nil {
		attempts = []model.Attempt{}
	}
	enriched := s.engine.PushAnnotate(r.Context(), []model.Task{*t})
	if len(enriched) == 1 {
		t = &enriched[0]
	}
	writeJSON(w, http.StatusOK, taskDetail{Task: *t, Attempts: attempts})
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	var t model.Task
	if err := decodeJSON(r, &t); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if t.Title == "" {
		writeErr(w, http.StatusBadRequest, "title is required")
		return
	}
	for _, a := range t.Attachments {
		if !isUploadURL(a) {
			writeErr(w, http.StatusBadRequest, "invalid attachment URL: "+a)
			return
		}
	}
	if t.RiskTier != "" && !model.ValidRiskTier(t.RiskTier) {
		writeErr(w, http.StatusBadRequest, "invalid riskTier: "+t.RiskTier)
		return
	}
	if t.Priority != "" && !model.ValidPriority(t.Priority) {
		writeErr(w, http.StatusBadRequest, "invalid priority: "+t.Priority)
		return
	}
	t.ID = ""
	t.Reporter = model.ReporterUser
	// Derive the risk tier from the paths the task will touch (manifest [risk]
	// globs) unless the caller pinned one explicitly. Drives per-tier routing
	// now and the merge gate in Phase 3.
	if t.RiskTier == "" && s.cfg != nil {
		t.RiskTier = s.cfg.TierFor(t.TouchPaths)
	}
	if err := s.store.Tasks.Create(&t); err != nil {
		mapStoreErr(w, err)
		return
	}
	s.hub.Broadcast(Event{Type: "task.created", Payload: t})
	s.engine.Wake()
	writeJSON(w, http.StatusCreated, t)
}

// listBigTasks returns every big task newest-first, so the Define surface can
// show their planning status (and any error reason) live.
func (s *Server) listBigTasks(w http.ResponseWriter, r *http.Request) {
	bts, err := s.store.BigTasks.List()
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	if bts == nil {
		bts = []model.BigTask{}
	}
	writeJSON(w, http.StatusOK, bts)
}

// getBigTask returns a single big task so agents can poll one item's status
// without fetching and filtering the whole list.
func (s *Server) getBigTask(w http.ResponseWriter, r *http.Request) {
	bt, err := s.store.BigTasks.Get(r.PathValue("id"))
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, bt)
}

// createBigTask stores the BigTask and decomposes it into work. When a planner
// agent is configured, planning runs asynchronously (the BigTask goes to
// `planning`; a proposed Plan with `planned` tasks + open decisions appears via
// events for the human to Approve). With no planner, it falls back to the Phase 0
// passthrough: a single ready task mirroring the intent. (SPECS §13 Phase 2.)
func (s *Server) createBigTask(w http.ResponseWriter, r *http.Request) {
	var bt model.BigTask
	if err := decodeJSON(r, &bt); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if bt.Title == "" {
		writeErr(w, http.StatusBadRequest, "title is required")
		return
	}
	for _, a := range bt.Attachments {
		if !isUploadURL(a) {
			writeErr(w, http.StatusBadRequest, "invalid attachment URL: "+a)
			return
		}
	}
	// Backlog items are parked as-is: no Preflight, no planning, no tasks.
	if bt.Status == model.BigTaskBacklog {
		bt.ID = ""
		if err := s.store.BigTasks.Create(&bt); err != nil {
			mapStoreErr(w, err)
			return
		}
		s.hub.Broadcast(Event{Type: "bigtask.created", Payload: bt})
		writeJSON(w, http.StatusCreated, bt)
		return
	}
	// Preflight the repo synchronously so a missing initial commit (or non-repo)
	// surfaces here as a clear 400, instead of failing silently in the async
	// planner with a raw git error the UI never sees.
	if err := s.engine.Preflight(r.Context()); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	bt.ID = ""
	if err := s.store.BigTasks.Create(&bt); err != nil {
		mapStoreErr(w, err)
		return
	}
	s.hub.Broadcast(Event{Type: "bigtask.created", Payload: bt})

	// Planner agent available -> decompose asynchronously into a proposed plan.
	if _, ok := s.engine.PlannerAgent(); ok {
		s.engine.PlanBigTask(bt)
		writeJSON(w, http.StatusCreated, bt)
		return
	}

	// Fallback: no planner registered -> one ready task mirroring the intent.
	plan := planner.Passthrough(bt)
	for i := range plan.Tasks {
		t := plan.Tasks[i]
		if err := s.store.Tasks.Create(&t); err != nil {
			mapStoreErr(w, err)
			return
		}
		s.hub.Broadcast(Event{Type: "task.created", Payload: t})
	}
	// Advance the big task out of draft so it isn't a dead-end in the UI: its
	// task is already ready, mirroring ApprovePlan's transition to running.
	if err := s.store.BigTasks.UpdateStatus(bt.ID, model.BigTaskRunning); err != nil {
		mapStoreErr(w, err)
		return
	}
	bt.Status = model.BigTaskRunning
	s.hub.Broadcast(Event{Type: "bigtask.updated", Payload: bt})
	s.engine.Wake()

	writeJSON(w, http.StatusCreated, bt)
}

// promoteBigTask moves a backlog BigTask into the planning flow. It runs the
// same decomposition logic createBigTask uses for a freshly created draft.
func (s *Server) promoteBigTask(w http.ResponseWriter, r *http.Request) {
	bt, err := s.store.BigTasks.Get(r.PathValue("id"))
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	if bt.Status != model.BigTaskBacklog {
		writeErr(w, http.StatusConflict, "big task is "+bt.Status+", not in backlog")
		return
	}
	if err := s.engine.Preflight(r.Context()); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if plannerAgent, ok := s.engine.PlannerAgent(); ok {
		if err := s.store.BigTasks.UpdateStatus(bt.ID, model.BigTaskDraft); err != nil {
			mapStoreErr(w, err)
			return
		}
		bt.Status = model.BigTaskDraft
		if err := s.store.BigTasks.SetPlannerAgent(bt.ID, plannerAgent.ID); err != nil {
			mapStoreErr(w, err)
			return
		}
		bt.PlannerAgentID = plannerAgent.ID
		s.engine.PlanBigTask(*bt)
		writeJSON(w, http.StatusOK, bt)
		return
	}
	plan := planner.Passthrough(*bt)
	for i := range plan.Tasks {
		t := plan.Tasks[i]
		if err := s.store.Tasks.Create(&t); err != nil {
			mapStoreErr(w, err)
			return
		}
		s.hub.Broadcast(Event{Type: "task.created", Payload: t})
	}
	if err := s.store.BigTasks.UpdateStatus(bt.ID, model.BigTaskRunning); err != nil {
		mapStoreErr(w, err)
		return
	}
	bt.Status = model.BigTaskRunning
	s.hub.Broadcast(Event{Type: "bigtask.updated", Payload: bt})
	s.engine.Wake()
	writeJSON(w, http.StatusOK, bt)
}

// replanBigTask re-queues an errored plan request for planning, so a planner
// failure is recoverable from the UI instead of a delete-and-retype dead end.
func (s *Server) replanBigTask(w http.ResponseWriter, r *http.Request) {
	bt, err := s.store.BigTasks.Get(r.PathValue("id"))
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	if bt.Status != model.BigTaskError {
		writeErr(w, http.StatusConflict, "plan request is "+bt.Status+", not errored")
		return
	}
	if _, ok := s.engine.PlannerAgent(); !ok {
		writeErr(w, http.StatusConflict, "no planner agent is enabled — enable one in Agents, then retry")
		return
	}
	s.engine.PlanBigTask(*bt)
	writeJSON(w, http.StatusOK, map[string]string{"status": model.BigTaskDraft})
}

func (s *Server) stopBigTask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	_ = decodeJSON(r, &body) // body is optional
	if err := s.engine.CancelPlanning(r.PathValue("id"), body.Reason); err != nil {
		mapEngineErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) reorderBigTasks(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.BigTasks.Reorder(body.IDs); err != nil {
		mapStoreErr(w, err)
		return
	}
	s.hub.Broadcast(Event{Type: "bigtask.reordered", Payload: body.IDs})
	writeJSON(w, http.StatusOK, map[string]any{"ids": body.IDs})
}

func (s *Server) deleteBigTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.engine.DeleteBigTask(id); err != nil {
		mapEngineErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// reviewItem is a surfaced task awaiting human judgment, with its latest attempt.
type reviewItem struct {
	Task    model.Task     `json:"task"`
	Attempt *model.Attempt `json:"attempt"`
}

// collectReviews gathers tasks awaiting the human: review, failed, blocked.
func (s *Server) collectReviews() ([]reviewItem, error) {
	tasks, err := s.store.Tasks.List()
	if err != nil {
		return nil, err
	}
	items := []reviewItem{}
	for _, t := range tasks {
		switch t.Status {
		case model.TaskReview, model.TaskFailed, model.TaskBlocked:
			att, err := s.store.Attempts.LatestForTask(t.ID)
			if err != nil && err != store.ErrNotFound {
				return nil, err
			}
			items = append(items, reviewItem{Task: t, Attempt: att})
		}
	}
	return items, nil
}

// listReviews returns tasks awaiting the human: green (review), red (failed),
// and escalated (blocked), each with its latest attempt's evidence.
func (s *Server) listReviews(w http.ResponseWriter, r *http.Request) {
	items, err := s.collectReviews()
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) acceptTask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Force bool `json:"force"`
	}
	_ = decodeJSON(r, &body) // body is optional; absent means a normal accept
	if err := s.engine.Accept(r.PathValue("id"), body.Force); err != nil {
		// A stale-branch conflict is handed to the agent to auto-resolve and merge;
		// that is a successful hand-off, not an error the human must act on.
		if s.engine.IsResolutionStarted(err) {
			writeJSON(w, http.StatusOK, map[string]string{"status": "resolving"})
			return
		}
		mapEngineErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "merged"})
}

// deleteTask permanently discards a closed (kicked-back) task. The engine
// enforces that only closed tasks qualify.
func (s *Server) deleteTask(w http.ResponseWriter, r *http.Request) {
	if err := s.engine.DeleteTask(r.PathValue("id")); err != nil {
		mapEngineErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) retryTask(w http.ResponseWriter, r *http.Request) {
	if err := s.engine.Retry(r.PathValue("id")); err != nil {
		mapEngineErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// requestChanges sends a review-state task back for another run. The guidance
// is recorded as a user comment so the next run's prompt carries it; absent
// guidance is allowed (earlier comments still ride along).
func (s *Server) requestChanges(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Guidance string `json:"guidance"`
	}
	_ = decodeJSON(r, &body)
	if err := s.engine.RequestChanges(r.PathValue("id"), strings.TrimSpace(body.Guidance)); err != nil {
		mapEngineErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) rejectTask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	_ = decodeJSON(r, &body) // reason is optional
	if err := s.engine.Reject(r.PathValue("id"), body.Reason); err != nil {
		mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "closed"})
}

type batchResult struct {
	ID  string `json:"id"`
	OK  bool   `json:"ok"`
	Err string `json:"error,omitempty"`
}

func (s *Server) acceptBatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(body.IDs) == 0 {
		writeErr(w, http.StatusBadRequest, "ids is required")
		return
	}
	results := make([]batchResult, len(body.IDs))
	for i, id := range body.IDs {
		results[i] = batchResult{ID: id}
		// A conflict handed to the agent for auto-resolution is a successful
		// hand-off, not a batch failure.
		if err := s.engine.Accept(id, false); err != nil && !s.engine.IsResolutionStarted(err) {
			results[i].Err = err.Error()
		} else {
			results[i].OK = true
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (s *Server) retryBatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(body.IDs) == 0 {
		writeErr(w, http.StatusBadRequest, "ids is required")
		return
	}
	results := make([]batchResult, len(body.IDs))
	for i, id := range body.IDs {
		results[i] = batchResult{ID: id}
		if err := s.engine.Retry(id); err != nil {
			results[i].Err = err.Error()
		} else {
			results[i].OK = true
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}
