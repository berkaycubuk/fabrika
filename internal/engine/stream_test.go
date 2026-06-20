package engine

import (
	"testing"
	"time"

	"github.com/berkaycubuk/fabrika/internal/agent"
	"github.com/berkaycubuk/fabrika/internal/model"
)

// TestStreamingImplementerStall proves the walk-away safety net survives the
// streaming dispatch path: an implementer whose command routes through RunStream
// (the command contains "stream-json", so StreamCommand reports stream=true) and
// then goes silent is killed by the runner's idle timeout, and the engine still
// records it as a failed task carrying a dedicated "liveness" evidence stage.
// Mirrors TestStalledAgentFailsAsLiveness but exercises the RunStream route.
func TestStreamingImplementerStall(t *testing.T) {
	eng, st, _ := setup(t)

	// Swap in a runner with a short idle timeout, heartbeats wired to the engine,
	// so the stall trips in milliseconds on the streaming path too.
	sp := agent.NewSubprocess()
	sp.IdleTimeout = 200 * time.Millisecond
	sp.Heartbeat = eng.onHeartbeat
	eng.agent = sp

	// Writes a file (so it's not an empty-diff failure), emits one stream-json
	// line, then hangs. The trailing "# stream-json" comment makes the fake
	// command still run while putting the substring StreamCommand keys on into the
	// command, so the engine dispatches it via RunStream.
	registerAgent(t, st, `printf 'work' > out.txt && echo '{"type":"system"}' && sleep 30 # stream-json`)

	task := &model.Task{Title: "streams then hangs", Spec: "do work then hang"}
	if err := st.Tasks.Create(task); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	if !eng.dispatchOnce() {
		t.Fatal("expected the task to be dispatched")
	}
	// The whole dispatch must finish near the idle timeout, not the 30s sleep.
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("dispatch took %s; a stalled streaming agent should be killed promptly", elapsed)
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

// streamActivityCmd emits one assistant stream-json line (which ParseActivity
// turns into a "think" event) and writes a file so the run produces a non-empty
// diff. The "stream-json" substring routes dispatch through RunStream.
const streamActivityCmd = `printf 'work' > out.txt && ` +
	`printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"thinking hard"}]}}' # stream-json`

// TestStreamingImplementerPersistsActivity proves the implementer activity the
// engine already emits over the WebSocket is ALSO persisted to TaskActivity, so
// a reload (or a late-joining client) can render the run's timeline — mirroring
// how the planner persists its own activity in plan.go.
func TestStreamingImplementerPersistsActivity(t *testing.T) {
	eng, st, _ := setup(t)
	registerAgent(t, st, streamActivityCmd)

	task := &model.Task{Title: "streams activity", Spec: "emit one activity line"}
	if err := st.Tasks.Create(task); err != nil {
		t.Fatal(err)
	}
	if !eng.dispatchOnce() {
		t.Fatal("expected the task to be dispatched")
	}

	acts, err := st.TaskActivity.List(task.ID)
	if err != nil {
		t.Fatalf("list task activity: %v", err)
	}
	var found bool
	for _, a := range acts {
		if a.Type == "think" && a.Summary == "thinking hard" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a persisted 'think' activity, got %v", acts)
	}
}

// TestStreamingRunResetsPriorActivity proves a fresh run wipes the previous
// run's timeline first, so a retry shows only the latest run — mirroring the
// planner's PlanActivity reset in plan.go. A stale entry seeded before dispatch
// must be gone, replaced solely by the new run's activity.
func TestStreamingRunResetsPriorActivity(t *testing.T) {
	eng, st, _ := setup(t)
	registerAgent(t, st, streamActivityCmd)

	task := &model.Task{Title: "resets activity", Spec: "emit one activity line"}
	if err := st.Tasks.Create(task); err != nil {
		t.Fatal(err)
	}
	// Seed a stale entry from a notional prior run; the next run must clear it.
	if err := st.TaskActivity.Append(task.ID, model.PlanActivity{Type: "read", Summary: "stale prior run"}); err != nil {
		t.Fatalf("seed stale activity: %v", err)
	}

	if !eng.dispatchOnce() {
		t.Fatal("expected the task to be dispatched")
	}

	acts, err := st.TaskActivity.List(task.ID)
	if err != nil {
		t.Fatalf("list task activity: %v", err)
	}
	for _, a := range acts {
		if a.Summary == "stale prior run" {
			t.Fatalf("stale activity from a prior run survived the reset: %v", acts)
		}
	}
	if len(acts) == 0 {
		t.Fatal("expected the fresh run's activity to be persisted")
	}
}
