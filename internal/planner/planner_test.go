package planner

import (
	"strings"
	"testing"

	"github.com/berkaycubuk/fabrika/internal/model"
)

func TestParseExtractsJSONFromProse(t *testing.T) {
	out := `Sure! Here is the plan you asked for:

{
  "tasks": [
    {"title": "A", "spec": "do a", "acceptance": {"verifyCmds": ["go test ./..."]}},
    {"title": "B", "spec": "do b", "dependsOn": ["A"], "riskTier": "high"}
  ],
  "decisions": [
    {"question": "Which DB?", "options": ["sqlite", "postgres"]}
  ]
}

Hope that helps!`
	raw, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(raw.Tasks) != 2 || len(raw.Decisions) != 1 {
		t.Fatalf("parsed %d tasks, %d decisions", len(raw.Tasks), len(raw.Decisions))
	}
}

func TestParseFailsWithoutJSON(t *testing.T) {
	if _, err := Parse("no json here"); err == nil {
		t.Fatal("expected error when no JSON object is present")
	}
}

func TestBuildResolvesDepsAndDefaults(t *testing.T) {
	bt := model.BigTask{ID: "bt1", Title: "ship it"}
	raw := RawPlan{
		Tasks: []rawTask{
			{Title: "Schema", Spec: "create tables"},
			{Title: "API", Spec: "endpoints", DependsOn: []string{"schema"}, RiskTier: "weird"},
			{Title: "UI", DependsOn: []string{"2"}}, // index reference -> API
		},
		Decisions: []rawDecision{
			{Question: "Auth method?", Options: []string{"oauth", "password"}},
			{Question: "  "}, // blank -> dropped
		},
	}
	tasks, decisions := Build(bt, "plan1", raw)

	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(tasks))
	}
	for _, tk := range tasks {
		if tk.BigTaskID != "bt1" {
			t.Fatalf("task %q big task id = %q", tk.Title, tk.BigTaskID)
		}
		if tk.Status != model.TaskPlanned {
			t.Fatalf("task %q status = %q, want planned", tk.Title, tk.Status)
		}
		if tk.ID == "" {
			t.Fatalf("task %q has no ID", tk.Title)
		}
	}

	byTitle := map[string]model.Task{}
	for _, tk := range tasks {
		byTitle[tk.Title] = tk
	}
	// API depends on Schema (resolved by title, case-insensitive).
	if got := byTitle["API"].DependsOn; len(got) != 1 || got[0] != byTitle["Schema"].ID {
		t.Fatalf("API dependsOn = %v, want [%s]", got, byTitle["Schema"].ID)
	}
	// UI depends on API (resolved by 1-based index "2").
	if got := byTitle["UI"].DependsOn; len(got) != 1 || got[0] != byTitle["API"].ID {
		t.Fatalf("UI dependsOn = %v, want [%s]", got, byTitle["API"].ID)
	}
	// Unknown risk tier falls back to low.
	if byTitle["API"].RiskTier != model.RiskLow {
		t.Fatalf("API risk = %q, want low (invalid input)", byTitle["API"].RiskTier)
	}

	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision (blank dropped), got %d", len(decisions))
	}
	if decisions[0].PlanID != "plan1" || decisions[0].Status != model.DecisionOpen {
		t.Fatalf("decision = %+v", decisions[0])
	}
}

func TestRenderPromptIncludesSchemaAndPlanFile(t *testing.T) {
	bt := model.BigTask{Title: "X", Intent: "do x", Constraints: []string{"fast"}}
	p := RenderPrompt(bt, []model.Convention{{Rule: "use tabs"}}, "", "/tmp/plan.json", []string{"/repo/.fabrika/uploads/a.png"})
	for _, want := range []string{"do x", "fast", "use tabs", "/tmp/plan.json", "verifyCmds", "dependsOn", "/repo/.fabrika/uploads/a.png", "heldOutFiles"} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}
}

func TestParseHeldOutFiles(t *testing.T) {
	out := `{
  "tasks": [{
    "title": "T",
    "spec": "s",
    "acceptance": {
      "verifyCmds": ["true"],
      "heldOut": ["node --test test/heldout/x.test.ts"],
      "heldOutFiles": { "test/heldout/x.test.ts": "import {test} from \"node:test\";" }
    }
  }]
}`
	p, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	got := p.Tasks[0].Acceptance.HeldOutFiles
	if len(got) != 1 || !strings.Contains(got["test/heldout/x.test.ts"], "node:test") {
		t.Fatalf("heldOutFiles = %v", got)
	}
}
