package store

import (
	"database/sql"

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
	if c.CreatedAt != "" {
		_, err := r.db.Exec(
			`INSERT INTO comments (id, task_id, author_type, author_id, body, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			c.ID, c.TaskID, c.AuthorType, c.AuthorID, c.Body, c.CreatedAt,
		)
		return err
	}
	_, err := r.db.Exec(
		`INSERT INTO comments (id, task_id, author_type, author_id, body) VALUES (?, ?, ?, ?, ?)`,
		c.ID, c.TaskID, c.AuthorType, c.AuthorID, c.Body,
	)
	return err
}

// ListForTask returns every comment for a task, oldest first.
func (r *CommentRepo) ListForTask(taskID string) ([]model.Comment, error) {
	rows, err := r.db.Query(
		`SELECT id, task_id, author_type, author_id, body, created_at FROM comments WHERE task_id=? ORDER BY created_at ASC, id ASC`,
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

func scanComment(s scanner) (*model.Comment, error) {
	var c model.Comment
	if err := s.Scan(&c.ID, &c.TaskID, &c.AuthorType, &c.AuthorID, &c.Body, &c.CreatedAt); err != nil {
		return nil, err
	}
	return &c, nil
}
