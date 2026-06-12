package api

import (
	"net/http"

	"github.com/berkaycubuk/fabrika/internal/model"
)

// Interactive chat sessions (SPECS.md §16): the in-UI replacement for ad-hoc
// terminal work. Creation, turns, finish, and discard are engine actions; the
// store serves reads, with the transient busy flag filled in by the engine.

func (s *Server) listSessions(w http.ResponseWriter, _ *http.Request) {
	sessions, err := s.store.Sessions.List()
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	if sessions == nil {
		sessions = []model.Session{}
	}
	for i := range sessions {
		sessions[i].Busy = s.engine.SessionBusy(sessions[i].ID)
	}
	writeJSON(w, http.StatusOK, sessions)
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AgentID    string `json:"agentId"`
		Model      string `json:"model"`
		BaseBranch string `json:"baseBranch"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.AgentID == "" {
		writeErr(w, http.StatusBadRequest, "agentId is required")
		return
	}
	sess, err := s.engine.CreateSession(in.AgentID, in.Model, in.BaseBranch)
	if err != nil {
		mapEngineErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sess)
}

// sessionDetail bundles a session with its transcript for the chat view.
type sessionDetail struct {
	Session  model.Session          `json:"session"`
	Messages []model.SessionMessage `json:"messages"`
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, err := s.store.Sessions.Get(id)
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	msgs, err := s.store.Sessions.Messages(id)
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	if msgs == nil {
		msgs = []model.SessionMessage{}
	}
	sess.Busy = s.engine.SessionBusy(id)
	writeJSON(w, http.StatusOK, sessionDetail{Session: *sess, Messages: msgs})
}

func (s *Server) sendSessionMessage(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Body        string   `json:"body"`
		Attachments []string `json:"attachments"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// Attachments must be URLs createUpload produced, never arbitrary strings.
	for _, a := range in.Attachments {
		if !isUploadURL(a) {
			writeErr(w, http.StatusBadRequest, "invalid attachment URL: "+a)
			return
		}
	}
	msg, err := s.engine.SendSessionMessage(r.PathValue("id"), in.Body, in.Attachments)
	if err != nil {
		mapEngineErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, msg)
}

func (s *Server) finishSession(w http.ResponseWriter, r *http.Request) {
	if err := s.engine.FinishSession(r.PathValue("id")); err != nil {
		mapEngineErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": model.SessionGating})
}

func (s *Server) discardSession(w http.ResponseWriter, r *http.Request) {
	if err := s.engine.DiscardSession(r.PathValue("id")); err != nil {
		mapEngineErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": model.SessionClosed})
}
