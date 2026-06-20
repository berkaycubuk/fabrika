package agent

import (
	"encoding/json"
	"testing"
)

// buildOpencodeToolLine returns an opencode `--format json` NDJSON line for a
// tool_use part with the given tool name and state input/title.
func buildOpencodeToolLine(tool string, input map[string]any, title string) string {
	state := map[string]any{}
	if input != nil {
		raw, _ := json.Marshal(input)
		state["input"] = json.RawMessage(raw)
	}
	if title != "" {
		state["title"] = title
	}
	b, _ := json.Marshal(map[string]any{
		"type": "tool_use",
		"part": map[string]any{
			"type":  "tool",
			"tool":  tool,
			"state": state,
		},
	})
	return string(b)
}

// buildOpencodeTextLine returns an opencode text NDJSON line.
func buildOpencodeTextLine(text string) string {
	b, _ := json.Marshal(map[string]any{
		"type": "text",
		"part": map[string]any{"type": "text", "text": text},
	})
	return string(b)
}

// buildOpencodeStepFinishLine returns an opencode step_finish NDJSON line with
// the given token counts.
func buildOpencodeStepFinishLine(in, out, total int) string {
	b, _ := json.Marshal(map[string]any{
		"type": "step_finish",
		"part": map[string]any{
			"type": "step-finish",
			"tokens": map[string]any{
				"input":  in,
				"output": out,
				"total":  total,
			},
		},
	})
	return string(b)
}

func TestParseOpencodeActivity(t *testing.T) {
	cases := []struct {
		name        string
		line        string
		wantOK      bool
		wantType    string
		wantSummary string
	}{
		{
			name:        "read",
			line:        buildOpencodeToolLine("read", map[string]any{"filePath": "main.go"}, ""),
			wantOK:      true,
			wantType:    "read",
			wantSummary: "main.go",
		},
		{
			name:        "read falls back to title",
			line:        buildOpencodeToolLine("read", nil, "Read main.go"),
			wantOK:      true,
			wantType:    "read",
			wantSummary: "Read main.go",
		},
		{
			name:        "write",
			line:        buildOpencodeToolLine("write", map[string]any{"filePath": "out.go"}, ""),
			wantOK:      true,
			wantType:    "write",
			wantSummary: "out.go",
		},
		{
			name:        "edit",
			line:        buildOpencodeToolLine("edit", map[string]any{"filePath": "edit.go"}, ""),
			wantOK:      true,
			wantType:    "write",
			wantSummary: "edit.go",
		},
		{
			name:        "patch",
			line:        buildOpencodeToolLine("patch", map[string]any{"filePath": "patch.go"}, ""),
			wantOK:      true,
			wantType:    "write",
			wantSummary: "patch.go",
		},
		{
			name:        "grep",
			line:        buildOpencodeToolLine("grep", map[string]any{"pattern": "TODO"}, ""),
			wantOK:      true,
			wantType:    "search",
			wantSummary: "TODO",
		},
		{
			name:        "glob",
			line:        buildOpencodeToolLine("glob", map[string]any{"pattern": "**/*.go"}, ""),
			wantOK:      true,
			wantType:    "search",
			wantSummary: "**/*.go",
		},
		{
			name:        "list",
			line:        buildOpencodeToolLine("list", map[string]any{"path": "internal/"}, ""),
			wantOK:      true,
			wantType:    "search",
			wantSummary: "internal/",
		},
		{
			name:        "bash default uses bare tool name",
			line:        buildOpencodeToolLine("bash", map[string]any{"command": "go test ./..."}, ""),
			wantOK:      true,
			wantType:    "tool",
			wantSummary: "bash",
		},
		{
			name:        "uppercase tool name maps via lowercase",
			line:        buildOpencodeToolLine("Read", map[string]any{"filePath": "up.go"}, ""),
			wantOK:      true,
			wantType:    "read",
			wantSummary: "up.go",
		},
		{
			name:        "text becomes think",
			line:        buildOpencodeTextLine("  planning the change  "),
			wantOK:      true,
			wantType:    "think",
			wantSummary: "planning the change",
		},
		{
			name:        "step_finish becomes usage",
			line:        buildOpencodeStepFinishLine(100, 40, 140),
			wantOK:      true,
			wantType:    "usage",
			wantSummary: "100 in / 40 out",
		},
		{name: "blank line", line: "", wantOK: false},
		{name: "whitespace only", line: "   \t  ", wantOK: false},
		{name: "malformed json", line: "{not json", wantOK: false},
		{name: "empty text", line: buildOpencodeTextLine("   "), wantOK: false},
		{name: "step_start", line: `{"type":"step_start","part":{}}`, wantOK: false},
		{name: "error", line: `{"type":"error","part":{}}`, wantOK: false},
		{name: "tool_use nil part", line: `{"type":"tool_use"}`, wantOK: false},
		{name: "tool_use empty tool", line: `{"type":"tool_use","part":{"type":"tool"}}`, wantOK: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev, ok := ParseOpencodeActivity([]byte(tc.line))
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (event %+v)", ok, tc.wantOK, ev)
			}
			if !ok {
				return
			}
			if ev.Type != tc.wantType {
				t.Errorf("Type = %q, want %q", ev.Type, tc.wantType)
			}
			if ev.Summary != tc.wantSummary {
				t.Errorf("Summary = %q, want %q", ev.Summary, tc.wantSummary)
			}
			if ev.Summary == "" {
				t.Errorf("Summary must never be empty when ok")
			}
		})
	}
}

