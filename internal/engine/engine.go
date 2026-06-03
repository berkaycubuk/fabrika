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
	"math/rand"
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
	settingWIPCap   = "wip_cap"          // global max concurrently-running tasks (0 = unlimited)
	settingRoute    = "route_tier_"      // + tier -> agentID: per-risk-tier routing override
	settingAuditPct = "audit_rate"       // 0..1: share of auto-merged PRs sampled for human audit
	settingMutation = "mutation_testing" // "on" enables the mutation-testing gate validator
)

// runInfo records what an in-flight task is doing, for slot accounting and
// TouchPaths collision avoidance. Held in Engine.running under mu. cancel stops
// the task's subprocess for in-flight steering; cancelReason carries the human's
// note so the run goroutine can finalize the task as closed (not failed).
type runInfo struct {
	agentID      string
	touchPaths   []string
	cancel       context.CancelFunc
	cancelReason string
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

	// planning tracks agents currently busy planning a big task (agentID ->
	// active runs). Planning happens outside the task-dispatch loop, so without
	// this the planner agent would read as idle in the metrics while it works.
	planMu   sync.Mutex
	planning map[string]int

	// sample decides, per auto-merge, whether to flag a PR for post-merge audit.
	// Overridable in tests for determinism; defaults to a rate-based RNG.
	sample func(rate float64) bool
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
		planning: map[string]int{},
		sample: func(rate float64) bool {
			if rate <= 0 {
				return false
			}
			if rate >= 1 {
				return true
			}
			return rand.Float64() < rate
		},
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
		task, ag, base, taskCtx, ok := e.claim()
		if !ok {
			return
		}
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			e.run(taskCtx, task, ag, base)
			e.markDone(task.ID)
			e.Wake()
		}()
	}
}

// dispatchOnce claims one task and runs it to completion synchronously. Retained
// for tests and as the single-flight building block; the live loop uses
// dispatchReady for parallelism. Returns false when nothing could be dispatched.
func (e *Engine) dispatchOnce() bool {
	task, ag, base, taskCtx, ok := e.claim()
	if !ok {
		return false
	}
	e.run(taskCtx, task, ag, base)
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
func (e *Engine) claim() (model.Task, model.Agent, string, context.Context, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	tasks, err := e.store.Tasks.List()
	if err != nil {
		log.Printf("engine: list tasks: %v", err)
		return model.Task{}, model.Agent{}, "", nil, false
	}
	agents, err := e.store.Agents.List()
	if err != nil {
		log.Printf("engine: list agents: %v", err)
		return model.Task{}, model.Agent{}, "", nil, false
	}

	// Global WIP cap: stop dispatching once the configured ceiling is reached.
	if wip := e.wipCap(); wip > 0 && len(e.running) >= wip {
		return model.Task{}, model.Agent{}, "", nil, false
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
			return model.Task{}, model.Agent{}, "", nil, false
		}
		base, err := repo.CurrentBranch(e.ctx)
		if err != nil {
			log.Printf("engine: current branch: %v", err)
			return model.Task{}, model.Agent{}, "", nil, false
		}

		branch := "fabrika/task-" + shortID(t.ID)
		wt := e.worktreePath(t.ID)
		// Defensive cleanup of any stale worktree/branch from a previous crashed
		// run or a resumed task (so AddWorktree's -b doesn't hit an existing branch).
		_ = repo.RemoveWorktree(e.ctx, wt)
		_ = os.RemoveAll(wt)
		_ = repo.DeleteBranch(e.ctx, branch)
		if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
			log.Printf("engine: mkdir worktrees: %v", err)
			return model.Task{}, model.Agent{}, "", nil, false
		}
		if err := repo.AddWorktree(e.ctx, wt, branch, base); err != nil {
			log.Printf("engine: add worktree for %s: %v", t.ID, err)
			e.setStatus(t.ID, model.TaskFailed)
			return model.Task{}, model.Agent{}, "", nil, false
		}

		if err := e.store.Tasks.SetRun(t.ID, ag.ID, branch, model.TaskRunning); err != nil {
			log.Printf("engine: set run: %v", err)
			return model.Task{}, model.Agent{}, "", nil, false
		}
		t.AgentID, t.Branch, t.Status = ag.ID, branch, model.TaskRunning
		// Per-task context so in-flight steering can cancel this run's subprocess
		// without disturbing the rest of the pool.
		taskCtx, cancel := context.WithCancel(e.ctx)
		e.running[t.ID] = runInfo{agentID: ag.ID, touchPaths: t.TouchPaths, cancel: cancel}
		e.emitTask(t.ID)
		log.Printf("engine: dispatch task %q -> agent %q on %s", t.Title, ag.Name, branch)
		return t, *ag, base, taskCtx, true
	}
	return model.Task{}, model.Agent{}, "", nil, false
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

