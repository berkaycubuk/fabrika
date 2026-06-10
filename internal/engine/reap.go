package engine

import (
	"log"
	"syscall"
)

// reapOrphanProcesses kills any process groups recorded in active_runs from a
// previous fabrika invocation. Must be called before recoverOrphans so that a
// live orphan cannot race the worktree deletion and trigger a duplicate agent.
func (e *Engine) reapOrphanProcesses() {
	runs, err := e.store.ActiveRuns.List()
	if err != nil {
		log.Printf("engine: reap orphans: list active runs: %v", err)
		// still try to clear the table so we don't block recovery
	}

	for _, r := range runs {
		if r.PGID <= 1 {
			continue
		}
		if err := syscall.Kill(-r.PGID, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
			log.Printf("engine: reap orphans: kill pgid %d (task %s): %v", r.PGID, r.TaskID, err)
		}
	}

	if err := e.store.ActiveRuns.Clear(); err != nil {
		log.Printf("engine: reap orphans: clear active_runs: %v", err)
	}
}
