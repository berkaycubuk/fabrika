package agent

import (
	"bytes"
	"context"
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
	sink := &streamSink{onActivity: onActivity}
	res, err := s.runCore(ctx, a, t, worktree, promptFile, sink)
	// runCore has waited for the process and its output copiers, so no more
	// writes can race here; flush any final unterminated line.
	sink.flush()
	if err != nil {
		return res, err
	}
	if sink.haveUsage {
		res.Usage = sink.usage
	}
	return res, nil
}

// streamSink is the extra stdout writer leg for RunStream: it splits the
// agent's stdout into newline-terminated lines and, per complete line, forwards
// a parsed ActivityEvent to onActivity and tracks the latest stream-json result
// usage. It fires from exec's copier goroutine, so it stays cheap. It is not
// safe for concurrent use, which matches how exec drives a single copier.
type streamSink struct {
	onActivity func(ActivityEvent)
	buf        []byte // bytes of the in-progress (unterminated) line
	usage      model.Usage
	haveUsage  bool
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

// handle parses one stream-json line for activity and usage.
func (w *streamSink) handle(line []byte) {
	if len(bytes.TrimSpace(line)) == 0 {
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
}
