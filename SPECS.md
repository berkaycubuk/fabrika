# Fabrika — build spec

A local, single-binary tool that turns one person into a software factory. You define big tasks and make decisions; agents do the work; the tool handles build/test/verify and only surfaces what needs your judgment.

This spec is written to be handed to a coding agent and built incrementally. **Build Phase 0 first and measure before going further** (see Build Plan).

---

## 1. Purpose & principle

You sit on top of the system and do only four things: **define** big tasks, **approve** plans, **decide** the questions agents can't resolve, and **accept** finished work. You can also **steer** the flow at any time. Everything technical — decomposition, building, testing, verification, merging low-risk work — runs without you.

The product's core job is to **protect your attention**: route only judgments to you, and make each one fast. If something doesn't need you, you never see it.

## 2. Non-goals

- Not full autonomy. A human (you) remains the source of intent and the final judge of "is this the right thing." The tool never tries to remove that.
- Not a Jira / ticketing replacement for human teams. Agents own tasks, not you; the board is observability, not your workspace.
- Not multi-user or cloud. Single user, local machine, single binary.
- Not a model or agent itself. It **orchestrates** whatever local coding agents you already have.

## 3. Runtime shape & stack

- **One Go binary** (`fabrika`). Run it from the terminal inside a target repo; it starts a local HTTP server and opens the web UI in the browser.
- **Go backend**: engine, scheduler, git ops, gate runner, agent orchestration, REST + WebSocket API. Persistence in **SQLite** (single-file, local).
- **TypeScript web UI**: the cockpit. Built to static assets and **embedded into the Go binary via `go:embed`** so the binary is self-contained.
- **Local-first**: the binary has filesystem + subprocess access, so it operates on local git repos and invokes **local coding agents as subprocesses**.

```
fabrika            # in a repo with fabrika.toml -> starts UI at http://localhost:7777
fabrika --port 8080
fabrika init       # scaffolds a fabrika.toml in the current repo
```

State lives in two places: a **global store** (`~/.fabrika/fabrika.db`) for agent definitions and conventions (reusable across repos), and a **per-project store** (`.fabrika/` in the repo) for that project's tasks, runs, and config.

## 4. Architecture (layers, top to bottom)

```
You --(UI: define . approve . decide . accept . steer . manage agents)--+
------------------------------------------------------------------------ |  human / machine boundary
Planner        intent -> tasks + contracts                               |
Control plane  task DAG . scheduler . WIP . routing . re-queue           |
Agent pool     YOUR registered agents pull tasks into worktrees          |  runs without you
Technical gate build . test . lint . verify . evidence                   |
Merge gate     auto-merge low-risk . escalate high-risk                  |
```

Two flows are yours: agents send **decisions** up when stuck; you send **steer** down to reprioritize, redirect in-flight work, or reassign a task to a different agent.

## 5. Core data model (Go; TS UI mirrors these)

