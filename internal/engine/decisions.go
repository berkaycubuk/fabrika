package engine

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/berkaycubuk/fabrika/internal/config"
	"github.com/berkaycubuk/fabrika/internal/model"
)

// recordEscalation turns an agent's structured escalation (the JSON after the
// DecisionMarker) into an open, task-level Decision for the queue. Malformed or
// empty payloads still produce a decision so the human isn't left blind.
func (e *Engine) recordEscalation(task model.Task, payload string) {
	var parsed struct {
		Question string   `json:"question"`
		Options  []string `json:"options"`
		Context  string   `json:"context"`
	}
	_ = json.Unmarshal([]byte(strings.TrimSpace(payload)), &parsed)
	question := strings.TrimSpace(parsed.Question)
	if question == "" {
		question = "The agent escalated but gave no question. Decide how to proceed."
	}
	d := &model.Decision{
		TaskID:   task.ID,
		Question: question,
		Options:  parsed.Options,
		Context:  parsed.Context,
		Status:   model.DecisionOpen,
	}
	if err := e.store.Decisions.Create(d); err != nil {
		log.Printf("engine: record escalation: %v", err)
		return
	}
	e.emit("decision.created", *d)
}

// AnswerDecision records an answer to a decision and applies its consequences:
//   - promote=true adds the Q→A as a standing Convention injected into future runs.
//   - a task-level decision resumes its task: the resolution is appended to the
//     task spec and the task returns to ready so the scheduler re-runs it.
//
// Plan-level decisions (from the planner) only record the answer (+ convention).
func (e *Engine) AnswerDecision(id, answer string, promote bool) error {
	d, err := e.store.Decisions.Get(id)
	if err != nil {
		return err
	}
	if d.Status == model.DecisionAnswered {
		return fmt.Errorf("decision already answered")
	}
	if err := e.store.Decisions.Answer(id, answer, promote); err != nil {
		return err
	}

	if promote {
		conv := &model.Convention{Rule: fmt.Sprintf("%s → %s", d.Question, answer)}
		if cerr := e.store.Conventions.Create(conv); cerr != nil {
			log.Printf("engine: promote convention: %v", cerr)
		} else {
			e.emit("convention.created", *conv)
		}
	}

	if d.TaskID != "" {
		if rerr := e.resumeTask(d.TaskID, d.Question, answer); rerr != nil {
			log.Printf("engine: resume task %s: %v", d.TaskID, rerr)
		}
	}

	d.Answer, d.Promote, d.Status = answer, promote, model.DecisionAnswered
	e.emit("decision.updated", *d)
	return nil
}

// resumeTask injects the resolved decision into the task spec and returns it to
// ready so the scheduler picks it up again with the answer in context.
func (e *Engine) resumeTask(taskID, question, answer string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	t, err := e.store.Tasks.Get(taskID)
	if err != nil {
		return err
	}
	if t.Status == model.TaskMerged || t.Status == model.TaskClosed {
		return nil // nothing to resume
	}
	resolution := fmt.Sprintf("\n\n## Resolved decision\n**Q:** %s\n**A:** %s\n", question, answer)
	if err := e.store.Tasks.SetSpec(taskID, t.Spec+resolution); err != nil {
		return err
	}
	if err := e.store.Tasks.UpdateStatus(taskID, model.TaskReady); err != nil {
		return err
	}
	if updated, gerr := e.store.Tasks.Get(taskID); gerr == nil {
		e.emit("task.updated", *updated)
	}
	e.Wake()
	return nil
}

// ApprovePlan marks a plan approved and promotes its planned tasks to ready so
// the scheduler can dispatch them. The big task moves to running.
func (e *Engine) ApprovePlan(planID string) error {
	p, err := e.store.Plans.Get(planID)
	if err != nil {
		return err
	}
	if p.Status == model.PlanApproved {
		return fmt.Errorf("plan already approved")
	}
	if p.Status == model.PlanRejected {
		return fmt.Errorf("plan was rejected")
	}
	if err := e.store.Plans.UpdateStatus(planID, model.PlanApproved); err != nil {
		return err
	}
	n, err := e.store.Tasks.MarkReadyByBigTask(p.BigTaskID)
	if err != nil {
		return err
	}
	e.setBigTaskStatus(p.BigTaskID, model.BigTaskRunning)
	e.emit("plan.updated", *p)
	log.Printf("engine: approved plan %s -> %d task(s) ready", planID, n)
	e.Wake()
	return nil
}

// RejectPlan marks a plan rejected and closes its still-planned tasks. The big
// task returns to draft so it can be re-planned.
func (e *Engine) RejectPlan(planID string) error {
	p, err := e.store.Plans.Get(planID)
	if err != nil {
		return err
	}
	if err := e.store.Plans.UpdateStatus(planID, model.PlanRejected); err != nil {
		return err
	}
	tasks, _ := e.store.Tasks.ListByBigTask(p.BigTaskID)
	for _, t := range tasks {
		if t.Status == model.TaskPlanned {
			_ = e.store.Tasks.UpdateStatus(t.ID, model.TaskClosed)
			if updated, gerr := e.store.Tasks.Get(t.ID); gerr == nil {
				e.emit("task.updated", *updated)
			}
		}
	}
	e.setBigTaskStatus(p.BigTaskID, model.BigTaskDraft)
	e.emit("plan.updated", *p)
	return nil
}

// lockedViolations returns the changed files that match any locked glob. The
// implementer must not edit these (spec-derived locked acceptance, SPECS §8).
func lockedViolations(changed, lockedGlobs []string) []string {
	if len(lockedGlobs) == 0 {
		return nil
	}
	var hits []string
	for _, f := range changed {
		f = strings.TrimPrefix(strings.TrimSpace(f), "./")
		for _, g := range lockedGlobs {
			if config.MatchGlob(strings.TrimSpace(g), f) {
				hits = append(hits, f)
				break
			}
		}
	}
	return hits
}
