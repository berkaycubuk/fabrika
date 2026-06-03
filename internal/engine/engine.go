// Package engine owns the task lifecycle state machine and the scheduler that
// dispatches ready tasks across agent slots, respecting dependencies, touch-path
// collisions, and the global WIP cap.
//
// STUB: this phase ships the skeleton + plumbing only. The live dispatch loop
// (worktree -> agent -> gate -> evidence -> merge) is deferred to the next pass;
// the surrounding packages (git, gate, agent, store) already expose the pieces
// it will wire together. See SPECS.md §7, §13 (Phase 0 completion).
package engine

import (
	"github.com/berkaycubuk/fabrika/internal/agent"
	"github.com/berkaycubuk/fabrika/internal/config"
	"github.com/berkaycubuk/fabrika/internal/gate"
	"github.com/berkaycubuk/fabrika/internal/store"
)

// Engine coordinates scheduling and the verification/merge gates. In this phase
// it only holds its collaborators; dispatch is not yet implemented.
type Engine struct {
	Store *store.Store
	Cfg   *config.Config
	Gate  gate.Runner
	Agent agent.Runner
}

// New constructs an Engine. Wiring it into a running dispatch loop is deferred.
func New(s *store.Store, cfg *config.Config) *Engine {
	return &Engine{
		Store: s,
		Cfg:   cfg,
		Gate:  gate.New(),
		Agent: agent.NewSubprocess(),
	}
}

// TODO(phase0-completion): Start(ctx) should run the scheduler loop —
//   - find ready tasks whose DependsOn are merged and whose TouchPaths don't
//     collide with running tasks,
//   - Route them to a free agent slot (agent.Route + per-agent concurrency),
//   - create a worktree (git.AddWorktree), render+run the agent (agent.Runner),
//   - run the gate (gate.Runner), persist an Attempt + Evidence,
//   - auto-merge low-risk green branches or surface a review item.
