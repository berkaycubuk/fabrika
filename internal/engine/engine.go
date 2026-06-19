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
	cimgr "github.com/berkaycubuk/fabrika/internal/ci"
	"github.com/berkaycubuk/fabrika/internal/config"
	"github.com/berkaycubuk/fabrika/internal/gate"
	"github.com/berkaycubuk/fabrika/internal/git"
	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/berkaycubuk/fabrika/internal/planner"
	releasemgr "github.com/berkaycubuk/fabrika/internal/release"
	"github.com/berkaycubuk/fabrika/internal/store"
)

// EventFunc emits a UI event (the api layer adapts this to its WebSocket hub).
// Engine stays decoupled from api to avoid an import cycle.
type EventFunc func(eventType string, payload any)

// Settings keys read from the global store to tune the scheduler at runtime.
const (
	settingWIPCap              = "wip_cap"              // global max concurrently-running tasks (0 = unlimited)
	settingRoute               = "route_tier_"          // + tier -> agentID: per-risk-tier routing override
	settingAuditPct            = "audit_rate"           // 0..1: share of auto-merged PRs sampled for human audit
	settingMutation            = "mutation_testing"     // "on" enables the mutation-testing gate validator
	settingQuarantineThreshold = "quarantine_threshold" // consecutive fails before an agent is skipped
	settingIdleTimeout         = "agent_idle_timeout"   // duration of agent silence before it's killed as stalled (0/"off" disables)
	settingAutoMode            = "auto_mode"            // "on" auto-merges review-queue tasks without a human
)

// runInfo records what an in-flight task is doing, for slot accounting and
// TouchPaths collision avoidance. Held in Engine.running under mu. cancel stops
// the task's subprocess for in-flight steering; cancelReason carries the human's
// note so the run goroutine can finalize the task as closed (not failed).
type runInfo struct {
	agentID      string
	agentName    string
	touchPaths   []string
	cancel       context.CancelFunc
	cancelReason string

	// Liveness, updated from the agent runner's heartbeats (onHeartbeat). startedAt
	// anchors elapsed time; the rest mirror the latest pulse so a just-connected
	// client (or the attention API) can report whether the run is making progress.
	startedAt  time.Time
	lastBeatAt time.Time
	idleFor    time.Duration
	lastLine   string
}

// planRunInfo records an in-flight planning run for a big task. Held in
// Engine.planRuns under planMu. cancel stops this specific planner run
// without disturbing the engine or other planning runs; reason carries the
// human's stop note so the run goroutine can finalize the big task as error.
type planRunInfo struct {
	cancel context.CancelFunc
	reason string
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
	cancel  context.CancelFunc
	wake    chan struct{}
	mu      sync.Mutex         // guards running + serializes git worktree/state writes
	running map[string]runInfo // taskID -> in-flight info
	wg      sync.WaitGroup     // tracks dispatched goroutines

	// planning tracks agents currently busy planning a big task (agentID ->
	// active runs). Planning happens outside the task-dispatch loop, so without
	// this the planner agent would read as idle in the metrics while it works.
	planMu   sync.Mutex
	planning map[string]int

	// planRuns tracks in-flight planning runs keyed by big-task ID, guarded by
	// planMu. The cancel func stops this specific planner run without disturbing
	// the engine; reason carries the human's stop note.
	planRuns map[string]planRunInfo

	// Interactive chat sessions (sessions.go). sessRuns tracks the in-flight
	// turn per session (at most one), guarded by sessMu; sessStreams holds each
	// turn's stdout-so-far for the coalesced "session.stream" emits; sessAgent
	// is a dedicated runner whose heartbeats/active-run records are keyed by
	// session ID rather than task ID.
	sessMu      sync.Mutex
	sessRuns    map[string]sessionRunInfo
	sessStreams map[string]*sessionStream
	sessAgent   agent.Runner

	// askMu guards askRuns: at most one in-flight agent reply per task (mirrors
	// the sessRuns/sessMu pattern for interactive sessions).
	askMu   sync.Mutex
	askRuns map[string]struct{}

	// sample decides, per auto-merge, whether to flag a PR for post-merge audit.
	// Overridable in tests for determinism; defaults to a rate-based RNG.
	sample func(rate float64) bool

	release *releasemgr.Manager
	ci      *cimgr.Poller
}

