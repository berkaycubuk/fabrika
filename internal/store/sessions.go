package store

import (
	"database/sql"
	"errors"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/google/uuid"
)

// SessionRepo persists interactive chat sessions and their messages in the
// per-project store. The transient Busy flag is engine state, never stored.
type SessionRepo struct{ db *sql.DB }

const sessionCols = `id, title, agent_id, model, base_branch, branch, status, evidence, created_at, updated_at`

// Create inserts a session, assigning an ID and default status if absent.
func (r *SessionRepo) Create(s *model.Session) error {
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	if s.Status == "" {
		s.Status = model.SessionActive
	}
	_, err := r.db.Exec(
		`INSERT INTO sessions (id, title, agent_id, model, base_branch, branch, status, evidence) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Title, s.AgentID, s.Model, s.BaseBranch, s.Branch, s.Status, s.Evidence,
	)
	return err
}

// Get returns a session by ID.
func (r *SessionRepo) Get(id string) (*model.Session, error) {
	row := r.db.QueryRow(`SELECT `+sessionCols+` FROM sessions WHERE id=?`, id)
	s, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return s, err
}

// List returns all non-closed sessions, newest-first.
func (r *SessionRepo) List() ([]model.Session, error) {
	rows, err := r.db.Query(`SELECT ` + sessionCols + ` FROM sessions WHERE status != 'closed' ORDER BY created_at DESC, rowid DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// ListByStatus returns sessions with the given status, newest-first.
func (r *SessionRepo) ListByStatus(status string) ([]model.Session, error) {
	rows, err := r.db.Query(`SELECT `+sessionCols+` FROM sessions WHERE status=? ORDER BY created_at DESC, rowid DESC`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// SetStatus updates a session's status and touches updated_at.
func (r *SessionRepo) SetStatus(id, status string) error {
	_, err := r.db.Exec(`UPDATE sessions SET status=?, updated_at=datetime('now') WHERE id=?`, status, id)
	return err
}

// SetTitle updates a session's title (set from the first user message).
func (r *SessionRepo) SetTitle(id, title string) error {
	_, err := r.db.Exec(`UPDATE sessions SET title=?, updated_at=datetime('now') WHERE id=?`, title, id)
	return err
}

// SetEvidence stores the gate evidence JSON from the last Finish attempt.
func (r *SessionRepo) SetEvidence(id, evidence string) error {
	_, err := r.db.Exec(`UPDATE sessions SET evidence=?, updated_at=datetime('now') WHERE id=?`, evidence, id)
	return err
}

// AddMessage appends a message to a session's transcript, assigning an ID if
// absent, and touches the session's updated_at.
func (r *SessionRepo) AddMessage(m *model.SessionMessage) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	if m.Role == "" {
		m.Role = model.SessionRoleUser
	}
	if _, err := r.db.Exec(
		`INSERT INTO session_messages (id, session_id, role, body, attachments) VALUES (?, ?, ?, ?, ?)`,
		m.ID, m.SessionID, m.Role, m.Body, jsonStrings(m.Attachments),
	); err != nil {
		return err
	}
	row := r.db.QueryRow(`SELECT created_at FROM session_messages WHERE id=?`, m.ID)
	_ = row.Scan(&m.CreatedAt)
	_, err := r.db.Exec(`UPDATE sessions SET updated_at=datetime('now') WHERE id=?`, m.SessionID)
	return err
}

// Messages returns a session's transcript, oldest first.
func (r *SessionRepo) Messages(sessionID string) ([]model.SessionMessage, error) {
	rows, err := r.db.Query(
		`SELECT id, session_id, role, body, attachments, created_at FROM session_messages WHERE session_id=? ORDER BY created_at ASC, rowid ASC`,
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.SessionMessage
	for rows.Next() {
		var m model.SessionMessage
		var attachments string
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Body, &attachments, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Attachments = scanStrings(attachments)
		out = append(out, m)
	}
	return out, rows.Err()
}

func scanSession(s scanner) (*model.Session, error) {
	var sess model.Session
	if err := s.Scan(&sess.ID, &sess.Title, &sess.AgentID, &sess.Model, &sess.BaseBranch,
		&sess.Branch, &sess.Status, &sess.Evidence, &sess.CreatedAt, &sess.UpdatedAt); err != nil {
		return nil, err
	}
	return &sess, nil
}
