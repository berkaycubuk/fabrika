package api

import (
	"net/http"
	"strings"

	"github.com/berkaycubuk/fabrika/internal/model"
)

// listComments returns every comment on a task, oldest first.
func (s *Server) listComments(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.store.Tasks.Get(id); err != nil {
		mapStoreErr(w, err)
		return
	}
	comments, err := s.store.Comments.ListForTask(id)
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	if comments == nil {
		comments = []model.Comment{}
	}
	writeJSON(w, http.StatusOK, comments)
}

// listBigTaskComments returns every comment on a big task, oldest first.
func (s *Server) listBigTaskComments(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.store.BigTasks.Get(id); err != nil {
		mapStoreErr(w, err)
		return
	}
	comments, err := s.store.Comments.ListForBigTask(id)
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	if comments == nil {
		comments = []model.Comment{}
	}
	writeJSON(w, http.StatusOK, comments)
}

// createBigTaskComment appends a human-authored note to a big task and pushes it live.
func (s *Server) createBigTaskComment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.store.BigTasks.Get(id); err != nil {
		mapStoreErr(w, err)
		return
	}
	var body struct {
		Body        string   `json:"body"`
		Attachments []string `json:"attachments"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	trimmed := strings.TrimSpace(body.Body)
	if trimmed == "" && len(body.Attachments) == 0 {
		writeErr(w, http.StatusBadRequest, "body or attachments required")
		return
	}
	for _, a := range body.Attachments {
		if !isUploadURL(a) {
			writeErr(w, http.StatusBadRequest, "invalid attachment URL: "+a)
			return
		}
	}
	comment := model.Comment{
		BigTaskID:   id,
		AuthorType:  "user",
		Body:        trimmed,
		Attachments: body.Attachments,
	}
	if err := s.store.Comments.Create(&comment); err != nil {
		mapStoreErr(w, err)
		return
	}
	s.hub.Broadcast(Event{Type: "bigtask.comment.added", Payload: comment})
	writeJSON(w, http.StatusCreated, comment)
}

// createComment appends a human-authored note to a task and pushes it live.
// If AgentId is supplied the question is routed to that agent instead; the
// engine stores the comment and emits "task.comment.added" itself, so this
// handler returns 202 Accepted without broadcasting again.
func (s *Server) createComment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.store.Tasks.Get(id); err != nil {
		mapStoreErr(w, err)
		return
	}
	var body struct {
		Body        string   `json:"body"`
		Attachments []string `json:"attachments"`
		AgentId     string   `json:"agentId"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	trimmed := strings.TrimSpace(body.Body)
	if trimmed == "" && len(body.Attachments) == 0 {
		writeErr(w, http.StatusBadRequest, "body or attachments required")
		return
	}
	for _, a := range body.Attachments {
		if !isUploadURL(a) {
			writeErr(w, http.StatusBadRequest, "invalid attachment URL: "+a)
			return
		}
	}
	if body.AgentId != "" {
		ag, err := s.store.Agents.Get(body.AgentId)
		if err != nil {
			mapStoreErr(w, err)
			return
		}
		if !ag.Enabled {
			writeErr(w, http.StatusBadRequest, "agent is disabled")
			return
		}
		comment, err := s.engine.AskTaskQuestion(id, body.AgentId, trimmed, body.Attachments)
		if err != nil {
			mapEngineErr(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, comment)
		return
	}
	comment := model.Comment{
		TaskID:      id,
		AuthorType:  "user",
		AuthorID:    "",
		Body:        trimmed,
		Attachments: body.Attachments,
	}
	if err := s.store.Comments.Create(&comment); err != nil {
		mapStoreErr(w, err)
		return
	}
	s.hub.Broadcast(Event{Type: "task.comment.added", Payload: comment})
	writeJSON(w, http.StatusCreated, comment)
}