func TestParseOpencodeStreamUsage(t *testing.T) {
	cases := []struct {
		name      string
		line      string
		wantOK    bool
		wantIn    int
		wantOut   int
		wantTotal int
	}{
		{
			name:      "step_finish with total",
			line:      buildOpencodeStepFinishLine(100, 40, 140),
			wantOK:    true,
			wantIn:    100,
			wantOut:   40,
			wantTotal: 140,
		},
		{
			name:      "derives total when zero",
			line:      buildOpencodeStepFinishLine(100, 40, 0),
			wantOK:    true,
			wantIn:    100,
			wantOut:   40,
			wantTotal: 140,
		},
		{name: "text line", line: buildOpencodeTextLine("hi"), wantOK: false},
		{name: "tool_use line", line: buildOpencodeToolLine("read", map[string]any{"filePath": "a.go"}, ""), wantOK: false},
		{name: "step_finish no tokens", line: `{"type":"step_finish","part":{"type":"step-finish"}}`, wantOK: false},
		{name: "malformed json", line: "{nope", wantOK: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, ok := ParseOpencodeStreamUsage([]byte(tc.line))
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (usage %+v)", ok, tc.wantOK, u)
			}
			if !ok {
				return
			}
			if u.InputTokens != tc.wantIn || u.OutputTokens != tc.wantOut || u.TotalTokens != tc.wantTotal {
				t.Errorf("usage = %+v, want in=%d out=%d total=%d", u, tc.wantIn, tc.wantOut, tc.wantTotal)
			}
		})
	}
}

func TestOpencodeStreamText(t *testing.T) {
	if txt, ok := opencodeStreamText([]byte(buildOpencodeTextLine("  verbatim  "))); !ok || txt != "  verbatim  " {
		t.Errorf("opencodeStreamText = %q, %v; want verbatim text, true", txt, ok)
	}
	if _, ok := opencodeStreamText([]byte(buildOpencodeStepFinishLine(1, 2, 3))); ok {
		t.Errorf("opencodeStreamText on step_finish should be false")
	}
	if _, ok := opencodeStreamText([]byte(`{"type":"text","part":{"type":"text","text":""}}`)); ok {
		t.Errorf("opencodeStreamText on empty text should be false")
	}
}
