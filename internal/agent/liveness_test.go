package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/berkaycubuk/fabrika/internal/model"
)

// runIn invokes a Subprocess against a shell command in dir and returns the
// result. A short hard timeout keeps a buggy test from hanging CI.
func runIn(t *testing.T, s *Subprocess, dir, command string) (AgentResult, error) {
	t.Helper()
	a := model.Agent{Name: "fake", Command: command, Timeout: "30s"}
	return s.Run(context.Background(), a, model.Task{ID: "task-1"}, dir, "")
}

// TestStallKilled proves the core safety net: an agent that produces no output
// for IdleTimeout is killed as stalled (not left to burn the long hard timeout),
// and the result is flagged Stalled — distinct from a clean exit or a timeout.
func TestStallKilled(t *testing.T) {
	s := NewSubprocess()
	s.IdleTimeout = 150 * time.Millisecond

	start := time.Now()
	// Emit one line, then go silent well past the idle timeout.
	res, err := runIn(t, s, t.TempDir(), "echo starting; sleep 5")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a stall error, got nil")
	}
	if !res.Stalled {
		t.Fatalf("expected Stalled=true, got %+v (err=%v)", res, err)
	}
	if res.TimedOut {
		t.Fatal("a stall must not be reported as a hard timeout")
	}
	if !strings.Contains(res.Stdout, "starting") {
		t.Fatalf("expected captured stdout before the stall, got %q", res.Stdout)
	}
	// It must die on the idle threshold, not wait out the 5s sleep / 30s timeout.
	if elapsed > 2*time.Second {
		t.Fatalf("stall took %s; expected a prompt kill near the idle timeout", elapsed)
	}
}

// TestActiveAgentNotStalled proves a chatty agent that keeps producing output is
// never killed, even though its total run far exceeds the idle timeout: liveness
// is measured by output cadence, not wall-clock.
func TestActiveAgentNotStalled(t *testing.T) {
	s := NewSubprocess()
	s.IdleTimeout = 300 * time.Millisecond

	// Print a line every 50ms for ~600ms — twice the idle window — never silent.
	res, err := runIn(t, s, t.TempDir(),
		"for i in 1 2 3 4 5 6 7 8 9 10 11 12; do echo tick $i; sleep 0.05; done")
	if err != nil {
		t.Fatalf("active agent should finish cleanly, got err=%v res=%+v", err, res)
	}
	if res.Stalled {
		t.Fatal("a continuously-producing agent must not be flagged stalled")
	}
	if !strings.Contains(res.Stdout, "tick 12") {
		t.Fatalf("expected the agent to run to completion, got %q", res.Stdout)
	}
}

// TestStallDisabled proves IdleTimeout<=0 turns off stall detection: a silent
// agent runs to its natural completion under the hard timeout instead.
func TestStallDisabled(t *testing.T) {
	s := NewSubprocess()
	s.IdleTimeout = 0 // disabled

	res, err := runIn(t, s, t.TempDir(), "echo hi; sleep 0.3; echo bye")
	if err != nil {
		t.Fatalf("with stall detection off the agent should complete, got %v", err)
	}
	if res.Stalled {
		t.Fatal("stall detection was disabled; Stalled must be false")
	}
	if !strings.Contains(res.Stdout, "bye") {
		t.Fatalf("expected full output, got %q", res.Stdout)
	}
}

// TestHeartbeatPulses proves the runner emits liveness pulses while an agent
// works, carrying the agent name, task id, the latest output line, and a
// rising idle measure once the agent goes quiet.
func TestHeartbeatPulses(t *testing.T) {
	var mu sync.Mutex
	var beats []HeartbeatInfo
	s := NewSubprocess()
	s.IdleTimeout = 0 // don't kill; we only want to observe pulses
	s.beatInterval = 40 * time.Millisecond
	s.Heartbeat = func(hb HeartbeatInfo) {
		mu.Lock()
		beats = append(beats, hb)
		mu.Unlock()
	}

	// Produce a line, then idle ~250ms so several heartbeats land on the silence.
	if _, err := runIn(t, s, t.TempDir(), "echo hello world; sleep 0.25"); err != nil {
		t.Fatalf("run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(beats) == 0 {
		t.Fatal("expected at least one heartbeat")
	}
	last := beats[len(beats)-1]
	if last.TaskID != "task-1" || last.AgentName != "fake" {
		t.Fatalf("heartbeat missing identity: %+v", last)
	}
	if !strings.Contains(last.LastLine, "hello world") {
		t.Fatalf("expected last line to carry agent output, got %q", last.LastLine)
	}
	if last.OutputBytes == 0 {
		t.Fatal("expected non-zero output byte count")
	}
	// By the final pulse the agent has been silent a while — idle should be > 0.
	if last.IdleFor <= 0 {
		t.Fatalf("expected a positive idle duration on the trailing pulse, got %s", last.IdleFor)
	}
}

// TestActivityMeterLastLine unit-tests the line tracking the heartbeat reports:
// the last completed line wins, and an unterminated partial line still counts as
// activity (a progress bar without a newline isn't silence).
func TestActivityMeterLastLine(t *testing.T) {
	m := newActivityMeter()
	if _, err := m.Write([]byte("first\nsecond\n")); err != nil {
		t.Fatal(err)
	}
	if got := m.lastLine(); got != "second" {
		t.Fatalf("lastLine = %q, want %q", got, "second")
	}
	// A trailing partial line (no newline) becomes the visible activity.
	if _, err := m.Write([]byte("partial progress")); err != nil {
		t.Fatal(err)
	}
	if got := m.lastLine(); got != "partial progress" {
		t.Fatalf("lastLine = %q, want partial line", got)
	}
	if m.total() == 0 {
		t.Fatal("expected total bytes to accumulate")
	}
}
