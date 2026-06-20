package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/berkaycubuk/fabrika/internal/model"
)

// RunStream runs the agent like Run but treats its stdout as claude
// stream-json NDJSON, forwarding each line's activity to onActivity as it
// arrives. It is the streaming run path used by the planner: because every
// stream-json line is fresh output, the activity meter keeps ticking and the
// IdleTimeout stall monitor regains real stall detection on long planner runs
// (the whole point of streaming).
//
// Process, timeout, and stall semantics are identical to Run — they share
// runCore. As each complete line arrives it is parsed with ParseActivity; when
// ok, ev.Ts is stamped with the current unix-millis and onActivity is called
// (incrementally, from exec's copier goroutine, so it must be cheap and
// non-blocking; a nil onActivity is allowed). Usage comes from the stream's
// result event via ParseStreamUsage (last result line wins), NOT the
// fabrika_USAGE marker. Raw stdout/stderr are still buffered into res for
// parity with Run, so a caller's plan-file fallback and logging keep working.
func (s *Subprocess) RunStream(ctx context.Context, a model.Agent, t model.Task, worktree, promptFile string, onActivity func(ActivityEvent)) (AgentResult, error) {
	sink := &streamSink{onActivity: onActivity, format: streamFormat(a.Command)}
	res, err := s.runCore(ctx, a, t, worktree, promptFile, sink)
	// runCore has waited for the process and its output copiers, so no more
	// writes can race here; flush any final unterminated line.
	sink.flush()
	if err != nil {
		return res, err
	}
	// Parse the plain-text transcript for the same markers Run parses from
	// buffered stdout. The agent's textual output (where markers live) arrives
	// inside assistant text blocks in stream-json format — scanning the raw
	// NDJSON would hit JSON syntax instead of the markers themselves.
	transcript := sink.transcript.String()
	if q, ok := parseEscalation(transcript); ok {
		res.Escalated = true
		res.Decision = q
	}
	res.Comments = parseComments(transcript)
	res.Evidence = parseEvidence(transcript)
	// Prefer stream-json result-event usage (haveUsage); only if no result
	// event appeared, fall back to the fabrika_USAGE marker in the transcript.
	if sink.haveUsage {
		res.Usage = sink.usage
	} else if u, ok := parseUsage(transcript); ok {
		res.Usage = u
	}
	return res, nil
}

// streamSink is the extra stdout writer leg for RunStream: it splits the
// agent's stdout into newline-terminated lines and, per complete line, forwards
// a parsed ActivityEvent to onActivity, tracks the latest stream-json result
// usage, and accumulates a plain-text transcript from assistant text blocks so
// marker parsers can find escalation/comment/evidence lines after the run. It
// fires from exec's copier goroutine, so it stays cheap. It is not safe for
// concurrent use, which matches how exec drives a single copier.
type streamSink struct {
	onActivity func(ActivityEvent)
	// format selects the line parser. The zero value "" behaves identically to
	// "claude" (claude stream-json), so existing claude callers and tests that
	// build &streamSink{} keep working untouched. "opencode" routes lines
	// through the opencode --format json parsers.
	format     string
	buf        []byte // bytes of the in-progress (unterminated) line
	usage      model.Usage
	haveUsage  bool
	transcript strings.Builder // plain-text from assistant text blocks, for marker parsing
}

// streamFormat reports which line parser RunStream should use for a command:
// "opencode" when the command runs opencode (or already carries --format json),
// otherwise "claude" (the default stream-json path).
func streamFormat(command string) string {
	trimmed := strings.TrimSpace(command)
	if strings.Contains(trimmed, "--format json") || strings.Contains(trimmed, "--format=json") || invokesOpencode(trimmed) {
		return "opencode"
	}
	return "claude"
}

func (w *streamSink) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.handle(w.buf[:i])
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
}

// flush processes any trailing line not terminated by a newline (e.g. a final
// result line emitted without a closing '\n'). It must run only after all
// writes have completed.
func (w *streamSink) flush() {
	if len(w.buf) > 0 {
		w.handle(w.buf)
		w.buf = w.buf[:0]
	}
}

// handle parses one stream line for activity, usage, and transcript text,
// branching on the sink's format. The claude/default path is left byte-for-byte
// unchanged; the opencode path uses the Task 1 NDJSON parsers.
func (w *streamSink) handle(line []byte) {
	if len(bytes.TrimSpace(line)) == 0 {
		return
	}
	if w.format == "opencode" {
		w.handleOpencode(line)
		return
	}
	if ev, ok := ParseActivity(line); ok {
		ev.Ts = time.Now().UnixMilli()
		if w.onActivity != nil {
			w.onActivity(ev)
		}
	}
	if u, ok := ParseStreamUsage(line); ok {
		w.usage = u
		w.haveUsage = true
	}
	// Accumulate text from assistant content blocks into a plain-text
	// transcript. Markers (fabrika_DECISION: etc.) live in the agent's textual
	// output, which claude --output-format stream-json wraps inside assistant
	// text blocks; scanning the raw NDJSON line would match JSON syntax noise
	// instead. Embedded newlines are preserved verbatim; blocks are separated
	// by a newline so each marker stays on its own line.
	var sl streamLine
	if json.Unmarshal(line, &sl) == nil && sl.Type == "assistant" && sl.Message != nil {
		for _, b := range sl.Message.Content {
			if b.Type == "text" && b.Text != "" {
				w.transcript.WriteString(b.Text)
				if !strings.HasSuffix(b.Text, "\n") {
					w.transcript.WriteByte('\n')
				}
			}
		}
	}
}

// handleOpencode parses one opencode --format json NDJSON line for activity,
// usage, and transcript text. Unlike claude's last-wins absolute usage, opencode
// emits per-step token counts, so usage is ACCUMULATED across step_finish lines
// (the last cumulative total wins). Top-level `text` parts are written to the
// transcript so fabrika_* markers stay parseable, mirroring the claude path.
func (w *streamSink) handleOpencode(line []byte) {
	if ev, ok := ParseOpencodeActivity(line); ok {
		ev.Ts = time.Now().UnixMilli()
		if w.onActivity != nil {
			w.onActivity(ev)
		}
	}
	if u, ok := ParseOpencodeStreamUsage(line); ok {
		w.usage.InputTokens += u.InputTokens
		w.usage.OutputTokens += u.OutputTokens
		if u.TotalTokens > 0 {
			w.usage.TotalTokens = u.TotalTokens
		}
		w.haveUsage = true
	}
	if text, ok := opencodeStreamText(line); ok {
		w.transcript.WriteString(text)
		if !strings.HasSuffix(text, "\n") {
			w.transcript.WriteByte('\n')
		}
	}
}
