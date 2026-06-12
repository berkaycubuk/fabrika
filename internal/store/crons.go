package store

import (
	"database/sql"
	"errors"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/google/uuid"
)

// CronRepo persists scheduled prompts in the per-project store.
type CronRepo struct{ db *sql.DB }

// bootstrap creates the cron_schedules table and index if they do not exist.
// Called from Open so that deployments without a migration file still work.
func (r *CronRepo) bootstrap() error {
	_, err := r.db.Exec(`
CREATE TABLE IF NOT EXISTS cron_schedules (
    id          TEXT PRIMARY KEY,
    title       TEXT NOT NULL,
    prompt      TEXT NOT NULL DEFAULT '',
    agent_id    TEXT NOT NULL DEFAULT '',
    expr        TEXT NOT NULL,
    enabled     INTEGER NOT NULL DEFAULT 1,
    last_run_at TEXT NOT NULL DEFAULT '',
    next_run_at TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_cron_enabled_next ON cron_schedules(enabled, next_run_at);
`)
	return err
}

const cronCols = `id, title, prompt, agent_id, expr, enabled, last_run_at, next_run_at, created_at`

// Create inserts a cron schedule, assigning a UUID if c.ID is empty.
func (r *CronRepo) Create(c *model.CronSchedule) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	_, err := r.db.Exec(
		`INSERT INTO cron_schedules (id, title, prompt, agent_id, expr, enabled, last_run_at, next_run_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.Title, c.Prompt, c.AgentID, c.Expr, boolToInt(c.Enabled), c.LastRunAt, c.NextRunAt,
	)
	return err
}

// Get returns a single cron schedule by ID.
func (r *CronRepo) Get(id string) (*model.CronSchedule, error) {
	row := r.db.QueryRow(`SELECT `+cronCols+` FROM cron_schedules WHERE id=?`, id)
	c, err := scanCron(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

// List returns all cron schedules, newest-first.
func (r *CronRepo) List() ([]model.CronSchedule, error) {
	rows, err := r.db.Query(`SELECT ` + cronCols + ` FROM cron_schedules ORDER BY created_at DESC, rowid DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.CronSchedule
	for rows.Next() {
		c, err := scanCron(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// Update overwrites title, prompt, agent_id, expr, enabled, and next_run_at for an existing schedule.
func (r *CronRepo) Update(c *model.CronSchedule) error {
	res, err := r.db.Exec(
		`UPDATE cron_schedules SET title=?, prompt=?, agent_id=?, expr=?, enabled=?, next_run_at=? WHERE id=?`,
		c.Title, c.Prompt, c.AgentID, c.Expr, boolToInt(c.Enabled), c.NextRunAt, c.ID,
	)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

// Delete removes a cron schedule by ID.
func (r *CronRepo) Delete(id string) error {
	res, err := r.db.Exec(`DELETE FROM cron_schedules WHERE id=?`, id)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

// SetEnabled flips the enabled flag on a schedule.
func (r *CronRepo) SetEnabled(id string, enabled bool) error {
	res, err := r.db.Exec(`UPDATE cron_schedules SET enabled=? WHERE id=?`, boolToInt(enabled), id)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

// MarkRun records the timestamps of the most-recent fire and the next scheduled fire.
func (r *CronRepo) MarkRun(id, lastRunAt, nextRunAt string) error {
	res, err := r.db.Exec(
		`UPDATE cron_schedules SET last_run_at=?, next_run_at=? WHERE id=?`,
		lastRunAt, nextRunAt, id,
	)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

func scanCron(s scanner) (*model.CronSchedule, error) {
	var c model.CronSchedule
	var enabled int
	if err := s.Scan(&c.ID, &c.Title, &c.Prompt, &c.AgentID, &c.Expr, &enabled,
		&c.LastRunAt, &c.NextRunAt, &c.CreatedAt); err != nil {
		return nil, err
	}
	c.Enabled = enabled != 0
	return &c, nil
}
