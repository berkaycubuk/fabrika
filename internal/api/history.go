package api

import (
	"net/http"

	"github.com/berkaycubuk/fabrika/internal/model"
)

// listTaskHistory returns every status transition recorded for a task, oldest first.
func (s *Server) listTaskHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.store.Tasks.Get(id); err != nil {
		mapStoreErr(w, err)
		return
	}
	transitions, err := s.store.Transitions.ListForTask(id)
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	if transitions == nil {
		transitions = []model.TaskTransition{}
	}
	writeJSON(w, http.StatusOK, transitions)
}
