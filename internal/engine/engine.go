// Package engine owns the task lifecycle and the dispatch loop that turns a
// ready task into shipped (or surfaced) work: route it to an agent, run the
// agent in an isolated git worktree, verify the result through the gate, and
// record normalized Evidence. Accepted work is merged on human approval.
//
// Phase 1: dispatch is parallel. The scheduler tracks each agent's free slots
// (Concurrency minus running attempts), honors a global WIP cap, avoids
// TouchPaths collisions between concurrently running tasks, and gates tasks on
// their DependsOn edges. Merge is still manual (the human Accepts); risk-tiered
// auto-merge is Phase 3. See SPECS.md §7, §8, §9, §13.
package engine

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/berkaycubuk/fabrika/internal/agent"
	"github.com/berkaycubuk/fabrika/internal/config"
	"github.com/berkaycubuk/fabrika/internal/gate"
	"github.com/berkaycubuk/fabrika/internal/git"
	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/berkaycubuk/fabrika/internal/store"
)

// EventFunc emits a UI event (the api layer adapts this to its WebSocket hub).
// Engine stays decoupled from api to avoid an import cycle.
type EventFunc func(eventType string, payload any)

// Settings keys read from the global store to tune the scheduler at runtime.
const (
	settingWIPCap = "wip_cap"     // global max concurrently-running tasks (0 = unlimited)
	settingRoute  = "route_tier_" // + tier -> agentID: per-risk-tier routing override
)

// runInfo records what an in-flight task is doing, for slot accounting and
// TouchPaths collision avoidance. Held in Engine.running under mu.
type runInfo struct {
	agentID    string
	touchPaths []string
}

// Engine coordinates dispatch, verification, and merge.
type Engine struct {
	store    *store.Store
	cfg      *config.Config
	repoRoot string
	gate     gate.Runner
	agent    agent.Runner
	emit     EventFunc

	ctx     context.Context
	wake    chan struct{}
	mu      sync.Mutex         // guards running + serializes git worktree/state writes
	running map[string]runInfo // taskID -> in-flight info
	wg      sync.WaitGroup     // tracks dispatched goroutines
}

// New constructs an Engine rooted at repoRoot (the target repo). emit may be nil.
func New(s *store.Store, cfg *config.Config, repoRoot string, emit EventFunc) *Engine {
	if emit == nil {
		emit = func(string, any) {}
	}
	return &Engine{
		store:    s,
		cfg:      cfg,
		repoRoot: repoRoot,
		gate:     gate.New(),
		agent:    agent.NewSubprocess(),
		emit:     emit,
		wake:     make(chan struct{}, 1),
		running:  map[string]runInfo{},
	}
}

// Start launches the dispatch loop until ctx is cancelled.
func (e *Engine) Start(ctx context.Context) {
	e.ctx = ctx
	go e.loop()
}

// Wake nudges the loop to re-scan for ready work (called after a task is created).
func (e *Engine) Wake() {
	select {
	case e.wake <- struct{}{}:
	default: // a wake is already pending
	}
}

func (e *Engine) loop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		e.dispatchReady()
		select {
		case <-e.ctx.Done():
			return
		case <-e.wake:
		case <-ticker.C:
		}
	}
}

// dispatchReady launches every currently-dispatchable task, each in its own
// goroutine, until the scheduler can place no more (slots full, WIP cap reached,
// dependencies unmet, or collisions). Each goroutine frees its slot and re-wakes
// the loop on completion so newly unblocked work flows immediately.
func (e *Engine) dispatchReady() {
	for {
		if e.ctx.Err() != nil {
			return
		}
		task, ag, base, ok := e.claim()
		if !ok {
			return
		}
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			e.run(task, ag, base)
			e.markDone(task.ID)
			e.Wake()
		}()
	}
}

// dispatchOnce claims one task and runs it to completion synchronously. Retained
// for tests and as the single-flight building block; the live loop uses
// dispatchReady for parallelism. Returns false when nothing could be dispatched.
func (e *Engine) dispatchOnce() bool {
	task, ag, base, ok := e.claim()
	if !ok {
		return false
	}
	e.run(task, ag, base)
	e.markDone(task.ID)
	return true
}

