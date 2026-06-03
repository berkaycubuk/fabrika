package api

import (
	"net/http"

	"github.com/berkaycubuk/fabrika/internal/store"
)

// assignTask pins a task to a specific agent (SPECS §7 routing, §10 steer). A
// ready task re-routes to that agent on the next dispatch; passing an empty
// agentId clears the pin. Takes effect on the task's next run, not in-flight.
func (s *Server) assignTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		AgentID string `json:"agentId"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.AgentID != "" {
		if _, err := s.store.Agents.Get(body.AgentID); err != nil {
			mapStoreErr(w, err)
			return
		}
	}
	if err := s.store.Tasks.SetPreferredAgent(id, body.AgentID); err != nil {
		mapStoreErr(w, err)
		return
	}
	t, err := s.store.Tasks.Get(id)
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	s.hub.Broadcast(Event{Type: "task.updated", Payload: *t})
	s.engine.Wake()
	writeJSON(w, http.StatusOK, t)
}

// steer is the general steering entrypoint (SPECS §10). Phase 1 supports
// redirecting a task to another agent ("assign") and cancelling a task
// ("cancel"); reprioritization and in-flight pause arrive with later phases.
func (s *Server) steer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action  string `json:"action"`
		TaskID  string `json:"taskId"`
		AgentID string `json:"agentId"`
		Reason  string `json:"reason"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.TaskID == "" {
		writeErr(w, http.StatusBadRequest, "taskId is required")
		return
	}

	switch body.Action {
	case "assign":
		if body.AgentID != "" {
			if _, err := s.store.Agents.Get(body.AgentID); err != nil {
				mapStoreErr(w, err)
				return
			}
		}
		if err := s.store.Tasks.SetPreferredAgent(body.TaskID, body.AgentID); err != nil {
			mapStoreErr(w, err)
			return
		}
		if t, err := s.store.Tasks.Get(body.TaskID); err == nil {
			s.hub.Broadcast(Event{Type: "task.updated", Payload: *t})
		}
		s.engine.Wake()
	case "cancel":
		if err := s.engine.Reject(body.TaskID, body.Reason); err != nil {
			if err == store.ErrNotFound {
				writeErr(w, http.StatusNotFound, "not found")
				return
			}
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
	default:
		writeErr(w, http.StatusBadRequest, "unknown action: "+body.Action)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
