package engine

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/berkaycubuk/fabrika/internal/git"
	"github.com/berkaycubuk/fabrika/internal/model"
)

// errResolutionStarted is returned by Accept when a stale-branch conflict was
// handed to the agent for auto-resolution rather than merged outright. It is a
// success signal, not a failure: callers (the API handler, the auto-merge sweep)
// special-case it instead of surfacing it as an error.
var errResolutionStarted = errors.New("conflict resolution started")

// IsResolutionStarted reports whether an Accept error signals that the task's
// stale-branch conflict was handed to the agent for auto-resolution (a success
// hand-off) rather than a real failure. The API layer uses it to return 200.
func (e *Engine) IsResolutionStarted(err error) bool {
	return errors.Is(err, errResolutionStarted)
}

// startConflictResolution dispatches an async agent run that resolves a stale
// branch's conflicts with base in its existing worktree, re-gates the result,
// and merges it. The caller must hold e.mu (the task is registered as running
// before the lock is released so it cannot be double-claimed).
//
// It reuses the task's original implementer agent; if that agent is gone the
// resolution cannot proceed and an error is returned so the caller falls back to
// surfacing the conflict.
func (e *Engine) startConflictResolution(task model.Task, base string, conflicts []string) error {
	ag, err := e.store.Agents.Get(task.AgentID)
	if err != nil || ag == nil {
		return fmt.Errorf("no agent to resolve with")
	}
	taskCtx, cancel := context.WithCancel(e.ctx)
	e.running[task.ID] = runInfo{
		agentID: ag.ID, agentName: ag.Name, touchPaths: task.TouchPaths,
		cancel: cancel, startedAt: time.Now(),
	}
	e.setStatus(task.ID, model.TaskRunning)
	e.emitTask(task.ID)
	log.Printf("engine: auto-resolving conflict for task %q against %s in %s",
		task.Title, base, strings.Join(conflicts, ", "))
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.runResolve(taskCtx, task, *ag, base)
		e.markDone(task.ID)
		e.Wake()
	}()
	return nil
}

// runResolve performs the slow resolution work (agent + gate) outside e.mu. It
// re-creates the conflicting merge in the worktree, asks the agent to resolve
// it, commits the resolution, re-runs the verification gate, and — on green —
// merges the branch. Any failure routes the task back to review and parks it so
// the sweep won't immediately re-resolve it (cleared when main advances or the
// human Retries).
func (e *Engine) runResolve(ctx context.Context, task model.Task, ag model.Agent, base string) {
	wt := e.worktreePath(task.ID)
	repo, err := git.Open(e.ctx, e.repoRoot)
	if err != nil {
		e.failResolution(task, ag, "open repo: "+err.Error())
		return
	}

	conflicts, err := repo.StartConflictMerge(e.ctx, wt, base)
	if err != nil {
		e.failResolution(task, ag, fmt.Sprintf("could not begin merge with %s: %v", base, err))
		return
	}
	if len(conflicts) == 0 {
		// Became cleanly mergeable between detection and now — merge straight away.
		e.finalizeResolution(task, ag, base, repo,
			model.Evidence{Stages: map[string]model.StageResult{
				"resolve": {Pass: true, Output: "no conflicts to resolve; base merged cleanly"},
			}}, "", model.Usage{})
		return
	}

	prompt := renderResolvePrompt(task, base, conflicts, e.taskGuidance(task.ID))
	promptFile, cleanup, perr := writeTempPrompt(prompt)
	if perr != nil {
		_ = repo.AbortMerge(e.ctx, wt)
		e.failResolution(task, ag, "write prompt: "+perr.Error())
		return
	}
	defer cleanup()

	agentRes, aerr := e.agent.Run(ctx, ag, task, wt, promptFile)
	if reason, cancelled := e.cancellation(task.ID); cancelled {
		_ = repo.AbortMerge(e.ctx, wt)
		e.finalizeCancelled(task, ag, reason)
		return
	}
	if agentRes.Stalled {
		_ = repo.AbortMerge(e.ctx, wt)
		e.failResolution(task, ag, fmt.Sprintf("agent went silent for %s while resolving conflicts", agentRes.IdleFor))
		return
	}
	if aerr != nil {
		_ = repo.AbortMerge(e.ctx, wt)
		e.failResolution(task, ag, "agent error during resolution: "+aerr.Error())
		return
	}

	// The agent must have cleared every conflict marker. Check the working tree
	// directly: the index stays "unmerged" until we stage, so an index-based
	// check would wrongly flag a correctly-edited file. A leftover marker means
	// the resolution is incomplete and cannot be trusted.
	if remaining := conflictMarkersRemain(wt, conflicts); len(remaining) > 0 {
		_ = repo.AbortMerge(e.ctx, wt)
		e.failResolution(task, ag, "agent left unresolved conflict markers in: "+strings.Join(remaining, ", "))
		return
	}
	if err := repo.CommitMerge(e.ctx, wt,
		git.WithCoAuthor("fabrika: merge "+base+" into "+task.Branch+" (auto-resolved conflicts)")); err != nil {
		_ = repo.AbortMerge(e.ctx, wt)
		e.failResolution(task, ag, "commit resolved merge: "+err.Error())
		return
	}

	logText := combineLog(agentRes.Stdout, agentRes.Stderr)

	// Held-out checks need their files materialized (untracked) before the gate,
	// exactly as the original run did.
	if werr := writeHeldOutFiles(wt, task.Acceptance.HeldOutFiles); werr != nil {
		e.failResolution(task, ag, "write held-out files: "+werr.Error())
		return
	}

	var diff string
	if d, derr := repo.Diff(e.ctx, base, task.Branch); derr == nil {
		diff = d
	}

	// Re-gate the resolved tree: the merge could have reintroduced a regression
	// even with every marker cleared, so the suite must pass before we merge.
	gateCmds := append(append([]string{}, task.Acceptance.VerifyCmds...), task.Acceptance.HeldOut...)
	ev, gerr := e.gate.Run(ctx, wt, e.cfg.Verbs, gateCmds)
	if gerr != nil {
		log.Printf("engine: resolution gate: %v", gerr)
	}
	ev.Diff = diff
	if !gatePassed(ev) {
		e.recordAttempt(task, ag, ev, model.ResultFail, logText, agentRes.Usage)
		e.failResolution(task, ag, "gate failed after conflict resolution")
		return
	}
	e.finalizeResolution(task, ag, base, repo, ev, logText, agentRes.Usage)
}