// markDone releases a finished task's slot so the scheduler can place more work.
func (e *Engine) markDone(taskID string) {
	e.mu.Lock()
	delete(e.running, taskID)
	e.mu.Unlock()
}

// claim selects and marks one ready task running under the lock, returning the
// work to do. It enforces the Phase 1 scheduling rules: per-agent free slots, a
// global WIP cap, TouchPaths collision avoidance, and DependsOn gating. The slow
// agent/gate work happens outside the lock (see run).
func (e *Engine) claim() (model.Task, model.Agent, string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	tasks, err := e.store.Tasks.List()
	if err != nil {
		log.Printf("engine: list tasks: %v", err)
		return model.Task{}, model.Agent{}, "", false
	}
	agents, err := e.store.Agents.List()
	if err != nil {
		log.Printf("engine: list agents: %v", err)
		return model.Task{}, model.Agent{}, "", false
	}

	// Global WIP cap: stop dispatching once the configured ceiling is reached.
	if wip := e.wipCap(); wip > 0 && len(e.running) >= wip {
		return model.Task{}, model.Agent{}, "", false
	}

	// Free slots per agent = Concurrency minus tasks it's currently running.
	free := map[string]int{}
	for i := range agents {
		free[agents[i].ID] = agents[i].Concurrency
	}
	for _, ri := range e.running {
		free[ri.agentID]--
	}

	byID := map[string]model.Task{}
	for _, t := range tasks {
		byID[t.ID] = t
	}
	tierRoutes := e.tierRoutes()

	// List is newest-first; iterate oldest-first so tasks run FIFO.
	for i := len(tasks) - 1; i >= 0; i-- {
		t := tasks[i]
		if t.Status != model.TaskReady {
			continue
		}
		if !depsSatisfied(t, byID) {
			continue // a prerequisite hasn't merged yet
		}
		if e.collides(t.TouchPaths) {
			continue // would write paths a running task is already touching
		}

		// Apply per-risk-tier routing as an effective pin when the task isn't
		// already pinned, then route honoring live free slots.
		routed := t
		if routed.PreferredAgentID == "" {
			if a := tierRoutes[t.RiskTier]; a != "" {
				routed.PreferredAgentID = a
			}
		}
		ag := agent.Route(routed, agents, free)
		if ag == nil {
			continue // no eligible agent with a free slot; try the next task
		}

		repo, err := git.Open(e.ctx, e.repoRoot)
		if err != nil {
			log.Printf("engine: open repo: %v", err)
			return model.Task{}, model.Agent{}, "", false
		}
		base, err := repo.CurrentBranch(e.ctx)
		if err != nil {
			log.Printf("engine: current branch: %v", err)
			return model.Task{}, model.Agent{}, "", false
		}

		branch := "fabrika/task-" + shortID(t.ID)
		wt := e.worktreePath(t.ID)
		// Defensive cleanup of any stale worktree from a previous crashed run.
		_ = repo.RemoveWorktree(e.ctx, wt)
		_ = os.RemoveAll(wt)
		if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
			log.Printf("engine: mkdir worktrees: %v", err)
			return model.Task{}, model.Agent{}, "", false
		}
		if err := repo.AddWorktree(e.ctx, wt, branch, base); err != nil {
			log.Printf("engine: add worktree for %s: %v", t.ID, err)
			e.setStatus(t.ID, model.TaskFailed)
			return model.Task{}, model.Agent{}, "", false
		}

		if err := e.store.Tasks.SetRun(t.ID, ag.ID, branch, model.TaskRunning); err != nil {
			log.Printf("engine: set run: %v", err)
			return model.Task{}, model.Agent{}, "", false
		}
		t.AgentID, t.Branch, t.Status = ag.ID, branch, model.TaskRunning
		e.running[t.ID] = runInfo{agentID: ag.ID, touchPaths: t.TouchPaths}
		e.emitTask(t.ID)
		log.Printf("engine: dispatch task %q -> agent %q on %s", t.Title, ag.Name, branch)
		return t, *ag, base, true
	}
	return model.Task{}, model.Agent{}, "", false
}

