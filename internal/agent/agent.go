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
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/berkaycubuk/fabrika/internal/model"
)

// DefaultTimeout bounds a single agent subprocess run when the agent does not
// specify its own Timeout (or specifies an unparseable/non-positive one). It
// guards the engine against a hung or runaway agent blocking the dispatch loop.
const DefaultTimeout = 30 * time.Minute

// DefaultIdleTimeout is the stall threshold: if a running agent produces no
// output (stdout or stderr) for this long, it is presumed hung and killed,
// rather than waiting out the much longer hard DefaultTimeout. A healthy CLI
// coding agent streams output continuously; prolonged silence is the most
// reliable agent-agnostic signal that it is stuck. Set IdleTimeout to 0 to
// disable stall detection and rely only on the hard timeout.
const DefaultIdleTimeout = 5 * time.Minute

// maxHeartbeatLine bounds the LastLine carried on a heartbeat so a chatty agent
// can't push huge payloads to every connected UI client.
const maxHeartbeatLine = 200

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
// ProposedConventions are short, reusable rules the reviewer noticed in the diff
// that could become project-wide conventions.
type ReviewVerdict struct {
	Approve             bool     `json:"approve"`
	Notes               string   `json:"notes"`
	ProposedConventions []string `json:"proposedConventions"`
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
	TimedOut  bool          // true when the run was aborted because the agent's own hard timeout elapsed, distinct from a human steer/cancel
	Stalled   bool          // true when the run was killed for producing no output for IdleTimeout (a hung agent), distinct from TimedOut
	IdleFor   time.Duration // when Stalled, how long the agent was silent before the kill
}

// HeartbeatInfo is a liveness pulse emitted periodically while an agent runs, so
// the cockpit can show that a walk-away run is actually making progress (and go
// amber when it falls quiet) instead of an opaque "running" with no signal.
type HeartbeatInfo struct {
	TaskID      string        // the task whose agent this pulse describes
	AgentName   string        // the agent doing the work
	LastLine    string        // most recent non-empty output line (truncated), as a sign of life
	IdleFor     time.Duration // time since the agent last produced any output
	OutputBytes int64         // total stdout+stderr bytes produced so far
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
	// IdleTimeout is the stall threshold for this runner; <= 0 disables stall
	// detection. Defaults to DefaultIdleTimeout via NewSubprocess.
	IdleTimeout time.Duration
	// Heartbeat, if set, is called periodically while an agent runs with a
	// liveness pulse. It must be cheap and non-blocking — it fires from the
	// monitor goroutine while the agent's process is alive.
	Heartbeat func(HeartbeatInfo)
	// OnStart, if set, is called once immediately after the agent subprocess
	// starts, with the task ID and the process-group id (pgid). With Setpgid the
	// leader's pgid equals cmd.Process.Pid. It must be cheap and non-blocking.
	OnStart func(taskID string, pgid int)
	// OnOutput, if set, is called with each chunk of stdout as the agent
	// produces it (stderr is excluded — stdout is what becomes a chat reply).
	// It fires from exec's copier goroutine while the agent writes, so it must
	// be cheap and non-blocking, and must not retain chunk past the call.
	OnOutput func(taskID string, chunk []byte)
	// beatInterval overrides the heartbeat/stall-check cadence (tests only). When
	// 0, the cadence is derived from IdleTimeout.
	beatInterval time.Duration
}

