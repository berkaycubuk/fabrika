package model

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Validators for the package's status fields. Callers use these instead of
// comparing against raw string literals, keeping the magic strings defined once
// in model.go. Each ValidX reports whether s is one of the constants for that
// field; the empty string is never valid.

// oneOf reports whether s equals any of the given values.
func oneOf(s string, values ...string) bool {
	return slices.Contains(values, s)
}

// ValidTaskStatus reports whether s is a defined Task.Status.
func ValidTaskStatus(s string) bool {
	return oneOf(s,
		TaskPlanned, TaskReady, TaskClaimed, TaskRunning, TaskVerifying,
		TaskReview, TaskMerged, TaskBlocked, TaskFailed, TaskClosed)
}

// ValidBigTaskStatus reports whether s is a defined BigTask.Status.
func ValidBigTaskStatus(s string) bool {
	return oneOf(s,
		BigTaskDraft, BigTaskPlanning, BigTaskPlanned, BigTaskRunning,
		BigTaskDone, BigTaskError, BigTaskBacklog)
}

// ValidPlanStatus reports whether s is a defined Plan.Status.
func ValidPlanStatus(s string) bool {
	return oneOf(s, PlanProposed, PlanApproved, PlanRejected)
}

// ValidConventionStatus reports whether s is a defined Convention.Status.
func ValidConventionStatus(s string) bool {
	return oneOf(s, ConventionProposed, ConventionApproved, ConventionRejected)
}

// ValidDecisionStatus reports whether s is a defined Decision.Status.
func ValidDecisionStatus(s string) bool {
	return oneOf(s, DecisionOpen, DecisionAnswered)
}

// ValidRiskTier reports whether s is a defined risk tier.
func ValidRiskTier(s string) bool {
	return oneOf(s, RiskLow, RiskMedium, RiskHigh)
}

// ValidPriority reports whether s is a defined task priority.
func ValidPriority(s string) bool {
	return oneOf(s, PriorityLow, PriorityMedium, PriorityHigh)
}

// ValidAgentRole reports whether s is a defined agent role.
func ValidAgentRole(s string) bool {
	return oneOf(s, RoleImplementer, RolePlanner, RoleReviewer)
}

// ValidReporter reports whether s is a defined task reporter.
func ValidReporter(s string) bool {
	return oneOf(s, ReporterUser, ReporterPlanner)
}

// ValidAttemptResult reports whether s is a defined Attempt.Result.
func ValidAttemptResult(s string) bool {
	return oneOf(s, ResultPass, ResultFail, ResultEscalated)
}

// ValidateAgent returns a descriptive error for the first problem found in a,
// or nil when a is well-formed. An empty Roles slice is allowed.
func ValidateAgent(a Agent) error {
	if strings.TrimSpace(a.Name) == "" {
		return fmt.Errorf("agent name is required")
	}
	if strings.TrimSpace(a.Command) == "" {
		return fmt.Errorf("agent command is required")
	}
	for _, role := range a.Roles {
		if !ValidAgentRole(role) {
			return fmt.Errorf("invalid agent role: %q", role)
		}
	}
	if a.Timeout != "" {
		d, err := time.ParseDuration(a.Timeout)
		if err != nil || d <= 0 {
			return fmt.Errorf("agent timeout must be a positive duration like \"20m\"")
		}
	}
	return nil
}
