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

// createComment appends a human-authored note to a task and pushes it live.
func (s *Server) createComment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.store.Tasks.Get(id); err != nil {
		mapStoreErr(w, err)
		return
	}
	var body struct {
		Body string `json:"body"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	trimmed := strings.TrimSpace(body.Body)
	if trimmed == "" {
		writeErr(w, http.StatusBadRequest, "body is required")
		return
	}
	comment := model.Comment{
		TaskID:     id,
		AuthorType: "user",
		AuthorID:   "",
		Body:       trimmed,
	}
	if err := s.store.Comments.Create(&comment); err != nil {
		mapStoreErr(w, err)
		return
	}
	s.hub.Broadcast(Event{Type: "task.comment.added", Payload: comment})
	writeJSON(w, http.StatusCreated, comment)
}
