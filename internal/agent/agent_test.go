package agent

import (
	"strings"
	"testing"

	"github.com/berkaycubuk/fabrika/internal/model"
)

func TestRenderCommandModel(t *testing.T) {
	tmpl := "claude --prompt {prompt_file} --cwd {worktree} --model {model}"

	got := RenderCommand(tmpl, "/p/prompt.md", "/w/tree", "claude-sonnet-4-6")
	want := "claude --prompt /p/prompt.md --cwd /w/tree --model claude-sonnet-4-6"
	if got != want {
		t.Fatalf("RenderCommand = %q, want %q", got, want)
	}

	// Empty model substitutes to an empty string.
	got = RenderCommand(tmpl, "/p/prompt.md", "/w/tree", "")
	want = "claude --prompt /p/prompt.md --cwd /w/tree --model "
	if got != want {
		t.Fatalf("RenderCommand (empty model) = %q, want %q", got, want)
	}

	// Templates with no {model} token are unaffected (back-compat).
	noToken := "aider {prompt_file} {worktree}"
	got = RenderCommand(noToken, "/p/prompt.md", "/w/tree", "deepseek-chat")
	want = "aider /p/prompt.md /w/tree"
	if got != want {
		t.Fatalf("RenderCommand (no token) = %q, want %q", got, want)
	}
}

func TestRenderPromptCoAuthor(t *testing.T) {
	out := RenderPrompt(model.Task{Title: "x"}, nil, nil, nil, "")
	if !strings.Contains(out, "Co-authored-by: fabrika <noreply@fabrika-ai.com>") {
		t.Fatalf("RenderPrompt output missing fabrika co-author instruction:\n%s", out)
	}
}

func TestRenderPromptAttachments(t *testing.T) {
	out := RenderPrompt(model.Task{Title: "x"}, nil, []string{"/repo/.fabrika/uploads/a.png"}, nil, "")
	if !strings.Contains(out, "## Attached files") || !strings.Contains(out, "/repo/.fabrika/uploads/a.png") {
		t.Fatalf("RenderPrompt output missing attachment paths:\n%s", out)
	}
}

func TestParseEvidence(t *testing.T) {
	out := strings.Join([]string{
		"some build output",
		"fabrika_EVIDENCE: shots/login.png | login page after fix",
		"prefix fabrika_EVIDENCE: docs/run log.txt", // marker mid-line; path keeps its space
		"fabrika_EVIDENCE:   ",                      // empty path skipped
		"fabrika_EVIDENCE: demo.mp4",                // no caption
	}, "\n")
	want := []EvidenceRef{
		{Path: "shots/login.png", Caption: "login page after fix"},
		{Path: "docs/run log.txt"},
		{Path: "demo.mp4"},
	}
	got := parseEvidence(out)
	if len(got) != len(want) {
		t.Fatalf("parseEvidence = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parseEvidence[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestRenderPromptEvidenceRule(t *testing.T) {
	out := RenderPrompt(model.Task{Title: "x"}, nil, nil, nil, "")
	if !strings.Contains(out, EvidenceMarker) {
		t.Fatalf("RenderPrompt output missing evidence marker instruction:\n%s", out)
	}
}

func TestRenderPromptUsageRule(t *testing.T) {
	out := RenderPrompt(model.Task{Title: "x"}, nil, nil, nil, "")
	if !strings.Contains(out, UsageMarker) {
		t.Fatalf("RenderPrompt output missing usage marker instruction:\n%s", out)
	}
}

func TestParseUsage(t *testing.T) {
	out := strings.Join([]string{
		"some build output",
		`fabrika_USAGE: {"inputTokens":100,"outputTokens":50,"totalTokens":150}`,
	}, "\n")
	got, ok := parseUsage(out)
	if !ok {
		t.Fatalf("parseUsage: ok=false, want true")
	}
	want := model.Usage{InputTokens: 100, OutputTokens: 50, TotalTokens: 150}
	if got != want {
		t.Fatalf("parseUsage = %+v, want %+v", got, want)
	}
}

func TestParseUsageLastWins(t *testing.T) {
	out := strings.Join([]string{
		`fabrika_USAGE: {"inputTokens":1,"outputTokens":2,"totalTokens":3}`,
		`prefix fabrika_USAGE: {"inputTokens":10,"outputTokens":20,"totalTokens":30}`,
	}, "\n")
	got, ok := parseUsage(out)
	if !ok {
		t.Fatalf("parseUsage: ok=false, want true")
	}
	want := model.Usage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30}
	if got != want {
		t.Fatalf("parseUsage = %+v, want %+v", got, want)
	}
}

func TestParseUsageMalformedSkipped(t *testing.T) {
	if _, ok := parseUsage("fabrika_USAGE: not json"); ok {
		t.Fatalf("parseUsage(malformed): ok=true, want false")
	}
	if _, ok := parseUsage("no marker here"); ok {
		t.Fatalf("parseUsage(absent): ok=true, want false")
	}
}

func TestParseUsageTotalDerivation(t *testing.T) {
	out := `fabrika_USAGE: {"inputTokens":100,"outputTokens":50}`
	got, ok := parseUsage(out)
	if !ok {
		t.Fatalf("parseUsage: ok=false, want true")
	}
	if got.TotalTokens != 150 {
		t.Fatalf("parseUsage TotalTokens = %d, want 150 (derived)", got.TotalTokens)
	}
}

func TestRenderPromptGuidanceAndLastFailure(t *testing.T) {
	out := RenderPrompt(model.Task{Title: "x"}, nil, nil,
		[]string{"use the existing el() helper", "don't add a scrollbar"},
		`stage "verify" failed:`+"\nCould not find 'test/heldout/x.ts'")
	for _, want := range []string{
		"## Guidance from the human",
		"use the existing el() helper",
		"don't add a scrollbar",
		"## Previous attempt failed",
		"Could not find 'test/heldout/x.ts'",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("prompt missing %q:\n%s", want, out)
		}
	}

	// Both sections are omitted when there is nothing to say.
	out = RenderPrompt(model.Task{Title: "x"}, nil, nil, nil, "")
	if strings.Contains(out, "Guidance from the human") || strings.Contains(out, "Previous attempt failed") {
		t.Fatalf("empty guidance/failure should render no sections:\n%s", out)
	}
}