```go
type BigTask struct {
    ID          string
    Title       string     // outcome statement
    Intent      string     // the why + desired outcome
    Constraints []string   // e.g. "PCI-compliant", "works on mobile"
    RepoPath    string
    Status      string     // draft|planning|planned|running|done
}

type Plan struct {
    ID            string
    BigTaskID     string
    Tasks         []Task
    OpenDecisions []Decision // questions the planner couldn't resolve
    Status        string     // proposed|approved|rejected
}

type Task struct {
    ID               string
    BigTaskID        string
    Title            string
    Spec             string    // what to build, where, constraints
    Acceptance       Contract  // machine-verifiable; NOT authored by the implementing agent
    DependsOn        []string  // task IDs
    TouchPaths       []string  // files/dirs it will touch (collision avoidance + risk tiering)
    Tags             []string  // capability hints for routing: "frontend","go","sql"
    RiskTier         string    // low|medium|high
    Status           string    // ready|claimed|running|verifying|review|merged|blocked|failed
    Branch           string    // git worktree/branch
    AgentID          string    // which registered agent picked it up
    PreferredAgentID string    // optional: pin this task to a specific agent (steer/route)
}

// Agent: a registered worker, DEFINED AND MANAGED IN THE UI, persisted in the global store.
type Agent struct {
    ID          string
    Name        string   // "Claude Code", "Aider", "Reviewer-GPT"
    Command     string   // invocation template: substitutes {prompt_file} {worktree}
    Roles       []string // implementer|planner|reviewer  (an agent can hold several)
    Tags        []string // capability hints matched against Task.Tags (optional)
    Concurrency int      // max tasks this agent runs at once
    Timeout     string   // e.g. "20m"
    MaxAttempts int
    Enabled     bool
}

type Contract struct {
    VerifyCmds  []string // commands proving the task is done (run via manifest verbs)
    HeldOut     []string // checks the implementer agent never sees (Phase 2+)
    Properties  []string // invariants (Phase 2+)
    LockedGlobs []string // protected test files the implementer may not edit
    HeldOutFiles map[string]string // planner-authored files backing HeldOut checks: path -> contents, written into the worktree only at gate time
}

type Attempt struct {
    ID       string
    TaskID   string
    AgentID  string
    Result   string    // pass|fail|escalated
    Evidence Evidence
    Log      string
}

type Evidence struct {
    Stages    map[string]StageResult // build/test/lint/typecheck/verify -> pass/fail + output
    Diff      string                 // the branch diff (the "PR")
    Artifacts []string               // screenshots/recordings (Phase 3)
}

type Decision struct {
    ID       string
    TaskID   string   // empty if plan-level
    Question string
    Options  []string
    Context  string
    Answer   string
    Promote  bool     // promote answer to a standing Convention
}

type Convention struct {
    ID   string
    Rule string // standing context injected into future specs + agent runs
}
```

## 6. Project manifest — `fabrika.toml` (lives in the target repo)

This is what makes the tool **stack-agnostic**: it never knows about npm/go/cargo, only abstract verbs the repo maps to commands. Note: **agents are no longer defined here** — they are managed in the UI (Section 7). The manifest only configures the repo's build/verify verbs, risk, and autonomy.

```toml
[project]
name = "my-app"

[verbs]                       # abstract verb -> concrete command, run in the repo
setup     = "npm install"
build     = "npm run build"
test      = "npm test"
lint      = "npm run lint -- --max-warnings 0"
typecheck = "tsc --noEmit"
verify    = "npm run test:acceptance"   # spec-derived acceptance tests
e2e       = "npx playwright test"        # optional
run       = "npm run dev"                # optional, for preview

[risk]                        # path globs -> tier; anything unmatched = low
high   = ["**/auth/**", "**/payments/**", "migrations/**", "**/*.sql"]
medium = ["src/api/**"]

[autonomy]
auto_merge = ["low"]          # tiers that merge without you
escalate   = ["medium", "high"]
```

Verbs are optional; a missing verb means that gate stage is skipped. Risk tiers can require certain verbs (e.g. `high` requires `verify` + `e2e`) before auto-merge is allowed.

## 7. Agents — UI-defined, multiple, role-based

Agents are **first-class entities you create and edit in the UI**, persisted in the global store and reusable across projects. The system supports **any number of agents** at once.

**Each agent has:** a name, an invocation `Command` template (with `{prompt_file}` and `{worktree}` substituted), one or more **roles**, optional capability **tags**, a **concurrency** limit, timeout, and max-attempts. You add/edit/enable/disable them from the UI's Agents screen — no config-file editing.

**Roles** let one agent pool cover the whole pipeline:
- `implementer` — picks up tasks and writes code (the default pool).
- `planner` — turns a BigTask into a task DAG + contracts (Phase 2).
- `reviewer` — does first-pass review on PRs before they reach you (Phase 3).
You choose which agent fills the planner/reviewer roles in settings; multiple implementers run in parallel.

**Routing — how a ready task picks an agent:**
1. If `Task.PreferredAgentID` is set (you pinned it via steer) -> that agent.
2. Else, among enabled `implementer` agents with a free concurrency slot, prefer one whose `Tags` match `Task.Tags`.
3. Else any enabled implementer with a free slot.
4. If none free, the task waits in `ready`.
You can also set per-risk-tier routing in settings (e.g. send `high` tier to a specific, stronger agent).

**Scheduler:** tracks each agent's free slots (`Concurrency` minus running attempts), respects a global WIP cap, and dispatches ready tasks (dependencies met, no `TouchPaths` collision with a running task) to a matching agent.

