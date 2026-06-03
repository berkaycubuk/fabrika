package api

import (
	"net/http"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/berkaycubuk/fabrika/internal/planner"
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

func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	t, err := s.store.Tasks.Get(r.PathValue("id"))
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
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
	if err := s.store.Tasks.Create(&t); err != nil {
		mapStoreErr(w, err)
		return
	}
	s.hub.Broadcast(Event{Type: "task.created", Payload: t})
	writeJSON(w, http.StatusCreated, t)
}

// createBigTask stores the BigTask and, via the Phase 0 passthrough planner,
// also materializes its task(s) so the board has work to show. The planner agent
// (Phase 2) will replace the passthrough.
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
	bt.ID = ""
	if err := s.store.BigTasks.Create(&bt); err != nil {
		mapStoreErr(w, err)
		return
	}
	s.hub.Broadcast(Event{Type: "bigtask.created", Payload: bt})

	// Phase 0 passthrough: one task mirroring the intent.
	plan := planner.Plan(bt)
	for i := range plan.Tasks {
		t := plan.Tasks[i]
		if err := s.store.Tasks.Create(&t); err != nil {
			mapStoreErr(w, err)
			return
		}
		s.hub.Broadcast(Event{Type: "task.created", Payload: t})
	}

	writeJSON(w, http.StatusCreated, bt)
}
