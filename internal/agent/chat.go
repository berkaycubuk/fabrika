package agent

import (
	"fmt"
	"strings"

	"github.com/berkaycubuk/fabrika/internal/model"
)

// RenderChatPrompt builds the prompt file for one interactive session turn.
// Sessions reuse the agent's existing one-shot Command template: each turn the
// full conversation transcript is replayed into the prompt, the agent works in
// the session's worktree, and its stdout becomes the reply shown to the human.
// The transcript must already include the human's newest message (last entry).
// staged maps a message ID to the in-worktree paths of its image attachments
// (mockups, screenshots), referenced under that message so the agent reads them.
func RenderChatPrompt(transcript []model.SessionMessage, conventions []model.Convention, staged map[string][]string) string {
	var b strings.Builder
	b.WriteString("# Interactive session\n\n")
	b.WriteString("You are a coding agent in a live chat session with a human, working in this repository checkout. ")
	b.WriteString("This is ad-hoc work (bug fixes, small features) steered turn by turn — there is no task spec beyond the conversation. ")
	b.WriteString("Earlier turns in this session already ran in this same checkout, so changes you see on disk are your own prior work.\n\n")

	if len(conventions) > 0 {
		b.WriteString("## Conventions\n")
		for _, c := range conventions {
			fmt.Fprintf(&b, "  - %s\n", c.Rule)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Conversation so far\n")
	for _, m := range transcript {
		var label string
		switch m.Role {
		case model.SessionRoleUser:
			label = "Human"
		case model.SessionRoleAgent:
			label = "You"
		default:
			label = "System"
		}
		fmt.Fprintf(&b, "### %s\n%s\n", label, strings.TrimSpace(m.Body))
		if paths := staged[m.ID]; len(paths) > 0 {
			b.WriteString("Attached images — read these files for context (mockups, screenshots, diagrams):\n")
			for _, p := range paths {
				fmt.Fprintf(&b, "  - %s\n", p)
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("## Rules\n")
	b.WriteString("- Act on the human's latest message (the last entry above).\n")
	b.WriteString("- Everything you print to stdout is shown to the human as your chat reply. End with a short summary of what you did and what changed (files touched, commands run). Ask questions directly in your reply when you need a decision.\n")
	b.WriteString("- Make your changes in this checkout. You may commit them on this branch; anything left uncommitted is committed automatically when the human finishes the session.\n")
	fmt.Fprintf(&b, "- On completion, print your token usage on its own line: `%s {\"inputTokens\":N,\"outputTokens\":N,\"totalTokens\":N}`.\n", UsageMarker)
	return b.String()
}

// CleanChatReply strips Fabrika's stdout sentinel lines (usage, evidence,
// comments, decisions) from an agent's chat output so the reply shown in the
// conversation is just the agent's prose. Whole lines containing a marker are
// dropped; surrounding blank runs collapse.
func CleanChatReply(out string) string {
	markers := []string{DecisionMarker, CommentMarker, EvidenceMarker, ReviewMarker, UsageMarker}
	var kept []string
	for _, line := range strings.Split(out, "\n") {
		drop := false
		for _, m := range markers {
			if strings.Contains(line, m) {
				drop = true
				break
			}
		}
		if !drop {
			kept = append(kept, line)
		}
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}
