package engine

import (
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/berkaycubuk/fabrika/internal/git"
	"github.com/berkaycubuk/fabrika/internal/model"
)

// classifyOrphanBranches returns the subset of branches that are leaked Fabrika
// branches with nothing live owning them. The rules:
//   - every "fabrika/plan-*" branch is ALWAYS an orphan — no planning run is in
//     flight at boot, and the plan branch's shortID is derived from the big-task
//     ID, so a stale one collides with the next replan of the same big task.
//   - a "fabrika/task-*" branch is an orphan unless it is a key in
//     liveTaskBranches (a task whose status still legitimately owns a worktree).
//   - a "fabrika/session-*" branch is an orphan unless it is in liveSessionBranches.
//   - any branch not under the "fabrika/" prefix is NEVER an orphan (never ours).
func classifyOrphanBranches(branches []string, liveTaskBranches, liveSessionBranches map[string]bool) []string {
	var orphans []string
	for _, b := range branches {
		switch {
		case strings.HasPrefix(b, "fabrika/plan-"):
			orphans = append(orphans, b)
		case strings.HasPrefix(b, "fabrika/task-"):
			if !liveTaskBranches[b] {
				orphans = append(orphans, b)
			}
		case strings.HasPrefix(b, "fabrika/session-"):
			if !liveSessionBranches[b] {
				orphans = append(orphans, b)
			}
		}
	}
	return orphans
}

// classifyOrphanWorktrees returns the subset of worktree dir base names that are
// leaked. dirs are the entry names directly under <repoRoot>/.fabrika/worktrees/.
// The rules mirror classifyOrphanBranches:
//   - a "plan-*" dir is ALWAYS an orphan.
//   - a "session-<id>" dir is an orphan unless <id> (the name without the
//     "session-" prefix) is in liveSessionIDs.
//   - any other dir name is treated as a full task ID and is an orphan unless it
//     is in liveTaskIDs.
func classifyOrphanWorktrees(dirs []string, liveTaskIDs, liveSessionIDs map[string]bool) []string {
	var orphans []string
	for _, d := range dirs {
		switch {
		case strings.HasPrefix(d, "plan-"):
			orphans = append(orphans, d)
		case strings.HasPrefix(d, "session-"):
			if id := strings.TrimPrefix(d, "session-"); !liveSessionIDs[id] {
				orphans = append(orphans, d)
			}
		default:
			if !liveTaskIDs[d] {
				orphans = append(orphans, d)
			}
		}
	}
	return orphans
}

// gcOrphans deletes leaked Fabrika branches and worktree directories left behind
// by previous runs: stale "fabrika/plan-*" branches (which collide on the next
// replan of the same big task, since their shortID is derived from the big-task
// ID), branches/worktrees of tasks and sessions that no longer own them, and
// merged-task branches that would otherwise accumulate. It builds the live sets
// from the store, then deletes the orphan subset best-effort (logging and
// continuing on every error). Called from Start AFTER stale-lock clearing so a
// leftover index.lock doesn't wedge the branch/worktree deletes.
func (e *Engine) gcOrphans() {
	// Live tasks: those whose status legitimately still owns a worktree/branch.
	liveTaskBranches := map[string]bool{}
	liveTaskIDs := map[string]bool{}
	tasks, err := e.store.Tasks.ListByStatus(
		model.TaskClaimed, model.TaskRunning, model.TaskVerifying, model.TaskReview, model.TaskBlocked)
	if err != nil {
		log.Printf("engine: gc orphans: list tasks: %v", err)
	}
	for _, t := range tasks {
		liveTaskIDs[t.ID] = true
		if t.Branch != "" {
			liveTaskBranches[t.Branch] = true
		}
	}

	// Live sessions: those still active or mid-Finish (gating).
	liveSessionBranches := map[string]bool{}
	liveSessionIDs := map[string]bool{}
	for _, status := range []string{model.SessionActive, model.SessionGating} {
		sessions, serr := e.store.Sessions.ListByStatus(status)
		if serr != nil {
			log.Printf("engine: gc orphans: list sessions (%s): %v", status, serr)
			continue
		}
		for _, s := range sessions {
			liveSessionIDs[s.ID] = true
			if s.Branch != "" {
				liveSessionBranches[s.Branch] = true
			}
		}
	}

	repo, err := git.Open(e.ctx, e.repoRoot)
	if err != nil {
		log.Printf("engine: gc orphans: open repo: %v", err)
		return
	}

	// Branches: list fabrika/* and delete each orphan.
	branches, err := repo.ListBranches(e.ctx, "fabrika/*")
	if err != nil {
		log.Printf("engine: gc orphans: list branches: %v", err)
	}
	for _, b := range classifyOrphanBranches(branches, liveTaskBranches, liveSessionBranches) {
		if derr := repo.DeleteBranch(e.ctx, b); derr != nil {
			log.Printf("engine: gc orphans: delete branch %q: %v", b, derr)
			continue
		}
		log.Printf("engine: gc orphans: deleted orphan branch %q", b)
	}

	// Worktree directories: read the entries under .fabrika/worktrees and remove
	// each orphan dir (worktree metadata first, then the directory itself).
	wtRoot := filepath.Join(e.repoRoot, ".fabrika", "worktrees")
	entries, err := os.ReadDir(wtRoot)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("engine: gc orphans: read worktrees dir: %v", err)
		}
		return
	}
	var dirs []string
	for _, ent := range entries {
		if ent.IsDir() {
			dirs = append(dirs, ent.Name())
		}
	}
	for _, d := range classifyOrphanWorktrees(dirs, liveTaskIDs, liveSessionIDs) {
		path := filepath.Join(wtRoot, d)
		_ = repo.RemoveWorktree(e.ctx, path)
		if rerr := os.RemoveAll(path); rerr != nil {
			log.Printf("engine: gc orphans: remove worktree dir %q: %v", path, rerr)
			continue
		}
		log.Printf("engine: gc orphans: removed orphan worktree %q", d)
	}
}
