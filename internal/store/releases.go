package store

import (
	"database/sql"
	"errors"
	"time"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/google/uuid"
)

// ReleaseRepo persists Releases in the per-project store.
type ReleaseRepo struct{ db *sql.DB }

const releaseCols = `id, sha, prev_sha, status, deploy_log, health_log, error, created_at, deployed_at, live_at`

// Create inserts a Release, assigning an ID and stamping created_at if absent.
// It mutates rel in place so the caller sees the generated ID/created_at.
func (r *ReleaseRepo) Create(rel *model.Release) error {
	if rel.ID == "" {
		rel.ID = uuid.NewString()
	}
	if rel.CreatedAt == "" {
		rel.CreatedAt = time.Now().UTC().Format("2006-01-02 15:04:05")
	}
	_, err := r.db.Exec(
		`INSERT INTO releases (`+releaseCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rel.ID, rel.SHA, rel.PrevSHA, rel.Status, rel.DeployLog, rel.HealthLog, rel.Error,
		rel.CreatedAt, rel.DeployedAt, rel.LiveAt,
	)
	return err
}

// Get returns a Release by ID, ErrNotFound when no row matched.
func (r *ReleaseRepo) Get(id string) (*model.Release, error) {
	row := r.db.QueryRow(`SELECT `+releaseCols+` FROM releases WHERE id=?`, id)
	return scanRelease(row)
}

// List returns all Releases newest-first.
func (r *ReleaseRepo) List() ([]model.Release, error) {
	rows, err := r.db.Query(`SELECT ` + releaseCols + ` FROM releases ORDER BY created_at DESC, rowid DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Release
	for rows.Next() {
		rel, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rel)
	}
	return out, rows.Err()
}

// Update overwrites a Release's mutable columns by ID, ErrNotFound when no row matched.
func (r *ReleaseRepo) Update(rel *model.Release) error {
	res, err := r.db.Exec(
		`UPDATE releases SET sha=?, prev_sha=?, status=?, deploy_log=?, health_log=?, error=?, deployed_at=?, live_at=? WHERE id=?`,
		rel.SHA, rel.PrevSHA, rel.Status, rel.DeployLog, rel.HealthLog, rel.Error,
		rel.DeployedAt, rel.LiveAt, rel.ID,
	)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

// LatestDeployed returns the most recent release that is live or baking — the
// SHA currently serving (or about to serve) traffic. ErrNotFound if none.
func (r *ReleaseRepo) LatestDeployed() (*model.Release, error) {
	row := r.db.QueryRow(
		`SELECT ` + releaseCols + ` FROM releases WHERE status IN ('live', 'baking') ORDER BY created_at DESC, rowid DESC LIMIT 1`,
	)
	return scanRelease(row)
}

// InMotion returns the most recent release that is still in flight (pending,
// deploying, or baking), driving single-flight deploys. ErrNotFound if none.
func (r *ReleaseRepo) InMotion() (*model.Release, error) {
	row := r.db.QueryRow(
		`SELECT ` + releaseCols + ` FROM releases WHERE status IN ('pending', 'deploying', 'baking') ORDER BY created_at DESC, rowid DESC LIMIT 1`,
	)
	return scanRelease(row)
}

func scanRelease(s scanner) (*model.Release, error) {
	var rel model.Release
	err := s.Scan(&rel.ID, &rel.SHA, &rel.PrevSHA, &rel.Status, &rel.DeployLog, &rel.HealthLog,
		&rel.Error, &rel.CreatedAt, &rel.DeployedAt, &rel.LiveAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &rel, nil
}
