package store

import (
	"database/sql"
	"errors"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/google/uuid"
)

// PlanRepo persists proposed/approved plans in the per-project store. A plan's
// tasks live in the tasks table (keyed by big_task_id) and its open decisions in
// the decisions table (keyed by plan_id); Get returns the bare plan row and
// callers assemble the rest. See SPECS.md §5.
type PlanRepo struct{ db *sql.DB }

const planCols = `id, big_task_id, status`

// Create inserts a plan, assigning an ID and default status if absent.
func (r *PlanRepo) Create(p *model.Plan) error {
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	if p.Status == "" {
		p.Status = model.PlanProposed
	}
	_, err := r.db.Exec(
		`INSERT INTO plans (`+planCols+`) VALUES (?, ?, ?)`,
		p.ID, p.BigTaskID, p.Status,
	)
	return err
}

// Get returns the bare plan row (Tasks/OpenDecisions are not populated).
func (r *PlanRepo) Get(id string) (*model.Plan, error) {
	row := r.db.QueryRow(`SELECT `+planCols+` FROM plans WHERE id=?`, id)
	return scanPlan(row)
}

// GetByBigTask returns the most recent plan for a big task, or ErrNotFound.
func (r *PlanRepo) GetByBigTask(bigTaskID string) (*model.Plan, error) {
	row := r.db.QueryRow(
		`SELECT `+planCols+` FROM plans WHERE big_task_id=? ORDER BY created_at DESC, rowid DESC LIMIT 1`,
		bigTaskID,
	)
	return scanPlan(row)
}

// List returns all plans newest-first (bare rows).
func (r *PlanRepo) List() ([]model.Plan, error) {
	rows, err := r.db.Query(`SELECT ` + planCols + ` FROM plans ORDER BY created_at DESC, rowid DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Plan
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// UpdateStatus sets a plan's status (proposed|approved|rejected).
func (r *PlanRepo) UpdateStatus(id, status string) error {
	res, err := r.db.Exec(`UPDATE plans SET status=? WHERE id=?`, status, id)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

func scanPlan(s scanner) (*model.Plan, error) {
	var p model.Plan
	err := s.Scan(&p.ID, &p.BigTaskID, &p.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// DecisionRepo persists decisions (plan-level questions from the planner and
// mid-run escalations from agents) in the per-project store.
type DecisionRepo struct{ db *sql.DB }

const decisionCols = `id, plan_id, task_id, question, options, context, answer, promote, status`

// Create inserts a decision, assigning an ID and default status if absent.
func (r *DecisionRepo) Create(d *model.Decision) error {
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	if d.Status == "" {
		d.Status = model.DecisionOpen
	}
	_, err := r.db.Exec(
		`INSERT INTO decisions (`+decisionCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.PlanID, d.TaskID, d.Question, jsonStrings(d.Options), d.Context,
		d.Answer, boolToInt(d.Promote), d.Status,
	)
	return err
}

// Get returns a decision by ID.
func (r *DecisionRepo) Get(id string) (*model.Decision, error) {
	row := r.db.QueryRow(`SELECT `+decisionCols+` FROM decisions WHERE id=?`, id)
	return scanDecision(row)
}

// ListOpen returns every unanswered decision, oldest-first (queue order).
func (r *DecisionRepo) ListOpen() ([]model.Decision, error) {
	return r.query(`SELECT `+decisionCols+` FROM decisions WHERE status=? ORDER BY created_at, rowid`, model.DecisionOpen)
}

// ListForPlan returns the plan-level decisions for a plan.
func (r *DecisionRepo) ListForPlan(planID string) ([]model.Decision, error) {
	return r.query(`SELECT `+decisionCols+` FROM decisions WHERE plan_id=? ORDER BY created_at, rowid`, planID)
}

// ListForTask returns the decisions raised against a task.
func (r *DecisionRepo) ListForTask(taskID string) ([]model.Decision, error) {
	return r.query(`SELECT `+decisionCols+` FROM decisions WHERE task_id=? ORDER BY created_at, rowid`, taskID)
}

// Answer records an answer (+ promote flag) and marks the decision answered.
func (r *DecisionRepo) Answer(id, answer string, promote bool) error {
	res, err := r.db.Exec(
		`UPDATE decisions SET answer=?, promote=?, status=? WHERE id=?`,
		answer, boolToInt(promote), model.DecisionAnswered, id,
	)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

func (r *DecisionRepo) query(q string, args ...any) ([]model.Decision, error) {
	rows, err := r.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Decision
	for rows.Next() {
		d, err := scanDecision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func scanDecision(s scanner) (*model.Decision, error) {
	var d model.Decision
	var options string
	var promote int
	err := s.Scan(&d.ID, &d.PlanID, &d.TaskID, &d.Question, &options, &d.Context, &d.Answer, &promote, &d.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d.Options = scanStrings(options)
	d.Promote = promote != 0
	return &d, nil
}
