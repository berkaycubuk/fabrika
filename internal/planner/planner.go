// Package planner turns a BigTask into a task DAG plus contracts and open
// decisions. Phase 2 assigns an agent the planner role: it reads the intent and
// emits a structured plan (JSON) which this package parses into model types.
// When no planner agent is configured the engine falls back to Passthrough.
// See SPECS.md §7, §13.
package planner

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/google/uuid"
)

// RawPlan is the JSON contract the planner agent writes. Task dependencies are
// expressed by referencing another task's title (or 1-based index) so the agent
// never has to invent IDs; Build resolves them to task IDs.
type RawPlan struct {
	Tasks     []rawTask     `json:"tasks"`
	Decisions []rawDecision `json:"decisions"`
}

type rawTask struct {
	Title      string         `json:"title"`
	Spec       string         `json:"spec"`
	DependsOn  []string       `json:"dependsOn"`
	TouchPaths []string       `json:"touchPaths"`
	Tags       []string       `json:"tags"`
	RiskTier   string         `json:"riskTier"`
	Acceptance model.Contract `json:"acceptance"`
}

type rawDecision struct {
	Question string   `json:"question"`
	Options  []string `json:"options"`
	Context  string   `json:"context"`
}

// Passthrough is the degenerate plan used when no planner agent is available:
// a single ready task mirroring the BigTask intent (the Phase 0 behavior).
func Passthrough(bt model.BigTask) model.Plan {
	return model.Plan{
		BigTaskID: bt.ID,
		Status:    model.PlanProposed,
		Tasks: []model.Task{
			{
				BigTaskID:   bt.ID,
				Title:       bt.Title,
				Spec:        bt.Intent,
				Attachments: bt.Attachments,
				Status:      model.TaskReady,
				RiskTier:    model.RiskLow,
				Reporter:    model.ReporterUser,
			},
		},
	}
}

// RenderPrompt builds the planner prompt: the intent + constraints + standing
// conventions, the output JSON schema, and an instruction to write the plan to
// planFile. The planner authors the acceptance contract (verify commands, locked
// globs, held-out checks) — the implementer never sees held-out checks.
// attachments are local paths to images attached when the big task was defined.
func RenderPrompt(bt model.BigTask, conventions []model.Convention, knowledge string, planFile string, attachments []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Plan this work: %s\n\n", bt.Title)
	if bt.Intent != "" {
		fmt.Fprintf(&b, "## Intent\n%s\n\n", bt.Intent)
	}
	if len(bt.Constraints) > 0 {
		b.WriteString("## Constraints\n")
		for _, c := range bt.Constraints {
			fmt.Fprintf(&b, "  - %s\n", c)
		}
		b.WriteString("\n")
	}
	if len(attachments) > 0 {
		b.WriteString("## Attached images\nThe big task includes these image files — read them for context (mockups, screenshots, diagrams):\n")
		for _, p := range attachments {
			fmt.Fprintf(&b, "  - %s\n", p)
		}
		b.WriteString("\n")
	}
	if k := strings.TrimSpace(knowledge); k != "" {
		b.WriteString("## Project knowledge base\n")
		b.WriteString("Persistent architecture/design context for this repo:\n")
		fmt.Fprintf(&b, "%s\n\n", k)
	}
	if len(conventions) > 0 {
		b.WriteString("## Standing conventions (obey these)\n")
		for _, c := range conventions {
			fmt.Fprintf(&b, "  - %s\n", c.Rule)
		}
		b.WriteString("\n")
	}

	if fb := strings.TrimSpace(bt.PlanFeedback); fb != "" {
		b.WriteString("## Revision requested\n")
		fmt.Fprintf(&b, "%s\n\n", fb)
		b.WriteString("Reconsider your previous plan in light of the feedback above and produce an updated plan that addresses it.\n\n")
	}

	b.WriteString("## Your job\n")
	b.WriteString("Decompose this into the smallest set of independently-verifiable tasks. ")
	b.WriteString("For each task author a machine-checkable acceptance contract: shell `verifyCmds` that prove it is done, ")
	b.WriteString("optional `lockedGlobs` the implementer must not edit, and optional `heldOut` checks the implementer never sees. ")
	b.WriteString("A `heldOut` command must be runnable as-is: if it needs a test file that does not already exist in the repo, ")
	b.WriteString("author that file yourself in `heldOutFiles` (worktree-relative path -> full file contents) — the implementer cannot create it ")
	b.WriteString("and fabrika writes it into the worktree only at gate time. Never reference a held-out file you did not provide. ")
	b.WriteString("This is machine-validated: a plan whose heldOut command references a file that is neither in the repo, ")
	b.WriteString("in that task's touchPaths, nor authored in heldOutFiles will be rejected. ")
	b.WriteString("Express ordering with `dependsOn` referencing another task's exact title. ")
	b.WriteString("List the files/dirs each task will touch in `touchPaths` (drives collision avoidance + risk). ")
	b.WriteString("If something genuinely cannot be decided without the human, add it to `decisions` instead of guessing.\n\n")

	fmt.Fprintf(&b, "## Output\nWrite ONLY a JSON object to this file: %s\n", planFile)
	b.WriteString("Schema:\n```json\n")
	b.WriteString(`{
  "tasks": [
    {
      "title": "short imperative title",
      "spec": "what to build, where, and constraints",
      "dependsOn": ["title of a prerequisite task"],
      "touchPaths": ["src/foo", "path/to/file"],
      "tags": ["go", "frontend"],
      "riskTier": "low|medium|high",
      "acceptance": {
        "verifyCmds": ["go test ./..."],
        "lockedGlobs": ["**/*_test.go"],
        "heldOut": ["go test -run Hidden ./..."],
        "heldOutFiles": { "internal/foo/hidden_test.go": "package foo\n\n// full file contents..." }
      }
    }
  ],
  "decisions": [
    { "question": "...", "options": ["A", "B"], "context": "why this is ambiguous" }
  ]
}` + "\n```\n")
	b.WriteString("\n")
	b.WriteString("- On completion, print your token usage: `fabrika_USAGE: {\"inputTokens\":N,\"outputTokens\":N,\"totalTokens\":N}`.\n")
	return b.String()
}

