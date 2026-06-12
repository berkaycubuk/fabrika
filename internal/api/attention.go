package api

import (
	"net/http"

	"github.com/berkaycubuk/fabrika/internal/model"
)

// attentionResponse bundles everything needing agent/human judgment.
type attentionResponse struct {
	Reviews   []reviewItem     `json:"reviews"`
	Decisions []model.Decision `json:"decisions"`
	Audits    []reviewItem     `json:"audits"`
	Plans     []planView       `json:"plans"`
}

// getAttention returns reviews, audits, decisions, and plans in one response.
func (s *Server) getAttention(w http.ResponseWriter, r *http.Request) {
	reviews, err := s.collectReviews()
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	audits, err := s.collectAudits()
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	decisions, err := s.collectDecisions()
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	allPlans, err := s.collectPlans()
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	// Only plans still awaiting judgment belong in the attention feed. The
	// desktop Approve view filters client-side, but remote clients (phone
	// relay) render this response as-is.
	plans := []planView{}
	for _, p := range allPlans {
		if p.Status == model.PlanProposed {
			plans = append(plans, p)
		}
	}
	writeJSON(w, http.StatusOK, attentionResponse{
		Reviews:   reviews,
		Decisions: decisions,
		Audits:    audits,
		Plans:     plans,
	})
}
