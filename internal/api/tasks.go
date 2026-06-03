package api

import (
	"errors"
	"net/http"

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
	writeJSON(w, http.StatusOK, tasks)
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
	t.ID = ""
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

// reviewItem is a surfaced task awaiting human judgment, with its latest attempt.
type reviewItem struct {
	Task    model.Task     `json:"task"`
	Attempt *model.Attempt `json:"attempt"`
}

// listReviews returns tasks awaiting the human: green (review), red (failed),
// and escalated (blocked), each with its latest attempt's evidence.
func (s *Server) listReviews(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.store.Tasks.List()
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	items := []reviewItem{}
	for _, t := range tasks {
		switch t.Status {
		case model.TaskReview, model.TaskFailed, model.TaskBlocked:
			att, err := s.store.Attempts.LatestForTask(t.ID)
			if err != nil && err != store.ErrNotFound {
				mapStoreErr(w, err)
				return
			}
			items = append(items, reviewItem{Task: t, Attempt: att})
		}
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) acceptTask(w http.ResponseWriter, r *http.Request) {
	if err := s.engine.Accept(r.PathValue("id")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "merged"})
}

func (s *Server) retryTask(w http.ResponseWriter, r *http.Request) {
	if err := s.engine.Retry(r.PathValue("id")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		writeErr(w, http.StatusConflict, err.Error())
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
