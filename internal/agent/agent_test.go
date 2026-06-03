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
	out := RenderPrompt(model.Task{Title: "x"}, nil, nil)
	if !strings.Contains(out, "Co-authored-by: fabrika <fabrika@berkaycubuk.com>") {
		t.Fatalf("RenderPrompt output missing fabrika co-author instruction:\n%s", out)
	}
}

func TestRenderPromptAttachments(t *testing.T) {
	out := RenderPrompt(model.Task{Title: "x"}, nil, []string{"/repo/.fabrika/uploads/a.png"})
	if !strings.Contains(out, "## Attached images") || !strings.Contains(out, "/repo/.fabrika/uploads/a.png") {
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
	out := RenderPrompt(model.Task{Title: "x"}, nil, nil)
	if !strings.Contains(out, EvidenceMarker) {
		t.Fatalf("RenderPrompt output missing evidence marker instruction:\n%s", out)
	}
}
