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

// DefaultTimeout bounds a single agent subprocess run when the agent does not
// specify its own Timeout (or specifies an unparseable/non-positive one). It
// guards the engine against a hung or runaway agent blocking the dispatch loop.
const DefaultTimeout = 30 * time.Minute

// ParseTimeout interprets an agent's configured Timeout string. It trims
// surrounding whitespace; an empty string, a value time.ParseDuration cannot
// parse, or a non-positive duration all fall back to DefaultTimeout. Otherwise
// it returns the parsed duration.
func ParseTimeout(s string) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" {
		return DefaultTimeout
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return DefaultTimeout
	}
	return d
}

// DecisionMarker is the stdout sentinel an agent emits to escalate a question
// instead of failing. The remainder of the line is a JSON Decision payload.
const DecisionMarker = "fabrika_DECISION:"

// CommentMarker is the stdout sentinel an agent emits to add a comment.
// The remainder of the line is the comment text.
const CommentMarker = "fabrika_COMMENT:"

// EvidenceMarker is the stdout sentinel an agent emits to attach a proof file
// (screenshot, recording, log). The remainder of the line is a worktree path,
// optionally followed by " | " and a caption.
const EvidenceMarker = "fabrika_EVIDENCE:"

// ReviewMarker is the stdout sentinel a reviewer agent emits with its verdict on
// a finished branch. The remainder of the line is a JSON ReviewVerdict payload.
const ReviewMarker = "fabrika_REVIEW:"

// UsageMarker is the stdout sentinel an agent emits on completion to report its
// token usage. The remainder of the line is a JSON model.Usage payload.
const UsageMarker = "fabrika_USAGE:"

// ReviewVerdict is a reviewer agent's first-pass judgment on a branch (SPECS §7,
// §13). Approve gates auto-merge; Notes surface to the human when it's kicked up.
type ReviewVerdict struct {
	Approve bool   `json:"approve"`
	Notes   string `json:"notes"`
}

// EvidenceRef is one artifact an agent pointed at via EvidenceMarker: a path
// inside its worktree and an optional human caption.
type EvidenceRef struct {
	Path    string
	Caption string
}

// AgentResult is the normalized outcome of one agent subprocess run.
type AgentResult struct {
	ExitCode  int
	Stdout    string
	Stderr    string
	Escalated bool          // true if the agent emitted a DecisionMarker
	Decision  string        // raw JSON after the marker, if Escalated
	Comments  []string      `json:"comments"` // text after each CommentMarker, in order
	Evidence  []EvidenceRef // files after each EvidenceMarker, in order
	Usage     model.Usage   // token usage parsed from the last UsageMarker, if any
	TimedOut  bool          // true when the run was aborted because the agent's own timeout elapsed, distinct from a human steer/cancel
}

// Runner invokes a registered agent against a task in a worktree.
type Runner interface {
	Run(ctx context.Context, a model.Agent, t model.Task, worktree, promptFile string) (AgentResult, error)
}

// Subprocess is the default Runner: it executes the agent's Command template as
// a subprocess. It satisfies Runner.
type Subprocess struct {
	// Shell runs the substituted command string. Defaults to "bash -c".
	Shell []string
}

// NewSubprocess returns a Subprocess runner with defaults.
func NewSubprocess() *Subprocess {
	return &Subprocess{Shell: []string{"bash", "-c"}}
}

// RenderCommand substitutes {prompt_file}, {worktree}, and {model} into the
// template. An empty model substitutes to an empty string; templates with no
// {model} token are unaffected.
func RenderCommand(template, promptFile, worktree, model string) string {
	r := strings.NewReplacer(
		"{prompt_file}", promptFile,
		"{worktree}", worktree,
		"{model}", model,
	)
	return r.Replace(template)
}

