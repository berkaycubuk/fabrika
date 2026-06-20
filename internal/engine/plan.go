package engine

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

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

// PlanBigTask is the public trigger to (eventually) decompose a big task into a
// proposed plan. It does NOT run the planner directly: planning is gated by the
// planner agent's Concurrency, so excess big tasks must WAIT in `draft` (the
// board renders that as "Queued for planning") until a planner slot frees. It
// simply ensures the big task is in `draft` and wakes the gated dispatcher
// (dispatchPlanning), which claims a slot and runs the plan FIFO.
func (e *Engine) PlanBigTask(bt model.BigTask) {
	if bt.Status != model.BigTaskDraft {
		e.setBigTaskStatus(bt.ID, model.BigTaskDraft)
	}
	e.Wake()
}

// dispatchPlanning launches every currently-plannable big task, each in its own
// goroutine, respecting the planner agent's Concurrency. A planner's planning
// runs share one Concurrency budget with its running implementer tasks, so it
// stops claiming once that budget is full; the rest wait in `draft`. Each run's
// goroutine releases the claimed slot exactly once on completion and re-wakes the
// loop so the next queued big task flows immediately.
func (e *Engine) dispatchPlanning() {
	for {
		if e.ctx != nil && e.ctx.Err() != nil {
			return
		}
		bt, ag, ok := e.claimPlanning()
		if !ok {
			return
		}
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			// The slot was claimed (incremented) synchronously in claimPlanning;
			// release it exactly once here so it is never double-counted.
			defer e.markPlanning(ag.ID, -1)
			e.planBigTaskCore(bt, ag)
			e.Wake()
		}()
	}
}

// claimPlanning selects the oldest `draft` big task and claims a planning slot for
// the planner agent under the lock, returning the work to do. It enforces the
// per-planner Concurrency cap: planning runs plus that agent's running implementer
// tasks are counted jointly against its single Concurrency value. The slot (the
// planning-count increment) is taken synchronously here, BEFORE the run goroutine
// starts, so two dispatcher iterations can't both observe a free slot and
// double-spawn. Returns false when no slot is free or no big task is queued.
func (e *Engine) claimPlanning() (model.BigTask, model.Agent, bool) {
	ag, ok := e.PlannerAgent()
	if !ok {
		return model.BigTask{}, model.Agent{}, false
	}
	bts, err := e.store.BigTasks.List()
	if err != nil {
		log.Printf("engine: list bigtasks for planning: %v", err)
		return model.BigTask{}, model.Agent{}, false
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.planMu.Lock()
	defer e.planMu.Unlock()

	// Joint budget: active planning runs + this planner's running implementer
	// tasks share the agent's (store-normalized, >=1) Concurrency value.
	used := e.planning[ag.ID]
	for _, ri := range e.running {
		if ri.agentID == ag.ID {
			used++
		}
	}
	if used >= ag.Concurrency {
		return model.BigTask{}, model.Agent{}, false
	}

	// List is newest-first; iterate oldest-first so big tasks plan FIFO.
	for i := len(bts) - 1; i >= 0; i-- {
		bt := bts[i]
		if bt.Status != model.BigTaskDraft {
			continue
		}
		// Claim the slot now, under the lock, so the next iteration sees it taken.
		e.planning[ag.ID]++
		// Record the planner agent before flipping status so the emit carries the
		// agent ID and the UI shows it in the card.
		if serr := e.store.BigTasks.SetPlannerAgent(bt.ID, ag.ID); serr != nil {
			log.Printf("engine: set planner agent %s: %v", bt.ID, serr)
		}
		e.setBigTaskStatus(bt.ID, model.BigTaskPlanning)
		bt.Status = model.BigTaskPlanning
		bt.PlannerAgentID = ag.ID
		return bt, ag, true
	}
	return model.BigTask{}, model.Agent{}, false
}

// planBigTask runs the planner against a big task with self-contained slot
// accounting. It is the synchronous entry retained for direct callers (tests):
// it claims a planning slot itself, then runs the core. The gated dispatcher does
// NOT go through here — it claims the slot in claimPlanning and calls
// planBigTaskCore directly, so the slot is never counted twice.
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

	e.planBigTaskCore(bt, ag)
}

