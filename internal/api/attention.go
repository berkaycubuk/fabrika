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
	plans, err := s.collectPlans()
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, attentionResponse{
		Reviews:   reviews,
		Decisions: decisions,
		Audits:    audits,
		Plans:     plans,
	})
}
