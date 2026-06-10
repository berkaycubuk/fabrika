package store

import (
	"database/sql"
	"encoding/json"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/google/uuid"
)

// CommentRepo persists task comments in the per-project store.
type CommentRepo struct{ db *sql.DB }

// Create inserts a comment, assigning an ID if absent and defaulting the author
// type to "user". created_at defaults in SQL when left empty.
func (r *CommentRepo) Create(c *model.Comment) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.AuthorType == "" {
		c.AuthorType = "user"
	}
	attachments, err := json.Marshal(emptyIfNil(c.Attachments))
	if err != nil {
		return err
	}
	if c.CreatedAt != "" {
		_, err := r.db.Exec(
			`INSERT INTO comments (id, task_id, big_task_id, author_type, author_id, body, attachments, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			c.ID, c.TaskID, c.BigTaskID, c.AuthorType, c.AuthorID, c.Body, string(attachments), c.CreatedAt,
		)
		return err
	}
	_, err = r.db.Exec(
		`INSERT INTO comments (id, task_id, big_task_id, author_type, author_id, body, attachments) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.TaskID, c.BigTaskID, c.AuthorType, c.AuthorID, c.Body, string(attachments),
	)
	return err
}

// ListForTask returns every comment for a task, oldest first.
func (r *CommentRepo) ListForTask(taskID string) ([]model.Comment, error) {
	rows, err := r.db.Query(
		`SELECT id, task_id, big_task_id, author_type, author_id, body, attachments, created_at FROM comments WHERE task_id=? ORDER BY created_at ASC, id ASC`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Comment
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// ListForBigTask returns every comment for a big task, oldest first.
func (r *CommentRepo) ListForBigTask(bigTaskID string) ([]model.Comment, error) {
	rows, err := r.db.Query(
		`SELECT id, task_id, big_task_id, author_type, author_id, body, attachments, created_at FROM comments WHERE big_task_id=? ORDER BY created_at ASC, id ASC`,
		bigTaskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Comment
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func scanComment(s scanner) (*model.Comment, error) {
	var c model.Comment
	var attachments string
	if err := s.Scan(&c.ID, &c.TaskID, &c.BigTaskID, &c.AuthorType, &c.AuthorID, &c.Body, &attachments, &c.CreatedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(attachments), &c.Attachments); err != nil {
		return nil, err
	}
	return &c, nil
}

func emptyIfNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// DeleteByTask removes a task's comments when the task itself is deleted.
func (r *CommentRepo) DeleteByTask(taskID string) error {
	_, err := r.db.Exec(`DELETE FROM comments WHERE task_id=?`, taskID)
	return err
}

// DeleteByBigTask removes all comments for a big task when it is deleted.
func (r *CommentRepo) DeleteByBigTask(bigTaskID string) error {
	_, err := r.db.Exec(`DELETE FROM comments WHERE big_task_id=?`, bigTaskID)
	return err
}
