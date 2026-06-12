package agent

import (
	"strings"
	"testing"

	"github.com/berkaycubuk/fabrika/internal/model"
)

func TestRenderChatPrompt(t *testing.T) {
	transcript := []model.SessionMessage{
		{ID: "m1", Role: model.SessionRoleUser, Body: "fix the banner overlap", Attachments: []string{"/api/uploads/shot.png"}},
		{Role: model.SessionRoleAgent, Body: "Done — adjusted styles.css"},
		{Role: model.SessionRoleSystem, Body: "gate failed: lint"},
		{Role: model.SessionRoleUser, Body: "lint failed, fix it"},
	}
	conventions := []model.Convention{{Rule: "use tabs"}}
	staged := map[string][]string{"m1": {".fabrika/attachments/shot.png"}}

	got := RenderChatPrompt(transcript, conventions, staged)

	for _, want := range []string{
		"### Human\nfix the banner overlap",
		"### You\nDone — adjusted styles.css",
		"### System\ngate failed: lint",
		"lint failed, fix it",
		"use tabs",
		"Attached images",
		".fabrika/attachments/shot.png",
		UsageMarker,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q\n---\n%s", want, got)
		}
	}
}

func TestRenderChatPromptNoConventions(t *testing.T) {
	got := RenderChatPrompt([]model.SessionMessage{{Role: "user", Body: "hi"}}, nil, nil)
	if strings.Contains(got, "## Conventions") {
		t.Errorf("empty conventions should omit the section:\n%s", got)
	}
	if strings.Contains(got, "Attached images") {
		t.Errorf("no attachments should omit the image hint:\n%s", got)
	}
}

func TestCleanChatReply(t *testing.T) {
	out := strings.Join([]string{
		"I fixed the overlap.",
		"",
		UsageMarker + ` {"inputTokens":1,"outputTokens":2}`,
		"Changed: web/src/style.css",
		CommentMarker + " side note",
	}, "\n")
	got := CleanChatReply(out)
	want := "I fixed the overlap.\n\nChanged: web/src/style.css"
	if got != want {
		t.Errorf("CleanChatReply = %q, want %q", got, want)
	}
}
