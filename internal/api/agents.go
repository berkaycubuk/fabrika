package api

import (
	"net/http"

	"github.com/berkaycubuk/fabrika/internal/model"
)

// maxPhotoBytes caps the decoded photo data URI string at 2 MiB.
const maxPhotoBytes = 2 * 1024 * 1024

func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := s.store.Agents.List()
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	if agents == nil {
		agents = []model.Agent{}
	}
	writeJSON(w, http.StatusOK, agents)
}

func (s *Server) createAgent(w http.ResponseWriter, r *http.Request) {
	var a model.Agent
	if err := decodeJSON(r, &a); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if a.Name == "" || a.Command == "" {
		writeErr(w, http.StatusBadRequest, "name and command are required")
		return
	}
	if len(a.Photo) > maxPhotoBytes {
		writeErr(w, http.StatusBadRequest, "photo exceeds maximum size of 2 MiB")
		return
	}
	a.ID = "" // server assigns
	if err := s.store.Agents.Create(&a); err != nil {
		mapStoreErr(w, err)
		return
	}
	s.hub.Broadcast(Event{Type: "agent.created", Payload: a})
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) updateAgent(w http.ResponseWriter, r *http.Request) {
	var a model.Agent
	if err := decodeJSON(r, &a); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.ID = r.PathValue("id")
	if a.Name == "" || a.Command == "" {
		writeErr(w, http.StatusBadRequest, "name and command are required")
		return
	}
	if len(a.Photo) > maxPhotoBytes {
		writeErr(w, http.StatusBadRequest, "photo exceeds maximum size of 2 MiB")
		return
	}
	if err := s.store.Agents.Update(&a); err != nil {
		mapStoreErr(w, err)
		return
	}
	s.hub.Broadcast(Event{Type: "agent.updated", Payload: a})
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) deleteAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.store.Agents.Delete(id); err != nil {
		mapStoreErr(w, err)
		return
	}
	s.hub.Broadcast(Event{Type: "agent.deleted", Payload: map[string]string{"id": id}})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) enableAgent(w http.ResponseWriter, r *http.Request)  { s.setAgentEnabled(w, r, true) }
func (s *Server) disableAgent(w http.ResponseWriter, r *http.Request) { s.setAgentEnabled(w, r, false) }

func (s *Server) setAgentEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	id := r.PathValue("id")
	if err := s.store.Agents.SetEnabled(id, enabled); err != nil {
		mapStoreErr(w, err)
		return
	}
	a, err := s.store.Agents.Get(id)
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	s.hub.Broadcast(Event{Type: "agent.updated", Payload: *a})
	writeJSON(w, http.StatusOK, a)
}
