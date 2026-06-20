package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/berkaycubuk/fabrika/internal/model"
)

// maxAttentionWait caps how long a single long-poll may block, regardless of
// the client's requested `wait`.
const maxAttentionWait = 60 * time.Second

// attentionResponse bundles everything needing agent/human judgment. Cursor is
// the monotonic event sequence at the time the snapshot was taken; clients pass
// it back as `since` to long-poll for the next change.
type attentionResponse struct {
	Reviews   []reviewItem     `json:"reviews"`
	Decisions []model.Decision `json:"decisions"`
	Audits    []reviewItem     `json:"audits"`
	Plans     []planView       `json:"plans"`
	Cursor    int64            `json:"cursor"`
}

// getAttention returns reviews, audits, decisions, and plans in one response.
//
// It supports two optional query params for long-polling:
//   - since (int64): the cursor the client last observed.
//   - wait  (int, seconds, capped at 60): how long to block when there is
//     nothing new. <=0 or absent means return immediately.
//
// When wait > 0 and the hub's current sequence is still <= since, the request
// blocks until ANY event is broadcast, the wait timeout elapses, or the client
// disconnects, then returns a fresh snapshot. This is a notify-then-snapshot
// scheme, not a precise diff: the long-poll wakes on any broadcast event, so
// the returned snapshot may be unchanged in the dimensions a given client cares
// about. Clients compare cursors (and the payload) to decide what changed.
func (s *Server) getAttention(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	wait := 0
	if v, err := strconv.Atoi(r.URL.Query().Get("wait")); err == nil {
		wait = v
	}

	// Block only when asked to wait and there is nothing newer than `since`. If
	// the cursor has already advanced we fall straight through to the snapshot.
	if wait > 0 && s.hub.Seq() <= since {
		d := time.Duration(wait) * time.Second
		if d > maxAttentionWait {
			d = maxAttentionWait
		}
		events, cancel := s.hub.Subscribe(1)
		defer cancel()
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-events: // any broadcast wakes us; we return a fresh snapshot
		case <-timer.C: // wait timeout elapsed
		case <-r.Context().Done(): // client disconnected
		}
	}

	resp, err := s.attentionSnapshot()
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// attentionSnapshot collects the current attention feed and stamps it with the
// hub's current event cursor.
func (s *Server) attentionSnapshot() (attentionResponse, error) {
	reviews, err := s.collectReviews()
	if err != nil {
		return attentionResponse{}, err
	}
	audits, err := s.collectAudits()
	if err != nil {
		return attentionResponse{}, err
	}
	decisions, err := s.collectDecisions()
	if err != nil {
		return attentionResponse{}, err
	}
	allPlans, err := s.collectPlans()
	if err != nil {
		return attentionResponse{}, err
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
	return attentionResponse{
		Reviews:   reviews,
		Decisions: decisions,
		Audits:    audits,
		Plans:     plans,
		Cursor:    s.hub.Seq(),
	}, nil
}
