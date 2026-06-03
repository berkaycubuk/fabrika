// Package planner turns a BigTask into a task DAG plus contracts and open
// decisions. In Phase 0 it is a passthrough; the planner-agent role arrives in
// Phase 2 (SPECS.md §13).
//
// STUB: Plan currently returns a single task mirroring the BigTask so the rest
// of the pipeline has something to carry. No decomposition or contract synthesis
// yet.
package planner

import (
	"github.com/berkaycubuk/fabrika/internal/model"
)

// Plan produces a Plan from a BigTask. Phase 0 passthrough: one task, no
// decisions, proposed status.
func Plan(bt model.BigTask) model.Plan {
	return model.Plan{
		BigTaskID: bt.ID,
		Status:    model.PlanProposed,
		Tasks: []model.Task{
			{
				BigTaskID: bt.ID,
				Title:     bt.Title,
				Spec:      bt.Intent,
				Status:    model.TaskReady,
				RiskTier:  model.RiskLow,
			},
		},
	}
}

// TODO(phase2): assign an agent the planner role to decompose intent into a real
// DAG with DependsOn edges, TouchPaths, spec-derived locked acceptance contracts,
// and OpenDecisions for questions it cannot resolve.
