package engine

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"

	"github.com/berkaycubuk/fabrika/internal/agent"
	"github.com/berkaycubuk/fabrika/internal/config"
	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/berkaycubuk/fabrika/internal/mutate"
)

// settingRoleReviewer names the agent that holds the reviewer role (optional
// override; otherwise any enabled agent with the reviewer role is used).
const settingRoleReviewer = "role_reviewer"

// mutationBudget caps how many mutants the validator runs per task. Mutation
// testing re-runs the whole test verb per mutant, so this bounds the wall-clock.
const mutationBudget = 8

// ReviewerAgent returns the agent that performs first-pass review, and whether
// one exists. It prefers the configured role_reviewer override (if enabled), else
// the first enabled agent carrying the reviewer role (SPECS §7).
func (e *Engine) ReviewerAgent() (model.Agent, bool) {
	agents, err := e.store.Agents.List()
	if err != nil {
		log.Printf("engine: list agents for reviewer: %v", err)
		return model.Agent{}, false
	}
	if id, _ := e.store.Settings.Get(settingRoleReviewer); id != "" {
		for _, a := range agents {
			if a.ID == id && a.Enabled && agent.HasRole(a, model.RoleReviewer) {
				return a, true
			}
		}
	}
	for _, a := range agents {
		if a.Enabled && agent.HasRole(a, model.RoleReviewer) {
			return a, true
		}
	}
	return model.Agent{}, false
}

// runReviewer runs the reviewer agent against the finished branch in its
// worktree and returns its verdict (approved?) and notes. A missing or malformed
// verdict is a non-approval so ambiguous reviews fall back to a human.
func (e *Engine) runReviewer(ctx context.Context, rev model.Agent, task model.Task, diff string, conventions []model.Convention) (bool, string) {
	prompt := agent.RenderReviewPrompt(task, diff, conventions)
	promptFile, cleanup, err := writeTempPrompt(prompt)
	if err != nil {
		log.Printf("engine: reviewer write prompt: %v", err)
		return false, "reviewer setup failed: " + err.Error()
	}
	defer cleanup()

	res, err := e.agent.Run(ctx, rev, task, e.worktreePath(task.ID), promptFile)
	if err != nil {
		log.Printf("engine: reviewer run: %v", err)
		return false, "reviewer error: " + err.Error()
	}
	verdict, ok := agent.ParseReview(res.Stdout)
	if !ok {
		return false, "reviewer returned no verdict (treated as not approved)"
	}
	notes := strings.TrimSpace(verdict.Notes)
	if notes == "" {
		if verdict.Approve {
			notes = "approved"
		} else {
			notes = "rejected"
		}
	}
	return verdict.Approve, notes
}

// mutationEnabled reports whether the mutation-testing validator is turned on.
func (e *Engine) mutationEnabled() bool {
	v, _ := e.store.Settings.Get(settingMutation)
	return strings.EqualFold(strings.TrimSpace(v), "on")
}

// runMutation perturbs the task's changed source files (excluding test files and
// locked globs) and confirms the repo's test verb catches the perturbation.
func (e *Engine) runMutation(ctx context.Context, wt string, changed, lockedGlobs []string) mutate.Result {
	files := mutableFiles(changed, lockedGlobs)
	testCmd := e.cfg.Verbs.Test
	test := func(ctx context.Context) bool {
		cmd := exec.CommandContext(ctx, "sh", "-c", testCmd)
		cmd.Dir = wt
		return cmd.Run() == nil
	}
	return mutate.Run(ctx, wt, files, test, mutationBudget)
}

// mutableFiles selects changed files worth mutating: it drops test files (whose
// own failure proves nothing about coverage) and locked globs.
func mutableFiles(changed, lockedGlobs []string) []string {
	var out []string
	for _, f := range changed {
		f = strings.TrimPrefix(strings.TrimSpace(f), "./")
		if f == "" || isTestFile(f) {
			continue
		}
		locked := false
		for _, g := range lockedGlobs {
			if config.MatchGlob(strings.TrimSpace(g), f) {
				locked = true
				break
			}
		}
		if !locked {
			out = append(out, f)
		}
	}
	return out
}

// isTestFile heuristically flags test/spec files by name across common stacks.
func isTestFile(path string) bool {
	base := strings.ToLower(path)
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	return strings.HasSuffix(base, "_test.go") ||
		strings.Contains(base, ".test.") ||
		strings.Contains(base, ".spec.") ||
		strings.HasPrefix(base, "test_") ||
		strings.Contains(path, "/tests/") ||
		strings.Contains(path, "/__tests__/")
}

// mutationSummary renders a one-line human summary of a mutation run.
func mutationSummary(r mutate.Result) string {
	if r.Skipped != "" {
		return "mutation testing skipped: " + r.Skipped
	}
	s := fmt.Sprintf("caught %d/%d mutants", r.Caught, r.Tested)
	if len(r.Survived) > 0 {
		s += "; survivors (tests too weak): " + strings.Join(r.Survived, ", ")
	}
	return s
}

// effectiveTier is the higher of the task's declared tier and the tier of the
// files it actually changed. This hardens auto-merge: an agent that edits an
// undeclared high-risk path can't sneak in under a low declared tier (SPECS §9).
func (e *Engine) effectiveTier(t model.Task, changed []string) string {
	declared := t.RiskTier
	if declared == "" {
		declared = model.RiskLow
	}
	if e.cfg == nil {
		return declared
	}
	return maxTier(declared, e.cfg.TierFor(changed))
}

func tierRank(t string) int {
	switch t {
	case model.RiskHigh:
		return 2
	case model.RiskMedium:
		return 1
	default:
		return 0
	}
}

func maxTier(a, b string) string {
	if tierRank(b) > tierRank(a) {
		return b
	}
	return a
}

// auditRate reads the post-merge audit sampling rate from settings (0..1). A
// malformed or out-of-range value disables sampling.
func (e *Engine) auditRate() float64 {
	v, err := e.store.Settings.Get(settingAuditPct)
	if err != nil || strings.TrimSpace(v) == "" {
		return 0
	}
	r, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || r < 0 || r > 1 {
		return 0
	}
	return r
}
