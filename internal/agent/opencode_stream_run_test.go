package agent

import (
	"strings"
	"testing"
)

// feedOpencode writes each line (newline-terminated) through the sink, then
// flushes, matching how RunStream drives the sink from exec's copier.
func feedOpencode(t *testing.T, sink *streamSink, lines []string) {
	t.Helper()
	for _, l := range lines {
		if _, err := sink.Write([]byte(l + "\n")); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	sink.flush()
}

// TestOpencodeStreamSinkActivity verifies that an opencode-format streamSink
// fires onActivity with the right typed events for tool_use/text/step_finish
// lines, and that each event carries a stamped timestamp.
func TestOpencodeStreamSinkActivity(t *testing.T) {
	var got []ActivityEvent
	sink := &streamSink{
		format:     "opencode",
		onActivity: func(ev ActivityEvent) { got = append(got, ev) },
	}

	lines := []string{
		buildOpencodeToolLine("read", map[string]any{"filePath": "main.go"}, ""),
		buildOpencodeToolLine("edit", map[string]any{"filePath": "out.go"}, ""),
		buildOpencodeToolLine("grep", map[string]any{"pattern": "TODO"}, ""),
		buildOpencodeTextLine("thinking about the plan"),
		buildOpencodeStepFinishLine(100, 40, 140),
	}
	feedOpencode(t, sink, lines)

	want := []struct {
		typ     string
		summary string
	}{
		{"read", "main.go"},
		{"write", "out.go"},
		{"search", "TODO"},
		{"think", "thinking about the plan"},
		{"usage", "100 in / 40 out"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d activity events, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Type != w.typ || got[i].Summary != w.summary {
			t.Errorf("event[%d]=%+v, want type=%q summary=%q", i, got[i], w.typ, w.summary)
		}
		if got[i].Ts == 0 {
			t.Errorf("event[%d] Ts not stamped", i)
		}
	}
}

// TestOpencodeStreamSinkUsageAccumulates verifies that usage is summed across
// multiple step_finish lines (unlike claude's last-wins absolute usage), with
// the last cumulative total winning.
func TestOpencodeStreamSinkUsageAccumulates(t *testing.T) {
	sink := &streamSink{format: "opencode"}

	lines := []string{
		buildOpencodeStepFinishLine(100, 40, 140),
		buildOpencodeStepFinishLine(50, 20, 210),
		buildOpencodeStepFinishLine(30, 10, 250),
	}
	feedOpencode(t, sink, lines)

	if !sink.haveUsage {
		t.Fatalf("haveUsage=false, want true")
	}
	if sink.usage.InputTokens != 180 {
		t.Errorf("InputTokens=%d, want 180 (100+50+30)", sink.usage.InputTokens)
	}
	if sink.usage.OutputTokens != 70 {
		t.Errorf("OutputTokens=%d, want 70 (40+20+10)", sink.usage.OutputTokens)
	}
	if sink.usage.TotalTokens != 250 {
		t.Errorf("TotalTokens=%d, want 250 (last cumulative total)", sink.usage.TotalTokens)
	}
}

// TestOpencodeStreamSinkMarkerTranscript verifies that fabrika_COMMENT and
// fabrika_EVIDENCE markers emitted inside opencode `text` parts surface through
// the transcript for the existing marker parsers.
func TestOpencodeStreamSinkMarkerTranscript(t *testing.T) {
	sink := &streamSink{format: "opencode"}

	lines := []string{
		buildOpencodeTextLine("fabrika_COMMENT: looks good"),
		buildOpencodeTextLine("fabrika_EVIDENCE: out/result.png | final screenshot"),
	}
	feedOpencode(t, sink, lines)

	transcript := sink.transcript.String()

	comments := parseComments(transcript)
	if len(comments) != 1 || comments[0] != "looks good" {
		t.Errorf("parseComments=%v, want [\"looks good\"]", comments)
	}

	evidence := parseEvidence(transcript)
	if len(evidence) != 1 || evidence[0].Path != "out/result.png" || evidence[0].Caption != "final screenshot" {
		t.Errorf("parseEvidence=%v, want [{out/result.png, final screenshot}]", evidence)
	}
}

// TestStreamFormat verifies streamFormat routes opencode commands to the
// opencode parser and everything else to claude.
func TestStreamFormat(t *testing.T) {
	cases := []struct {
		command string
		want    string
	}{
		{"opencode run -p hi --format json", "opencode"},
		{"opencode run -p hi", "opencode"},
		{"/usr/local/bin/opencode run", "opencode"},
		{"claude -p hi --output-format stream-json --verbose", "claude"},
		{"some-other-agent --go", "claude"},
		{"", "claude"},
	}
	for _, c := range cases {
		if got := streamFormat(c.command); got != c.want {
			t.Errorf("streamFormat(%q)=%q, want %q", c.command, got, c.want)
		}
	}
}

// TestOpencodeStreamSinkText ensures opencode text parts that do not already end
// in a newline get one appended so markers stay on their own line.
func TestOpencodeStreamSinkText(t *testing.T) {
	sink := &streamSink{format: "opencode"}
	// Two separate text parts without trailing newlines; each must end up on its
	// own line in the transcript.
	feedOpencode(t, sink, []string{
		buildOpencodeTextLine("fabrika_COMMENT: first"),
		buildOpencodeTextLine("fabrika_COMMENT: second"),
	})
	if got := strings.Count(sink.transcript.String(), "\n"); got != 2 {
		t.Errorf("transcript newline count=%d, want 2; transcript=%q", got, sink.transcript.String())
	}
}