// wipCap reads the global work-in-progress ceiling from settings (0/unset =
// unlimited). A malformed value is treated as unlimited.
func (e *Engine) wipCap() int {
	v, err := e.store.Settings.Get(settingWIPCap)
	if err != nil || v == "" {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// tierRoutes returns the per-risk-tier agent overrides (risk tier -> agentID).
func (e *Engine) tierRoutes() map[string]string {
	out := map[string]string{}
	for _, tier := range []string{model.RiskLow, model.RiskMedium, model.RiskHigh} {
		if a, _ := e.store.Settings.Get(settingRoute + tier); a != "" {
			out[tier] = a
		}
	}
	return out
}

// collides reports whether paths overlap any currently-running task's
// TouchPaths. Overlap means equality or directory containment either way. A task
// that declares no paths never collides (we can't reason about it, so we let it
// run — Phase 1 best-effort, as TouchPaths is the declared collision surface).
func (e *Engine) collides(paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	for _, ri := range e.running {
		if pathsOverlap(paths, ri.touchPaths) {
			return true
		}
	}
	return false
}

// depsSatisfied reports whether every task this one DependsOn has merged.
func depsSatisfied(t model.Task, byID map[string]model.Task) bool {
	for _, dep := range t.DependsOn {
		d, ok := byID[dep]
		if !ok || d.Status != model.TaskMerged {
			return false
		}
	}
	return true
}

// pathsOverlap reports whether any path in a equals or contains (or is contained
// by) any path in b, treating entries as path prefixes.
func pathsOverlap(a, b []string) bool {
	for _, x := range a {
		x = strings.TrimSuffix(x, "/")
		for _, y := range b {
			y = strings.TrimSuffix(y, "/")
			if x == y || strings.HasPrefix(x, y+"/") || strings.HasPrefix(y, x+"/") {
				return true
			}
		}
	}
	return false
}

// run executes the agent then the gate (both unlocked, as they are slow), and
// records the attempt + resulting status.
func (e *Engine) run(task model.Task, ag model.Agent, base string) {
	wt := e.worktreePath(task.ID)

	// Render the prompt to a temp file the agent command can read.
	conventions, _ := e.store.Conventions.List()
	promptFile, cleanup, err := writeTempPrompt(agent.RenderPrompt(task, conventions))
	if err != nil {
		log.Printf("engine: write prompt: %v", err)
		e.finish(task, ag, model.Evidence{}, model.ResultFail, "write prompt: "+err.Error(), model.TaskFailed)
		return
	}
	defer cleanup()

	agentRes, err := e.agent.Run(e.ctx, ag, task, wt, promptFile)
	if err != nil {
		log.Printf("engine: agent run %q: %v", ag.Name, err)
		e.finish(task, ag, model.Evidence{}, model.ResultFail, "agent error: "+err.Error(), model.TaskFailed)
		return
	}

	logText := combineLog(agentRes.Stdout, agentRes.Stderr)

	// Agent escalated a question it couldn't resolve -> block (no decision queue
	// until Phase 2; surface the question in the attempt log).
	if agentRes.Escalated {
		e.finish(task, ag, model.Evidence{}, model.ResultEscalated,
			"DECISION: "+agentRes.Decision+"\n\n"+logText, model.TaskBlocked)
		return
	}

	// Capture whatever the agent produced and compute the branch diff.
	var diff string
	e.mu.Lock()
	if repo, rerr := git.Open(e.ctx, e.repoRoot); rerr == nil {
		if _, cerr := repo.AddAllAndCommit(e.ctx, wt, "fabrika: "+task.Title); cerr != nil {
			log.Printf("engine: auto-commit: %v", cerr)
		}
		if d, derr := repo.Diff(e.ctx, base, task.Branch); derr == nil {
			diff = d
		}
	}
	e.setStatus(task.ID, model.TaskVerifying)
	e.mu.Unlock()
	e.emitTask(task.ID)

	// Verification gate (slow; unlocked).
	ev, err := e.gate.Run(e.ctx, wt, e.cfg.Verbs, task.Acceptance.VerifyCmds)
	if err != nil {
		log.Printf("engine: gate: %v", err)
	}
	ev.Diff = diff

	result, status := model.ResultPass, model.TaskReview
	if !gatePassed(ev) {
		result, status = model.ResultFail, model.TaskFailed
	}
	e.finish(task, ag, ev, result, logText, status)
	log.Printf("engine: task %q -> %s (%s)", task.Title, status, result)
}

// finish persists the attempt and sets the terminal-for-now status, emitting an
// update. Holds the lock for the DB writes.
func (e *Engine) finish(task model.Task, ag model.Agent, ev model.Evidence, result, logText, status string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	att := &model.Attempt{
		TaskID:   task.ID,
		AgentID:  ag.ID,
		Result:   result,
		Evidence: ev,
		Log:      logText,
	}
	if err := e.store.Attempts.Create(att); err != nil {
		log.Printf("engine: create attempt: %v", err)
	}
	e.setStatus(task.ID, status)
	e.emitTask(task.ID)
}

// Accept merges a reviewed task's branch into the base branch and marks it
// merged. Only valid for tasks in review (green). On merge conflict it returns
// an error (decision-based conflict resolution is Phase 2).
func (e *Engine) Accept(taskID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	t, err := e.store.Tasks.Get(taskID)
	if err != nil {
		return err
	}
	if t.Status != model.TaskReview {
		return fmt.Errorf("task is %s, not awaiting accept", t.Status)
	}
	repo, err := git.Open(e.ctx, e.repoRoot)
	if err != nil {
		return err
	}
	base, err := repo.CurrentBranch(e.ctx)
	if err != nil {
		return err
	}
	if err := repo.Merge(e.ctx, base, t.Branch); err != nil {
		return fmt.Errorf("merge failed: %w", err)
	}
	_ = repo.RemoveWorktree(e.ctx, e.worktreePath(taskID))
	e.setStatus(taskID, model.TaskMerged)
	e.emitTask(taskID)
	log.Printf("engine: merged task %q (%s -> %s)", t.Title, t.Branch, base)
	return nil
}

// Reject dismisses a surfaced task (review/failed/blocked) without merging,
// cleaning up its worktree. Terminal for this phase; rework/retry arrives with
// decisions (Phase 2).
func (e *Engine) Reject(taskID, reason string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	t, err := e.store.Tasks.Get(taskID)
	if err != nil {
		return err
	}
	if repo, rerr := git.Open(e.ctx, e.repoRoot); rerr == nil {
		_ = repo.RemoveWorktree(e.ctx, e.worktreePath(taskID))
	}
	if reason != "" {
		_ = e.store.Attempts.Create(&model.Attempt{
			TaskID: taskID, AgentID: t.AgentID, Result: model.ResultFail,
			Log: "REJECTED: " + reason,
		})
	}
	e.setStatus(taskID, model.TaskClosed)
	e.emitTask(taskID)
	return nil
}

// --- helpers ---

func (e *Engine) worktreePath(taskID string) string {
	return filepath.Join(e.repoRoot, ".fabrika", "worktrees", taskID)
}

func (e *Engine) setStatus(id, status string) {
	if err := e.store.Tasks.UpdateStatus(id, status); err != nil {
		log.Printf("engine: set status %s=%s: %v", id, status, err)
	}
}

// emitTask broadcasts the current task state so the UI live-updates.
func (e *Engine) emitTask(id string) {
	t, err := e.store.Tasks.Get(id)
	if err != nil {
		return
	}
	e.emit("task.updated", *t)
}

// gatePassed reports whether every non-skipped stage passed. A task with no
// runnable stages passes vacuously.
func gatePassed(ev model.Evidence) bool {
	for _, s := range ev.Stages {
		if !s.Skipped && !s.Pass {
			return false
		}
	}
	return true
}

func shortID(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}

func combineLog(stdout, stderr string) string {
	var b strings.Builder
	if stdout != "" {
		b.WriteString(stdout)
	}
	if stderr != "" {
		if b.Len() > 0 {
			b.WriteString("\n--- stderr ---\n")
		}
		b.WriteString(stderr)
	}
	return b.String()
}

func writeTempPrompt(content string) (path string, cleanup func(), err error) {
	f, err := os.CreateTemp("", "fabrika-prompt-*.md")
	if err != nil {
		return "", func() {}, err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", func() {}, err
	}
	f.Close()
	return f.Name(), func() { os.Remove(f.Name()) }, nil
}
