package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/berkaycubuk/fabrika/internal/model"
)

// buildAssistantLine returns a stream-json NDJSON line for an assistant message
// with a single text block containing text.
func buildAssistantLine(text string) string {
	b, _ := json.Marshal(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"content": []any{
				map[string]any{"type": "text", "text": text},
			},
		},
	})
	return string(b)
}

// buildResultLine returns a stream-json NDJSON result line carrying usage.
func buildResultLine(in, out, total int) string {
	b, _ := json.Marshal(map[string]any{
		"type": "result",
		"usage": map[string]any{
			"input_tokens":  in,
			"output_tokens": out,
			"total_tokens":  total,
		},
	})
	return string(b)
}

// TestStreamSinkTranscript verifies that streamSink accumulates assistant text
// blocks into a plain-text transcript that the marker parsers can read.
func TestStreamSinkTranscript(t *testing.T) {
	sink := &streamSink{}

	lines := []string{
		buildAssistantLine(`fabrika_DECISION: {"question":"which way?","options":["left","right"]}`),
		buildAssistantLine("fabrika_COMMENT: looks good"),
		buildAssistantLine("fabrika_EVIDENCE: out/result.png | final screenshot"),
		buildResultLine(100, 40, 140),
	}
	for _, l := range lines {
		if _, err := sink.Write([]byte(l + "\n")); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	sink.flush()

	transcript := sink.transcript.String()

	q, ok := parseEscalation(transcript)
	if !ok {
		t.Fatalf("parseEscalation on transcript: ok=false; transcript=%q", transcript)
	}
	if !strings.Contains(q, "which way?") {
		t.Errorf("parseEscalation payload=%q, missing expected question text", q)
	}

	comments := parseComments(transcript)
	if len(comments) != 1 || comments[0] != "looks good" {
		t.Errorf("parseComments=%v, want [\"looks good\"]", comments)
	}

	evidence := parseEvidence(transcript)
	if len(evidence) != 1 || evidence[0].Path != "out/result.png" || evidence[0].Caption != "final screenshot" {
		t.Errorf("parseEvidence=%v, want [{out/result.png, final screenshot}]", evidence)
	}

	if !sink.haveUsage || sink.usage.InputTokens != 100 || sink.usage.OutputTokens != 40 || sink.usage.TotalTokens != 140 {
		t.Errorf("sink usage=%+v haveUsage=%v, want {100,40,140}", sink.usage, sink.haveUsage)
	}
}

// TestRunStreamResultParity verifies that RunStream populates Escalated,
// Decision, Comments, Evidence, and Usage identically to Run when the agent
// emits the markers inside stream-json assistant text blocks.
func TestRunStreamResultParity(t *testing.T) {
	dir := t.TempDir()

	ndjson := strings.Join([]string{
		buildAssistantLine(`fabrika_DECISION: {"question":"which approach?","options":["fast","safe"]}`),
		buildAssistantLine("fabrika_COMMENT: task complete"),
		buildAssistantLine("fabrika_EVIDENCE: proof.txt | the log"),
		buildResultLine(200, 80, 280),
	}, "\n") + "\n"

	ndjsonPath := filepath.Join(dir, "stream.ndjson")
	if err := os.WriteFile(ndjsonPath, []byte(ndjson), 0o644); err != nil {
		t.Fatal(err)
	}

	sub := NewSubprocess()
	sub.IdleTimeout = 0 // disable stall detection for deterministic test
	a := model.Agent{
		Name:    "test-stream",
		Command: "cat '" + ndjsonPath + "'",
	}
	task := model.Task{ID: "t1", Title: "parity test"}

	res, err := sub.RunStream(context.Background(), a, task, dir, "", nil)
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	if !res.Escalated {
		t.Errorf("Escalated=false, want true")
	}
	if !strings.Contains(res.Decision, "which approach?") {
		t.Errorf("Decision=%q, missing expected question text", res.Decision)
	}
	if len(res.Comments) != 1 || res.Comments[0] != "task complete" {
		t.Errorf("Comments=%v, want [\"task complete\"]", res.Comments)
	}
	if len(res.Evidence) != 1 || res.Evidence[0].Path != "proof.txt" || res.Evidence[0].Caption != "the log" {
		t.Errorf("Evidence=%v, want [{proof.txt, the log}]", res.Evidence)
	}
	if res.Usage.InputTokens != 200 || res.Usage.OutputTokens != 80 || res.Usage.TotalTokens != 280 {
		t.Errorf("Usage=%+v, want {200,80,280}", res.Usage)
	}
}

// TestRunStreamUsageFallback verifies that when no stream result event appears,
// RunStream falls back to parseUsage over the transcript (fabrika_USAGE marker).
func TestRunStreamUsageFallback(t *testing.T) {
	dir := t.TempDir()

	ndjson := buildAssistantLine(`fabrika_USAGE: {"inputTokens":10,"outputTokens":5,"totalTokens":15}`) + "\n"
	ndjsonPath := filepath.Join(dir, "stream.ndjson")
	if err := os.WriteFile(ndjsonPath, []byte(ndjson), 0o644); err != nil {
		t.Fatal(err)
	}

	sub := NewSubprocess()
	sub.IdleTimeout = 0
	a := model.Agent{
		Name:    "test-usage-fallback",
		Command: "cat '" + ndjsonPath + "'",
	}
	task := model.Task{ID: "t2", Title: "usage fallback test"}

	res, err := sub.RunStream(context.Background(), a, task, dir, "", nil)
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if res.Usage.InputTokens != 10 || res.Usage.OutputTokens != 5 || res.Usage.TotalTokens != 15 {
		t.Errorf("Usage=%+v, want {10,5,15} via transcript fallback", res.Usage)
	}
}

// TestStreamSinkEmbeddedNewlines verifies that embedded newlines in assistant
// text blocks are preserved verbatim so markers on non-first lines are found.
func TestStreamSinkEmbeddedNewlines(t *testing.T) {
	sink := &streamSink{}
	// Text block where the marker is on the second line (embedded newline).
	text := "some preamble\nfabrika_COMMENT: embedded newline comment"
	_, _ = sink.Write([]byte(buildAssistantLine(text) + "\n"))
	sink.flush()

	comments := parseComments(sink.transcript.String())
	if len(comments) != 1 || comments[0] != "embedded newline comment" {
		t.Errorf("parseComments=%v, want [\"embedded newline comment\"]", comments)
	}
}
