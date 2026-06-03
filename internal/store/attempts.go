package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/google/uuid"
)

// AttemptRepo persists agent run attempts (with evidence) in the per-project store.
type AttemptRepo struct{ db *sql.DB }

// Create inserts an attempt, assigning an ID if absent.
func (r *AttemptRepo) Create(a *model.Attempt) error {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	ev, err := json.Marshal(a.Evidence)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(
		`INSERT INTO attempts (id, task_id, agent_id, result, evidence, log) VALUES (?, ?, ?, ?, ?, ?)`,
		a.ID, a.TaskID, a.AgentID, a.Result, string(ev), a.Log,
	)
	return err
}

// LatestForTask returns the most recent attempt for a task, or ErrNotFound.
func (r *AttemptRepo) LatestForTask(taskID string) (*model.Attempt, error) {
	row := r.db.QueryRow(
		`SELECT id, task_id, agent_id, result, evidence, log FROM attempts WHERE task_id=? ORDER BY created_at DESC, rowid DESC LIMIT 1`,
		taskID,
	)
	return scanAttempt(row)
}

// ListForTask returns every attempt for a task, newest first.
func (r *AttemptRepo) ListForTask(taskID string) ([]model.Attempt, error) {
	rows, err := r.db.Query(
		`SELECT id, task_id, agent_id, result, evidence, log FROM attempts WHERE task_id=? ORDER BY created_at DESC, rowid DESC`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Attempt
	for rows.Next() {
		a, err := scanAttempt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func scanAttempt(s scanner) (*model.Attempt, error) {
	var a model.Attempt
	var ev string
	err := s.Scan(&a.ID, &a.TaskID, &a.AgentID, &a.Result, &ev, &a.Log)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if ev != "" {
		_ = json.Unmarshal([]byte(ev), &a.Evidence)
	}
	return &a, nil
}
