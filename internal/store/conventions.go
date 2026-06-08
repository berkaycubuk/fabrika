package store

import (
	"database/sql"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/google/uuid"
)

// ConventionRepo persists standing conventions in the global store. Used in
// Phase 2+ to inject context into future runs; CRUD is available now.
type ConventionRepo struct{ db *sql.DB }

// Create inserts a convention, assigning an ID if absent. Status defaults to
// ConventionApproved so human/promoted conventions flow into prompt injection.
func (r *ConventionRepo) Create(c *model.Convention) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.Status == "" {
		c.Status = model.ConventionApproved
	}
	_, err := r.db.Exec(
		`INSERT INTO conventions (id, rule, status) VALUES (?, ?, ?)`,
		c.ID, c.Rule, c.Status,
	)
	return err
}

// List returns only approved conventions, ORDER BY rowid.
func (r *ConventionRepo) List() ([]model.Convention, error) {
	return r.queryConventions(
		`SELECT id, rule, status FROM conventions WHERE status=? ORDER BY rowid`,
		model.ConventionApproved,
	)
}

// ListByStatus returns all conventions with the given status, ORDER BY rowid.
func (r *ConventionRepo) ListByStatus(status string) ([]model.Convention, error) {
	return r.queryConventions(
		`SELECT id, rule, status FROM conventions WHERE status=? ORDER BY rowid`,
		status,
	)
}

// ListAll returns every convention regardless of status, ORDER BY rowid.
func (r *ConventionRepo) ListAll() ([]model.Convention, error) {
	return r.queryConventions(`SELECT id, rule, status FROM conventions ORDER BY rowid`)
}

// SetStatus updates the status of a convention by ID; returns ErrNotFound when
// no row matches.
func (r *ConventionRepo) SetStatus(id, status string) error {
	res, err := r.db.Exec(`UPDATE conventions SET status=? WHERE id=?`, status, id)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

// Delete removes a convention by ID.
func (r *ConventionRepo) Delete(id string) error {
	res, err := r.db.Exec(`DELETE FROM conventions WHERE id=?`, id)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

func (r *ConventionRepo) queryConventions(q string, args ...any) ([]model.Convention, error) {
	rows, err := r.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Convention
	for rows.Next() {
		var c model.Convention
		if err := rows.Scan(&c.ID, &c.Rule, &c.Status); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SettingsRepo is a simple key/value store (role assignments, per-tier routing,
// WIP cap) kept in the global store.
type SettingsRepo struct{ db *sql.DB }

// Get returns the value for a key, or ("", nil) if unset.
func (r *SettingsRepo) Get(key string) (string, error) {
	var v string
	err := r.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// Set upserts a key/value pair.
func (r *SettingsRepo) Set(key, value string) error {
	_, err := r.db.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		key, value,
	)
	return err
}

// All returns the full settings map.
func (r *SettingsRepo) All() (map[string]string, error) {
	rows, err := r.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}
