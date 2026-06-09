package engine

import (
	"testing"
	"time"

	"github.com/berkaycubuk/fabrika/internal/agent"
	"github.com/berkaycubuk/fabrika/internal/model"
)

// TestStalledAgentFailsAsLiveness proves the walk-away safety net end to end: an
// agent that produces output then goes silent is killed by the runner's idle
// timeout, and the engine records it as a failed task carrying a dedicated
// "liveness" evidence stage — so a hung agent surfaces as a clear failure in the
// Accept queue instead of silently occupying a slot until the hard timeout.
func TestStalledAgentFailsAsLiveness(t *testing.T) {
	eng, st, _ := setup(t)

	// Swap in a runner with a short idle timeout, heartbeats wired to the engine
	// (exercising onHeartbeat too), so the stall trips in milliseconds.
	sp := agent.NewSubprocess()
	sp.IdleTimeout = 200 * time.Millisecond
	sp.Heartbeat = eng.onHeartbeat
	eng.agent = sp

	// Writes a file (so it's not an empty-diff failure), prints a line, then hangs.
	registerAgent(t, st, "printf 'work' > out.txt && echo starting && sleep 30")

	task := &model.Task{Title: "hangs midway", Spec: "do work then hang"}
	if err := st.Tasks.Create(task); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	if !eng.dispatchOnce() {
		t.Fatal("expected the task to be dispatched")
	}
	// The whole dispatch must finish near the idle timeout, not the 30s sleep.
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("dispatch took %s; a stalled agent should be killed promptly", elapsed)
	}

	got, _ := st.Tasks.Get(task.ID)
	if got.Status != model.TaskFailed {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	att, err := st.Attempts.LatestForTask(task.ID)
	if err != nil {
		t.Fatalf("attempt: %v", err)
	}
	if att.Result != model.ResultFail {
		t.Fatalf("result = %q, want fail", att.Result)
	}
	stage, ok := att.Evidence.Stages["liveness"]
	if !ok {
		t.Fatalf("expected a 'liveness' evidence stage, got stages %v", att.Evidence.Stages)
	}
	if stage.Pass {
		t.Fatal("liveness stage should be a failure")
	}
}
