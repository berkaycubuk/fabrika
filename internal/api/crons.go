package api

import (
	"net/http"
	"time"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/berkaycubuk/fabrika/internal/schedule"
)

func (s *Server) listCrons(w http.ResponseWriter, r *http.Request) {
	crons, err := s.store.Crons.List()
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	if crons == nil {
		crons = []model.CronSchedule{}
	}
	writeJSON(w, http.StatusOK, crons)
}

func (s *Server) createCron(w http.ResponseWriter, r *http.Request) {
	var c model.CronSchedule
	if err := decodeJSON(r, &c); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if c.Title == "" || c.Prompt == "" || c.AgentID == "" {
		writeErr(w, http.StatusBadRequest, "title, prompt, and agentId are required")
		return
	}
	if err := schedule.Valid(c.Expr); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	c.ID = ""
	c.CreatedAt = ""
	c.LastRunAt = ""
	next, err := schedule.Next(c.Expr, time.Now())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	c.NextRunAt = next.UTC().Format(time.RFC3339)
	if err := s.store.Crons.Create(&c); err != nil {
		mapStoreErr(w, err)
		return
	}
	s.hub.Broadcast(Event{Type: "cron.created", Payload: c})
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) updateCron(w http.ResponseWriter, r *http.Request) {
	var c model.CronSchedule
	if err := decodeJSON(r, &c); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	c.ID = r.PathValue("id")
	if c.Title == "" || c.Prompt == "" || c.AgentID == "" {
		writeErr(w, http.StatusBadRequest, "title, prompt, and agentId are required")
		return
	}
	if err := schedule.Valid(c.Expr); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	next, err := schedule.Next(c.Expr, time.Now())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	c.NextRunAt = next.UTC().Format(time.RFC3339)
	if err := s.store.Crons.Update(&c); err != nil {
		mapStoreErr(w, err)
		return
	}
	s.hub.Broadcast(Event{Type: "cron.updated", Payload: c})
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) deleteCron(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.store.Crons.Delete(id); err != nil {
		mapStoreErr(w, err)
		return
	}
	s.hub.Broadcast(Event{Type: "cron.deleted", Payload: map[string]string{"id": id}})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) enableCron(w http.ResponseWriter, r *http.Request)  { s.setCronEnabled(w, r, true) }
func (s *Server) disableCron(w http.ResponseWriter, r *http.Request) { s.setCronEnabled(w, r, false) }

func (s *Server) setCronEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	id := r.PathValue("id")
	if err := s.store.Crons.SetEnabled(id, enabled); err != nil {
		mapStoreErr(w, err)
		return
	}
	c, err := s.store.Crons.Get(id)
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	s.hub.Broadcast(Event{Type: "cron.updated", Payload: *c})
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) runCron(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t, err := s.engine.FireCron(id)
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}
