package agent

import (
	"strings"
	"testing"

	"github.com/berkaycubuk/fabrika/internal/model"
)

func TestRenderPromptCoAuthor(t *testing.T) {
	out := RenderPrompt(model.Task{Title: "x"}, nil)
	if !strings.Contains(out, "Co-authored-by: fabrika <fabrika@berkaycubuk.com>") {
		t.Fatalf("RenderPrompt output missing fabrika co-author instruction:\n%s", out)
	}
}