// attachmentPaths maps stored upload URLs (/api/uploads/<name>) to the absolute
// files under <repoRoot>/.fabrika/uploads so agent prompts can reference images
// on disk. Malformed entries are skipped (creation already validated them).
func (e *Engine) attachmentPaths(urls []string) []string {
	var out []string
	for _, u := range urls {
		name, ok := strings.CutPrefix(u, "/api/uploads/")
		if !ok || name == "" || strings.ContainsAny(name, `/\`) {
			continue
		}
		out = append(out, filepath.Join(e.repoRoot, ".fabrika", "uploads", name))
	}
	return out
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
// records the attempt + resulting status. ctx is the task's own context: it is
// cancelled when a human steers the task mid-flight, killing the subprocess.
func (e *Engine) run(ctx context.Context, task model.Task, ag model.Agent, base string) {
	wt := e.worktreePath(task.ID)

	// Render the prompt to a temp file the agent command can read.
	conventions, _ := e.store.Conventions.List()
	promptFile, cleanup, err := writeTempPrompt(agent.RenderPrompt(task, conventions, e.attachmentPaths(task.Attachments)))
	if err != nil {
		log.Printf("engine: write prompt: %v", err)
		e.finish(task, ag, model.Evidence{}, model.ResultFail, "write prompt: "+err.Error(), model.TaskFailed, model.Usage{})
		return
	}
	defer cleanup()

	agentRes, err := e.agent.Run(ctx, ag, task, wt, promptFile)
	// A cancelled context means the human stopped this task in flight (steer).
	// Finalize as closed with their reason rather than treating it as a failure.
	if reason, cancelled := e.cancellation(task.ID); cancelled {
		e.finalizeCancelled(task, ag, reason)
		return
	}
	if err != nil {
		log.Printf("engine: agent run %q: %v", ag.Name, err)
		e.finish(task, ag, model.Evidence{}, model.ResultFail, "agent error: "+err.Error(), model.TaskFailed, model.Usage{})
		return
	}

	// Persist agent-authored comments so they survive the run regardless of outcome.
	for _, text := range agentRes.Comments {
		c := &model.Comment{TaskID: task.ID, AuthorType: "agent", AuthorID: ag.ID, Body: text}
		if err := e.store.Comments.Create(c); err != nil {
			log.Printf("engine: create comment: %v", err)
		} else {
			e.emit("task.comment.added", c)
		}
	}

	// Copy evidence artifacts (fabrika_EVIDENCE: lines) out of the worktree while
	// it still exists, and surface them in the task thread as one agent comment.
	artifactURLs, captions := e.ingestEvidence(wt, agentRes.Evidence)
	if len(artifactURLs) > 0 {
		c := &model.Comment{
			TaskID: task.ID, AuthorType: "agent", AuthorID: ag.ID,
			Body: evidenceCommentBody(artifactURLs, captions), Attachments: artifactURLs,
		}
		if err := e.store.Comments.Create(c); err != nil {
			log.Printf("engine: create evidence comment: %v", err)
		} else {
			e.emit("task.comment.added", c)
		}
	}

	logText := combineLog(agentRes.Stdout, agentRes.Stderr)

	// Agent escalated a question it couldn't resolve -> record a Decision for the
	// queue and block the task. Answering it (AnswerDecision) resumes the task.
	if agentRes.Escalated {
		e.recordEscalation(task, agentRes.Decision)
		e.finish(task, ag, model.Evidence{Artifacts: artifactURLs}, model.ResultEscalated,
			"DECISION: "+agentRes.Decision+"\n\n"+logText, model.TaskBlocked, agentRes.Usage)
		return
	}

	// Capture whatever the agent produced and compute the branch diff + the set
	// of files it changed (for locked-glob enforcement).
	var diff string
	var changed []string
	e.mu.Lock()
	if repo, rerr := git.Open(e.ctx, e.repoRoot); rerr == nil {
		if _, cerr := repo.AddAllAndCommit(e.ctx, wt, "fabrika: "+task.Title); cerr != nil {
			log.Printf("engine: auto-commit: %v", cerr)
		}
		// Rewrite the branch's commits so each carries the fabrika co-author and
		// no foreign attribution before the diff/gate verify and any merge.
		if nerr := repo.NormalizeCommitTrailers(e.ctx, base, task.Branch); nerr != nil {
			log.Printf("engine: normalize trailers: %v", nerr)
		}
		if d, derr := repo.Diff(e.ctx, base, task.Branch); derr == nil {
			diff = d
		}
		if files, ferr := repo.ChangedFiles(e.ctx, base, task.Branch); ferr == nil {
			changed = files
		}
	}
	e.setStatus(task.ID, model.TaskVerifying)
	e.mu.Unlock()
	e.emitTask(task.ID)

	// Integrity: the implementer may not touch locked test files. A violation
	// fails the task before the gate even runs — its acceptance can't be trusted.
	if viol := lockedViolations(changed, task.Acceptance.LockedGlobs); len(viol) > 0 {
		ev := model.Evidence{Stages: map[string]model.StageResult{
			"locked": {Pass: false, Output: "branch edits protected files: " + strings.Join(viol, ", ")},
		}, Diff: diff, Artifacts: artifactURLs}
		e.finish(task, ag, ev, model.ResultFail,
			"LOCKED GLOB VIOLATION: "+strings.Join(viol, ", ")+"\n\n"+logText, model.TaskFailed, agentRes.Usage)
		log.Printf("engine: task %q failed: locked globs touched (%v)", task.Title, viol)
		return
	}

	// Verification gate (slow; unlocked). Held-out checks run here too — they are
	// never shown to the implementer (RenderPrompt omits them) but must pass.
	gateCmds := append(append([]string{}, task.Acceptance.VerifyCmds...), task.Acceptance.HeldOut...)
	ev, err := e.gate.Run(ctx, wt, e.cfg.Verbs, gateCmds)
	if err != nil {
		log.Printf("engine: gate: %v", err)
	}
	ev.Diff = diff
	ev.Artifacts = artifactURLs

	// A red gate fails the task outright. The green path runs the Phase 3 trust
	// checks (reviewer + mutation testing) and decides auto-merge vs human review.
	if !gatePassed(ev) {
		e.finish(task, ag, ev, model.ResultFail, logText, model.TaskFailed, agentRes.Usage)
		log.Printf("engine: task %q -> failed (gate red)", task.Title)
		return
	}
	e.finishGreen(ctx, task, ag, ev, logText, base, changed, conventions, agentRes.Usage)
}

// finishGreen handles a task whose gate passed: it runs the optional reviewer
// agent and mutation-testing validator (recording each as an advisory evidence
// stage), then either auto-merges low-risk, reviewer-approved work or surfaces it
// to the human Accept queue. A random sample of auto-merges is flagged for a
// post-merge audit so trust can be calibrated without re-introducing a human in
// the common path (SPECS.md §9, §13 Phase 3).
func (e *Engine) finishGreen(ctx context.Context, task model.Task, ag model.Agent, ev model.Evidence, logText, base string, changed []string, conventions []model.Convention, usage model.Usage) {
	wt := e.worktreePath(task.ID)
	if ev.Stages == nil {
		ev.Stages = map[string]model.StageResult{}
	}

	// First-pass review by a reviewer-role agent, if one is configured. A missing
	// or non-approving verdict blocks auto-merge (work falls back to the human).
	reviewApproved := true
	if rev, ok := e.ReviewerAgent(); ok && rev.ID != ag.ID {
		verdict, notes := e.runReviewer(ctx, rev, task, ev.Diff, conventions)
		reviewApproved = verdict
		ev.Stages["review"] = model.StageResult{Pass: verdict, Output: notes}
	}

	// Mutation testing (opt-in): perturb changed source and confirm the suite
	// catches it. Survivors mean the tests are too weak to trust autonomously.
	mutationOK := true
	if e.mutationEnabled() && e.cfg != nil && e.cfg.Verbs.Test != "" {
		res := e.runMutation(ctx, wt, changed, task.Acceptance.LockedGlobs)
		mutationOK = res.Pass()
		ev.Stages["mutation"] = model.StageResult{Pass: mutationOK, Output: mutationSummary(res)}
	}

	tier := e.effectiveTier(task, changed)
	eligible := e.cfg != nil && e.cfg.AutoMerges(tier) && reviewApproved && mutationOK

	if !eligible {
		e.finish(task, ag, ev, model.ResultPass, logText, model.TaskReview, usage)
		log.Printf("engine: task %q -> review (tier=%s autoMerge=%v review=%v mutation=%v)",
			task.Title, tier, e.cfg != nil && e.cfg.AutoMerges(tier), reviewApproved, mutationOK)
		return
	}

	// Auto-merge: record the attempt, merge the branch, mark it machine-merged.
	// A sampled fraction is flagged for post-merge audit (merge still proceeds).
	e.mu.Lock()
	defer e.mu.Unlock()
	e.recordAttempt(task, ag, ev, model.ResultPass, logText, usage)

	repo, err := git.Open(e.ctx, e.repoRoot)
	if err != nil {
		e.setStatus(task.ID, model.TaskReview)
		e.emitTask(task.ID)
		log.Printf("engine: auto-merge open repo: %v (-> review)", err)
		return
	}
	if err := repo.Merge(e.ctx, base, task.Branch); err != nil {
		// Conflict or merge error: don't fail the work — hand it to the human.
		e.setStatus(task.ID, model.TaskReview)
		e.emitTask(task.ID)
		log.Printf("engine: auto-merge %q conflict: %v (-> review)", task.Title, err)
		return
	}
	_ = repo.RemoveWorktree(e.ctx, wt)

	audit := e.sample(e.auditRate())
	if err := e.store.Tasks.MarkMerged(task.ID, true, audit); err != nil {
		log.Printf("engine: mark auto-merged: %v", err)
	}
	e.emitTask(task.ID)
	log.Printf("engine: task %q -> auto-merged (tier=%s, audit=%v)", task.Title, tier, audit)
}

// finish persists the attempt and sets the terminal-for-now status, emitting an
// update. Holds the lock for the DB writes.
func (e *Engine) finish(task model.Task, ag model.Agent, ev model.Evidence, result, logText, status string, usage model.Usage) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.recordAttempt(task, ag, ev, result, logText, usage)
	e.setStatus(task.ID, status)
	e.emitTask(task.ID)
}

// recordAttempt persists one attempt row. The caller must hold e.mu.
func (e *Engine) recordAttempt(task model.Task, ag model.Agent, ev model.Evidence, result, logText string, usage model.Usage) {
	att := &model.Attempt{
		TaskID:   task.ID,
		AgentID:  ag.ID,
		Result:   result,
		Evidence: ev,
		Usage:    usage,
		Log:      logText,
	}
	if err := e.store.Attempts.Create(att); err != nil {
		log.Printf("engine: create attempt: %v", err)
	}
}

// cancellation reports whether the task was steered (cancelled) mid-flight and
// the human's reason, reading the in-flight record under the lock.
func (e *Engine) cancellation(taskID string) (string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ri, ok := e.running[taskID]
	if !ok || ri.cancel == nil {
		return "", false
	}
	// cancelReason is only set by Reject when it cancels an in-flight task.
	if ri.cancelReason == "" {
		// Distinguish a steer-cancel from engine shutdown: only steer sets a reason.
		// No reason -> not a deliberate per-task cancel.
		return "", false
	}
	return ri.cancelReason, true
}

// finalizeCancelled records a steered (stopped) in-flight task as closed and
// cleans up its worktree, mirroring Reject's terminal handling.
func (e *Engine) finalizeCancelled(task model.Task, ag model.Agent, reason string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if repo, rerr := git.Open(e.ctx, e.repoRoot); rerr == nil {
		_ = repo.RemoveWorktree(e.ctx, e.worktreePath(task.ID))
	}
	note := "STOPPED in flight"
	if reason != "" {
		note += ": " + reason
	}
	e.recordAttempt(task, ag, model.Evidence{}, model.ResultFail, note, model.Usage{})
	e.setStatus(task.ID, model.TaskClosed)
	e.emitTask(task.ID)
	log.Printf("engine: task %q stopped in flight", task.Title)
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

// Push ships the integration branch — the current branch that accepted tasks
// merge into — to its remote, so the human can publish the work agents have
// accumulated locally. The branch is pushed by name, so it is safe to run
// alongside the dispatch loop without holding e.mu (an in-flight Accept's
// checkout cannot change which ref gets pushed). It targets "origin" when
// present, else the sole remote, and errors when there is none or the choice is
// ambiguous. Returns git's push summary for the UI to surface.
func (e *Engine) Push(ctx context.Context) (string, error) {
	repo, err := git.Open(ctx, e.repoRoot)
	if err != nil {
		return "", err
	}
	branch, err := repo.CurrentBranch(ctx)
	if err != nil {
		return "", err
	}
	remotes, err := repo.Remotes(ctx)
	if err != nil {
		return "", err
	}
	remote, err := pickRemote(remotes)
	if err != nil {
		return "", err
	}
	summary, err := repo.Push(ctx, remote, branch)
	if err != nil {
		return "", err
	}
	log.Printf("engine: pushed %s to %s", branch, remote)
	return summary, nil
}

// PushState describes whether the integration branch has work waiting to be
// pushed. The UI uses it to decide whether to offer the Push action at all.
type PushState struct {
	CanPush bool   `json:"canPush"`
	Ahead   int    `json:"ahead"`
	Branch  string `json:"branch"`
	Remote  string `json:"remote"`
}

// PushStatus reports how far the current branch is ahead of its remote (per the
// local remote-tracking ref — no network round-trip). When no usable remote is
// configured it returns a zero PushState rather than an error: there is nowhere
// to push, so the UI simply doesn't offer the action.
func (e *Engine) PushStatus(ctx context.Context) (PushState, error) {
	repo, err := git.Open(ctx, e.repoRoot)
	if err != nil {
		return PushState{}, err
	}
	branch, err := repo.CurrentBranch(ctx)
	if err != nil {
		return PushState{}, err
	}
	remotes, err := repo.Remotes(ctx)
	if err != nil {
		return PushState{}, err
	}
	remote, err := pickRemote(remotes)
	if err != nil {
		return PushState{}, nil
	}
	ahead, err := repo.Ahead(ctx, remote, branch)
	if err != nil {
		return PushState{}, err
	}
	return PushState{CanPush: ahead > 0, Ahead: ahead, Branch: branch, Remote: remote}, nil
}

// pickRemote chooses which remote to push to: the only one if there is a single
// remote, "origin" when several are configured, otherwise an error.
func pickRemote(remotes []string) (string, error) {
	switch len(remotes) {
	case 0:
		return "", fmt.Errorf("no git remote configured")
	case 1:
		return remotes[0], nil
	}
	for _, r := range remotes {
		if r == "origin" {
			return "origin", nil
		}
	}
	return "", fmt.Errorf("multiple remotes (%s); none named origin", strings.Join(remotes, ", "))
}

// Reject dismisses a task without merging, cleaning up its worktree. For an
// in-flight (running) task it steers it to a stop: it cancels the task's context
// to kill the subprocess and lets that run's goroutine finalize it as closed with
// the reason. For a surfaced task (review/failed/blocked) it closes it directly.
func (e *Engine) Reject(taskID, reason string) error {
	e.mu.Lock()

	t, err := e.store.Tasks.Get(taskID)
	if err != nil {
		e.mu.Unlock()
		return err
	}

	// In-flight: signal the running goroutine to stop and finalize. Done under the
	// lock so the reason is visible to cancellation() before the cancel lands.
	if ri, ok := e.running[taskID]; ok && ri.cancel != nil {
		if reason == "" {
			reason = "stopped by steer"
		}
		ri.cancelReason = reason
		e.running[taskID] = ri
		cancel := ri.cancel
		e.mu.Unlock()
		cancel()
		return nil
	}
	defer e.mu.Unlock()

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

// Retry re-queues a stuck task for a fresh run from scratch. Valid for terminal
// non-merged states (failed, blocked, closed); an in-flight task must be stopped
// first. It drops any stale worktree/branch and resets the task to ready, then
// wakes the dispatch loop so claim() rebuilds a clean worktree and re-routes it.
// Prior attempts are kept as history.
func (e *Engine) Retry(taskID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, ok := e.running[taskID]; ok {
		return fmt.Errorf("task is running; stop it before retrying")
	}
	t, err := e.store.Tasks.Get(taskID)
	if err != nil {
		return err
	}
	switch t.Status {
	case model.TaskFailed, model.TaskBlocked, model.TaskClosed:
		// retryable
	default:
		return fmt.Errorf("task is %s, not retryable", t.Status)
	}

	// Drop any stale worktree/branch so the next claim starts clean. claim() also
	// does this defensively, but clearing it here makes the UI reflect it at once.
	if repo, rerr := git.Open(e.ctx, e.repoRoot); rerr == nil {
		wt := e.worktreePath(taskID)
		_ = repo.RemoveWorktree(e.ctx, wt)
		_ = os.RemoveAll(wt)
		if t.Branch != "" {
			_ = repo.DeleteBranch(e.ctx, t.Branch)
		}
	}

	if err := e.store.Tasks.UpdateStatus(taskID, model.TaskReady); err != nil {
		return err
	}
	e.emitTask(taskID)
	e.Wake()
	log.Printf("engine: task %q re-queued (retry)", t.Title)
	return nil
}

// AckAudit acknowledges a sampled post-merge audit as acceptable, removing it
// from the audit queue (SPECS.md §13 Phase 3).
func (e *Engine) AckAudit(taskID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.store.Tasks.ClearAuditFlag(taskID); err != nil {
		return err
	}
	e.emitTask(taskID)
	return nil
}

// Revert records a merged task as a change-failure (it feeds change-failure-rate)
// and clears any audit flag. The git revert itself stays with the human — Fabrika
// won't rewrite main on its own.
func (e *Engine) Revert(taskID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, err := e.store.Tasks.Get(taskID)
	if err != nil {
		return err
	}
	if t.Status != model.TaskMerged {
		return fmt.Errorf("task is %s, not merged", t.Status)
	}
	if err := e.store.Tasks.SetReverted(taskID); err != nil {
		return err
	}
	e.emitTask(taskID)
	log.Printf("engine: task %q marked reverted (change-failure)", t.Title)
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
