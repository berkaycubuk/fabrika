// Package agent adapts any CLI coding agent into Fabrika. The adapter is kept
// deliberately thin: render a prompt file from the task, substitute {prompt_file}
// and {worktree} into the agent's command template, run it as a subprocess, and
// surface any structured escalation.
//
// This is plumbing for the deferred live loop: the Runner and prompt rendering
// are usable and tested, but no scheduler dispatches tasks to it yet.
// See SPECS.md §7.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/berkaycubuk/fabrika/internal/model"
)

// DecisionMarker is the stdout sentinel an agent emits to escalate a question
// instead of failing. The remainder of the line is a JSON Decision payload.
const DecisionMarker = "fabrika_DECISION:"

// ReviewMarker is the stdout sentinel a reviewer agent emits with its verdict on
// a finished branch. The remainder of the line is a JSON ReviewVerdict payload.
const ReviewMarker = "fabrika_REVIEW:"

// ReviewVerdict is a reviewer agent's first-pass judgment on a branch (SPECS §7,
// §13). Approve gates auto-merge; Notes surface to the human when it's kicked up.
type ReviewVerdict struct {
	Approve bool   `json:"approve"`
	Notes   string `json:"notes"`
}

// AgentResult is the normalized outcome of one agent subprocess run.
type AgentResult struct {
	ExitCode  int
	Stdout    string
	Stderr    string
	Escalated bool   // true if the agent emitted a DecisionMarker
	Decision  string // raw JSON after the marker, if Escalated
}

// Runner invokes a registered agent against a task in a worktree.
type Runner interface {
	Run(ctx context.Context, a model.Agent, t model.Task, worktree, promptFile string) (AgentResult, error)
}

// Subprocess is the default Runner: it executes the agent's Command template as
// a subprocess. It satisfies Runner.
type Subprocess struct {
	// Shell runs the substituted command string. Defaults to "sh -c".
	Shell []string
}

// NewSubprocess returns a Subprocess runner with defaults.
func NewSubprocess() *Subprocess {
	return &Subprocess{Shell: []string{"sh", "-c"}}
}

// RenderCommand substitutes {prompt_file} and {worktree} into the template.
func RenderCommand(template, promptFile, worktree string) string {
	r := strings.NewReplacer(
		"{prompt_file}", promptFile,
		"{worktree}", worktree,
	)
	return r.Replace(template)
}

// RenderPrompt builds the prompt file contents for a task run: the spec, the
// acceptance contract, relevant conventions, and the standing run rules. The
// implementing agent must not edit locked test files.
func RenderPrompt(t model.Task, conventions []model.Convention) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Task: %s\n\n", t.Title)
	if t.Spec != "" {
		fmt.Fprintf(&b, "## Specification\n%s\n\n", t.Spec)
	}

	b.WriteString("## Acceptance (machine-verified — do not weaken)\n")
	if len(t.Acceptance.VerifyCmds) > 0 {
		b.WriteString("These commands must pass:\n")
		for _, c := range t.Acceptance.VerifyCmds {
			fmt.Fprintf(&b, "  - `%s`\n", c)
		}
	}
	if len(t.Acceptance.LockedGlobs) > 0 {
		b.WriteString("\nDo NOT edit these protected files (the gate will reject branches that touch them):\n")
		for _, g := range t.Acceptance.LockedGlobs {
			fmt.Fprintf(&b, "  - `%s`\n", g)
		}
	}
	b.WriteString("\n")

	if len(conventions) > 0 {
		b.WriteString("## Conventions\n")
		for _, c := range conventions {
			fmt.Fprintf(&b, "  - %s\n", c.Rule)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Rules\n")
	b.WriteString("- Make commits on this branch.\n")
	b.WriteString("- On every commit, add the trailer `Co-authored-by: fabrika <fabrika@berkaycubuk.com>` and do not add yourself or any other `Co-authored-by` line.\n")
	b.WriteString("- Do not edit locked test files listed above.\n")
	fmt.Fprintf(&b, "- If you hit a question you cannot resolve, print a single line: `%s {\"question\":\"...\",\"options\":[\"...\"]}` and stop.\n", DecisionMarker)
	return b.String()
}

// RenderReviewPrompt builds the prompt for a reviewer agent's first-pass review
// of a finished branch (SPECS §7 reviewer role). It gets the task intent, the
// acceptance contract, and the branch diff, and must end with a verdict line.
func RenderReviewPrompt(t model.Task, diff string, conventions []model.Convention) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Review: %s\n\n", t.Title)
	b.WriteString("You are a reviewer agent doing a first-pass review before a human sees this work. ")
	b.WriteString("Judge whether the change correctly and safely satisfies the task.\n\n")
	if t.Spec != "" {
		fmt.Fprintf(&b, "## Task specification\n%s\n\n", t.Spec)
	}
	if len(t.Acceptance.VerifyCmds) > 0 {
		b.WriteString("## Acceptance (already passed the gate)\n")
		for _, c := range t.Acceptance.VerifyCmds {
			fmt.Fprintf(&b, "  - `%s`\n", c)
		}
		b.WriteString("\n")
	}
	if len(conventions) > 0 {
		b.WriteString("## Conventions to enforce\n")
		for _, c := range conventions {
			fmt.Fprintf(&b, "  - %s\n", c.Rule)
		}
		b.WriteString("\n")
	}
	b.WriteString("## Diff under review\n```diff\n")
	b.WriteString(diff)
	b.WriteString("\n```\n\n")
	b.WriteString("## Verdict\n")
	b.WriteString("Do NOT modify any files. End your output with a single line:\n")
	fmt.Fprintf(&b, "`%s {\"approve\": true|false, \"notes\": \"...\"}`\n", ReviewMarker)
	b.WriteString("Approve only if the change is correct, scoped, and safe to merge.\n")
	return b.String()
}

