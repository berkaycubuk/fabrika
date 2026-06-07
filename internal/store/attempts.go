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
		`INSERT INTO attempts (id, task_id, agent_id, result, evidence, log, input_tokens, output_tokens, total_tokens) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.TaskID, a.AgentID, a.Result, string(ev), a.Log,
		a.Usage.InputTokens, a.Usage.OutputTokens, a.Usage.TotalTokens,
	)
	return err
}

// LatestForTask returns the most recent attempt for a task, or ErrNotFound.
func (r *AttemptRepo) LatestForTask(taskID string) (*model.Attempt, error) {
	row := r.db.QueryRow(
		`SELECT id, task_id, agent_id, result, evidence, log, input_tokens, output_tokens, total_tokens FROM attempts WHERE task_id=? ORDER BY created_at DESC, rowid DESC LIMIT 1`,
		taskID,
	)
	return scanAttempt(row)
}

// ListForTask returns every attempt for a task, newest first.
func (r *AttemptRepo) ListForTask(taskID string) ([]model.Attempt, error) {
	rows, err := r.db.Query(
		`SELECT id, task_id, agent_id, result, evidence, log, input_tokens, output_tokens, total_tokens FROM attempts WHERE task_id=? ORDER BY created_at DESC, rowid DESC`,
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
	err := s.Scan(&a.ID, &a.TaskID, &a.AgentID, &a.Result, &ev, &a.Log,
		&a.Usage.InputTokens, &a.Usage.OutputTokens, &a.Usage.TotalTokens)
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

// RecentByAgent returns the agent's most recent attempts, newest first, capped
// at limit. Returns nil/empty slice (no error) when the agent has no attempts.
func (r *AttemptRepo) RecentByAgent(agentID string, limit int) ([]model.Attempt, error) {
	rows, err := r.db.Query(
		`SELECT id, task_id, agent_id, result, evidence, log, input_tokens, output_tokens, total_tokens FROM attempts WHERE agent_id=? ORDER BY created_at DESC, rowid DESC LIMIT ?`,
		agentID, limit,
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

// TokensByAgent returns per-agent token totals summed across all attempts in
// the project store, keyed by agent ID. Rows with an empty agent_id are skipped.
func (r *AttemptRepo) TokensByAgent() (map[string]model.Usage, error) {
	rows, err := r.db.Query(
		`SELECT agent_id, SUM(input_tokens), SUM(output_tokens), SUM(total_tokens) FROM attempts GROUP BY agent_id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]model.Usage)
	for rows.Next() {
		var agentID string
		var u model.Usage
		if err := rows.Scan(&agentID, &u.InputTokens, &u.OutputTokens, &u.TotalTokens); err != nil {
			return nil, err
		}
		if agentID == "" {
			continue
		}
		out[agentID] = u
	}
	return out, rows.Err()
}

// DeleteByTask removes a task's attempt history when the task itself is deleted.
func (r *AttemptRepo) DeleteByTask(taskID string) error {
	_, err := r.db.Exec(`DELETE FROM attempts WHERE task_id=?`, taskID)
	return err
}
