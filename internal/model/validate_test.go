package model

import "testing"

func TestValidators(t *testing.T) {
	cases := []struct {
		name  string
		fn    func(string) bool
		valid []string
	}{
		{"TaskStatus", ValidTaskStatus, []string{
			TaskPlanned, TaskReady, TaskClaimed, TaskRunning, TaskVerifying,
			TaskReview, TaskMerged, TaskBlocked, TaskFailed, TaskClosed}},
		{"BigTaskStatus", ValidBigTaskStatus, []string{
			BigTaskDraft, BigTaskPlanning, BigTaskPlanned, BigTaskRunning,
			BigTaskDone, BigTaskError}},
		{"PlanStatus", ValidPlanStatus, []string{PlanProposed, PlanApproved, PlanRejected}},
		{"DecisionStatus", ValidDecisionStatus, []string{DecisionOpen, DecisionAnswered}},
		{"RiskTier", ValidRiskTier, []string{RiskLow, RiskMedium, RiskHigh}},
		{"Priority", ValidPriority, []string{PriorityLow, PriorityMedium, PriorityHigh}},
		{"AgentRole", ValidAgentRole, []string{RoleImplementer, RolePlanner, RoleReviewer}},
		{"Reporter", ValidReporter, []string{ReporterUser, ReporterPlanner}},
		{"AttemptResult", ValidAttemptResult, []string{ResultPass, ResultFail, ResultEscalated}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, v := range c.valid {
				if !c.fn(v) {
					t.Errorf("%s(%q) = false, want true", c.name, v)
				}
			}
			// Negative paths: empty string and an obvious non-member.
			if c.fn("") {
				t.Errorf("%s(\"\") = true, want false", c.name)
			}
			if c.fn("definitely-not-a-status") {
				t.Errorf("%s(%q) = true, want false", c.name, "definitely-not-a-status")
			}
		})
	}
}
