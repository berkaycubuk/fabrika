package store

import (
	"database/sql"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/google/uuid"
)

// ConventionRepo persists standing conventions in the global store. Used in
// Phase 2+ to inject context into future runs; CRUD is available now.
type ConventionRepo struct{ db *sql.DB }

// Create inserts a convention, assigning an ID if absent.
func (r *ConventionRepo) Create(c *model.Convention) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	_, err := r.db.Exec(`INSERT INTO conventions (id, rule) VALUES (?, ?)`, c.ID, c.Rule)
	return err
}

// List returns all conventions.
func (r *ConventionRepo) List() ([]model.Convention, error) {
	rows, err := r.db.Query(`SELECT id, rule FROM conventions ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Convention
	for rows.Next() {
		var c model.Convention
		if err := rows.Scan(&c.ID, &c.Rule); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Delete removes a convention by ID.
func (r *ConventionRepo) Delete(id string) error {
	res, err := r.db.Exec(`DELETE FROM conventions WHERE id=?`, id)
	if err != nil {
		return err
	}
	return mustAffect(res)
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
