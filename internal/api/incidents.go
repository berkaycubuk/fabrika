package api

import (
	"net/http"

	"github.com/berkaycubuk/fabrika/internal/model"
)

func (s *Server) listIncidents(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	incidents, err := s.engine.ListIncidents(status)
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	if incidents == nil {
		incidents = []model.Incident{}
	}
	writeJSON(w, http.StatusOK, incidents)
}

func (s *Server) ignoreIncident(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.engine.IgnoreIncident(id); err != nil {
		mapEngineErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
}

func (s *Server) resolveIncident(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.engine.ResolveIncident(id); err != nil {
		mapEngineErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "resolved"})
}