// ParseReview scans output for the last ReviewMarker line and decodes its verdict.
// Missing or malformed verdicts are treated as a non-approval (ok=false) so the
// work falls back to a human rather than auto-merging on an ambiguous review.
func ParseReview(out string) (ReviewVerdict, bool) {
	var payload string
	found := false
	for _, line := range strings.Split(out, "\n") {
		if idx := strings.Index(line, ReviewMarker); idx >= 0 {
			payload = strings.TrimSpace(line[idx+len(ReviewMarker):])
			found = true
		}
	}
	if !found {
		return ReviewVerdict{}, false
	}
	var v ReviewVerdict
	if err := json.Unmarshal([]byte(payload), &v); err != nil {
		return ReviewVerdict{}, false
	}
	return v, true
}

// Run executes the agent command in the worktree and parses any escalation.
func (s *Subprocess) Run(ctx context.Context, a model.Agent, t model.Task, worktree, promptFile string) (AgentResult, error) {
	command := RenderCommand(a.Command, promptFile, worktree)
	shell := s.Shell
	if len(shell) == 0 {
		shell = []string{"sh", "-c"}
	}
	args := append(append([]string{}, shell[1:]...), command)

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, shell[0], args...)
	cmd.Dir = worktree
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// When ctx is cancelled (in-flight steer), the agent's process is killed; but a
	// grandchild (e.g. a `sleep` under `sh -c`) can inherit the output pipe and
	// keep Run blocked. WaitDelay bounds that: after the kill, exec force-closes
	// the pipes and returns rather than hanging on the orphan.
	cmd.WaitDelay = 3 * time.Second
	runErr := cmd.Run()

	res := AgentResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if ee, ok := runErr.(*exec.ExitError); ok {
		res.ExitCode = ee.ExitCode()
	} else if runErr != nil {
		return res, fmt.Errorf("run agent %q: %w", a.Name, runErr)
	}

	if q, ok := parseEscalation(res.Stdout); ok {
		res.Escalated = true
		res.Decision = q
	}
	return res, nil
}

// parseEscalation scans output for the last DecisionMarker line and returns the
// trailing JSON payload.
func parseEscalation(out string) (string, bool) {
	var payload string
	found := false
	for _, line := range strings.Split(out, "\n") {
		if idx := strings.Index(line, DecisionMarker); idx >= 0 {
			payload = strings.TrimSpace(line[idx+len(DecisionMarker):])
			found = true
		}
	}
	return payload, found
}