// Parse decodes the planner's JSON output (from file contents or stdout). It is
// tolerant of surrounding prose: it extracts the outermost JSON object.
func Parse(output string) (RawPlan, error) {
	js := extractJSON(output)
	if js == "" {
		return RawPlan{}, fmt.Errorf("no JSON object found in planner output")
	}
	var p RawPlan
	if err := json.Unmarshal([]byte(js), &p); err != nil {
		return RawPlan{}, fmt.Errorf("parse planner JSON: %w", err)
	}
	return p, nil
}

// Build maps a parsed plan into model types: it assigns task IDs, resolves
// dependsOn references (by title, case-insensitive, or 1-based index) to those
// IDs, and produces plan-level decisions. Tasks default to planned status (they
// become ready on plan approval) and low risk. Unresolved dependencies are
// dropped so a task can never be permanently blocked by a typo.
func Build(bt model.BigTask, planID string, raw RawPlan) ([]model.Task, []model.Decision) {
	tasks := make([]model.Task, 0, len(raw.Tasks))
	idByTitle := map[string]string{}
	ids := make([]string, len(raw.Tasks))
	for i := range raw.Tasks {
		ids[i] = uuid.NewString()
		idByTitle[normalize(raw.Tasks[i].Title)] = ids[i]
	}

	for i, rt := range raw.Tasks {
		risk := strings.ToLower(strings.TrimSpace(rt.RiskTier))
		if risk != model.RiskMedium && risk != model.RiskHigh {
			risk = model.RiskLow
		}
		var deps []string
		for _, ref := range rt.DependsOn {
			if id := resolveDep(ref, idByTitle, ids); id != "" && id != ids[i] {
				deps = append(deps, id)
			}
		}
		tasks = append(tasks, model.Task{
			ID:         ids[i],
			BigTaskID:  bt.ID,
			Title:      strings.TrimSpace(rt.Title),
			Spec:       rt.Spec,
			Acceptance: rt.Acceptance,
			DependsOn:  deps,
			TouchPaths: rt.TouchPaths,
			Tags:       rt.Tags,
			RiskTier:   risk,
			Status:     model.TaskPlanned,
			Reporter:   model.ReporterPlanner,
		})
	}

	decisions := make([]model.Decision, 0, len(raw.Decisions))
	for _, rd := range raw.Decisions {
		if strings.TrimSpace(rd.Question) == "" {
			continue
		}
		decisions = append(decisions, model.Decision{
			PlanID:   planID,
			Question: strings.TrimSpace(rd.Question),
			Options:  rd.Options,
			Context:  rd.Context,
			Status:   model.DecisionOpen,
		})
	}
	return tasks, decisions
}

// resolveDep maps a dependsOn reference to a task ID by title or 1-based index.
func resolveDep(ref string, idByTitle map[string]string, ids []string) string {
	if id, ok := idByTitle[normalize(ref)]; ok {
		return id
	}
	if n, err := strconv.Atoi(strings.TrimSpace(ref)); err == nil && n >= 1 && n <= len(ids) {
		return ids[n-1]
	}
	return ""
}

func normalize(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// extractJSON returns the substring from the first '{' to its matching '}',
// tracking string literals so braces inside strings don't confuse the scan.
func extractJSON(s string) string {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return ""
	}
	depth, inStr, esc := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}
