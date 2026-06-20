package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/berkaycubuk/fabrika/internal/model"
)

// opencodeLine is the subset of an opencode `--format json` NDJSON line we care
// about. Each stdout line is one JSON object {"type":..., "part":{...}} with the
// top-level type one of step_start|tool_use|text|step_finish|error.
type opencodeLine struct {
	Type string        `json:"type"`
	Part *opencodePart `json:"part"`
}

type opencodePart struct {
	Type   string          `json:"type"`
	Tool   string          `json:"tool"`
	Text   string          `json:"text"`
	State  *opencodeState  `json:"state"`
	Tokens *opencodeTokens `json:"tokens"`
}

type opencodeState struct {
	Status string          `json:"status"`
	Input  json.RawMessage `json:"input"`
	Title  string          `json:"title"`
}

type opencodeTokens struct {
	Input  int `json:"input"`
	Output int `json:"output"`
	Total  int `json:"total"`
}

// opencodeToolInput captures the input fields we summarize across the tools we
// map from opencode's stream-json output.
type opencodeToolInput struct {
	FilePath string `json:"filePath"`
	Pattern  string `json:"pattern"`
	Path     string `json:"path"`
	Query    string `json:"query"`
	Command  string `json:"command"`
}

// ParseOpencodeActivity parses ONE NDJSON line of opencode `--format json` output
// into a typed ActivityEvent, mirroring ParseActivity's contract. It returns
// ok=false for blank/whitespace-only lines, non-JSON lines, and lines with no
// user-meaningful activity (step_start, error, and text parts whose trimmed text
// is empty).
func ParseOpencodeActivity(line []byte) (ActivityEvent, bool) {
	if len(bytes.TrimSpace(line)) == 0 {
		return ActivityEvent{}, false
	}
	var ol opencodeLine
	if err := json.Unmarshal(line, &ol); err != nil {
		return ActivityEvent{}, false
	}
	switch ol.Type {
	case "tool_use":
		if ol.Part == nil || ol.Part.Tool == "" {
			return ActivityEvent{}, false
		}
		return opencodeToolEvent(ol.Part), true
	case "text":
		if ol.Part == nil {
			return ActivityEvent{}, false
		}
		if t := strings.TrimSpace(ol.Part.Text); t != "" {
			return ActivityEvent{Type: "think", Summary: truncate(t, activityMaxLen)}, true
		}
		return ActivityEvent{}, false
	case "step_finish":
		if ol.Part == nil || ol.Part.Tokens == nil {
			return ActivityEvent{}, false
		}
		return ActivityEvent{
			Type:    "usage",
			Summary: fmt.Sprintf("%d in / %d out", ol.Part.Tokens.Input, ol.Part.Tokens.Output),
		}, true
	}
	return ActivityEvent{}, false
}

// opencodeToolEvent maps an opencode tool_use part to a typed activity. The
// Summary always falls back to the tool name so it is never empty.
func opencodeToolEvent(p *opencodePart) ActivityEvent {
	var in opencodeToolInput
	var title string
	if p.State != nil {
		title = p.State.Title
		if len(p.State.Input) > 0 {
			_ = json.Unmarshal(p.State.Input, &in)
		}
	}
	switch strings.ToLower(p.Tool) {
	case "read":
		return ActivityEvent{Type: "read", Summary: firstNonEmpty(in.FilePath, title, p.Tool)}
	case "write", "edit", "patch":
		return ActivityEvent{Type: "write", Summary: firstNonEmpty(in.FilePath, title, p.Tool)}
	case "grep", "glob":
		return ActivityEvent{Type: "search", Summary: firstNonEmpty(in.Pattern, in.Query, in.Path, title, p.Tool)}
	case "list":
		return ActivityEvent{Type: "search", Summary: firstNonEmpty(in.Path, title, p.Tool)}
	default:
		return ActivityEvent{Type: "tool", Summary: truncate(firstNonEmpty(in.Command, title, p.Tool), activityMaxLen)}
	}
}

// ParseOpencodeStreamUsage extracts token usage from a step_finish opencode line
// carrying a `tokens` object. It returns ok=false for any non-step_finish line or
// a step_finish with no part.tokens. TotalTokens is derived as input+output when
// the payload carries no explicit total (consistent with ParseStreamUsage).
func ParseOpencodeStreamUsage(line []byte) (model.Usage, bool) {
	var ol opencodeLine
	if err := json.Unmarshal(line, &ol); err != nil {
		return model.Usage{}, false
	}
	if ol.Type != "step_finish" || ol.Part == nil || ol.Part.Tokens == nil {
		return model.Usage{}, false
	}
	t := ol.Part.Tokens
	u := model.Usage{
		InputTokens:  t.Input,
		OutputTokens: t.Output,
		TotalTokens:  t.Total,
	}
	if u.TotalTokens == 0 && (u.InputTokens != 0 || u.OutputTokens != 0) {
		u.TotalTokens = u.InputTokens + u.OutputTokens
	}
	return u, true
}

// opencodeStreamText returns the verbatim text of a top-level "text" line so the
// streaming sink can accumulate a plain-text transcript that keeps fabrika_*
// markers parseable. It returns ok=false for any other line. The text is NOT
// trimmed — it is preserved verbatim.
func opencodeStreamText(line []byte) (string, bool) {
	var ol opencodeLine
	if err := json.Unmarshal(line, &ol); err != nil {
		return "", false
	}
	if ol.Type == "text" && ol.Part != nil && ol.Part.Text != "" {
		return ol.Part.Text, true
	}
	return "", false
}
