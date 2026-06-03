package agent

import "github.com/berkaycubuk/fabrika/internal/model"

// HasRole reports whether an agent holds the given role.
func HasRole(a model.Agent, role string) bool {
	for _, r := range a.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// Route picks an agent for a ready task from the enabled implementer pool.
// It implements the SPECS §7 routing precedence, ignoring concurrency/slots
// (the scheduler that tracks live slots arrives in a later phase):
//
//	1. PreferredAgentID, if set and that agent is enabled.
//	2. An enabled implementer whose Tags overlap the task's Tags.
//	3. Any enabled implementer.
//	4. nil if none match (task waits in ready).
func Route(t model.Task, agents []model.Agent) *model.Agent {
	byID := map[string]*model.Agent{}
	for i := range agents {
		byID[agents[i].ID] = &agents[i]
	}

	if t.PreferredAgentID != "" {
		if a, ok := byID[t.PreferredAgentID]; ok && a.Enabled {
			return a
		}
	}

	var anyImplementer *model.Agent
	for i := range agents {
		a := &agents[i]
		if !a.Enabled || !HasRole(*a, model.RoleImplementer) {
			continue
		}
		if anyImplementer == nil {
			anyImplementer = a
		}
		if tagsOverlap(a.Tags, t.Tags) {
			return a
		}
	}
	return anyImplementer
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