// finalizeResolution merges a successfully-resolved branch and marks the task
// merged. The branch already contains base (the merge commit), so the
// integration merge is clean. Acquires e.mu for the DB/git state writes.
func (e *Engine) finalizeResolution(task model.Task, ag model.Agent, base string, repo *git.Repo, ev model.Evidence, logText string, usage model.Usage) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.recordAttempt(task, ag, ev, model.ResultPass, logText, usage)

	wt := e.worktreePath(task.ID)
	if err := repo.Merge(e.ctx, base, task.Branch); err != nil {
		// Shouldn't happen (base is already merged into the branch), but guard the
		// loop: park and route to review rather than retry.
		e.setStatus(task.ID, model.TaskReview)
		e.markMergeConflict(task.ID)
		e.emitTask(task.ID)
		log.Printf("engine: resolved branch %q still failed to merge: %v (-> review)", task.Title, err)
		return
	}
	if sha, rerr := repo.RevParse(e.ctx, "HEAD"); rerr == nil {
		_ = e.store.Tasks.SetMergeCommitSHA(task.ID, sha)
	}
	_ = repo.RemoveWorktree(e.ctx, wt)

	audit := e.sample(e.auditRate())
	if err := e.store.Tasks.MarkMerged(task.ID, true, audit); err != nil {
		log.Printf("engine: mark merged after resolution: %v", err)
	}
	e.clearMergeConflict(task.ID)
	e.logTransition(task.ID, model.TaskRunning, model.TaskMerged, "engine", "auto-merged after conflict resolution")
	e.emitTask(task.ID)
	log.Printf("engine: task %q -> merged (conflict auto-resolved, audit=%v)", task.Title, audit)
}

// failResolution records a failed resolution attempt, routes the task back to
// review, and parks it so the auto-merge sweep won't immediately re-resolve it.
// The park clears when main advances or the human Retries.
func (e *Engine) failResolution(task model.Task, ag model.Agent, msg string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ev := model.Evidence{Stages: map[string]model.StageResult{
		"resolve": {Pass: false, Output: msg},
	}}
	e.recordAttempt(task, ag, ev, model.ResultFail, msg, model.Usage{})
	e.setStatus(task.ID, model.TaskReview)
	e.markMergeConflict(task.ID)
	e.emitTask(task.ID)
	log.Printf("engine: conflict resolution for %q failed: %s (-> review)", task.Title, msg)
}

// conflictMarkersRemain returns the subset of files (relative to wt) that still
// contain Git conflict markers, i.e. a line beginning with "<<<<<<<" or
// ">>>>>>>". A file that was deleted as the resolution reads as resolved.
func conflictMarkersRemain(wt string, files []string) []string {
	var bad []string
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(wt, f))
		if err != nil {
			continue // deleted/unreadable -> nothing to conflict
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "<<<<<<<") || strings.HasPrefix(line, ">>>>>>>") {
				bad = append(bad, f)
				break
			}
		}
	}
	return bad
}

// renderResolvePrompt builds the agent instruction for resolving a stale-branch
// merge. The conflict markers are already in the worktree; the agent edits the
// files in place to produce a correct, marker-free result that preserves the
// intent of both sides.
func renderResolvePrompt(task model.Task, base string, conflicts []string, guidance []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are resolving a git merge conflict, not implementing a new task.\n\n")
	fmt.Fprintf(&b, "The branch for task %q has been merged with the latest %q and Git left conflict markers in the working tree. Your job is to resolve every conflict so the file is correct and contains no conflict markers (<<<<<<<, =======, >>>>>>>).\n\n", task.Title, base)
	fmt.Fprintf(&b, "Resolve the conflicts to preserve the intent of BOTH sides: the work done on this task AND the changes that landed on %s. Do not discard either side's behavior unless they are genuinely mutually exclusive; when they are, keep this task's change and integrate it with the surrounding updated code.\n\n", base)
	b.WriteString("Conflicted files:\n")
	for _, f := range conflicts {
		fmt.Fprintf(&b, "  - %s\n", f)
	}
	b.WriteString("\nOriginal task context (for intent only — do not re-do the work):\n")
	if strings.TrimSpace(task.Spec) != "" {
		fmt.Fprintf(&b, "%s\n", strings.TrimSpace(task.Spec))
	} else {
		fmt.Fprintf(&b, "%s\n", task.Title)
	}
	if len(guidance) > 0 {
		b.WriteString("\nHuman guidance on this task:\n")
		for _, g := range guidance {
			fmt.Fprintf(&b, "  - %s\n", g)
		}
	}
	b.WriteString("\nEdit the files in place. Do not commit — the system commits the resolution and re-runs the tests after you finish. Do not change files that are not in conflict beyond what is needed to make the merge consistent.\n")
	return b.String()
}