func TestRoutePriority(t *testing.T) {
	impl := []string{model.RoleImplementer}

	ag := func(id string, priority int, tags []string) model.Agent {
		return model.Agent{ID: id, Name: id, Roles: impl, Enabled: true, Priority: priority, Tags: tags}
	}

	// Tag-matching beats priority for subset selection:
	// even though "c" has highest priority (99), "a" matches the tag "go"
	// and is chosen from the tag-overlapping subset.
	agents := []model.Agent{
		ag("a", 0, []string{"go"}),
		ag("b", 5, []string{"js"}),
		ag("c", 99, nil),
	}
	task := model.Task{Title: "go-task", Tags: []string{"go"}}
	got := Route(task, agents, nil)
	if got == nil || got.ID != "a" {
		t.Fatalf("tag-matching agent should win despite lower priority: got %v", got)
	}

	// Within tag-overlapping subset, highest priority wins.
	agents2 := []model.Agent{
		ag("low", 0, []string{"go"}),
		ag("high", 7, []string{"go"}),
	}
	got2 := Route(model.Task{Tags: []string{"go"}}, agents2, nil)
	if got2 == nil || got2.ID != "high" {
		t.Fatalf("within tag-matching subset highest priority should win: got %v", got2)
	}

	// Without tag overlap, highest priority from all eligible wins.
	agents3 := []model.Agent{
		ag("low", 0, nil),
		ag("mid", 5, nil),
		ag("top", 10, nil),
	}
	got3 := Route(model.Task{Title: "any"}, agents3, nil)
	if got3 == nil || got3.ID != "top" {
		t.Fatalf("without tag match highest priority should win: got %v", got3)
	}

	// Equal priorities: first in slice order wins (backward compatible).
	agents4 := []model.Agent{
		ag("first", 0, nil),
		ag("second", 0, nil),
	}
	got4 := Route(model.Task{Title: "any"}, agents4, nil)
	if got4 == nil || got4.ID != "first" {
		t.Fatalf("equal priority should pick first in slice order: got %v", got4)
	}

	// PreferredAgentID pin still takes precedence over priority.
	agents5 := []model.Agent{
		ag("pinned", 0, nil),
		ag("high", 99, nil),
	}
	got5 := Route(model.Task{PreferredAgentID: "pinned"}, agents5, nil)
	if got5 == nil || got5.ID != "pinned" {
		t.Fatalf("pin should take precedence over priority: got %v", got5)
	}

	// Priority does not override concurrency limits: busy agent skipped.
	agents6 := []model.Agent{
		ag("busy", 99, nil),
		ag("free", 0, nil),
	}
	got6 := Route(model.Task{Title: "any"}, agents6, map[string]int{"busy": 0, "free": 1})
	if got6 == nil || got6.ID != "free" {
		t.Fatalf("busy agent should be skipped despite priority: got %v", got6)
	}
}