// RenderPrompt builds the prompt file contents for a task run: the spec, the
// acceptance contract, relevant conventions, and the standing run rules. The
// implementing agent must not edit locked test files. attachments are local
// paths to images attached at task creation (mockups, screenshots). guidance
// carries human comments on the task (oldest first) so a person can steer a
// retry by commenting; lastFailure summarizes the previous failed attempt so
// the agent corrects course instead of repeating it.
func RenderPrompt(t model.Task, conventions []model.Convention, attachments []string, guidance []string, lastFailure string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Task: %s\n\n", t.Title)
	if t.Spec != "" {
		fmt.Fprintf(&b, "## Specification\n%s\n\n", t.Spec)
	}
	if len(guidance) > 0 {
		b.WriteString("## Guidance from the human (follow this — it overrides the spec where they conflict)\n")
		for _, g := range guidance {
			fmt.Fprintf(&b, "  - %s\n", strings.ReplaceAll(strings.TrimSpace(g), "\n", "\n    "))
		}
		b.WriteString("\n")
	}
	if lastFailure != "" {
		fmt.Fprintf(&b, "## Previous attempt failed\nA prior run of this task failed. Do not repeat it — diagnose and fix the cause:\n\n```\n%s\n```\n\n", lastFailure)
	}
	if len(attachments) > 0 {
		b.WriteString("## Attached images\nThe task includes these image files — read them for context (mockups, screenshots, diagrams):\n")
		for _, p := range attachments {
			fmt.Fprintf(&b, "  - %s\n", p)
		}
		b.WriteString("\n")
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
	b.WriteString("- On every commit, add the trailer `Co-authored-by: fabrika <noreply@fabrika-ai.com>` and do not add yourself or any other `Co-authored-by` line.\n")
	b.WriteString("- Do not edit locked test files listed above.\n")
	fmt.Fprintf(&b, "- If you hit a question you cannot resolve, print a single line: `%s {\"question\":\"...\",\"options\":[\"...\"]}` and stop.\n", DecisionMarker)
	fmt.Fprintf(&b, "- To attach proof of your work (screenshot, recording, log), print one line per file: `%s <path-in-worktree> | optional caption`.\n", EvidenceMarker)
	fmt.Fprintf(&b, "- On completion, print your token usage: `%s {\"inputTokens\":N,\"outputTokens\":N,\"totalTokens\":N}`.\n", UsageMarker)
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

// lastMarkerPayload scans out for the last line containing marker and returns the
// trimmed remainder of that line. The marker may appear mid-line. found is false
// if no line contains the marker (last-wins, used by the single-marker parsers).
func lastMarkerPayload(out, marker string) (payload string, found bool) {
	for _, line := range strings.Split(out, "\n") {
		if idx := strings.Index(line, marker); idx >= 0 {
			payload = strings.TrimSpace(line[idx+len(marker):])
			found = true
		}
	}
	return payload, found
}

// markerPayloads scans out for every line containing marker and returns the
// trimmed remainder of each, in order (all-occurrences, used by the multi-marker
// parsers). Empty remainders are included; callers filter as needed.
func markerPayloads(out, marker string) []string {
	var payloads []string
	for _, line := range strings.Split(out, "\n") {
		if idx := strings.Index(line, marker); idx >= 0 {
			payloads = append(payloads, strings.TrimSpace(line[idx+len(marker):]))
		}
	}
	return payloads
}

// ParseReview scans output for the last ReviewMarker line and decodes its verdict.
// Missing or malformed verdicts are treated as a non-approval (ok=false) so the
// work falls back to a human rather than auto-merging on an ambiguous review.
func ParseReview(out string) (ReviewVerdict, bool) {
	payload, found := lastMarkerPayload(out, ReviewMarker)
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
	command := RenderCommand(a.Command, promptFile, worktree, a.Model)
	shell := s.Shell
	if len(shell) == 0 {
		shell = []string{"bash", "-c"}
	}
	args := append(append([]string{}, shell[1:]...), command)

	// Bound the run by the agent's own timeout so a hung or runaway process
	// self-terminates instead of blocking the engine forever. This deadline is
	// distinct from the parent ctx, which carries human steer/cancel.
	d := ParseTimeout(a.Timeout)
	tctx, tcancel := context.WithTimeout(ctx, d)
	defer tcancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(tctx, shell[0], args...)
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
	// Our timeout fired and the parent ctx was NOT cancelled (so this is the
	// agent's own deadline, not a human steer): record it as a timed-out failure.
	// The engine's run() routes this through finishFail and retries per MaxAttempts.
	if tctx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
		res.TimedOut = true
		return res, fmt.Errorf("agent %q timed out after %s", a.Name, d)
	}
	if ee, ok := runErr.(*exec.ExitError); ok {
		res.ExitCode = ee.ExitCode()
	} else if runErr != nil {
		return res, fmt.Errorf("run agent %q: %w", a.Name, runErr)
	}

	if q, ok := parseEscalation(res.Stdout); ok {
		res.Escalated = true
		res.Decision = q
	}
	res.Comments = parseComments(res.Stdout)
	res.Evidence = parseEvidence(res.Stdout)
	if u, ok := parseUsage(res.Stdout); ok {
		res.Usage = u
	}
	return res, nil
}

// parseUsage scans output for the last UsageMarker line and decodes its JSON
// payload (last-wins, consistent with parseEscalation/ParseReview). A malformed
// or absent payload returns ok=false. When the agent reports a zero total but
// non-zero input/output, the total is derived as input+output.
func parseUsage(out string) (model.Usage, bool) {
	payload, found := lastMarkerPayload(out, UsageMarker)
	if !found {
		return model.Usage{}, false
	}
	var u model.Usage
	if err := json.Unmarshal([]byte(payload), &u); err != nil {
		return model.Usage{}, false
	}
	if u.TotalTokens == 0 && (u.InputTokens != 0 || u.OutputTokens != 0) {
		u.TotalTokens = u.InputTokens + u.OutputTokens
	}
	return u, true
}

// parseEscalation scans output for the last DecisionMarker line and returns the
// trailing JSON payload.
func parseEscalation(out string) (string, bool) {
	return lastMarkerPayload(out, DecisionMarker)
}

// parseComments scans output for all lines beginning with CommentMarker and
// returns the trimmed text after the marker, preserving order and skipping empty.
func parseComments(out string) []string {
	var comments []string
	for _, text := range markerPayloads(out, CommentMarker) {
		if text != "" {
			comments = append(comments, text)
		}
	}
	return comments
}

// parseEvidence scans output for all lines containing EvidenceMarker and returns
// the referenced files in order. The remainder of the line is split once on
// " | " into a worktree path and an optional caption, so paths may contain
// spaces; lines with an empty path are skipped.
func parseEvidence(out string) []EvidenceRef {
	var refs []EvidenceRef
	for _, rest := range markerPayloads(out, EvidenceMarker) {
		path, caption, _ := strings.Cut(rest, " | ")
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		refs = append(refs, EvidenceRef{Path: path, Caption: strings.TrimSpace(caption)})
	}
	return refs
}
