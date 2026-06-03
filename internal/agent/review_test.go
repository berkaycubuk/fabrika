package agent

import (
	"strings"
	"testing"

	"github.com/berkaycubuk/fabrika/internal/model"
)

func TestParseReview(t *testing.T) {
	cases := []struct {
		name        string
		out         string
		wantOK      bool
		wantApprove bool
		wantNotes   string
	}{
		{"approve", "thinking...\nfabrika_REVIEW: {\"approve\": true, \"notes\": \"lgtm\"}", true, true, "lgtm"},
		{"reject", "fabrika_REVIEW: {\"approve\": false, \"notes\": \"missing test\"}", true, false, "missing test"},
		{"last wins", "fabrika_REVIEW: {\"approve\": false}\nfabrika_REVIEW: {\"approve\": true}", true, true, ""},
		{"no marker", "I think it's fine", false, false, ""},
		{"malformed", "fabrika_REVIEW: not json", false, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, ok := ParseReview(tc.out)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && (v.Approve != tc.wantApprove || v.Notes != tc.wantNotes) {
				t.Fatalf("verdict = %+v, want approve=%v notes=%q", v, tc.wantApprove, tc.wantNotes)
			}
		})
	}
}

func TestRenderReviewPromptIncludesDiffAndMarker(t *testing.T) {
	task := model.Task{Title: "add login", Spec: "build login", Acceptance: model.Contract{VerifyCmds: []string{"go test ./..."}}}
	p := RenderReviewPrompt(task, "diff --git a/x b/x\n+code", nil)
	for _, want := range []string{"add login", "build login", "go test ./...", "diff --git", ReviewMarker, "Do NOT modify"} {
		if !strings.Contains(p, want) {
			t.Errorf("review prompt missing %q", want)
		}
	}
}
