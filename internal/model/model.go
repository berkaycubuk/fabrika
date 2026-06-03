// Package model holds Fabrika's core domain types. The TypeScript UI mirrors
// these shapes; JSON tags define the wire contract. See SPECS.md §5.
package model

// Status constants for the task lifecycle and related entities. Kept as plain
// strings (not enums) to stay close to the spec and trivially JSON-serializable.
const (
	// BigTask.Status
	BigTaskDraft    = "draft"
	BigTaskPlanning = "planning"
	BigTaskPlanned  = "planned"
	BigTaskRunning  = "running"
	BigTaskDone     = "done"

	// Plan.Status
	PlanProposed = "proposed"
	PlanApproved = "approved"
	PlanRejected = "rejected"

	// Task.Status
	TaskPlanned   = "planned" // belongs to a proposed plan; not dispatchable until approved (Phase 2)
	TaskReady     = "ready"
	TaskClaimed   = "claimed"
	TaskRunning   = "running"
	TaskVerifying = "verifying"
	TaskReview    = "review"
	TaskMerged    = "merged"
	TaskBlocked   = "blocked"
	TaskFailed    = "failed"
	TaskClosed    = "closed" // human dismissed without merging (engine extension)

	// Decision.Status (engine extension; the spec models answer presence only)
	DecisionOpen     = "open"
	DecisionAnswered = "answered"

	// Risk tiers
	RiskLow    = "low"
	RiskMedium = "medium"
	RiskHigh   = "high"

	// Agent roles
	RoleImplementer = "implementer"
	RolePlanner     = "planner"
	RoleReviewer    = "reviewer"

	// Attempt.Result
	ResultPass      = "pass"
	ResultFail      = "fail"
	ResultEscalated = "escalated"
)

// BigTask is an outcome the human defines; the planner turns it into Tasks.
type BigTask struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`       // outcome statement
	Intent      string   `json:"intent"`      // the why + desired outcome
	Constraints []string `json:"constraints"` // e.g. "PCI-compliant", "works on mobile"
	RepoPath    string   `json:"repoPath"`
	Status      string   `json:"status"` // draft|planning|planned|running|done
}

// Plan is a proposed decomposition of a BigTask into Tasks.
type Plan struct {
	ID            string     `json:"id"`
	BigTaskID     string     `json:"bigTaskId"`
	Tasks         []Task     `json:"tasks"`
	OpenDecisions []Decision `json:"openDecisions"` // questions the planner couldn't resolve
	Status        string     `json:"status"`        // proposed|approved|rejected
}

// Task is a single unit of work an agent picks up.
type Task struct {
	ID               string   `json:"id"`
	BigTaskID        string   `json:"bigTaskId"`
	Title            string   `json:"title"`
	Spec             string   `json:"spec"`       // what to build, where, constraints
	Acceptance       Contract `json:"acceptance"` // machine-verifiable; not authored by the implementer
	DependsOn        []string `json:"dependsOn"`  // task IDs
	TouchPaths       []string `json:"touchPaths"` // files/dirs it will touch
	Tags             []string `json:"tags"`       // capability hints for routing
	RiskTier         string   `json:"riskTier"`   // low|medium|high
	Status           string   `json:"status"`     // ready|claimed|running|verifying|review|merged|blocked|failed
	Branch           string   `json:"branch"`     // git worktree/branch
	AgentID          string   `json:"agentId"`    // which registered agent picked it up
	PreferredAgentID string   `json:"preferredAgentId"`
}

// Agent is a registered worker, defined and managed in the UI, persisted in the
// global store and reusable across projects.
type Agent struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`        // "Claude Code", "Aider", "Reviewer-GPT"
	Command     string   `json:"command"`     // template: substitutes {prompt_file} {worktree}
	Roles       []string `json:"roles"`       // implementer|planner|reviewer
	Tags        []string `json:"tags"`        // capability hints matched against Task.Tags
	Concurrency int      `json:"concurrency"` // max tasks this agent runs at once
	Timeout     string   `json:"timeout"`     // e.g. "20m"
	MaxAttempts int      `json:"maxAttempts"`
	Enabled     bool     `json:"enabled"`
}

// Contract is the machine-verifiable acceptance criteria for a Task.
type Contract struct {
	VerifyCmds  []string `json:"verifyCmds"`  // commands proving the task is done
	HeldOut     []string `json:"heldOut"`     // checks the implementer never sees (Phase 2+)
	Properties  []string `json:"properties"`  // invariants (Phase 2+)
	LockedGlobs []string `json:"lockedGlobs"` // protected test files the implementer may not edit
}

// Attempt records one agent run against a Task.
type Attempt struct {
	ID       string   `json:"id"`
	TaskID   string   `json:"taskId"`
	AgentID  string   `json:"agentId"`
	Result   string   `json:"result"` // pass|fail|escalated
	Evidence Evidence `json:"evidence"`
	Log      string   `json:"log"`
}

// Evidence is the normalized output of the verification gate.
type Evidence struct {
	Stages    map[string]StageResult `json:"stages"`    // build/test/lint/typecheck/verify -> result
	Diff      string                 `json:"diff"`      // the branch diff (the "PR")
	Artifacts []string               `json:"artifacts"` // screenshots/recordings (Phase 3)
}

// StageResult is the outcome of one gate stage.
type StageResult struct {
	Pass     bool   `json:"pass"`
	Output   string `json:"output"`
	Skipped  bool   `json:"skipped"`
	ExitCode int    `json:"exitCode"`
}

// Decision is a question an agent or the planner couldn't resolve. PlanID and
// Status are engine extensions: PlanID ties a plan-level decision to its plan
// (TaskID empty), and Status tracks open|answered for the decision queue.
type Decision struct {
	ID       string   `json:"id"`
	PlanID   string   `json:"planId"` // set for plan-level decisions (TaskID empty)
	TaskID   string   `json:"taskId"` // set for task-level escalations (PlanID empty)
	Question string   `json:"question"`
	Options  []string `json:"options"`
	Context  string   `json:"context"`
	Answer   string   `json:"answer"`
	Promote  bool     `json:"promote"` // promote answer to a standing Convention
	Status   string   `json:"status"`  // open|answered
}

// Convention is standing context injected into future specs + agent runs.
type Convention struct {
	ID   string `json:"id"`
	Rule string `json:"rule"`
}