// New constructs an Engine rooted at repoRoot (the target repo). emit may be nil.
func New(s *store.Store, cfg *config.Config, repoRoot string, emit EventFunc) *Engine {
	if emit == nil {
		emit = func(string, any) {}
	}
	var deploy config.Deploy
	if cfg != nil {
		deploy = cfg.Deploy
	}
	sp := agent.NewSubprocess()
	ssp := agent.NewSubprocess()
	e := &Engine{
		store:       s,
		cfg:         cfg,
		repoRoot:    repoRoot,
		gate:        gate.New(),
		agent:       sp,
		sessAgent:   ssp,
		emit:        emit,
		wake:        make(chan struct{}, 1),
		running:     map[string]runInfo{},
		planning:    map[string]int{},
		planRuns:    map[string]planRunInfo{},
		sessRuns:    map[string]sessionRunInfo{},
		sessStreams: map[string]*sessionStream{},
		askRuns:     map[string]struct{}{},
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
	e.release = releasemgr.NewManager(releasemgr.Deps{
		Releases: s.Releases,
		Tasks:    s.Tasks,
		Deploy:   deploy,
		RepoRoot: repoRoot,
		Cmd:      engineCommander{},
		Git:      engineGitter{repoRoot: repoRoot},
		Emit:     emit,
		Now:      time.Now,
	})
	var ciCommand string
	var ciPollSeconds int
	if cfg != nil {
		ciCommand = cfg.CI.Command
		ciPollSeconds = cfg.CI.PollSeconds
	}
	e.ci = cimgr.NewPoller(cimgr.Deps{
		Tasks:       s.Tasks,
		Cmd:         engineCommander{},
		Command:     ciCommand,
		RepoRoot:    repoRoot,
		Emit:        emit,
		PollSeconds: ciPollSeconds,
	})

	// Wire agent liveness: the runner pulses heartbeats while an agent works, and
	// kills one that's been silent past the idle timeout (DefaultIdleTimeout,
	// overridable by the global "agent_idle_timeout" setting; 0/"off" disables).
	sp.Heartbeat = e.onHeartbeat
	sp.OnStart = e.onAgentStart
	ssp.Heartbeat = e.onSessionHeartbeat
	ssp.OnStart = e.onSessionAgentStart
	ssp.OnOutput = e.onSessionOutput
	if d, ok := e.configuredIdleTimeout(); ok {
		sp.IdleTimeout = d
		ssp.IdleTimeout = d
	}
	return e
}

// configuredIdleTimeout reads the global "agent_idle_timeout" setting, if set.
// An empty/absent value leaves the runner default; "0" or "off" disables stall
// detection (returns 0); anything else is parsed as a duration. An unparseable
// value is ignored (ok=false) so a typo can't silently disable the safety net.
func (e *Engine) configuredIdleTimeout() (time.Duration, bool) {
	raw, err := e.store.Settings.Get(settingIdleTimeout)
	if err != nil || strings.TrimSpace(raw) == "" {
		return 0, false
	}
	raw = strings.TrimSpace(raw)
	if raw == "0" || strings.EqualFold(raw, "off") {
		return 0, true
	}
	d, perr := time.ParseDuration(raw)
	if perr != nil || d < 0 {
		return 0, false
	}
	return d, true
}

// Start launches the dispatch loop until ctx is cancelled.
func (e *Engine) Start(ctx context.Context) {
	e.ctx, e.cancel = context.WithCancel(ctx)
	e.reapOrphanProcesses()
	e.recoverOrphans()
	e.release.ResumeBakeTimers()
	e.ci.Start(ctx)
	go e.cronLoop(e.ctx)
	go e.loop()
}

// Stop cancels the dispatch loop and any in-flight subprocess, then waits for
// all goroutines to finish. It returns true if the WaitGroup drained within
// timeout, false on timeout. Safe to call when Start was never called (e.cancel
// is nil); in that case it still waits the timeout for any pre-existing wg work.
func (e *Engine) Stop(timeout time.Duration) bool {
	if e.cancel != nil {
		e.cancel()
	}
	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// recoverOrphans re-queues work stranded by a previous process. In-flight state
// (the running map, planning counts) lives only in memory, so after a restart a
// task can sit at claimed/running/verifying — and a big task at planning — in
// the DB with nothing driving it; the scheduler only claims `ready`/`draft`, so
// it would show as in-flight in the UI forever. Runs once from Start, before
// the dispatch loop, when the in-memory maps are necessarily empty. Stale
// worktrees/branches are dropped so the next claim rebuilds clean, same as
// Retry.
func (e *Engine) recoverOrphans() {
	tasks, err := e.store.Tasks.List()
	if err != nil {
		log.Printf("engine: recover orphans: list tasks: %v", err)
	}
	repo, rerr := git.Open(e.ctx, e.repoRoot)
	for _, t := range tasks {
		switch t.Status {
		case model.TaskClaimed, model.TaskRunning, model.TaskVerifying:
		default:
			continue
		}
		if rerr == nil {
			wt := e.worktreePath(t.ID)
			_ = repo.RemoveWorktree(e.ctx, wt)
			_ = os.RemoveAll(wt)
			if t.Branch != "" {
				_ = repo.DeleteBranch(e.ctx, t.Branch)
			}
		}
		e.setStatus(t.ID, model.TaskReady)
		e.emitTask(t.ID)
		log.Printf("engine: task %q was orphaned by a restart — re-queued", t.Title)
	}

	bts, err := e.store.BigTasks.List()
	if err != nil {
		log.Printf("engine: recover orphans: list bigtasks: %v", err)
		return
	}
	for _, bt := range bts {
		if bt.Status != model.BigTaskPlanning {
			continue
		}
		e.setBigTaskStatus(bt.ID, model.BigTaskDraft)
		log.Printf("engine: big task %q planning was orphaned by a restart — re-queued", bt.Title)
	}

	// A session caught mid-Finish (gating) by a restart lost its gate/merge
	// goroutine; its worktree survives, so reopen it for the human to retry.
	gating, err := e.store.Sessions.ListByStatus(model.SessionGating)
	if err != nil {
		log.Printf("engine: recover orphans: list sessions: %v", err)
		return
	}
	for _, s := range gating {
		e.setSessionStatus(s.ID, model.SessionActive)
		e.addSessionSystemMessage(s.ID, "Finish was interrupted by a restart — the session is open again; hit Finish to retry.")
		log.Printf("engine: session %q finish was orphaned by a restart — reopened", s.Title)
	}
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
		e.dispatchPlanning()
		e.sweepAutoMerge()
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

	tasks, err := e.store.Tasks.ListByStatus(model.TaskReady)
	if err != nil {
		log.Printf("engine: list tasks: %v", err)
		return model.Task{}, model.Agent{}, "", nil, false
	}
	agents, err := e.store.Agents.ListEnabled()
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

	if th := e.quarantineThreshold(); th > 0 {
		for i := range agents {
			recent, err := e.store.Attempts.RecentByAgent(agents[i].ID, th)
			if err != nil {
				log.Printf("engine: quarantine check for %q: %v", agents[i].Name, err)
				continue
			}
			if agent.Quarantined(recent, th) {
				log.Printf("engine: agent %q quarantined for this dispatch pass", agents[i].Name)
				free[agents[i].ID] = 0
			}
		}
	}

	depIDs := make([]string, 0, len(tasks))
	seenDep := map[string]bool{}
	for _, t := range tasks {
		for _, dep := range t.DependsOn {
			if !seenDep[dep] {
				seenDep[dep] = true
				depIDs = append(depIDs, dep)
			}
		}
	}
	statusByID, err := e.store.Tasks.StatusByIDs(depIDs)
	if err != nil {
		log.Printf("engine: dep status lookup: %v", err)
		return model.Task{}, model.Agent{}, "", nil, false
	}
	tierRoutes := e.tierRoutes()

	// List is newest-first; iterate oldest-first so tasks run FIFO.
	for i := len(tasks) - 1; i >= 0; i-- {
		t := tasks[i]
		if _, busy := e.running[t.ID]; busy {
			continue // an auto-retried run hasn't released its slot yet (markDone pending)
		}
		if !depsSatisfied(t, statusByID) {
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
		e.logTransition(t.ID, t.Status, model.TaskRunning, "engine", "claimed")
		t.AgentID, t.Branch, t.Status = ag.ID, branch, model.TaskRunning
		// Per-task context so in-flight steering can cancel this run's subprocess
		// without disturbing the rest of the pool.
		taskCtx, cancel := context.WithCancel(e.ctx)
		e.running[t.ID] = runInfo{
			agentID: ag.ID, agentName: ag.Name, touchPaths: t.TouchPaths,
			cancel: cancel, startedAt: time.Now(),
		}
		e.emitTask(t.ID)
		log.Printf("engine: dispatch task %q -> agent %q on %s", t.Title, ag.Name, branch)
		return t, *ag, base, taskCtx, true
	}
	return model.Task{}, model.Agent{}, "", nil, false
}

// onHeartbeat receives a liveness pulse from the agent runner while a task's
// agent is working. It refreshes the in-memory liveness for the task and pushes
// a "task.heartbeat" event so the cockpit can show a live pulse on the running
// card — and turn it amber when the agent falls quiet — without refetching the
// board. A pulse for a task no longer tracked as running is dropped (the run
// finished between the runner's tick and this call).
func (e *Engine) onHeartbeat(hb agent.HeartbeatInfo) {
	e.mu.Lock()
	ri, ok := e.running[hb.TaskID]
	if ok {
		ri.lastBeatAt = time.Now()
		ri.idleFor = hb.IdleFor
		ri.lastLine = hb.LastLine
		e.running[hb.TaskID] = ri
	}
	started := ri.startedAt
	e.mu.Unlock()
	if !ok {
		return
	}
	e.emit("task.heartbeat", map[string]any{
		"taskId":         hb.TaskID,
		"agentName":      hb.AgentName,
		"idleSeconds":    int(hb.IdleFor.Round(time.Second) / time.Second),
		"lastLine":       hb.LastLine,
		"outputBytes":    hb.OutputBytes,
		"runningSeconds": int(time.Since(started).Round(time.Second) / time.Second),
	})
}

// onAgentStart is called once the agent subprocess has started. It records the
// pgid in active_runs so a boot reaper can clean up orphaned process groups
// left by a crash. The row is removed by the defer in run().
func (e *Engine) onAgentStart(taskID string, pgid int) {
	e.mu.Lock()
	ri, ok := e.running[taskID]
	agentID := ri.agentID
	e.mu.Unlock()
	if !ok {
		return
	}
	if err := e.store.ActiveRuns.Record(taskID, pgid, agentID); err != nil {
		log.Printf("engine: record active run %s pgid=%d: %v", taskID, pgid, err)
	}
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

// quarantineThreshold reads the quarantine threshold from settings (0/unset/malformed/negative = disabled).
func (e *Engine) quarantineThreshold() int {
	v, err := e.store.Settings.Get(settingQuarantineThreshold)
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
func depsSatisfied(t model.Task, statusByID map[string]string) bool {
	for _, dep := range t.DependsOn {
		if statusByID[dep] != model.TaskMerged {
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

// stageAttachments copies the task's uploaded images into the worktree (under a
// gitignored .fabrika/ path) and returns the in-worktree paths. The agent runs
// sandboxed to its worktree, so attachments referenced at their global
// .fabrika/uploads location are unreadable (rejected as an external directory);
// staging a copy inside the worktree makes them readable. The worktree's
// .gitignore excludes /.fabrika/, so the copies never enter the branch diff.
// Best-effort: an attachment that can't be staged is skipped, not fatal.
func (e *Engine) stageAttachments(wt string, urls []string) []string {
	srcs := e.attachmentPaths(urls)
	if len(srcs) == 0 {
		return nil
	}
	dir := filepath.Join(wt, ".fabrika", "attachments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("engine: stage attachments: mkdir: %v", err)
		return nil
	}
	var out []string
	for _, src := range srcs {
		data, err := os.ReadFile(src)
		if err != nil {
			log.Printf("engine: stage attachment %s: %v", src, err)
			continue
		}
		dst := filepath.Join(dir, filepath.Base(src))
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			log.Printf("engine: stage attachment %s: %v", dst, err)
			continue
		}
		out = append(out, dst)
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

	// Backstop for plan-time validation: a held-out check referencing a file
	// that neither exists, is authored in HeldOutFiles, nor will be created by
	// this task (touchPaths) can never pass — the implementer is locked out of
	// held-out paths. Fail it as a plan defect BEFORE spending an agent run, so
	// the failure is attributed to the contract, not to correct implementer work.
	if missing := planner.MissingHeldOutRefs(wt, task.Acceptance, task.TouchPaths); len(missing) > 0 {
		msg := "plan defect: held-out check references missing file(s): " + strings.Join(missing, ", ") +
			" — the planner must author them in heldOutFiles (or the contract must be edited); the implementer cannot create them"
		ev := model.Evidence{Stages: map[string]model.StageResult{
			"contract": {Pass: false, Output: msg},
		}}
		e.finish(task, ag, ev, model.ResultFail, msg, model.TaskFailed, model.Usage{})
		log.Printf("engine: task %q failed before dispatch: %s", task.Title, msg)
		return
	}

	// Render the prompt to a temp file the agent command can read. Human
	// comments on the task ride along as guidance — commenting then hitting
	// Retry is how a person steers the next run — and a previous failed
	// attempt's evidence is summarized so the agent corrects instead of repeats.
	conventions, _ := e.store.Conventions.List()
	promptFile, cleanup, err := writeTempPrompt(agent.RenderPrompt(
		task, conventions, e.stageAttachments(wt, task.Attachments),
		e.taskGuidance(task.ID), e.lastFailureSummary(task.ID)))
	if err != nil {
		log.Printf("engine: write prompt: %v", err)
		e.finish(task, ag, model.Evidence{}, model.ResultFail, "write prompt: "+err.Error(), model.TaskFailed, model.Usage{})
		return
	}
	defer cleanup()

	defer func() {
		if err := e.store.ActiveRuns.Delete(task.ID); err != nil {
			log.Printf("engine: delete active run %s: %v", task.ID, err)
		}
	}()
	agentRes, err := e.agent.Run(ctx, ag, task, wt, promptFile)
	// A cancelled context means the human stopped this task in flight (steer).
	// Finalize as closed with their reason rather than treating it as a failure.
	if reason, cancelled := e.cancellation(task.ID); cancelled {
		e.finalizeCancelled(task, ag, reason)
		return
	}
	// The agent went silent and was killed as stalled. Surface it as a distinct
	// liveness failure (not a generic agent error) with its own evidence stage so
	// the human can tell "hung agent" apart from "agent ran and failed". It
	// retries within MaxAttempts like any other failure.
	if agentRes.Stalled {
		msg := fmt.Sprintf("agent produced no output for %s and was killed as stalled", agentRes.IdleFor)
		ev := model.Evidence{Stages: map[string]model.StageResult{
			"liveness": {Pass: false, Output: msg},
		}}
		e.finishFail(task, ag, ev, "STALLED: "+msg+"\n\n"+combineLog(agentRes.Stdout, agentRes.Stderr), agentRes.Usage)
		log.Printf("engine: task %q stalled: %s", task.Title, msg)
		return
	}
	if err != nil {
		log.Printf("engine: agent run %q: %v", ag.Name, err)
		e.finishFail(task, ag, model.Evidence{}, "agent error: "+err.Error(), model.Usage{})
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

	// An empty branch diff means the agent committed nothing — it gave up or
	// never edited files. The gate would pass trivially against the unchanged
	// tree and the task would auto-merge as a silent no-op, discarding any
	// uncommitted work with the worktree. Fail it instead (auto-retries within
	// budget) so a do-nothing run is never reported as a success.
	if strings.TrimSpace(diff) == "" {
		ev := model.Evidence{Stages: map[string]model.StageResult{
			"diff": {Pass: false, Output: "agent produced no changes — empty branch diff; nothing to verify or merge"},
		}, Artifacts: artifactURLs}
		e.finishFail(task, ag, ev, "EMPTY DIFF: agent produced no changes\n\n"+logText, agentRes.Usage)
		log.Printf("engine: task %q failed: empty diff (agent produced no changes)", task.Title)
		return
	}

	// Integrity: the implementer may not touch locked test files, nor the paths
	// the planner-authored held-out files will occupy. A violation fails the task
	// before the gate even runs — its acceptance can't be trusted.
	locked := append([]string{}, task.Acceptance.LockedGlobs...)
	for p := range task.Acceptance.HeldOutFiles {
		locked = append(locked, p)
	}
	if viol := lockedViolations(changed, locked); len(viol) > 0 {
		ev := model.Evidence{Stages: map[string]model.StageResult{
			"locked": {Pass: false, Output: "branch edits protected files: " + strings.Join(viol, ", ")},
		}, Diff: diff, Artifacts: artifactURLs}
		e.finishFail(task, ag, ev,
			"LOCKED GLOB VIOLATION: "+strings.Join(viol, ", ")+"\n\n"+logText, agentRes.Usage)
		log.Printf("engine: task %q gate-blocked: locked globs touched (%v)", task.Title, viol)
		return
	}

	// Materialize planner-authored held-out files now — after the branch's
	// auto-commit, so they stay untracked (gate and mutation testing see them;
	// they never enter the branch or the merge).
	if werr := writeHeldOutFiles(wt, task.Acceptance.HeldOutFiles); werr != nil {
		ev := model.Evidence{Stages: map[string]model.StageResult{
			"heldout": {Pass: false, Output: "write held-out files: " + werr.Error()},
		}, Diff: diff, Artifacts: artifactURLs}
		e.finish(task, ag, ev, model.ResultFail, logText, model.TaskFailed, agentRes.Usage)
		log.Printf("engine: task %q failed: held-out files: %v", task.Title, werr)
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

	// A red gate fails the attempt; finishFail auto-retries while the agent's
	// MaxAttempts budget allows, else the task lands in failed. The green path
	// runs the Phase 3 trust checks (reviewer + mutation testing) and decides
	// auto-merge vs human review.
	if !gatePassed(ev) {
		e.finishFail(task, ag, ev, logText, agentRes.Usage)
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
		ranges := changedLineRanges(ev.Diff)
		res := e.runMutation(ctx, wt, changed, task.Acceptance.LockedGlobs, ranges)
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
	if sha, err := repo.RevParse(e.ctx, "HEAD"); err == nil {
		_ = e.store.Tasks.SetMergeCommitSHA(task.ID, sha)
	} else {
		log.Printf("engine: RevParse after auto-merge: %v", err)
	}
	_ = repo.RemoveWorktree(e.ctx, wt)

	audit := e.sample(e.auditRate())
	if err := e.store.Tasks.MarkMerged(task.ID, true, audit); err != nil {
		log.Printf("engine: mark auto-merged: %v", err)
	}
	e.logTransition(task.ID, model.TaskVerifying, model.TaskMerged, "engine", "auto-merged")
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

// finishFail records a failed attempt, then either re-queues the task for an
// automatic retry — while the agent's MaxAttempts budget allows — or marks it
// failed for the human. The re-run's prompt carries lastFailureSummary, so the
// next attempt corrects instead of repeats; claim() rebuilds a clean worktree.
// Counting is all-time (manual Retry keeps attempt history), so a human Retry
// after exhaustion grants one more run, not a fresh budget.
func (e *Engine) finishFail(task model.Task, ag model.Agent, ev model.Evidence, logText string, usage model.Usage) {
	e.mu.Lock()
	e.recordAttempt(task, ag, ev, model.ResultFail, logText, usage)
	status := model.TaskFailed
	budget := maxAttempts(ag)
	if fails, err := e.failedAttempts(task.ID); err == nil && fails < budget {
		status = model.TaskReady
		log.Printf("engine: task %q failed attempt %d/%d — auto-retrying", task.Title, fails, budget)
	} else {
		log.Printf("engine: task %q -> failed", task.Title)
	}
	e.setStatus(task.ID, status)
	e.mu.Unlock()
	e.emitTask(task.ID)
}

// failedAttempts counts the task's recorded failed attempts.
func (e *Engine) failedAttempts(taskID string) (int, error) {
	atts, err := e.store.Attempts.ListForTask(taskID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, a := range atts {
		if a.Result == model.ResultFail {
			n++
		}
	}
	return n, nil
}

// maxAttempts is the agent's retry budget, floored at one run.
func maxAttempts(ag model.Agent) int {
	if ag.MaxAttempts < 1 {
		return 1
	}
	return ag.MaxAttempts
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
	e.setStatusBy(task.ID, model.TaskClosed, "human", "stopped in flight")
	e.emitTask(task.ID)
	log.Printf("engine: task %q stopped in flight", task.Title)
}

// Accept merges a reviewed task's branch into the base branch and marks it
// merged. Normally only valid for tasks in review (green); with force it also
// merges failed/blocked tasks whose work the human judged good despite the red
// gate — the explicit flag keeps "merge red work" a deliberate act, never a
// default. Merge conflicts abort cleanly (git.Merge) and surface as an error;
// the human's recovery is Retry, which rebuilds on the current base.
func (e *Engine) Accept(taskID string, force bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	t, err := e.store.Tasks.Get(taskID)
	if err != nil {
		return err
	}
	switch {
	case t.Status == model.TaskReview:
	case force && (t.Status == model.TaskFailed || t.Status == model.TaskBlocked):
	case t.Status == model.TaskFailed || t.Status == model.TaskBlocked:
		return fmt.Errorf("task is %s; pass force to merge anyway", t.Status)
	default:
		return fmt.Errorf("task is %s, not awaiting accept", t.Status)
	}
	if t.Branch == "" {
		return fmt.Errorf("task has no branch to merge")
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
	if sha, err := repo.RevParse(e.ctx, "HEAD"); err == nil {
		_ = e.store.Tasks.SetMergeCommitSHA(taskID, sha)
	} else {
		log.Printf("engine: RevParse after accept merge: %v", err)
	}
	_ = repo.RemoveWorktree(e.ctx, e.worktreePath(taskID))
	e.setStatusBy(taskID, model.TaskMerged, "human", "accepted")
	e.emitTask(taskID)
	log.Printf("engine: merged task %q (%s -> %s, force=%v)", t.Title, t.Branch, base, force)
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

// annotatePushed sets t.Pushed for each merged task whose merge commit is
// reachable from the remote-tracking ref for the given remote/branch. Non-merged
// tasks and tasks with an empty MergeCommitSHA are left as Pushed=false without
// touching git. All merge commits are resolved in one PushedSet call so the
// cost stays flat as merged history grows; on error every field stays false
// (best-effort).
func annotatePushed(ctx context.Context, repo *git.Repo, remote, branch string, tasks []model.Task) []model.Task {
	shas := make([]string, 0, len(tasks))
	for i := range tasks {
		if tasks[i].Status == model.TaskMerged && tasks[i].MergeCommitSHA != "" {
			shas = append(shas, tasks[i].MergeCommitSHA)
		}
	}
	if len(shas) == 0 {
		return tasks
	}
	pushed, err := repo.PushedSet(ctx, remote, branch, shas)
	if err != nil {
		return tasks
	}
	for i := range tasks {
		if tasks[i].Status == model.TaskMerged && tasks[i].MergeCommitSHA != "" {
			tasks[i].Pushed = pushed[tasks[i].MergeCommitSHA]
		}
	}
	return tasks
}

// PushAnnotate enriches a task slice by setting Pushed on every merged task
// whose work has been pushed to the remote. It is best-effort: on any error
// (no repo, no remote, etc.) the input slice is returned unchanged so that
// an API handler never errors just because a remote isn't configured.
func (e *Engine) PushAnnotate(ctx context.Context, tasks []model.Task) []model.Task {
	repo, err := git.Open(ctx, e.repoRoot)
	if err != nil {
		return tasks
	}
	branch, err := repo.CurrentBranch(ctx)
	if err != nil {
		return tasks
	}
	remotes, err := repo.Remotes(ctx)
	if err != nil {
		return tasks
	}
	remote, err := pickRemote(remotes)
	if err != nil {
		return tasks
	}
	return annotatePushed(ctx, repo, remote, branch, tasks)
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
	e.setStatusBy(taskID, model.TaskClosed, "human", reason)
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
	e.logTransition(taskID, t.Status, model.TaskReady, "human", "retry")
	e.emitTask(taskID)
	e.Wake()
	log.Printf("engine: task %q re-queued (retry)", t.Title)
	return nil
}

// RequestChanges sends a reviewed task back for another run instead of merging
// or kicking it back — the one-step version of "comment, reject, retry". The
// guidance is recorded as a user comment so the next run's prompt carries it
// (taskGuidance). Only review-state tasks qualify: failed/blocked already have
// Retry, which picks up comments the same way.
func (e *Engine) RequestChanges(taskID, guidance string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	t, err := e.store.Tasks.Get(taskID)
	if err != nil {
		return err
	}
	if t.Status != model.TaskReview {
		return fmt.Errorf("task is %s, not awaiting accept", t.Status)
	}

	if guidance != "" {
		c := &model.Comment{TaskID: taskID, AuthorType: "user", Body: guidance}
		if err := e.store.Comments.Create(c); err != nil {
			return err
		}
		e.emit("task.comment.added", c)
	}

	// Drop the reviewed worktree/branch so the next claim rebuilds clean, same
	// as Retry: the next run starts fresh from the current base, guided by the
	// comments rather than continuing the old diff.
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
	e.logTransition(taskID, t.Status, model.TaskReady, "human", "changes requested")
	e.emitTask(taskID)
	e.Wake()
	log.Printf("engine: task %q sent back for changes", t.Title)
	return nil
}

// DeleteTask permanently removes a closed (kicked-back) task with its attempt
// and comment history, so the Closed shelf doesn't grow forever. Only closed
// tasks qualify: every other status either still has a UI exit of its own or
// is history worth keeping (merged). Stale worktree/branch leftovers are
// cleared so nothing orphaned survives the row.
func (e *Engine) DeleteTask(taskID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	t, err := e.store.Tasks.Get(taskID)
	if err != nil {
		return err
	}
	if t.Status != model.TaskClosed {
		return fmt.Errorf("task is %s; only closed tasks can be deleted", t.Status)
	}

	if repo, rerr := git.Open(e.ctx, e.repoRoot); rerr == nil {
		wt := e.worktreePath(taskID)
		_ = repo.RemoveWorktree(e.ctx, wt)
		_ = os.RemoveAll(wt)
		if t.Branch != "" {
			_ = repo.DeleteBranch(e.ctx, t.Branch)
		}
	}

	if err := e.store.Attempts.DeleteByTask(taskID); err != nil {
		return err
	}
	if err := e.store.Comments.DeleteByTask(taskID); err != nil {
		return err
	}
	if err := e.store.Transitions.DeleteByTask(taskID); err != nil {
		return err
	}
	if err := e.store.Tasks.Delete(taskID); err != nil {
		return err
	}
	e.emit("task.deleted", map[string]string{"id": taskID})
	log.Printf("engine: task %q deleted", t.Title)
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
// and clears any audit flag. When the merge captured a commit SHA (Phase 4+),
// a new revert task is spawned to run through the normal dispatch -> gates ->
// review/auto-merge pipeline.
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

	// Pre-Phase-4 merges have no captured SHA to revert, so only the
	// change-failure flag is recorded.
	if t.MergeCommitSHA == "" {
		if err := e.store.Tasks.SetReverted(taskID); err != nil {
			return err
		}
		e.emitTask(taskID)
		log.Printf("engine: task %q marked reverted (change-failure)", t.Title)
		return nil
	}

	// Spawn a revert task that flows through the normal dispatch -> gates ->
	// review/auto-merge pipeline.
	revertTask := model.Task{
		BigTaskID: t.BigTaskID,
		Title:     "Revert: " + t.Title,
		Spec:      fmt.Sprintf("Run `git revert -m 1 %s` to revert the changes from the merged task.\n\nOriginal task spec for context:\n%s", t.MergeCommitSHA, t.Spec),
		Priority:  model.PriorityHigh,
		RiskTier:  t.RiskTier,
	}
	if err := e.store.Tasks.Create(&revertTask); err != nil {
		return err
	}
	e.emit("task.created", revertTask)

	// Mark the original task as reverted.
	if err := e.store.Tasks.SetReverted(taskID); err != nil {
		return err
	}
	e.emitTask(taskID)
	log.Printf("engine: task %q marked reverted (change-failure); spawned revert task %q", t.Title, revertTask.ID)
	return nil
}

// CancelPlanning stops an in-flight planning run for the given big task.
// It returns store.ErrNotFound when the big task does not exist, a non-nil
// error when the big task is not currently planning, or nil once the reason is
// recorded and the run's cancel func is called.
func (e *Engine) CancelPlanning(bigTaskID, reason string) error {
	if _, err := e.store.BigTasks.Get(bigTaskID); err != nil {
		return err // store.ErrNotFound when absent
	}

	e.planMu.Lock()
	pri, ok := e.planRuns[bigTaskID]
	if !ok || pri.cancel == nil {
		e.planMu.Unlock()
		return fmt.Errorf("big task is not currently planning")
	}
	pri.reason = reason
	e.planRuns[bigTaskID] = pri
	cancel := pri.cancel
	e.planMu.Unlock()

	cancel()
	return nil
}

// planCancelReason reports whether a planning run was deliberately stopped and
// the human's reason. Returns ("", false) when the big task is not in an
// in-flight planning run or hasn't been deliberately stopped.
func (e *Engine) planCancelReason(bigTaskID string) (string, bool) {
	e.planMu.Lock()
	defer e.planMu.Unlock()
	pri, ok := e.planRuns[bigTaskID]
	if !ok || pri.reason == "" {
		return "", false
	}
	return pri.reason, true
}

// --- helpers ---

func (e *Engine) worktreePath(taskID string) string {
	return filepath.Join(e.repoRoot, ".fabrika", "worktrees", taskID)
}

func (e *Engine) setStatus(id, status string) {
	e.setStatusBy(id, status, "engine", "")
}

// setStatusBy mutates a task's status like setStatus but attributes the
// transition to the given actor/reason — used for human-initiated terminal moves.
func (e *Engine) setStatusBy(id, status, actor, reason string) {
	t, err := e.store.Tasks.Get(id)
	if err != nil {
		log.Printf("engine: set status get task %s: %v", id, err)
	}
	if err := e.store.Tasks.UpdateStatus(id, status); err != nil {
		log.Printf("engine: set status %s=%s: %v", id, status, err)
	}
	if t != nil {
		e.logTransition(id, t.Status, status, actor, reason)
	}
}

// logTransition persists a task's lifecycle move as a structured
// TaskTransition record carrying the actor (agent|human|engine) and a short
// reason. Errors are logged, never fatal — a missing transition must not fail
// the run.
func (e *Engine) logTransition(taskID, from, to, actor, reason string) {
	if from == to || (from == "" && to == "") {
		return
	}
	tr := &model.TaskTransition{TaskID: taskID, FromStatus: from, ToStatus: to, Actor: actor, Reason: reason}
	if err := e.store.Transitions.Create(tr); err != nil {
		log.Printf("engine: log transition: %v", err)
	} else {
		e.emit("task.transition.added", tr)
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

// taskGuidance returns the bodies of human ("user") comments on a task, oldest
// first, so they can be injected into the implementer prompt. Agent comments
// are excluded — guidance is the human steering channel.
func (e *Engine) taskGuidance(taskID string) []string {
	comments, err := e.store.Comments.ListForTask(taskID)
	if err != nil {
		log.Printf("engine: list comments for guidance %s: %v", taskID, err)
		return nil
	}
	var out []string
	for _, c := range comments {
		if c.AuthorType == "user" && strings.TrimSpace(c.Body) != "" {
			out = append(out, c.Body)
		}
	}
	return out
}

// stageOrder is the canonical reporting order for evidence stages.
var stageOrder = []string{"contract", "locked", "heldout", "setup", "typecheck", "lint", "build", "test", "verify", "e2e"}

// lastFailureSummary condenses the most recent failed attempt's evidence into a
// short report (failing stages + the tail of their output) for the next run's
// prompt, followed by one line per earlier failure so a fix for the newest
// problem doesn't reintroduce an older one. Empty when the task's latest
// attempt didn't fail.
func (e *Engine) lastFailureSummary(taskID string) string {
	atts, err := e.store.Attempts.ListForTask(taskID) // newest first
	if err != nil || len(atts) == 0 || atts[0].Result != model.ResultFail {
		return ""
	}
	var b strings.Builder
	b.WriteString(attemptFailureDetail(atts[0]))
	var prior []string
	for _, a := range atts[1:] {
		if a.Result == model.ResultFail {
			prior = append(prior, attemptFailureLine(a))
		}
	}
	if len(prior) > 0 {
		b.WriteString("\n\nEarlier attempts failed too (oldest first) — make sure your fix doesn't reintroduce these:\n")
		for i := len(prior) - 1; i >= 0; i-- {
			b.WriteString("- " + prior[i] + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// attemptFailureDetail reports each failing stage with the tail of its output,
// falling back to the attempt log when there is no stage evidence (e.g. the
// agent itself errored).
func attemptFailureDetail(att model.Attempt) string {
	var b strings.Builder
	for _, stage := range stageOrder {
		res, ok := att.Evidence.Stages[stage]
		if !ok || res.Pass {
			continue
		}
		fmt.Fprintf(&b, "stage %q failed:\n%s\n", stage, tailLines(res.Output, 40))
	}
	if b.Len() == 0 && att.Log != "" {
		b.WriteString(tailLines(att.Log, 40))
	}
	return strings.TrimSpace(b.String())
}

// attemptFailureLine condenses one failed attempt to a single line: the first
// failing stage plus the last line of its output (where the error usually is).
func attemptFailureLine(att model.Attempt) string {
	for _, stage := range stageOrder {
		res, ok := att.Evidence.Stages[stage]
		if !ok || res.Pass {
			continue
		}
		return fmt.Sprintf("stage %q failed: %s", stage, lastLine(res.Output))
	}
	return lastLine(att.Log)
}

// lastLine returns the last non-empty line of s, truncated for prompt brevity.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	out := strings.TrimSpace(lines[len(lines)-1])
	if len(out) > 200 {
		out = out[:200] + "…"
	}
	return out
}

// tailLines returns the last n lines of s, prefixing a marker when truncated.
func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) <= n {
		return strings.TrimSpace(s)
	}
	return "[... truncated ...]\n" + strings.Join(lines[len(lines)-n:], "\n")
}

// writeHeldOutFiles materializes planner-authored held-out test files
// (Contract.HeldOutFiles) into the worktree just before the gate runs. Paths
// are worktree-relative; anything absolute or escaping the worktree is
// rejected. Existing files are overwritten — the gate must run against the
// trusted contents, never an implementer-supplied copy.
func writeHeldOutFiles(wt string, files map[string]string) error {
	for p, contents := range files {
		rel := strings.TrimPrefix(strings.TrimSpace(p), "./")
		if rel == "" || filepath.IsAbs(rel) || !filepath.IsLocal(filepath.FromSlash(rel)) {
			return fmt.Errorf("held-out file path %q escapes the worktree", p)
		}
		dst := filepath.Join(wt, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, []byte(contents), 0o644); err != nil {
			return err
		}
	}
	return nil
}
