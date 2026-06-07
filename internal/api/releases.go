package api

import (
	"net/http"

	"github.com/berkaycubuk/fabrika/internal/model"
)

func (s *Server) listReleases(w http.ResponseWriter, r *http.Request) {
	releases, err := s.engine.ListReleases()
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	if releases == nil {
		releases = []model.Release{}
	}
	writeJSON(w, http.StatusOK, releases)
}

func (s *Server) shipRelease(w http.ResponseWriter, r *http.Request) {
	rel, err := s.engine.Ship(r.Context())
	if err != nil {
		mapEngineErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rel)
}

func (s *Server) getRelease(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	detail, err := s.engine.GetRelease(id)
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) rollbackRelease(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rel, err := s.engine.Rollback(r.Context(), id)
	if err != nil {
		mapEngineErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rel)
}

func (s *Server) unshippedReleases(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.engine.UnshippedTasks()
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	if tasks == nil {
		tasks = []model.Task{}
	}
	writeJSON(w, http.StatusOK, tasks)
}
