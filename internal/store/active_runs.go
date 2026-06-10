package store

import (
	"database/sql"
	"time"

	"github.com/berkaycubuk/fabrika/internal/model"
)

// ActiveRunRepo persists the pgid of each in-flight agent run in the per-project
// store, keyed by task. Rows are written at run start and removed when the run
// completes, leaving only true orphans to reap on the next boot.
type ActiveRunRepo struct{ db *sql.DB }

// Record upserts the active run for taskID, replacing any existing row so a
// re-recorded task never duplicates. started_at is stamped by the table default
// only on first insert.
func (r *ActiveRunRepo) Record(taskID string, pgid int, agentID string) error {
	_, err := r.db.Exec(
		`INSERT INTO active_runs (task_id, pgid, agent_id) VALUES (?, ?, ?)
		 ON CONFLICT(task_id) DO UPDATE SET pgid=excluded.pgid, agent_id=excluded.agent_id`,
		taskID, pgid, agentID,
	)
	return err
}

// List returns every active run.
func (r *ActiveRunRepo) List() ([]model.ActiveRun, error) {
	rows, err := r.db.Query(`SELECT task_id, pgid, agent_id, started_at FROM active_runs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ActiveRun
	for rows.Next() {
		var ar model.ActiveRun
		var startedAt string
		if err := rows.Scan(&ar.TaskID, &ar.PGID, &ar.AgentID, &startedAt); err != nil {
			return nil, err
		}
		// datetime('now') yields UTC in "2006-01-02 15:04:05"; tolerate empty.
		ar.StartedAt, _ = time.Parse("2006-01-02 15:04:05", startedAt)
		out = append(out, ar)
	}
	return out, rows.Err()
}

// Delete removes the active run for taskID. Deleting a missing row is not an error.
func (r *ActiveRunRepo) Delete(taskID string) error {
	_, err := r.db.Exec(`DELETE FROM active_runs WHERE task_id=?`, taskID)
	return err
}

// Clear removes all active runs; safe on an empty table.
func (r *ActiveRunRepo) Clear() error {
	_, err := r.db.Exec(`DELETE FROM active_runs`)
	return err
}