// NewSubprocess returns a Subprocess runner with defaults.
func NewSubprocess() *Subprocess {
	return &Subprocess{Shell: []string{"bash", "-c"}, IdleTimeout: DefaultIdleTimeout}
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
		b.WriteString("## Attached files\nThe task includes these files — read each one for context. They may be images (mockups, screenshots, diagrams) or documents (specs, data, logs):\n")
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
	fmt.Fprintf(&b, "`%s {\"approve\": true|false, \"notes\": \"...\", \"proposedConventions\": [\"rule one\", \"rule two\"]}`\n", ReviewMarker)
	b.WriteString("Approve only if the change is correct, scoped, and safe to merge.\n")
	b.WriteString("If you notice recurring patterns in the diff that could become project-wide conventions, suggest up to 3 short, reusable rules in `proposedConventions`. Omit the field or use an empty array if nothing stands out.\n")
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

	// A separate cancel layered under the hard timeout lets the stall monitor
	// kill a silent (hung) agent without tripping the DeadlineExceeded path, so
	// the three failure modes — human steer (ctx), hard timeout (tctx), and
	// stall (killStall) — stay distinguishable after the process exits.
	killCtx, killStall := context.WithCancel(tctx)
	defer killStall()

	// Tee both streams through an activity meter: it keeps the full output in the
	// buffers (as before) while recording the last-output time and last line, the
	// liveness signal the monitor and heartbeats read.
	var stdout, stderr bytes.Buffer
	meter := newActivityMeter()
	cmd := exec.CommandContext(killCtx, shell[0], args...)
	cmd.Dir = worktree
	cmd.Stdout = io.MultiWriter(&stdout, meter)
	if s.OnOutput != nil {
		cmd.Stdout = io.MultiWriter(&stdout, meter, writerFunc(func(p []byte) (int, error) {
			s.OnOutput(t.ID, p)
			return len(p), nil
		}))
	}
	cmd.Stderr = io.MultiWriter(&stderr, meter)
	// Run the agent in its own process group so a kill (stall, hard timeout, or
	// human steer — all routed through killCtx) takes down the whole tree, not
	// just the top shell. Otherwise a grandchild (e.g. `sleep` under `bash -c`)
	// is orphaned, keeps the output pipe open, and both leaks a process and
	// stalls Wait until WaitDelay. The group kill closes the pipe at once.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	setPdeathsig(cmd.SysProcAttr)
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			// Negative pid signals the whole process group.
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	// Backstop if a process somehow escapes the group kill: force-close the pipes
	// shortly after the kill rather than hanging on an orphan.
	cmd.WaitDelay = 3 * time.Second

	if err := cmd.Start(); err != nil {
		return AgentResult{Stdout: stdout.String(), Stderr: stderr.String()}, fmt.Errorf("run agent %q: %w", a.Name, err)
	}
	if s.OnStart != nil {
		s.OnStart(t.ID, cmd.Process.Pid)
	}

	// Monitor the agent's output cadence for the life of the process: emit a
	// heartbeat each tick and, if the agent has been silent past IdleTimeout,
	// kill it as stalled. The goroutine exits when the process does (done).
	var stalled atomic.Bool
	done := make(chan struct{})
	var mon sync.WaitGroup
	mon.Go(func() {
		interval := s.heartbeatInterval()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				idle := time.Since(meter.last())
				if s.Heartbeat != nil {
					s.Heartbeat(HeartbeatInfo{
						TaskID:      t.ID,
						AgentName:   a.Name,
						LastLine:    truncate(meter.lastLine(), maxHeartbeatLine),
						IdleFor:     idle,
						OutputBytes: meter.total(),
					})
				}
				if s.IdleTimeout > 0 && idle >= s.IdleTimeout {
					stalled.Store(true)
					killStall()
					return
				}
			}
		}
	})

	runErr := cmd.Wait()
	close(done)
	mon.Wait()

	res := AgentResult{Stdout: stdout.String(), Stderr: stderr.String()}
	// A human steer (parent ctx cancelled) takes precedence: the engine detects
	// that separately and finalizes the task as closed, so don't mislabel it.
	if ctx.Err() == nil && stalled.Load() {
		res.Stalled = true
		res.IdleFor = s.IdleTimeout
		return res, fmt.Errorf("agent %q stalled: no output for %s", a.Name, s.IdleTimeout)
	}
	// Our hard timeout fired and the parent ctx was NOT cancelled (so this is the
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

// heartbeatInterval is the cadence at which the monitor emits a heartbeat and
// checks for a stall. It's frequent enough to feel live in the UI but derived
// from IdleTimeout so a short test timeout is checked promptly.
func (s *Subprocess) heartbeatInterval() time.Duration {
	if s.beatInterval > 0 {
		return s.beatInterval
	}
	interval := 5 * time.Second
	if s.IdleTimeout > 0 && s.IdleTimeout/3 < interval {
		interval = s.IdleTimeout / 3
	}
	if interval < 20*time.Millisecond {
		interval = 20 * time.Millisecond
	}
	return interval
}

// writerFunc adapts a function to io.Writer, for the OnOutput MultiWriter leg.
type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// activityMeter tees an agent's output streams to record liveness: the wall time
// of the last write, the running byte count, and the most recent non-empty line.
// It is an io.Writer that records and discards (the full output is kept by the
// MultiWriter's other leg), so it stays cheap. Safe for concurrent reads from
// the monitor goroutine while exec's copier writes.
type activityMeter struct {
	lastNano atomic.Int64 // unix nanos of the last write
	bytes    atomic.Int64 // total bytes seen

	mu      sync.Mutex
	partial []byte // bytes of the in-progress (unterminated) line
	line    string // last completed non-empty line
}

func newActivityMeter() *activityMeter {
	m := &activityMeter{}
	m.lastNano.Store(time.Now().UnixNano())
	return m
}

func (m *activityMeter) Write(p []byte) (int, error) {
	m.lastNano.Store(time.Now().UnixNano())
	m.bytes.Add(int64(len(p)))
	m.mu.Lock()
	for _, b := range p {
		if b == '\n' || b == '\r' {
			if s := strings.TrimSpace(string(m.partial)); s != "" {
				m.line = s
			}
			m.partial = m.partial[:0]
		} else {
			m.partial = append(m.partial, b)
		}
	}
	m.mu.Unlock()
	return len(p), nil
}

func (m *activityMeter) last() time.Time { return time.Unix(0, m.lastNano.Load()) }
func (m *activityMeter) total() int64    { return m.bytes.Load() }

// lastLine returns the most recent completed line, or the in-progress partial
// line if the agent hasn't emitted a newline yet — so a single long-running line
// (e.g. a progress bar) still reads as activity rather than silence.
func (m *activityMeter) lastLine() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Prefer the in-progress partial line — it's the newest activity (e.g. a
	// progress bar still being written) — and fall back to the last completed
	// line once the agent has emitted a newline and gone quiet.
	if p := strings.TrimSpace(string(m.partial)); p != "" {
		return p
	}
	return m.line
}

// truncate shortens s to at most n runes, appending an ellipsis when it cuts.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
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
