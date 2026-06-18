package agent

import (
	"fmt"
	"strings"

	"github.com/berkaycubuk/fabrika/internal/model"
)

// RenderQuestionPrompt builds the prompt for an agent answering a question
// posted in a task's comment thread. The agent is read-only: it may inspect
// the repository checkout but must not change or merge code.
func RenderQuestionPrompt(t model.Task, thread []model.Comment) string {
	var b strings.Builder
	b.WriteString("# Question from a task comment thread\n\n")
	b.WriteString("A human has posted a question in a task's comment thread and you have been asked to answer it. ")
	b.WriteString("This is a read-only request: you may inspect the repository checkout you are running in, but you must not change or merge any code.\n\n")

	fmt.Fprintf(&b, "## Task: %s\n", t.Title)
	if t.Spec != "" {
		fmt.Fprintf(&b, "\n### Specification\n%s\n", t.Spec)
	}
	b.WriteString("\n")

	b.WriteString("## Comment thread\n")
	for _, c := range thread {
		var label string
		switch c.AuthorType {
		case "user":
			label = "Human"
		case "agent":
			label = c.AuthorID
			if label == "" {
				label = c.AuthorType
			}
		default:
			label = "System"
		}
		fmt.Fprintf(&b, "### %s\n%s\n\n", label, strings.TrimSpace(c.Body))
	}

	b.WriteString("## Rules\n")
	b.WriteString("- Answer the human's question (the final comment above) directly and in prose.\n")
	b.WriteString("- Do not modify, commit, or merge any files.\n")
	b.WriteString("- Everything you print to stdout becomes your reply shown in the thread.\n")
	fmt.Fprintf(&b, "- On completion, print your token usage on its own line: `%s {\"inputTokens\":N,\"outputTokens\":N,\"totalTokens\":N}`.\n", UsageMarker)
	return b.String()
}