**Per task, the assigned agent run:**
1. Create a git worktree on a fresh branch.
2. Render the prompt file: task spec + acceptance contract + relevant conventions + "make commits on this branch; do not edit locked test files."
3. Run the agent's `Command` (subprocess) with `{prompt_file}` and `{worktree}` substituted.
4. On exit, run the gate (Section 8) against the worktree.
5. If the agent emits a structured escalation (sentinel file or stdout marker `fabrika_DECISION: {json}`), create a `Decision` and pause the task instead of failing it.

Keep the adapter thin so any CLI agent works:
```go
type Runner interface {
    Run(ctx context.Context, agent Agent, task Task, worktree string) (AgentResult, error)
}
```

## 8. Verification gate

Run stages in order; stop on first hard failure; emit a normalized `Evidence`:
`setup -> typecheck -> lint -> build -> test -> verify -> e2e`

Integrity rules (the gate must be hard to fool):
- **Acceptance comes from the spec, not the implementer.** `Contract.VerifyCmds` and locked tests are authored by you or the planner agent — the implementing agent may not modify `LockedGlobs`. The gate rejects branches that touch them.
- **Determinism**: no live network/time/random in the gate; pin where possible.
- **Phase 2+**: run `HeldOut` checks the implementer never saw; add **mutation testing** as a validator-of-the-validator (inject a bug, confirm a test goes red).
- A `HeldOut` check needing a test file that doesn't exist in the repo must ship it in `HeldOutFiles` (planner-authored). The engine writes those files into the worktree after the branch's auto-commit and just before the gate, so they stay untracked: the implementer never sees them, they overwrite any implementer-supplied copy, and their paths are implicitly locked.
- **That invariant is enforced in code, not just prompted** (`planner.ValidateHeldOut`): a plan whose `HeldOut` command references a file that neither exists in the repo, is covered by the task's `TouchPaths`, nor is authored in `HeldOutFiles` is rejected at plan time — the planner gets one repair attempt with the violations fed back, then the big task errors. A dispatch-time backstop fails any already-persisted task with such a contract as a `contract` plan defect before the implementer runs, so no agent tokens are spent on work that can only gate red.
- Reject skipped tests and assertion-count regressions.

A branch only becomes a `review`/auto-merge candidate if every required stage passes.

## 9. Merge gate

- Compute the task's risk tier from `TouchPaths` x `[risk]`.
- If tier is in `auto_merge` and evidence is green -> merge to main, mark `merged`, re-queue any tasks it unblocks.
- Else -> create a `review` item surfaced to the UI ("Accept").
- Merge = git merge/rebase of the worktree branch. On conflict, escalate as a `Decision`.

## 10. Web UI (the surfaces + observability)

- **Define**: a box for a big task (intent + constraints). One submit.
- **Approve**: shows a proposed `Plan` (task list + dependency shape + open decisions). Approve / adjust / reject. *(Phase 2)*
- **Decide**: the decision queue — each item a question + options; answer with a tap, optional note, optional "save as convention."
- **Accept**: the review queue — each item a task with its `Evidence` (stage results + diff, later a recording). Merge or kick back with a reason.
- **Steer**: reprioritize the ready queue, pause/redirect in-flight tasks, change autonomy tiers, **reassign a task to a different agent**. To tell the implementer *what to do*, comment on the task and hit Retry: human comments are injected into the next run's prompt as guidance, together with a summary of the previous failed attempt's evidence.
- **Agents**: create/edit/enable/disable agents (name, command, roles, tags, concurrency, timeout). Assign which agent holds the planner/reviewer roles and set per-tier routing. Shows **live per-agent activity** — current tasks, throughput, and kick-back rate — so you can compare agents head to head.
- **Engine room** (observability, not control): live task DAG with statuses, which agent is on what, attempt counts, stuck/blocked items, and the metrics bar. You glance here to calibrate trust; you don't operate it.

UI updates live via WebSocket. Home screen = the things needing you (decide + accept + approve), with agents and engine room secondary.

## 11. API surface

