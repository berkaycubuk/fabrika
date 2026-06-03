package engine

import (
	"log"
	"os"
	"path/filepath"

	"github.com/berkaycubuk/fabrika/internal/agent"
	"github.com/berkaycubuk/fabrika/internal/git"
	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/berkaycubuk/fabrika/internal/planner"
)

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
		log.Printf("engine: no planner agent for big task %q", bt.Title)
		return
	}

	e.setBigTaskStatus(bt.ID, model.BigTaskPlanning)

	// A clean worktree gives the planner repo context without touching main.
	repo, err := git.Open(e.ctx, e.repoRoot)
	if err != nil {
		log.Printf("engine: planner open repo: %v", err)
		e.setBigTaskStatus(bt.ID, model.BigTaskDraft)
		return
	}
	base, err := repo.CurrentBranch(e.ctx)
	if err != nil {
		log.Printf("engine: planner current branch: %v", err)
		e.setBigTaskStatus(bt.ID, model.BigTaskDraft)
		return
	}
	branch := "fabrika/plan-" + shortID(bt.ID)
	wt := filepath.Join(e.repoRoot, ".fabrika", "worktrees", "plan-"+shortID(bt.ID))
	e.mu.Lock()
	_ = repo.RemoveWorktree(e.ctx, wt)
	_ = os.RemoveAll(wt)
	if mkErr := os.MkdirAll(filepath.Dir(wt), 0o755); mkErr != nil {
		e.mu.Unlock()
		log.Printf("engine: planner mkdir: %v", mkErr)
		e.setBigTaskStatus(bt.ID, model.BigTaskDraft)
		return
	}
	addErr := repo.AddWorktree(e.ctx, wt, branch, base)
	e.mu.Unlock()
	if addErr != nil {
		log.Printf("engine: planner worktree: %v", addErr)
		e.setBigTaskStatus(bt.ID, model.BigTaskDraft)
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
		log.Printf("engine: planner write prompt: %v", err)
		e.setBigTaskStatus(bt.ID, model.BigTaskDraft)
		return
	}
	defer cleanup()

	synthetic := model.Task{ID: bt.ID, Title: "plan: " + bt.Title}
	res, err := e.agent.Run(e.ctx, ag, synthetic, wt, promptFile)
	if err != nil {
		log.Printf("engine: planner run: %v", err)
		e.setBigTaskStatus(bt.ID, model.BigTaskDraft)
		return
	}

	// Prefer the plan file; fall back to the agent's stdout.
	output := res.Stdout
	if data, rerr := os.ReadFile(planFile); rerr == nil && len(data) > 0 {
		output = string(data)
	}
	raw, err := planner.Parse(output)
	if err != nil {
		log.Printf("engine: planner parse: %v", err)
		e.setBigTaskStatus(bt.ID, model.BigTaskDraft)
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

func (e *Engine) setBigTaskStatus(id, status string) {
	if err := e.store.BigTasks.UpdateStatus(id, status); err != nil {
		log.Printf("engine: set bigtask status %s=%s: %v", id, status, err)
		return
	}
	if bt, err := e.store.BigTasks.Get(id); err == nil {
		e.emit("bigtask.updated", *bt)
	}
}
