package store

import (
	"database/sql"

	"github.com/berkaycubuk/fabrika/internal/model"
)

// taskActivityCap bounds how many activity rows a single task retains. The
// oldest rows (lowest id) are dropped first, so a chatty agent can't bloat the
// per-project store.
const taskActivityCap = 500

// TaskActivityRepo persists a bounded, per-task implementation activity timeline.
type TaskActivityRepo struct{ db *sql.DB }

// Append inserts one activity row, then trims the task's log back to the
// most-recent taskActivityCap rows (oldest by id dropped first).
func (r *TaskActivityRepo) Append(taskID string, a model.PlanActivity) error {
	if _, err := r.db.Exec(
		`INSERT INTO task_activity (task_id, type, summary, ts) VALUES (?, ?, ?, ?)`,
		taskID, a.Type, a.Summary, a.Ts,
	); err != nil {
		return err
	}
	_, err := r.db.Exec(
		`DELETE FROM task_activity WHERE task_id=? AND id NOT IN (
			SELECT id FROM task_activity WHERE task_id=? ORDER BY id DESC LIMIT ?
		)`,
		taskID, taskID, taskActivityCap,
	)
	return err
}

// List returns a task's activity rows oldest-first, suitable for rendering a
// timeline.
func (r *TaskActivityRepo) List(taskID string) ([]model.PlanActivity, error) {
	rows, err := r.db.Query(
		`SELECT type, summary, ts FROM task_activity WHERE task_id=? ORDER BY id ASC`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.PlanActivity
	for rows.Next() {
		var a model.PlanActivity
		if err := rows.Scan(&a.Type, &a.Summary, &a.Ts); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeleteByTask removes all activity for a task when it is deleted.
// Removing zero rows is not an error.
func (r *TaskActivityRepo) DeleteByTask(taskID string) error {
	_, err := r.db.Exec(`DELETE FROM task_activity WHERE task_id=?`, taskID)
	return err
}
