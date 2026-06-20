package agent

import (
	"path/filepath"
	"strings"
)

// StreamCommand decides whether a rendered agent command should run via the
// streaming (stream-json) path and returns the command to actually execute.
//
// This is the engine-injection decision: the engine injects the flags for
// claude commands rather than requiring the operator to edit each agent, so
// existing claude agents stream automatically.
func StreamCommand(command string) (cmd string, stream bool) {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return "", false
	}

	// Already requests stream-json: idempotent, leave it untouched.
	if strings.Contains(trimmed, "stream-json") {
		return command, true
	}

	if invokesClaude(trimmed) {
		return command + " --output-format stream-json --verbose", true
	}

	// opencode and any other non-claude CLI keep running via buffered Run.
	return command, false
}

// invokesClaude reports whether a (trimmed, non-empty) command runs claude:
// either the program's basename is exactly "claude", or the command otherwise
// mentions the word claude.
func invokesClaude(trimmed string) bool {
	if prog := strings.Fields(trimmed); len(prog) > 0 {
		if filepath.Base(prog[0]) == "claude" {
			return true
		}
	}
	return strings.Contains(trimmed, "claude")
}
