package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/berkaycubuk/fabrika/internal/model"
)

// ActivityEvent is a typed, human-readable activity derived from one line of the
// claude CLI's `--output-format stream-json --verbose` output. It is what the
// runner forwards to the UI to show what the agent is doing live.
type ActivityEvent struct {
	Type    string `json:"type"`    // one of: read|search|write|think|usage|tool
	Summary string `json:"summary"` // short human-readable summary, never empty when ok
	Ts      int64  `json:"ts"`      // unix millis; leave 0 here (the runner stamps it)
}

// activityMaxLen caps the length of a think summary so a long assistant message
// doesn't flood the activity stream.
const activityMaxLen = 200

// streamLine is the subset of a claude stream-json NDJSON line we care about.
type streamLine struct {
	Type    string         `json:"type"`
	Message *streamMessage `json:"message"`
	Usage   *streamUsage   `json:"usage"`
}

type streamMessage struct {
	Content []streamBlock `json:"content"`
}

type streamBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type streamUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// toolInput captures the input fields we summarize across the tools we map.
type toolInput struct {
	FilePath string `json:"file_path"`
	Pattern  string `json:"pattern"`
	Path     string `json:"path"`
	Query    string `json:"query"`
}

// ParseActivity parses ONE NDJSON line of claude stream-json output into a typed
// ActivityEvent. It returns ok=false for blank lines, non-JSON, and lines with
// no user-meaningful activity (system init lines, user tool_result lines, and
// assistant messages with no tool_use and only empty text).
//
// When an assistant content array has multiple blocks, the first tool_use is
// summarized; otherwise the first non-empty text block becomes a "think" event.
func ParseActivity(line []byte) (ActivityEvent, bool) {
	if len(bytes.TrimSpace(line)) == 0 {
		return ActivityEvent{}, false
	}
	var sl streamLine
	if err := json.Unmarshal(line, &sl); err != nil {
		return ActivityEvent{}, false
	}
	switch sl.Type {
	case "assistant":
		if sl.Message == nil {
			return ActivityEvent{}, false
		}
		for _, b := range sl.Message.Content {
			if b.Type == "tool_use" {
				return toolEvent(b), true
			}
		}
		for _, b := range sl.Message.Content {
			if b.Type == "text" {
				if t := strings.TrimSpace(b.Text); t != "" {
					return ActivityEvent{Type: "think", Summary: truncate(t, activityMaxLen)}, true
				}
			}
		}
		return ActivityEvent{}, false
	case "result":
		if sl.Usage == nil {
			return ActivityEvent{}, false
		}
		return ActivityEvent{
			Type:    "usage",
			Summary: fmt.Sprintf("%d in / %d out", sl.Usage.InputTokens, sl.Usage.OutputTokens),
		}, true
	}
	return ActivityEvent{}, false
}

// toolEvent maps a tool_use block to a typed activity. The Summary always falls
// back to the tool name so it is never empty.
func toolEvent(b streamBlock) ActivityEvent {
	var in toolInput
	if len(b.Input) > 0 {
		_ = json.Unmarshal(b.Input, &in)
	}
	switch b.Name {
	case "Read":
		return ActivityEvent{Type: "read", Summary: firstNonEmpty(in.FilePath, b.Name)}
	case "Grep", "Glob":
		return ActivityEvent{Type: "search", Summary: firstNonEmpty(in.Pattern, in.Path, in.Query, b.Name)}
	case "Write", "Edit", "NotebookEdit":
		return ActivityEvent{Type: "write", Summary: firstNonEmpty(in.FilePath, b.Name)}
	default:
		return ActivityEvent{Type: "tool", Summary: b.Name}
	}
}

// ParseStreamUsage extracts token usage from a {"type":"result",...} stream-json
// line carrying a `usage` object. It replaces the fabrika_USAGE stdout marker for
// agents that emit stream-json. TotalTokens is derived as input+output when the
// payload carries no explicit total (consistent with parseUsage). It returns
// ok=false for any non-result line or a result line with no usage.
func ParseStreamUsage(line []byte) (model.Usage, bool) {
	var sl streamLine
	if err := json.Unmarshal(line, &sl); err != nil {
		return model.Usage{}, false
	}
	if sl.Type != "result" || sl.Usage == nil {
		return model.Usage{}, false
	}
	u := model.Usage{
		InputTokens:  sl.Usage.InputTokens,
		OutputTokens: sl.Usage.OutputTokens,
		TotalTokens:  sl.Usage.TotalTokens,
	}
	if u.TotalTokens == 0 && (u.InputTokens != 0 || u.OutputTokens != 0) {
		u.TotalTokens = u.InputTokens + u.OutputTokens
	}
	return u, true
}

// firstNonEmpty returns the first non-empty (after trimming) string, or "".
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
