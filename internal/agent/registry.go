package agent

import (
	"slices"

	"github.com/berkaycubuk/fabrika/internal/model"
)

// HasRole reports whether an agent holds the given role.
func HasRole(a model.Agent, role string) bool {
	return slices.Contains(a.Roles, role)
}

// Route picks an agent for a ready task from the enabled implementer pool,
// honoring live free slots (SPECS §7). free maps agentID -> remaining
// concurrency slots; an agent is eligible only when free[id] > 0. Precedence:
//
//	1. PreferredAgentID, if set and that agent is enabled. When it's pinned but
//	   has no free slot, the task WAITS for it (returns nil) rather than spilling
//	   onto another agent — pinning is a deliberate routing choice. A pin to a
//	   missing/disabled agent falls through to the general pool.
//	2. Capability (tag overlap) partitions the eligible pool. If any eligible
//	   agent's Tags overlap the task's, choose only from that subset.
//	3. Within the chosen subset, the highest Priority agent wins (larger int).
//	   Ties are broken by existing slice order (first wins).
//	4. nil if no eligible agents (task waits in ready).
func Route(t model.Task, agents []model.Agent, free map[string]int) *model.Agent {
	hasSlot := func(a *model.Agent) bool { return free == nil || free[a.ID] > 0 }

	byID := map[string]*model.Agent{}
	for i := range agents {
		byID[agents[i].ID] = &agents[i]
	}

	if t.PreferredAgentID != "" {
		if a, ok := byID[t.PreferredAgentID]; ok && a.Enabled {
			if hasSlot(a) {
				return a
			}
			return nil // pinned agent is busy; wait for it
		}
	}

	var tagMatch *model.Agent
	var anyMatch *model.Agent
	for i := range agents {
		a := &agents[i]
		if !a.Enabled || !HasRole(*a, model.RoleImplementer) || !hasSlot(a) {
			continue
		}
		if tagsOverlap(a.Tags, t.Tags) {
			if tagMatch == nil || a.Priority > tagMatch.Priority {
				tagMatch = a
			}
		} else {
			if anyMatch == nil || a.Priority > anyMatch.Priority {
				anyMatch = a
			}
		}
	}
	if tagMatch != nil {
		return tagMatch
	}
	return anyMatch
}

func tagsOverlap(a, b []string) bool {
	set := map[string]bool{}
	for _, t := range a {
		set[t] = true
	}
	for _, t := range b {
		if set[t] {
			return true
		}
	}
	return false
}
