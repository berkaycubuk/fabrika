package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/google/uuid"
)

// BigTaskRepo persists BigTasks in the per-project store.
type BigTaskRepo struct{ db *sql.DB }

const bigTaskCols = `id, title, intent, constraints, attachments, repo_path, status, error, planner_agent_id`

// Create inserts a BigTask, assigning an ID and default status if absent.
func (r *BigTaskRepo) Create(b *model.BigTask) error {
	if b.ID == "" {
		b.ID = uuid.NewString()
	}
	if b.Status == "" {
		b.Status = model.BigTaskDraft
	}
	_, err := r.db.Exec(
		`INSERT INTO bigtasks (`+bigTaskCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.ID, b.Title, b.Intent, jsonStrings(b.Constraints), jsonStrings(b.Attachments), b.RepoPath, b.Status, b.Error, b.PlannerAgentID,
	)
	return err
}

// Get returns a BigTask by ID.
func (r *BigTaskRepo) Get(id string) (*model.BigTask, error) {
	row := r.db.QueryRow(`SELECT `+bigTaskCols+` FROM bigtasks WHERE id=?`, id)
	b, err := scanBigTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return b, err
}

// List returns all BigTasks newest-first. The rowid tiebreaker keeps the order
// stable when rows share a (second-resolution) created_at, so callers that
// iterate in reverse get a deterministic oldest-first (FIFO) sequence.
func (r *BigTaskRepo) List() ([]model.BigTask, error) {
	rows, err := r.db.Query(`SELECT ` + bigTaskCols + ` FROM bigtasks ORDER BY created_at DESC, rowid DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.BigTask
	for rows.Next() {
		b, err := scanBigTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

func scanBigTask(s scanner) (*model.BigTask, error) {
	var b model.BigTask
	var constraints, attachments string
	err := s.Scan(&b.ID, &b.Title, &b.Intent, &constraints, &attachments, &b.RepoPath, &b.Status, &b.Error, &b.PlannerAgentID)
	if err != nil {
		return nil, err
	}
	b.Constraints = scanStrings(constraints)
	b.Attachments = scanStrings(attachments)
	return &b, nil
}

// SetPlannerAgent records which planner agent is working on a big task.
func (r *BigTaskRepo) SetPlannerAgent(id, agentID string) error {
	res, err := r.db.Exec(`UPDATE bigtasks SET planner_agent_id=? WHERE id=?`, agentID, id)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

// SetUsage records the token usage the planner agent self-reported for a big
// task, so planning tokens fold into the same per-agent totals as task attempts.
func (r *BigTaskRepo) SetUsage(id string, u model.Usage) error {
	res, err := r.db.Exec(
		`UPDATE bigtasks SET input_tokens=?, output_tokens=?, total_tokens=? WHERE id=?`,
		u.InputTokens, u.OutputTokens, u.TotalTokens, id,
	)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

// PlanningTokensByAgent returns per-planner token totals summed across all big
// tasks, keyed by planner agent ID. Rows with an empty planner_agent_id are skipped.
func (r *BigTaskRepo) PlanningTokensByAgent() (map[string]model.Usage, error) {
	rows, err := r.db.Query(
		`SELECT planner_agent_id, SUM(input_tokens), SUM(output_tokens), SUM(total_tokens) FROM bigtasks GROUP BY planner_agent_id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]model.Usage)
	for rows.Next() {
		var agentID string
		var u model.Usage
		if err := rows.Scan(&agentID, &u.InputTokens, &u.OutputTokens, &u.TotalTokens); err != nil {
			return nil, err
		}
		if agentID == "" {
			continue
		}
		out[agentID] = u
	}
	return out, rows.Err()
}

