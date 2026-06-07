package store

import (
	"database/sql"
	"errors"
	"time"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/google/uuid"
)

// IncidentRepo persists Incidents in the per-project store.
type IncidentRepo struct{ db *sql.DB }

const incidentCols = `id, fingerprint, title, stack, payload, count, first_seen, last_seen, status, task_id, suspect_release_id, suspect_task_id`

// Create inserts an Incident, assigning an ID and stamping first_seen/last_seen
// if absent. It mutates inc in place so the caller sees the generated values.
func (r *IncidentRepo) Create(inc *model.Incident) error {
	if inc.ID == "" {
		inc.ID = uuid.NewString()
	}
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	if inc.FirstSeen == "" {
		inc.FirstSeen = now
	}
	if inc.LastSeen == "" {
		inc.LastSeen = now
	}
	_, err := r.db.Exec(
		`INSERT INTO incidents (`+incidentCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		inc.ID, inc.Fingerprint, inc.Title, inc.Stack, inc.Payload, inc.Count,
		inc.FirstSeen, inc.LastSeen, inc.Status, inc.TaskID, inc.SuspectReleaseID, inc.SuspectTaskID,
	)
	return err
}

// Get returns an Incident by ID, ErrNotFound when no row matched.
func (r *IncidentRepo) Get(id string) (*model.Incident, error) {
	row := r.db.QueryRow(`SELECT `+incidentCols+` FROM incidents WHERE id=?`, id)
	return scanIncident(row)
}

// GetByFingerprint returns an Incident by fingerprint, ErrNotFound when none.
func (r *IncidentRepo) GetByFingerprint(fp string) (*model.Incident, error) {
	row := r.db.QueryRow(`SELECT `+incidentCols+` FROM incidents WHERE fingerprint=?`, fp)
	return scanIncident(row)
}

// List returns all Incidents newest-first.
func (r *IncidentRepo) List() ([]model.Incident, error) {
	rows, err := r.db.Query(`SELECT ` + incidentCols + ` FROM incidents ORDER BY last_seen DESC, rowid DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Incident
	for rows.Next() {
		inc, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *inc)
	}
	return out, rows.Err()
}

// ListByStatus returns Incidents with the given status, newest-first.
func (r *IncidentRepo) ListByStatus(status string) ([]model.Incident, error) {
	rows, err := r.db.Query(`SELECT `+incidentCols+` FROM incidents WHERE status=? ORDER BY last_seen DESC, rowid DESC`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Incident
	for rows.Next() {
		inc, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *inc)
	}
	return out, rows.Err()
}

// Update overwrites an Incident's mutable columns by ID, ErrNotFound when no row matched.
func (r *IncidentRepo) Update(inc *model.Incident) error {
	res, err := r.db.Exec(
		`UPDATE incidents SET title=?, stack=?, payload=?, count=?, last_seen=?, status=?, task_id=?, suspect_release_id=?, suspect_task_id=? WHERE id=?`,
		inc.Title, inc.Stack, inc.Payload, inc.Count, inc.LastSeen, inc.Status,
		inc.TaskID, inc.SuspectReleaseID, inc.SuspectTaskID, inc.ID,
	)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

func scanIncident(s scanner) (*model.Incident, error) {
	var inc model.Incident
	err := s.Scan(&inc.ID, &inc.Fingerprint, &inc.Title, &inc.Stack, &inc.Payload, &inc.Count,
		&inc.FirstSeen, &inc.LastSeen, &inc.Status, &inc.TaskID, &inc.SuspectReleaseID, &inc.SuspectTaskID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &inc, nil
}
