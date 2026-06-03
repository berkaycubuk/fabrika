package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/google/uuid"
)

// BigTaskRepo persists BigTasks in the per-project store.
type BigTaskRepo struct{ db *sql.DB }

const bigTaskCols = `id, title, intent, constraints, repo_path, status`

// Create inserts a BigTask, assigning an ID and default status if absent.
func (r *BigTaskRepo) Create(b *model.BigTask) error {
	if b.ID == "" {
		b.ID = uuid.NewString()
	}
	if b.Status == "" {
		b.Status = model.BigTaskDraft
	}
	_, err := r.db.Exec(
		`INSERT INTO bigtasks (`+bigTaskCols+`) VALUES (?, ?, ?, ?, ?, ?)`,
		b.ID, b.Title, b.Intent, jsonStrings(b.Constraints), b.RepoPath, b.Status,
	)
	return err
}

// Get returns a BigTask by ID.
func (r *BigTaskRepo) Get(id string) (*model.BigTask, error) {
	row := r.db.QueryRow(`SELECT `+bigTaskCols+` FROM bigtasks WHERE id=?`, id)
	var b model.BigTask
	var constraints string
	err := row.Scan(&b.ID, &b.Title, &b.Intent, &constraints, &b.RepoPath, &b.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	b.Constraints = scanStrings(constraints)
	return &b, nil
}

// List returns all BigTasks newest-first.
func (r *BigTaskRepo) List() ([]model.BigTask, error) {
	rows, err := r.db.Query(`SELECT ` + bigTaskCols + ` FROM bigtasks ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.BigTask
	for rows.Next() {
		var b model.BigTask
		var constraints string
		if err := rows.Scan(&b.ID, &b.Title, &b.Intent, &constraints, &b.RepoPath, &b.Status); err != nil {
			return nil, err
		}
		b.Constraints = scanStrings(constraints)
		out = append(out, b)
	}
	return out, rows.Err()
}

// TaskRepo persists Tasks in the per-project store.
type TaskRepo struct{ db *sql.DB }

const taskCols = `id, big_task_id, title, spec, acceptance, depends_on, touch_paths, tags, risk_tier, status, branch, agent_id, preferred_agent_id`

// Create inserts a Task, assigning an ID and defaults if absent.
func (r *TaskRepo) Create(t *model.Task) error {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	if t.Status == "" {
		t.Status = model.TaskReady
	}
	if t.RiskTier == "" {
		t.RiskTier = model.RiskLow
	}
	acc, err := json.Marshal(t.Acceptance)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(
		`INSERT INTO tasks (`+taskCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.BigTaskID, t.Title, t.Spec, string(acc),
		jsonStrings(t.DependsOn), jsonStrings(t.TouchPaths), jsonStrings(t.Tags),
		t.RiskTier, t.Status, t.Branch, t.AgentID, t.PreferredAgentID,
	)
	return err
}

// Get returns a Task by ID.
func (r *TaskRepo) Get(id string) (*model.Task, error) {
	row := r.db.QueryRow(`SELECT `+taskCols+` FROM tasks WHERE id=?`, id)
	return scanTask(row)
}

// List returns all Tasks newest-first.
func (r *TaskRepo) List() ([]model.Task, error) {
	rows, err := r.db.Query(`SELECT ` + taskCols + ` FROM tasks ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// UpdateStatus sets a task's status (used by the engine in later phases).
func (r *TaskRepo) UpdateStatus(id, status string) error {
	res, err := r.db.Exec(`UPDATE tasks SET status=? WHERE id=?`, status, id)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

// SetPreferredAgent pins a task to a specific agent (steer/route).
func (r *TaskRepo) SetPreferredAgent(id, agentID string) error {
	res, err := r.db.Exec(`UPDATE tasks SET preferred_agent_id=? WHERE id=?`, agentID, id)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

func scanTask(s scanner) (*model.Task, error) {
	var t model.Task
	var acc, dependsOn, touchPaths, tags string
	err := s.Scan(&t.ID, &t.BigTaskID, &t.Title, &t.Spec, &acc, &dependsOn, &touchPaths, &tags,
		&t.RiskTier, &t.Status, &t.Branch, &t.AgentID, &t.PreferredAgentID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if acc != "" {
		_ = json.Unmarshal([]byte(acc), &t.Acceptance)
	}
	t.DependsOn = scanStrings(dependsOn)
	t.TouchPaths = scanStrings(touchPaths)
	t.Tags = scanStrings(tags)
	return &t, nil
}
