package ci

import (
	"context"
	"errors"
	"testing"

	"github.com/berkaycubuk/fabrika/internal/model"
)

// --- ParseRuns ---

func TestParseRuns_Empty(t *testing.T) {
	for _, input := range []string{"", "   ", "\t\n"} {
		runs, err := ParseRuns(input)
		if err != nil {
			t.Fatalf("ParseRuns(%q) error: %v", input, err)
		}
		if len(runs) != 0 {
			t.Fatalf("ParseRuns(%q) = %v, want []", input, runs)
		}
	}
}

func TestParseRuns_Valid(t *testing.T) {
	input := `[{"sha":"abc123","conclusion":"failure","url":"https://ci/1"},{"sha":"def456","conclusion":"success","url":""}]`
	runs, err := ParseRuns(input)
	if err != nil {
		t.Fatalf("ParseRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("got %d runs, want 2", len(runs))
	}
	if runs[0].SHA != "abc123" || runs[0].Conclusion != "failure" || runs[0].URL != "https://ci/1" {
		t.Fatalf("runs[0] = %+v", runs[0])
	}
	if runs[1].SHA != "def456" || runs[1].Conclusion != "success" {
		t.Fatalf("runs[1] = %+v", runs[1])
	}
}

func TestParseRuns_Invalid(t *testing.T) {
	_, err := ParseRuns("not json")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// --- Match ---

func TestMatch_Basic(t *testing.T) {
	tasks := []model.Task{
		{ID: "t1", MergeCommitSHA: "sha1"},
		{ID: "t2", MergeCommitSHA: "sha2"},
	}
	runs := []Run{
		{SHA: "sha1", Conclusion: "failure", URL: "https://ci/1"},
		{SHA: "sha2", Conclusion: "success", URL: "https://ci/2"},
	}
	updates := Match(runs, tasks)
	if len(updates) != 2 {
		t.Fatalf("got %d updates, want 2", len(updates))
	}
	if updates[0].TaskID != "t1" || updates[0].Status != "failure" || updates[0].RunURL != "https://ci/1" {
		t.Fatalf("updates[0] = %+v", updates[0])
	}
	if updates[1].TaskID != "t2" || updates[1].Status != "success" {
		t.Fatalf("updates[1] = %+v", updates[1])
	}
}

func TestMatch_NoMatchingSHA(t *testing.T) {
	tasks := []model.Task{{ID: "t1", MergeCommitSHA: "sha1"}}
	runs := []Run{{SHA: "other", Conclusion: "failure"}}
	updates := Match(runs, tasks)
	if len(updates) != 0 {
		t.Fatalf("expected 0 updates, got %v", updates)
	}
}

func TestMatch_SkipsEmptySHA(t *testing.T) {
	tasks := []model.Task{{ID: "t1", MergeCommitSHA: ""}}
	runs := []Run{{SHA: "", Conclusion: "failure"}}
	updates := Match(runs, tasks)
	if len(updates) != 0 {
		t.Fatalf("expected 0 updates for empty SHA, got %v", updates)
	}
}

func TestMatch_IgnoresUnknownConclusion(t *testing.T) {
	tasks := []model.Task{{ID: "t1", MergeCommitSHA: "sha1"}}
	runs := []Run{
		{SHA: "sha1", Conclusion: "cancelled"},
		{SHA: "sha1", Conclusion: "skipped"},
	}
	updates := Match(runs, tasks)
	if len(updates) != 0 {
		t.Fatalf("expected 0 updates for ignored conclusions, got %v", updates)
	}
}

func TestMatch_PreservesOrder(t *testing.T) {
	tasks := []model.Task{
		{ID: "ta", MergeCommitSHA: "sha-a"},
		{ID: "tb", MergeCommitSHA: "sha-b"},
	}
	runs := []Run{
		{SHA: "sha-b", Conclusion: "success"},
		{SHA: "sha-a", Conclusion: "failure"},
	}
	updates := Match(runs, tasks)
	if len(updates) != 2 {
		t.Fatalf("got %d updates, want 2", len(updates))
	}
	if updates[0].TaskID != "tb" || updates[1].TaskID != "ta" {
		t.Fatalf("order wrong: %v", updates)
	}
}

// --- Commander interface (stub) ---

type stubCommander struct {
	out string
	err error
}

func (s *stubCommander) RunCommand(_ context.Context, _, _ string, _ []string) (string, error) {
	return s.out, s.err
}

// --- NewPoller nil Emit ---

func TestNewPoller_NilEmit(t *testing.T) {
	p := NewPoller(Deps{Command: "echo"})
	if p.d.Emit == nil {
		t.Fatal("Emit should not be nil after NewPoller")
	}
	// must not panic
	p.d.Emit("test", nil)
}

// --- PollOnce command error ---

func TestPollOnce_CommandError(t *testing.T) {
	p := NewPoller(Deps{
		Cmd:     &stubCommander{err: errors.New("cmd failed")},
		Command: "somecommand",
	})
	err := p.PollOnce(context.Background())
	if err == nil {
		t.Fatal("expected error from PollOnce when command fails")
	}
}

// --- PollOnce malformed JSON ---

func TestPollOnce_MalformedJSON(t *testing.T) {
	p := NewPoller(Deps{
		Cmd:     &stubCommander{out: "not json"},
		Command: "somecommand",
	})
	err := p.PollOnce(context.Background())
	if err == nil {
		t.Fatal("expected error from PollOnce when JSON is malformed")
	}
}

// --- Start does nothing when Command is empty ---

func TestStart_NoopOnEmptyCommand(t *testing.T) {
	p := NewPoller(Deps{Command: ""})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()     // immediately cancelled
	p.Start(ctx) // must not launch a goroutine / must not panic
}
