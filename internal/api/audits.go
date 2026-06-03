package api

import (
	"errors"
	"net/http"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/berkaycubuk/fabrika/internal/store"
)

// listAudits returns auto-merged tasks sampled for a post-merge audit and not yet
// acknowledged or reverted, each with its latest attempt's evidence. This is the
// trust backstop for autonomy: a random share of machine-merged work the human
// still eyeballs after the fact (SPECS §13 Phase 3).
func (s *Server) listAudits(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.store.Tasks.List()
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	items := []reviewItem{}
	for _, t := range tasks {
		if t.Status != model.TaskMerged || !t.AuditFlagged || t.Reverted {
			continue
		}
		att, err := s.store.Attempts.LatestForTask(t.ID)
		if err != nil && err != store.ErrNotFound {
			mapStoreErr(w, err)
			return
		}
		items = append(items, reviewItem{Task: t, Attempt: att})
	}
	writeJSON(w, http.StatusOK, items)
}

// ackAudit clears a sampled task's audit flag ("looks good"), removing it from
// the audit queue without otherwise touching the merge.
func (s *Server) ackAudit(w http.ResponseWriter, r *http.Request) {
	if err := s.engine.AckAudit(r.PathValue("id")); err != nil {
		mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// revertTask records a merged task as a change-failure (it feeds the
// change-failure-rate metric). The git revert itself is left to the human.
func (s *Server) revertTask(w http.ResponseWriter, r *http.Request) {
	if err := s.engine.Revert(r.PathValue("id")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "reverted"})
}
