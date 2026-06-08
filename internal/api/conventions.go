package api

import (
	"net/http"

	"github.com/berkaycubuk/fabrika/internal/model"
)

func (s *Server) listConventions(w http.ResponseWriter, r *http.Request) {
	conventions, err := s.store.Conventions.List()
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	if conventions == nil {
		conventions = []model.Convention{}
	}
	writeJSON(w, http.StatusOK, conventions)
}

func (s *Server) createConvention(w http.ResponseWriter, r *http.Request) {
	var c model.Convention
	if err := decodeJSON(r, &c); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if c.Rule == "" {
		writeErr(w, http.StatusBadRequest, "rule must not be empty")
		return
	}
	c.ID = "" // server assigns
	if err := s.store.Conventions.Create(&c); err != nil {
		mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) deleteConvention(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.store.Conventions.Delete(id); err != nil {
		mapStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