// UpdateStatus sets a big task's lifecycle status, clearing any prior error
// reason (a successful transition supersedes a past failure).
func (r *BigTaskRepo) UpdateStatus(id, status string) error {
	res, err := r.db.Exec(`UPDATE bigtasks SET status=?, error='' WHERE id=?`, status, id)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

// SetError marks a big task as failed (status 'error') with a human-readable
// reason, so the failure is visible in the UI instead of silently lost.
func (r *BigTaskRepo) SetError(id, reason string) error {
	res, err := r.db.Exec(`UPDATE bigtasks SET status=?, error=? WHERE id=?`, model.BigTaskError, reason, id)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

// Delete removes a big task by ID, returning ErrNotFound when no row matched.
func (r *BigTaskRepo) Delete(id string) error {
	res, err := r.db.Exec(`DELETE FROM bigtasks WHERE id=?`, id)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

// TaskRepo persists Tasks in the per-project store.
type TaskRepo struct{ db *sql.DB }

const taskCols = `id, big_task_id, title, spec, acceptance, depends_on, touch_paths, tags, attachments, risk_tier, priority, status, branch, agent_id, preferred_agent_id, auto_merged, audit_flagged, reverted, reporter`

// Create inserts a Task, assigning an ID and defaults if absent.
func (r *TaskRepo) Create(t *model.Task) error {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	if t.Status == "" {
		t.Status = model.TaskReady
	}
	if t.RiskTier == "" {
		t.RiskTier = model.RiskLow
	}
	if t.Priority == "" {
		t.Priority = model.PriorityMedium
	}
	if t.Reporter == "" {
		t.Reporter = model.ReporterUser
	}
	acc, err := json.Marshal(t.Acceptance)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(
		`INSERT INTO tasks (`+taskCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.BigTaskID, t.Title, t.Spec, string(acc),
		jsonStrings(t.DependsOn), jsonStrings(t.TouchPaths), jsonStrings(t.Tags), jsonStrings(t.Attachments),
		t.RiskTier, t.Priority, t.Status, t.Branch, t.AgentID, t.PreferredAgentID,
		boolToInt(t.AutoMerged), boolToInt(t.AuditFlagged), boolToInt(t.Reverted), t.Reporter,
	)
	return err
}

// Get returns a Task by ID.
func (r *TaskRepo) Get(id string) (*model.Task, error) {
	row := r.db.QueryRow(`SELECT `+taskCols+` FROM tasks WHERE id=?`, id)
	return scanTask(row)
}

// ListByBigTask returns the tasks belonging to a big task, newest-first.
func (r *TaskRepo) ListByBigTask(bigTaskID string) ([]model.Task, error) {
	rows, err := r.db.Query(`SELECT `+taskCols+` FROM tasks WHERE big_task_id=? ORDER BY created_at DESC`, bigTaskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// List returns all Tasks newest-first.
func (r *TaskRepo) List() ([]model.Task, error) {
	rows, err := r.db.Query(`SELECT ` + taskCols + ` FROM tasks ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// UpdateStatus sets a task's status (used by the engine in later phases).
func (r *TaskRepo) UpdateStatus(id, status string) error {
	res, err := r.db.Exec(`UPDATE tasks SET status=? WHERE id=?`, status, id)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

// SetSpec overwrites a task's spec (used to inject resolved decisions on resume).
func (r *TaskRepo) SetSpec(id, spec string) error {
	res, err := r.db.Exec(`UPDATE tasks SET spec=? WHERE id=?`, spec, id)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

// MarkReadyByBigTask flips every planned task of a big task to ready, returning
// how many were promoted. Called on plan approval so the scheduler can pick them.
func (r *TaskRepo) MarkReadyByBigTask(bigTaskID string) (int, error) {
	res, err := r.db.Exec(
		`UPDATE tasks SET status=? WHERE big_task_id=? AND status=?`,
		model.TaskReady, bigTaskID, model.TaskPlanned,
	)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// SetStatusByBigTask sets every task of a big task to status, returning the count.
func (r *TaskRepo) SetStatusByBigTask(bigTaskID, status string) (int, error) {
	res, err := r.db.Exec(`UPDATE tasks SET status=? WHERE big_task_id=?`, status, bigTaskID)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// SetRun records the agent + branch a task is running on and sets its status.
func (r *TaskRepo) SetRun(id, agentID, branch, status string) error {
	res, err := r.db.Exec(
		`UPDATE tasks SET agent_id=?, branch=?, status=? WHERE id=?`,
		agentID, branch, status, id,
	)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

// SetPreferredAgent pins a task to a specific agent (steer/route).
func (r *TaskRepo) SetPreferredAgent(id, agentID string) error {
	res, err := r.db.Exec(`UPDATE tasks SET preferred_agent_id=? WHERE id=?`, agentID, id)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

// MarkMerged flips a task to merged, recording whether the machine auto-merged it
// (no human accept) and whether it was sampled for a post-merge audit (Phase 3).
func (r *TaskRepo) MarkMerged(id string, auto, auditFlagged bool) error {
	res, err := r.db.Exec(
		`UPDATE tasks SET status=?, auto_merged=?, audit_flagged=? WHERE id=?`,
		model.TaskMerged, boolToInt(auto), boolToInt(auditFlagged), id,
	)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

// ClearAuditFlag acknowledges a sampled audit ("looks good"), removing it from
// the audit queue without changing the merge.
func (r *TaskRepo) ClearAuditFlag(id string) error {
	res, err := r.db.Exec(`UPDATE tasks SET audit_flagged=0 WHERE id=?`, id)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

// SetReverted records a merged task as a change-failure and clears its audit
// flag. The actual git revert is left to the human (the flag drives metrics).
func (r *TaskRepo) SetReverted(id string) error {
	res, err := r.db.Exec(`UPDATE tasks SET reverted=1, audit_flagged=0 WHERE id=?`, id)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

// DeleteByBigTask removes every task belonging to a big task. The project tables
// carry no foreign keys, so deletion is explicit; removing zero rows is not an error.
func (r *TaskRepo) DeleteByBigTask(bigTaskID string) error {
	_, err := r.db.Exec(`DELETE FROM tasks WHERE big_task_id=?`, bigTaskID)
	return err
}

// Delete removes a task by ID, returning ErrNotFound when no row matched.
func (r *TaskRepo) Delete(id string) error {
	res, err := r.db.Exec(`DELETE FROM tasks WHERE id=?`, id)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

func scanTask(s scanner) (*model.Task, error) {
	var t model.Task
	var acc, dependsOn, touchPaths, tags, attachments string
	var autoMerged, auditFlagged, reverted int
	err := s.Scan(&t.ID, &t.BigTaskID, &t.Title, &t.Spec, &acc, &dependsOn, &touchPaths, &tags, &attachments,
		&t.RiskTier, &t.Priority, &t.Status, &t.Branch, &t.AgentID, &t.PreferredAgentID,
		&autoMerged, &auditFlagged, &reverted, &t.Reporter)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if acc != "" {
		_ = json.Unmarshal([]byte(acc), &t.Acceptance)
	}
	t.DependsOn = scanStrings(dependsOn)
	t.TouchPaths = scanStrings(touchPaths)
	t.Tags = scanStrings(tags)
	t.Attachments = scanStrings(attachments)
	t.AutoMerged = autoMerged != 0
	t.AuditFlagged = auditFlagged != 0
	t.Reverted = reverted != 0
	return &t, nil
}
