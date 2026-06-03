package engine

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/berkaycubuk/fabrika/internal/agent"
	"github.com/berkaycubuk/fabrika/internal/git"
	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/berkaycubuk/fabrika/internal/planner"
)

// Preflight validates the target repo is ready to plan and dispatch work: it
// must be a git work tree with at least one commit (worktrees fork off HEAD).
// It returns a human-actionable error so callers can reject up front rather
// than failing asynchronously deep in the planner. See SPECS.md §7.
func (e *Engine) Preflight(ctx context.Context) error {
	repo, err := git.Open(ctx, e.repoRoot)
	if err != nil {
		return err
	}
	if !repo.HasCommits(ctx) {
		return fmt.Errorf("repo at %s has no commits yet — make an initial commit "+
			"(e.g. `git commit --allow-empty -m init`) before defining tasks", e.repoRoot)
	}
	return nil
}

// settingRolePlanner names the agent that holds the planner role (optional
// override; otherwise any enabled agent with the planner role is used).
const settingRolePlanner = "role_planner"

// planFileName is where the planner agent writes its JSON plan, relative to its
// worktree. The engine reads it back (falling back to stdout) after the run.
const planFileName = "fabrika_plan.json"

// PlannerAgent returns the agent that should plan, and whether one exists. It
// prefers the configured role_planner override (if enabled), else the first
// enabled agent carrying the planner role.
func (e *Engine) PlannerAgent() (model.Agent, bool) {
	agents, err := e.store.Agents.List()
	if err != nil {
		log.Printf("engine: list agents for planner: %v", err)
		return model.Agent{}, false
	}
	if id, _ := e.store.Settings.Get(settingRolePlanner); id != "" {
		for _, a := range agents {
			if a.ID == id && a.Enabled && agent.HasRole(a, model.RolePlanner) {
				return a, true
			}
		}
	}
	for _, a := range agents {
		if a.Enabled && agent.HasRole(a, model.RolePlanner) {
			return a, true
		}
	}
	return model.Agent{}, false
}

// PlanBigTask runs the planner agent against a big task and persists the
// resulting proposed plan (tasks in `planned` status, plus any open decisions).
// It runs asynchronously; the UI is notified via events as it progresses. The
// caller should only invoke this when PlannerAgent reports an available planner.
func (e *Engine) PlanBigTask(bt model.BigTask) {
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.planBigTask(bt)
	}()
}

func (e *Engine) planBigTask(bt model.BigTask) {
	ag, ok := e.PlannerAgent()
	if !ok {
		e.failBigTask(bt.ID, "no planner agent enabled — enable an agent with the planner role under Agents")
		return
	}

	// Mark the planner busy for the duration so it doesn't read as idle in the
	// metrics while it works (planning runs outside the task-dispatch loop).
	e.markPlanning(ag.ID, 1)
	defer e.markPlanning(ag.ID, -1)

	e.setBigTaskStatus(bt.ID, model.BigTaskPlanning)

	// A clean worktree gives the planner repo context without touching main.
	repo, err := git.Open(e.ctx, e.repoRoot)
	if err != nil {
		e.failBigTask(bt.ID, "open repo: %v", err)
		return
	}
	if !repo.HasCommits(e.ctx) {
		e.failBigTask(bt.ID, "repo at %s has no commits yet — make an initial commit "+
			"(e.g. `git commit --allow-empty -m init`) before defining tasks", e.repoRoot)
		return
	}
	base, err := repo.CurrentBranch(e.ctx)
	if err != nil {
		e.failBigTask(bt.ID, "determine current branch: %v", err)
		return
	}
	branch := "fabrika/plan-" + shortID(bt.ID)
	wt := filepath.Join(e.repoRoot, ".fabrika", "worktrees", "plan-"+shortID(bt.ID))
	e.mu.Lock()
	_ = repo.RemoveWorktree(e.ctx, wt)
	_ = os.RemoveAll(wt)
	if mkErr := os.MkdirAll(filepath.Dir(wt), 0o755); mkErr != nil {
		e.mu.Unlock()
		e.failBigTask(bt.ID, "create worktree dir: %v", mkErr)
		return
	}
	addErr := repo.AddWorktree(e.ctx, wt, branch, base)
	e.mu.Unlock()
	if addErr != nil {
		e.failBigTask(bt.ID, "create planner worktree: %v", addErr)
		return
	}
	defer func() {
		e.mu.Lock()
		_ = repo.RemoveWorktree(e.ctx, wt)
		e.mu.Unlock()
	}()

	planFile := filepath.Join(wt, planFileName)
	conventions, _ := e.store.Conventions.List()
	prompt := planner.RenderPrompt(bt, conventions, planFile)
	promptFile, cleanup, err := writeTempPrompt(prompt)
	if err != nil {
		e.failBigTask(bt.ID, "write planner prompt: %v", err)
		return
	}
	defer cleanup()

	synthetic := model.Task{ID: bt.ID, Title: "plan: " + bt.Title}
	res, err := e.agent.Run(e.ctx, ag, synthetic, wt, promptFile)
	if err != nil {
		e.failBigTask(bt.ID, "run planner agent: %v", err)
		return
	}

	// Prefer the plan file; fall back to the agent's stdout.
	output := res.Stdout
	if data, rerr := os.ReadFile(planFile); rerr == nil && len(data) > 0 {
		output = string(data)
	}
	raw, err := planner.Parse(output)
	if err != nil {
		e.failBigTask(bt.ID, "parse planner output: %v", err)
		return
	}

	e.persistPlan(bt, raw)
}

