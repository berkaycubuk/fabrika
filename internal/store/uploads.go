package store

import (
	"database/sql"
	"errors"

	"github.com/berkaycubuk/fabrika/internal/model"
)

// UploadRepo persists per-upload metadata in the per-project store. Blobs live
// on disk under .fabrika/uploads; these rows let them survive restart.
type UploadRepo struct{ db *sql.DB }

// Create inserts an upload row keyed by Name. created_at defaults in SQL when
// left empty.
func (r *UploadRepo) Create(u *model.Upload) error {
	if u.CreatedAt != "" {
		_, err := r.db.Exec(
			`INSERT INTO uploads (name, filename, content_type, size, created_at) VALUES (?, ?, ?, ?, ?)`,
			u.Name, u.Filename, u.ContentType, u.Size, u.CreatedAt,
		)
		return err
	}
	_, err := r.db.Exec(
		`INSERT INTO uploads (name, filename, content_type, size) VALUES (?, ?, ?, ?)`,
		u.Name, u.Filename, u.ContentType, u.Size,
	)
	return err
}

// Get returns the upload by its served name, or ErrNotFound when absent.
func (r *UploadRepo) Get(name string) (*model.Upload, error) {
	var u model.Upload
	err := r.db.QueryRow(
		`SELECT name, filename, content_type, size, created_at FROM uploads WHERE name=?`,
		name,
	).Scan(&u.Name, &u.Filename, &u.ContentType, &u.Size, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}
