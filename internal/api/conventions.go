package api

import (
	"net/http"

	"github.com/berkaycubuk/fabrika/internal/model"
)

func (s *Server) listConventions(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	var conventions []model.Convention
	var err error
	if status == "" {
		conventions, err = s.store.Conventions.ListAll()
	} else {
		if !model.ValidConventionStatus(status) {
			writeErr(w, http.StatusBadRequest, "invalid status")
			return
		}
		conventions, err = s.store.Conventions.ListByStatus(status)
	}
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

func (s *Server) approveConvention(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.store.Conventions.SetStatus(id, model.ConventionApproved); err != nil {
		mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": model.ConventionApproved})
}

func (s *Server) rejectConvention(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.store.Conventions.SetStatus(id, model.ConventionRejected); err != nil {
		mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": model.ConventionRejected})
}
