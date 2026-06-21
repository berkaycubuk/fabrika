package engine

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/google/uuid"
)

// This file holds tx-aware variants of the per-project store writes the engine
// needs to perform atomically. They deliberately mirror the column lists,
// default-filling, and JSON encoding of the corresponding store.*Repo.Create /
// update methods so that a write done through a transaction is byte-identical to
// the same write done through the repo. Threading these through one
// store.WithProjectTx closure makes a multi-row state transition crash-atomic.

// jsonStringsTx mirrors store.jsonStrings: a nil slice becomes "[]" so columns
// are never NULL.
func jsonStringsTx(v []string) string {
	if v == nil {
		v = []string{}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func boolToIntTx(b bool) int {
	if b {
		return 1
	}
	return 0
}

// insertPlanTx mirrors store.PlanRepo.Create.
func insertPlanTx(tx *sql.Tx, p *model.Plan) error {
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	if p.Status == "" {
		p.Status = model.PlanProposed
	}
	_, err := tx.Exec(
		`INSERT INTO plans (id, big_task_id, status) VALUES (?, ?, ?)`,
		p.ID, p.BigTaskID, p.Status,
	)
	return err
}

// insertTaskTx mirrors store.TaskRepo.Create (column list and default-filling).
func insertTaskTx(tx *sql.Tx, t *model.Task) error {
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
	_, err = tx.Exec(
		`INSERT INTO tasks (id, big_task_id, title, spec, acceptance, depends_on, touch_paths, tags, attachments, risk_tier, priority, status, branch, agent_id, preferred_agent_id, auto_merged, audit_flagged, reverted, reporter, merge_commit_sha, release_id, ci_status, ci_run_url) `+
			`VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.BigTaskID, t.Title, t.Spec, string(acc),
		jsonStringsTx(t.DependsOn), jsonStringsTx(t.TouchPaths), jsonStringsTx(t.Tags), jsonStringsTx(t.Attachments),
		t.RiskTier, t.Priority, t.Status, t.Branch, t.AgentID, t.PreferredAgentID,
		boolToIntTx(t.AutoMerged), boolToIntTx(t.AuditFlagged), boolToIntTx(t.Reverted), t.Reporter,
		t.MergeCommitSHA, t.ReleaseID, t.CIStatus, t.CIRunURL,
	)
	return err
}

// insertDecisionTx mirrors store.DecisionRepo.Create.
func insertDecisionTx(tx *sql.Tx, d *model.Decision) error {
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	if d.Status == "" {
		d.Status = model.DecisionOpen
	}
	_, err := tx.Exec(
		`INSERT INTO decisions (id, plan_id, task_id, question, options, context, answer, promote, status) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.PlanID, d.TaskID, d.Question, jsonStringsTx(d.Options), d.Context,
		d.Answer, boolToIntTx(d.Promote), d.Status,
	)
	return err
}

// insertAttemptTx mirrors store.AttemptRepo.Create.
func insertAttemptTx(tx *sql.Tx, a *model.Attempt) error {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	ev, err := json.Marshal(a.Evidence)
	if err != nil {
		return err
	}
	_, err = tx.Exec(
		`INSERT INTO attempts (id, task_id, agent_id, result, evidence, log, input_tokens, output_tokens, total_tokens) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.TaskID, a.AgentID, a.Result, string(ev), a.Log,
		a.Usage.InputTokens, a.Usage.OutputTokens, a.Usage.TotalTokens,
	)
	return err
}

// insertTransitionTx mirrors store.TransitionRepo.Create.
func insertTransitionTx(tx *sql.Tx, t *model.TaskTransition) error {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	if t.Actor == "" {
		t.Actor = "engine"
	}
	if t.CreatedAt == "" {
		t.CreatedAt = time.Now().UTC().Format("2006-01-02 15:04:05")
	}
	_, err := tx.Exec(
		`INSERT INTO task_transitions (id, task_id, from_status, to_status, actor, reason, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.TaskID, t.FromStatus, t.ToStatus, t.Actor, t.Reason, t.CreatedAt,
	)
	return err
}

// updateBigTaskStatusTx mirrors store.BigTaskRepo.UpdateStatus (also clears the
// prior error reason).
func updateBigTaskStatusTx(tx *sql.Tx, id, status string) error {
	_, err := tx.Exec(`UPDATE bigtasks SET status=?, error='' WHERE id=?`, status, id)
	return err
}

// setPlanFeedbackTx mirrors store.BigTaskRepo.SetPlanFeedback.
func setPlanFeedbackTx(tx *sql.Tx, id, feedback string) error {
	_, err := tx.Exec(`UPDATE bigtasks SET plan_feedback=? WHERE id=?`, feedback, id)
	return err
}

// updateTaskStatusTx mirrors store.TaskRepo.UpdateStatus.
func updateTaskStatusTx(tx *sql.Tx, id, status string) error {
	_, err := tx.Exec(`UPDATE tasks SET status=? WHERE id=?`, status, id)
	return err
}

// setMergeCommitSHATx mirrors store.TaskRepo.SetMergeCommitSHA.
func setMergeCommitSHATx(tx *sql.Tx, id, sha string) error {
	_, err := tx.Exec(`UPDATE tasks SET merge_commit_sha=? WHERE id=?`, sha, id)
	return err
}

// markMergedTx mirrors store.TaskRepo.MarkMerged.
func markMergedTx(tx *sql.Tx, id string, auto, auditFlagged bool) error {
	_, err := tx.Exec(
		`UPDATE tasks SET status=?, auto_merged=?, audit_flagged=? WHERE id=?`,
		model.TaskMerged, boolToIntTx(auto), boolToIntTx(auditFlagged), id,
	)
	return err
}
