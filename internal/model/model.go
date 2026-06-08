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
	BigTaskBacklog = "backlog" // parked idea; not yet planned/implemented
	BigTaskError   = "error"  // planning failed; Error holds a human-readable reason

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

	// Convention.Status
	ConventionProposed = "proposed"
	ConventionApproved = "approved"
	ConventionRejected = "rejected"

	// Decision.Status (engine extension; the spec models answer presence only)
	DecisionOpen     = "open"
	DecisionAnswered = "answered"

	// Release.Status
	ReleasePending    = "pending"
	ReleaseDeploying  = "deploying"
	ReleaseBaking     = "baking"
	ReleaseLive       = "live"
	ReleaseFailed     = "failed"
	ReleaseRolledBack = "rolled_back"

	// Risk tiers
	RiskLow    = "low"
	RiskMedium = "medium"
	RiskHigh   = "high"

	// Task priorities (human-set ordering hint; default medium)
	PriorityLow    = "low"
	PriorityMedium = "medium"
	PriorityHigh   = "high"

	// Task reporter: who created the task
	ReporterUser     = "user"
	ReporterPlanner = "planner"
	ReporterCI      = "ci"

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
	ID             string   `json:"id"`
	Title          string   `json:"title"`       // outcome statement
	Intent         string   `json:"intent"`      // the why + desired outcome
	Constraints    []string `json:"constraints"` // e.g. "PCI-compliant", "works on mobile"
	Attachments    []string `json:"attachments"` // image upload URLs (/api/uploads/<name>)
	RepoPath       string   `json:"repoPath"`
	Status         string   `json:"status"`         // backlog|draft|planning|planned|running|done|error
	Error          string   `json:"error"`          // failure reason when Status == error; cleared on retry
	PlannerAgentID string   `json:"plannerAgentId"` // which registered planner agent is decomposing this
	PlanFeedback   string   `json:"planFeedback"`   // asks the planner to re-think its plan
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
	Spec             string   `json:"spec"`        // what to build, where, constraints
	Acceptance       Contract `json:"acceptance"`  // machine-verifiable; not authored by the implementer
	DependsOn        []string `json:"dependsOn"`   // task IDs
	TouchPaths       []string `json:"touchPaths"`  // files/dirs it will touch
	Tags             []string `json:"tags"`        // capability hints for routing
	Attachments      []string `json:"attachments"` // image upload URLs (/api/uploads/<name>)
	RiskTier         string   `json:"riskTier"`    // low|medium|high
	Priority         string   `json:"priority"`    // low|medium|high (human-set ordering hint)
	Status           string   `json:"status"`      // ready|claimed|running|verifying|review|merged|blocked|failed
	Branch           string   `json:"branch"`      // git worktree/branch
	AgentID          string   `json:"agentId"`     // which registered agent picked it up
	PreferredAgentID string   `json:"preferredAgentId"`
	Reporter         string   `json:"reporter"` // user|planner

	// Phase 3 autonomy/trust annotations.
	AutoMerged   bool `json:"autoMerged"`   // merged by the machine (risk-tier auto-merge), no human accept
	AuditFlagged bool `json:"auditFlagged"` // auto-merged but sampled for post-merge human audit
	Reverted     bool `json:"reverted"`     // recorded as a change-failure (merged, then reverted/fixed)

	MergeCommitSHA string `json:"mergeCommitSha"` // captured at merge time
	ReleaseID      string `json:"releaseId"`      // set when a release covers this task

	// CIStatus is the last-known CI result for this task's pushed commit.
	// Allowed values: "" (no signal yet) | "pending" | "success" | "failure".
	CIStatus string `json:"ciStatus"`
	// CIRunURL is the URL of the CI run that produced CIStatus.
	CIRunURL string `json:"ciRunUrl"`

	// Pushed is computed at read time: true when the merged work's commit is
	// reachable from the remote-tracking ref for the base branch.
	Pushed bool `json:"pushed"`
}

// Release is a deployment of a merged SHA, progressing through a deploy/bake
// lifecycle to live (or failed/rolled_back). See SPECS-PHASE4 §3.2.
type Release struct {
	ID         string `json:"id"`
	SHA        string `json:"sha"`
	PrevSHA    string `json:"prevSha"`
	Status     string `json:"status"` // pending|deploying|baking|live|failed|rolled_back
	DeployLog  string `json:"deployLog"`
	HealthLog  string `json:"healthLog"`
	Error      string `json:"error"`
	CreatedAt  string `json:"createdAt"`
	DeployedAt string `json:"deployedAt"`
	LiveAt     string `json:"liveAt"`
}

// Agent is a registered worker, defined and managed in the UI, persisted in the
// global store and reusable across projects.
type Agent struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`        // "Claude Code", "Aider", "Reviewer-GPT"
	Photo       string   `json:"photo"`       // profile photo as a data URI string; empty = none
	Command     string   `json:"command"`     // template: substitutes {prompt_file} {worktree} {model}
	Model       string   `json:"model"`       // program-specific model id (e.g. "claude-sonnet-4-6"); empty = no explicit model
	Roles       []string `json:"roles"`       // implementer|planner|reviewer
	Tags        []string `json:"tags"`        // capability hints matched against Task.Tags
	Concurrency int      `json:"concurrency"` // max tasks this agent runs at once
	Timeout     string   `json:"timeout"`     // e.g. "20m"
	MaxAttempts int      `json:"maxAttempts"`
	Enabled     bool     `json:"enabled"`
	// Priority is a user-set routing weight: higher integer = higher priority
	// when choosing among eligible agents. 0 is the default/normal level.
	Priority int `json:"priority"`
}

// Contract is the machine-verifiable acceptance criteria for a Task.
type Contract struct {
	VerifyCmds  []string `json:"verifyCmds"`  // commands proving the task is done
	HeldOut     []string `json:"heldOut"`     // checks the implementer never sees (Phase 2+)
	Properties  []string `json:"properties"`  // invariants (Phase 2+)
	LockedGlobs []string `json:"lockedGlobs"` // protected test files the implementer may not edit
	// HeldOutFiles are planner-authored test files backing the HeldOut checks:
	// worktree-relative path -> full contents. They are written into the
	// worktree only at gate time (after the implementer finishes and the branch
	// is committed), so the implementer never sees them and they never merge.
	HeldOutFiles map[string]string `json:"heldOutFiles,omitempty"`
}

// Attempt records one agent run against a Task.
type Attempt struct {
	ID       string   `json:"id"`
	TaskID   string   `json:"taskId"`
	AgentID  string   `json:"agentId"`
	Result   string   `json:"result"` // pass|fail|escalated
	Evidence Evidence `json:"evidence"`
	Usage    Usage    `json:"usage"`
	Log      string   `json:"log"`
}

// Usage is the token usage an agent self-reports for a run. TotalTokens is the
// agent-reported total, or InputTokens+OutputTokens when the agent omits it.
type Usage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
	TotalTokens  int `json:"totalTokens"`
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

// Comment is a note on a Task, authored by a human or an agent.
type Comment struct {
	ID          string   `json:"id"`
	TaskID      string   `json:"taskId"`
	AuthorType  string   `json:"authorType"` // user|agent
	AuthorID    string   `json:"authorId"`   // agent id; empty for user
	Body        string   `json:"body"`
	Attachments []string `json:"attachments"` // image upload URLs (/api/uploads/<name>)
	CreatedAt   string   `json:"createdAt"`
}

// Convention is standing context injected into future specs + agent runs.
type Convention struct {
	ID     string `json:"id"`
	Rule   string `json:"rule"`
	Status string `json:"status"` // proposed|approved|rejected
}
