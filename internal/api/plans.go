package api

import (
	"net/http"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/berkaycubuk/fabrika/internal/store"
)

// planView is a plan assembled with its big task, tasks, and open decisions for
// the Approve surface (SPECS §10).
type planView struct {
	model.Plan
	BigTask *model.BigTask `json:"bigTask"`
}

// assemblePlan fills a bare plan row with its tasks (by big task) and decisions.
func (s *Server) assemblePlan(p model.Plan) (planView, error) {
	tasks, err := s.store.Tasks.ListByBigTask(p.BigTaskID)
	if err != nil {
		return planView{}, err
	}
	if tasks == nil {
		tasks = []model.Task{}
	}
	decisions, err := s.store.Decisions.ListForPlan(p.ID)
	if err != nil {
		return planView{}, err
	}
	if decisions == nil {
		decisions = []model.Decision{}
	}
	p.Tasks = tasks
	p.OpenDecisions = decisions
	bt, err := s.store.BigTasks.Get(p.BigTaskID)
	if err != nil && err != store.ErrNotFound {
		return planView{}, err
	}
	return planView{Plan: p, BigTask: bt}, nil
}

// collectPlans returns every plan assembled for the Approve view.
func (s *Server) collectPlans() ([]planView, error) {
	plans, err := s.store.Plans.List()
	if err != nil {
		return nil, err
	}
	out := []planView{}
	for _, p := range plans {
		pv, err := s.assemblePlan(p)
		if err != nil {
			return nil, err
		}
		out = append(out, pv)
	}
	return out, nil
}

// listPlans returns every plan assembled for the Approve view (newest-first).
func (s *Server) listPlans(w http.ResponseWriter, r *http.Request) {
	out, err := s.collectPlans()
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getPlan(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.Plans.Get(r.PathValue("id"))
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	pv, err := s.assemblePlan(*p)
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pv)
}

func (s *Server) approvePlan(w http.ResponseWriter, r *http.Request) {
	if err := s.engine.ApprovePlan(r.PathValue("id")); err != nil {
		mapEngineErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "approved"})
}

func (s *Server) rejectPlan(w http.ResponseWriter, r *http.Request) {
	if err := s.engine.RejectPlan(r.PathValue("id")); err != nil {
		mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
}

func (s *Server) revisePlan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Feedback    string   `json:"feedback"`
		Attachments []string `json:"attachments"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Feedback == "" {
		writeErr(w, http.StatusBadRequest, "feedback is required")
		return
	}
	for _, a := range body.Attachments {
		if !isUploadURL(a) {
			writeErr(w, http.StatusBadRequest, "invalid attachment URL: "+a)
			return
		}
	}
	if err := s.engine.RevisePlan(r.PathValue("id"), body.Feedback, body.Attachments); err != nil {
		mapEngineErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revising"})
}

// collectDecisions returns open decisions from the store.
func (s *Server) collectDecisions() ([]model.Decision, error) {
	ds, err := s.store.Decisions.ListOpen()
	if err != nil {
		return nil, err
	}
	if ds == nil {
		ds = []model.Decision{}
	}
	return ds, nil
}

// listDecisions returns the open decision queue (plan- and task-level).
func (s *Server) listDecisions(w http.ResponseWriter, r *http.Request) {
	ds, err := s.collectDecisions()
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ds)
}

func (s *Server) answerDecision(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Answer  string `json:"answer"`
		Promote bool   `json:"promote"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Answer == "" {
		writeErr(w, http.StatusBadRequest, "answer is required")
		return
	}
	if err := s.engine.AnswerDecision(r.PathValue("id"), body.Answer, body.Promote); err != nil {
		mapEngineErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "answered"})
}
