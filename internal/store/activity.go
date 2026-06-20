package store

import (
	"database/sql"

	"github.com/berkaycubuk/fabrika/internal/model"
)

// planActivityCap bounds how many activity rows a single big task retains. The
// oldest rows (lowest id) are dropped first, so a chatty planner can't bloat the
// per-project store.
const planActivityCap = 500

// PlanActivityRepo persists a bounded, per-big-task planner activity timeline.
type PlanActivityRepo struct{ db *sql.DB }

// Append inserts one activity row, then trims the big task's log back to the
// most-recent planActivityCap rows (oldest by id dropped first).
func (r *PlanActivityRepo) Append(bigTaskID string, a model.PlanActivity) error {
	if _, err := r.db.Exec(
		`INSERT INTO plan_activity (big_task_id, type, summary, ts) VALUES (?, ?, ?, ?)`,
		bigTaskID, a.Type, a.Summary, a.Ts,
	); err != nil {
		return err
	}
	_, err := r.db.Exec(
		`DELETE FROM plan_activity WHERE big_task_id=? AND id NOT IN (
			SELECT id FROM plan_activity WHERE big_task_id=? ORDER BY id DESC LIMIT ?
		)`,
		bigTaskID, bigTaskID, planActivityCap,
	)
	return err
}

// List returns a big task's activity rows oldest-first, suitable for rendering a
// timeline.
func (r *PlanActivityRepo) List(bigTaskID string) ([]model.PlanActivity, error) {
	rows, err := r.db.Query(
		`SELECT type, summary, ts FROM plan_activity WHERE big_task_id=? ORDER BY id ASC`,
		bigTaskID,
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

// DeleteByBigTask removes all activity for a big task when it is deleted.
// Removing zero rows is not an error.
func (r *PlanActivityRepo) DeleteByBigTask(bigTaskID string) error {
	_, err := r.db.Exec(`DELETE FROM plan_activity WHERE big_task_id=?`, bigTaskID)
	return err
}