// planBigTaskCore is the actual planner run body. The caller owns the
// planning-slot accounting (markPlanning), so this MUST NOT adjust it. Recording
// the planner agent and flipping status to `planning` is idempotent: the gated
// dispatcher already did both when it claimed the slot; direct callers rely on it.
func (e *Engine) planBigTaskCore(bt model.BigTask, ag model.Agent) {
	// Per-big-task cancellable context so cancelling this run stops only this
	// planner, not the whole engine (mirrors claim() at engine.go).
	planCtx, cancel := context.WithCancel(e.ctx)
	e.planMu.Lock()
	e.planRuns[bt.ID] = planRunInfo{cancel: cancel}
	e.planMu.Unlock()
	defer func() {
		e.planMu.Lock()
		delete(e.planRuns, bt.ID)
		e.planMu.Unlock()
		cancel()
	}()

	// Record the planner agent on the big task before flipping status so the
	// subsequent emit carries the agent ID and the UI shows it in the card.
	e.store.BigTasks.SetPlannerAgent(bt.ID, ag.ID)

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
	_ = repo.DeleteBranch(e.ctx, branch)
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
	basePrompt := planner.RenderPrompt(bt, conventions, planFile, e.attachmentPaths(bt.Attachments))
	synthetic := model.Task{ID: bt.ID, Title: "plan: " + bt.Title}

	// Reset the activity log so a re-plan shows only the latest run's timeline.
	// A failed run's activity is retained until the NEXT run starts.
	if err := e.store.PlanActivity.DeleteByBigTask(bt.ID); err != nil {
		log.Printf("engine: reset plan activity %s: %v", bt.ID, err)
	}

	// Run the planner, validate the contract invariants the prompt can only ask
	// for, and give the planner ONE repair attempt with the exact violations fed
	// back. Catching an unsatisfiable held-out check here turns a guaranteed
	// late gate failure (after a full implementer run) into an immediate,
	// correctly-attributed plan rejection.
	prompt := basePrompt
	var raw planner.RawPlan
	var usage model.Usage
	for attempt := 0; ; attempt++ {
		promptFile, cleanup, err := writeTempPrompt(prompt)
		if err != nil {
			e.failBigTask(bt.ID, "write planner prompt: %v", err)
			return
		}
		// Stream the planner so its activity meter keeps ticking (stall detection)
		// and the UI can show typed planner activity live. Fall back to the plain
		// Run path for alternate runners that aren't a *Subprocess.
		var res agent.AgentResult
		if sub, ok := e.agent.(*agent.Subprocess); ok {
			res, err = sub.RunStream(planCtx, ag, synthetic, wt, promptFile, func(ev agent.ActivityEvent) {
				e.emit("planner.activity", map[string]any{"bigTaskId": bt.ID, "event": ev})
				if aerr := e.store.PlanActivity.Append(bt.ID, model.PlanActivity{Type: ev.Type, Summary: ev.Summary, Ts: ev.Ts}); aerr != nil {
					log.Printf("engine: persist plan activity %s: %v", bt.ID, aerr)
				}
			})
		} else {
			res, err = e.agent.Run(planCtx, ag, synthetic, wt, promptFile)
		}
		cleanup()

		// A deliberate planning stop lands the big task in error.
		if reason, stopped := e.planCancelReason(bt.ID); stopped {
			e.failBigTask(bt.ID, "planning stopped: %s", reason)
			return
		}

		if err != nil {
			e.failBigTask(bt.ID, "run planner agent: %v", err)
			return
		}

		// Persist the planner's reported usage now, before parsing — the tokens
		// were consumed by the run regardless of whether the plan output parses
		// cleanly. Repair attempts accumulate.
		usage.InputTokens += res.Usage.InputTokens
		usage.OutputTokens += res.Usage.OutputTokens
		usage.TotalTokens += res.Usage.TotalTokens
		if uerr := e.store.BigTasks.SetUsage(bt.ID, usage); uerr != nil {
			log.Printf("engine: set bigtask usage %s: %v", bt.ID, uerr)
		}

		// Prefer the plan file; fall back to the agent's stdout.
		output := res.Stdout
		if data, rerr := os.ReadFile(planFile); rerr == nil && len(data) > 0 {
			output = string(data)
		}
		raw, err = planner.Parse(output)
		if err != nil {
			e.failBigTask(bt.ID, "parse planner output: %v", err)
			return
		}

		issues := planner.ValidateHeldOut(e.repoRoot, raw)
		if len(issues) == 0 {
			break
		}
		if attempt >= 1 {
			e.failBigTask(bt.ID, "plan rejected after repair attempt: %s", strings.Join(issues, "; "))
			return
		}
		log.Printf("engine: plan for %q rejected, retrying planner: %s", bt.Title, strings.Join(issues, "; "))
		var b strings.Builder
		b.WriteString(basePrompt)
		b.WriteString("\n## Previous attempt rejected\nYour previous plan was rejected for these contract violations:\n")
		for _, is := range issues {
			fmt.Fprintf(&b, "  - %s\n", is)
		}
		b.WriteString("\nFix them and write the FULL corrected plan JSON to the same file. ")
		b.WriteString("Remember: every file a `heldOut` command needs must already exist in the repo, ")
		b.WriteString("be listed in that task's `touchPaths` (the implementer will create it), ")
		b.WriteString("or be fully authored by you in `heldOutFiles`.\n")
		prompt = b.String()
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
	e.store.BigTasks.SetPlanFeedback(bt.ID, "")
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

// DeleteBigTask cascade-deletes a plan request and all its children (decisions,
// tasks, plans). It refuses if any of the big task's tasks are in an active state
// (claimed, running, or verifying). Connected UIs are notified via bigtask.deleted.
func (e *Engine) DeleteBigTask(id string) error {
	if _, err := e.store.BigTasks.Get(id); err != nil {
		return err
	}
	tasks, err := e.store.Tasks.ListByBigTask(id)
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if t.Status == model.TaskClaimed || t.Status == model.TaskRunning || t.Status == model.TaskVerifying {
			return fmt.Errorf("cannot delete a plan request with running tasks")
		}
	}
	if err := e.store.Decisions.DeleteByBigTask(id); err != nil {
		return err
	}
	if err := e.store.Tasks.DeleteByBigTask(id); err != nil {
		return err
	}
	if err := e.store.Plans.DeleteByBigTask(id); err != nil {
		return err
	}
	if err := e.store.Comments.DeleteByBigTask(id); err != nil {
		return err
	}
	if err := e.store.PlanActivity.DeleteByBigTask(id); err != nil {
		return err
	}
	if err := e.store.BigTasks.Delete(id); err != nil {
		return err
	}
	e.emit("bigtask.deleted", map[string]string{"id": id})
	return nil
}
