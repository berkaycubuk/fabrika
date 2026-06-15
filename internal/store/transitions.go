package store

import (
	"database/sql"
	"time"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/google/uuid"
)

// TransitionRepo persists task lifecycle transitions in the per-project store.
type TransitionRepo struct{ db *sql.DB }

// Create inserts a transition, assigning an ID if absent and defaulting the
// actor to "engine". A caller-supplied CreatedAt is honored; otherwise a
// timestamp is generated so both the row and the in-memory struct are populated.
func (r *TransitionRepo) Create(t *model.TaskTransition) error {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	if t.Actor == "" {
		t.Actor = "engine"
	}
	if t.CreatedAt == "" {
		t.CreatedAt = time.Now().UTC().Format("2006-01-02 15:04:05")
	}
	_, err := r.db.Exec(
		`INSERT INTO task_transitions (id, task_id, from_status, to_status, actor, reason, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.TaskID, t.FromStatus, t.ToStatus, t.Actor, t.Reason, t.CreatedAt,
	)
	return err
}

// ListForTask returns every transition for a task, oldest first.
func (r *TransitionRepo) ListForTask(taskID string) ([]model.TaskTransition, error) {
	rows, err := r.db.Query(
		`SELECT id, task_id, from_status, to_status, actor, reason, created_at FROM task_transitions WHERE task_id=? ORDER BY created_at ASC, id ASC`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.TaskTransition
	for rows.Next() {
		var t model.TaskTransition
		if err := rows.Scan(&t.ID, &t.TaskID, &t.FromStatus, &t.ToStatus, &t.Actor, &t.Reason, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteByTask removes a task's transitions when the task itself is deleted.
func (r *TransitionRepo) DeleteByTask(taskID string) error {
	_, err := r.db.Exec(`DELETE FROM task_transitions WHERE task_id=?`, taskID)
	return err
}