// persistPlan writes the plan row, its tasks (planned), and open decisions, then
// marks the big task planned and notifies the UI.
func (e *Engine) persistPlan(bt model.BigTask, raw planner.RawPlan) {
	e.mu.Lock()
	defer e.mu.Unlock()

	plan := &model.Plan{BigTaskID: bt.ID, Status: model.PlanProposed}
	if err := e.store.Plans.Create(plan); err != nil {
		log.Printf("engine: create plan: %v", err)
		return
	}
	tasks, decisions := planner.Build(bt, plan.ID, raw)
	for i := range tasks {
		t := tasks[i]
		if err := e.store.Tasks.Create(&t); err != nil {
			log.Printf("engine: create planned task: %v", err)
			continue
		}
		e.emit("task.created", t)
	}
	for i := range decisions {
		d := decisions[i]
		if err := e.store.Decisions.Create(&d); err != nil {
			log.Printf("engine: create decision: %v", err)
			continue
		}
		e.emit("decision.created", d)
	}
	if err := e.store.BigTasks.UpdateStatus(bt.ID, model.BigTaskPlanned); err != nil {
		log.Printf("engine: set bigtask planned: %v", err)
	}
	plan.Tasks, plan.OpenDecisions = tasks, decisions
	e.emit("plan.ready", *plan)
	log.Printf("engine: planned %q -> %d task(s), %d decision(s)", bt.Title, len(tasks), len(decisions))
}

// failBigTask records a planning failure on the big task (status 'error' + a
// human-readable reason) and notifies the UI, so failures surface instead of
// silently reverting to draft. The reason is also logged.
func (e *Engine) failBigTask(id, format string, args ...any) {
	reason := fmt.Sprintf(format, args...)
	log.Printf("engine: planner failed for big task %s: %s", id, reason)
	if err := e.store.BigTasks.SetError(id, reason); err != nil {
		log.Printf("engine: set bigtask error %s: %v", id, err)
		return
	}
	if bt, err := e.store.BigTasks.Get(id); err == nil {
		e.emit("bigtask.updated", *bt)
	}
}

// markPlanning adjusts the active planning-run count for an agent. A positive
// delta marks it busy; a negative delta releases it (the entry is dropped at
// zero so PlanningCounts only ever reports agents currently planning).
func (e *Engine) markPlanning(agentID string, delta int) {
	e.planMu.Lock()
	defer e.planMu.Unlock()
	e.planning[agentID] += delta
	if e.planning[agentID] <= 0 {
		delete(e.planning, agentID)
	}
}

// PlanningCounts returns a snapshot of how many big-task planning runs each
// agent is currently executing, so the metrics surface can show the planner as
// busy even though planning runs outside the task-dispatch loop.
func (e *Engine) PlanningCounts() map[string]int {
	e.planMu.Lock()
	defer e.planMu.Unlock()
	out := make(map[string]int, len(e.planning))
	for id, n := range e.planning {
		out[id] = n
	}
	return out
}

func (e *Engine) setBigTaskStatus(id, status string) {
	if err := e.store.BigTasks.UpdateStatus(id, status); err != nil {
		log.Printf("engine: set bigtask status %s=%s: %v", id, status, err)
		return
	}
	if bt, err := e.store.BigTasks.Get(id); err == nil {
		e.emit("bigtask.updated", *bt)
	}
}
