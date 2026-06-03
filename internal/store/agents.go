package store

import (
	"database/sql"
	"errors"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/google/uuid"
)

// ErrNotFound is returned when a lookup by ID matches no row.
var ErrNotFound = errors.New("not found")

// AgentRepo persists agent definitions in the global store.
type AgentRepo struct{ db *sql.DB }

const agentCols = `id, name, photo, command, model, roles, tags, concurrency, timeout, max_attempts, enabled`

// Create inserts a new agent, assigning an ID if absent.
func (r *AgentRepo) Create(a *model.Agent) error {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if a.Concurrency < 1 {
		a.Concurrency = 1
	}
	if a.MaxAttempts < 1 {
		a.MaxAttempts = 1
	}
	_, err := r.db.Exec(
		`INSERT INTO agents (`+agentCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Name, a.Photo, a.Command, a.Model, jsonStrings(a.Roles), jsonStrings(a.Tags),
		a.Concurrency, a.Timeout, a.MaxAttempts, boolToInt(a.Enabled),
	)
	return err
}

// Update overwrites an existing agent by ID.
func (r *AgentRepo) Update(a *model.Agent) error {
	if a.Concurrency < 1 {
		a.Concurrency = 1
	}
	if a.MaxAttempts < 1 {
		a.MaxAttempts = 1
	}
	res, err := r.db.Exec(
		`UPDATE agents SET name=?, photo=?, command=?, model=?, roles=?, tags=?, concurrency=?, timeout=?, max_attempts=?, enabled=?, updated_at=datetime('now') WHERE id=?`,
		a.Name, a.Photo, a.Command, a.Model, jsonStrings(a.Roles), jsonStrings(a.Tags),
		a.Concurrency, a.Timeout, a.MaxAttempts, boolToInt(a.Enabled), a.ID,
	)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

// SetEnabled flips an agent's enabled flag.
func (r *AgentRepo) SetEnabled(id string, enabled bool) error {
	res, err := r.db.Exec(
		`UPDATE agents SET enabled=?, updated_at=datetime('now') WHERE id=?`,
		boolToInt(enabled), id,
	)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

// Delete removes an agent by ID.
func (r *AgentRepo) Delete(id string) error {
	res, err := r.db.Exec(`DELETE FROM agents WHERE id=?`, id)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

// Get returns a single agent by ID.
func (r *AgentRepo) Get(id string) (*model.Agent, error) {
	row := r.db.QueryRow(`SELECT `+agentCols+` FROM agents WHERE id=?`, id)
	return scanAgent(row)
}

// List returns all agents ordered by name.
func (r *AgentRepo) List() ([]model.Agent, error) {
	rows, err := r.db.Query(`SELECT ` + agentCols + ` FROM agents ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Agent
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func scanAgent(s scanner) (*model.Agent, error) {
	var a model.Agent
	var roles, tags string
	var enabled int
	err := s.Scan(&a.ID, &a.Name, &a.Photo, &a.Command, &a.Model, &roles, &tags, &a.Concurrency, &a.Timeout, &a.MaxAttempts, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.Roles = scanStrings(roles)
	a.Tags = scanStrings(tags)
	a.Enabled = enabled != 0
	return &a, nil
}

// scanner abstracts *sql.Row and *sql.Rows for shared scan helpers.
type scanner interface {
	Scan(dest ...any) error
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func mustAffect(res interface{ RowsAffected() (int64, error) }) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