```
POST   /api/bigtasks                      # define
GET    /api/plans/:id                      # Phase 2
POST   /api/plans/:id/approve|reject
GET    /api/decisions                      POST /api/decisions/:id/answer
GET    /api/tasks                          GET  /api/tasks/:id          # DAG / observability
GET    /api/reviews                        POST /api/tasks/:id/accept|reject
POST   /api/tasks/:id/assign               # body {agentId}  -- steer routing
POST   /api/steer                          # reprioritize / pause / redirect

GET    /api/agents                         POST   /api/agents           # define an agent (UI)
PUT    /api/agents/:id                      DELETE /api/agents/:id
POST   /api/agents/:id/enable|disable
GET    /api/settings                       PUT  /api/settings           # role assignment, per-tier routing, WIP cap

GET    /api/metrics                        # touches/unit, change-failure-rate, throughput, per-agent kick-back
WS     /api/events                         # push: plan ready, decision, PR ready, status + metric updates
```

## 12. Suggested repo layout

```
fabrika/
  cmd/fabrika/main.go        # CLI: parse flags, start server, open browser
  internal/
    engine/                  # task lifecycle state machine + scheduler (multi-agent dispatch)
    store/                   # SQLite persistence (global + per-project)
    git/                     # worktree / branch / diff / merge (git CLI or go-git)
    gate/                    # runs manifest verbs, normalizes Evidence
    agent/                   # agent registry + adapter (subprocess invocation)
    planner/                 # BigTask -> Tasks (Phase 0: passthrough; Phase 2: planner agent)
    api/                     # REST + WS handlers
  web/                       # TypeScript UI, built + embedded via go:embed
  fabrika.toml               # example/default; real one lives in target repo
```

## 13. Build plan (build top-down, ship Phase 0 first)

### Phase 0 — Thin slice (prove the loop, then measure)
- `fabrika` starts the UI from a repo with a `fabrika.toml`.
- In the UI, **register at least one agent** (name + command template).
- Manually create **one** task in the UI (paste spec + verify commands). No planner.
- Fabrika makes a worktree, invokes that agent, runs the gate, captures evidence + diff.
- UI shows it as an **Accept** item; you merge or reject from the UI.
- **Acceptance**: take one real task from spec -> agent -> verified -> merged, entirely through the UI, and see whether the output was actually correct. Record *touches* and *pass/fail*.

### Phase 1 — Multiple agents, scheduling, parallelism
- Register **several agents** with per-agent concurrency; scheduler dispatches ready tasks across free agent slots.
- Tasks with `DependsOn` + `TouchPaths` + `Tags`; tag-based + per-tier routing; manual reassignment via steer; WIP cap; path-collision avoidance; parallel worktrees.
- Live engine-room DAG + per-agent activity view.
- **Acceptance**: drop N tasks, walk away, return to a queue of accept-items with no agent collisions, and see which agent did what.

### Phase 2 — Planner, decisions, conventions
- Assign an agent the `planner` role: BigTask -> draft task DAG + contracts + open decisions.
- Approve-plan flow; decision escalation from agents -> answer -> resume; promote answers to Conventions injected into future runs.
- Spec-derived locked acceptance + held-out checks.
- **Acceptance**: define a big task as plain intent, approve a plan, answer a couple decisions, get shipped work — **without writing a single task-level prompt**.

### Phase 3 — Autonomy, trust, hardening
- Risk tiering + auto-merge of low-risk; assign an agent the `reviewer` role for first-pass review; mutation testing; metrics dashboard; steering of in-flight work; random audit sampling of auto-merged PRs.
- **Acceptance**: *touches per shipped unit* trends down while *change-failure-rate* stays flat; most low-risk PRs merge without you.

## 14. Metrics to track from day one

- **Touches per shipped unit** — human interventions per merged change. The anti-bottleneck number; drive it down.
- **Change-failure rate** — share of merged changes later reverted/fixed. The trust number; keep it flat as you widen autonomy.
- **Per-agent kick-back rate** — share of an agent's PRs you reject. Lets you compare agents and route work to the ones that earn trust.

## 15. Open problems — NOT solved by this tool (keep human)

- **Context provisioning**: reliably getting the right repo context into each agent run. Start with conventions + explicit `TouchPaths`; expect to iterate.
- **Architectural coherence across parallel merges**: git merge is textual, not semantic — two branches can merge cleanly yet be architecturally incompatible. This is why you stay the architect; the tool gives you the audit/steer hooks to catch it, not a fix.
- **"Right thing" judgment**: no gate catches "passes tests but wasn't worth building." That's irreducibly yours, by design.
