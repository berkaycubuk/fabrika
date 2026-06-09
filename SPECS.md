# Fabrika — build spec

A local, single-binary tool that turns one person into a software factory. You define big tasks and make decisions; agents do the work; the tool handles build/test/verify and only surfaces what needs your judgment.

> **Status: Phases 0–3 complete; Phase 4 (releases + CI) partially shipped.** The
> full loop runs — define → plan → decide → implement → gate → review/auto-merge →
> ship. Releases (deploy + bake + rollback) and an external-CI poller are built; the
> incident/feedback correlation layer from [SPECS-PHASE4.md](SPECS-PHASE4.md) is the
> remaining work. This document describes the system **as built**; the Build Plan
> (§13) records phase status.

---

## 1. Purpose & principle

You sit on top of the system and do only four things: **define** big tasks, **approve** plans, **decide** the questions agents can't resolve, and **accept** finished work. You can also **steer** the flow at any time, and **ship** merged work to production. Everything technical — decomposition, building, testing, verification, reviewing, merging low-risk work — runs without you.

The product's core job is to **protect your attention**: route only judgments to you, and make each one fast. If something doesn't need you, you never see it. The UI exposes a single unified **attention feed** (`/api/attention`) gathering everything that wants a human: plans to approve, decisions to answer, work to accept, and auto-merged work sampled for audit.

## 2. Non-goals

- Not full autonomy. A human (you) remains the source of intent and the final judge of "is this the right thing." The tool never tries to remove that.
- Not a Jira / ticketing replacement for human teams. Agents own tasks, not you; the board is observability, not your workspace.
- Not multi-user or cloud. Single user, local machine, single binary.
- Not a model or agent itself. It **orchestrates** whatever local coding agents you already have.

## 3. Runtime shape & stack

- **One Go binary** (`fabrika`). Run it from the terminal inside a target repo; it starts a local HTTP server and opens the web UI in the browser.
- **Go backend**: engine, scheduler, git ops, gate runner, agent orchestration, release manager, CI poller, REST + WebSocket API. Persistence in **SQLite** (`modernc.org/sqlite`, pure Go, no cgo).
- **TypeScript web UI**: the cockpit (vanilla TS, built with esbuild). Built to static assets and **embedded into the Go binary via `go:embed`** so the binary is self-contained.
- **Local-first**: the binary has filesystem + subprocess access, so it operates on local git repos and invokes **local coding agents as subprocesses**.
- **Observability**: optional Sentry error reporting (`internal/observability`), enabled by default and disabled via `FABRIKA_SENTRY_DISABLE`.

```
fabrika            # in a repo with fabrika.toml -> starts UI at http://localhost:7777
fabrika --port 8080
fabrika --no-open  # don't auto-open the browser
fabrika init       # scaffolds a fabrika.toml (auto-detecting the stack's verbs)
fabrika version    # print build version
```

State lives in two places: a **global store** (`~/.fabrika/fabrika.db`) for agent definitions, conventions, and settings (reusable across repos), and a **per-project store** (`.fabrika/fabrika.db` in the repo) for that project's big tasks, tasks, plans, attempts, decisions, comments, and releases. Both DBs run WAL mode with foreign keys on and are migrated forward via a numbered migration sequence.

## 4. Architecture (layers, top to bottom)

```
You --(UI: define . approve . decide . accept . steer . ship . manage agents)--+
------------------------------------------------------------------------------ |  human / machine boundary
Planner        intent -> tasks + contracts + open decisions                    |
Control plane  task DAG . scheduler . WIP . routing . re-queue . quarantine     |
Agent pool     YOUR registered agents pull tasks into worktrees                 |  runs without you
Technical gate setup.typecheck.lint.build.test.verify.e2e -> evidence           |
Quality gate   reviewer agent . mutation testing                               |
Merge gate     auto-merge low-risk . escalate high-risk . audit sampling        |
Release        ship (deploy) . bake . rollback ; external-CI poller             |
```

Flows that are yours: agents send **decisions** up when stuck; you send **steer** down to reprioritize, redirect in-flight work, or reassign a task; you **ship** merged work; the machine guards the backward direction (rollback). 

## 5. Core data model (Go; the TS UI mirrors these — see `internal/model/model.go`)

Statuses are plain strings, JSON-serializable. The current set:

```go
type BigTask struct {
    ID             string
    Title          string     // outcome statement
    Intent         string     // the why + desired outcome
    Constraints    []string   // e.g. "PCI-compliant", "works on mobile"
    Attachments    []string   // image upload URLs (/api/uploads/<name>)
    RepoPath       string
    Status         string     // backlog|draft|planning|planned|running|done|error
    Error          string     // failure reason when Status == error; cleared on retry
    PlannerAgentID string     // which registered planner agent is decomposing this
    PlanFeedback   string     // human nudge asking the planner to re-think its plan
}

type Plan struct {
    ID            string
    BigTaskID     string
    Tasks         []Task     // assembled from the tasks table (not stored on the row)
    OpenDecisions []Decision // assembled from the decisions table
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
    Attachments      []string  // image upload URLs
    RiskTier         string    // low|medium|high
    Priority         string    // low|medium|high (human-set ordering hint)
    Status           string    // planned|ready|claimed|running|verifying|review|merged|blocked|failed|closed
    Branch           string    // git worktree/branch
    AgentID          string    // which registered agent picked it up
    PreferredAgentID string    // optional: pin this task to a specific agent (steer/route)
    Reporter         string    // user|planner|ci -- who created the task

    // Phase 3 autonomy/trust annotations
    AutoMerged   bool          // merged by the machine (risk-tier auto-merge), no human accept
    AuditFlagged bool          // auto-merged but sampled for post-merge human audit
    Reverted     bool          // recorded as a change-failure (merged, then reverted/fixed)

    // Phase 4 release/CI annotations
    MergeCommitSHA string      // captured at merge time
    ReleaseID      string      // set when a release covers this task
    CIStatus       string      // "" | pending | success | failure (external CI for the merged commit)
    CIRunURL       string      // URL of the CI run that produced CIStatus
    Pushed         bool        // computed at read time: merged commit reachable from the remote-tracking ref
}

// Agent: a registered worker, DEFINED AND MANAGED IN THE UI, persisted in the global store.
type Agent struct {
    ID          string
    Name        string   // "Claude Code", "Aider", "Reviewer-GPT"
    Photo       string   // profile photo as a data URI; empty = none
    Command     string   // invocation template: substitutes {prompt_file} {worktree} {model}
    Model       string   // program-specific model id (e.g. "claude-sonnet-4-6"); empty = no explicit model
    Roles       []string // implementer|planner|reviewer  (an agent can hold several)
    Tags        []string // capability hints matched against Task.Tags (optional)
    Concurrency int      // max tasks this agent runs at once
    Timeout     string   // e.g. "20m"
    MaxAttempts int
    Enabled     bool
    Priority    int      // routing weight: higher = preferred among eligible agents; 0 = normal
}

type Contract struct {
    VerifyCmds   []string          // commands proving the task is done (run in the `verify` stage)
    HeldOut      []string          // checks the implementer never sees
    Properties   []string          // invariants
    LockedGlobs  []string          // protected test files the implementer may not edit
    HeldOutFiles map[string]string // planner-authored files backing HeldOut checks: path -> contents,
                                    // written into the worktree only at gate time
}

type Attempt struct {
    ID       string
    TaskID   string
    AgentID  string
    Result   string    // pass|fail|escalated
    Evidence Evidence
    Usage    Usage      // token usage self-reported by the agent
    Log      string
}

type Usage struct {
    InputTokens  int
    OutputTokens int
    TotalTokens  int    // agent-reported, or Input+Output when omitted
}

type Evidence struct {
    Stages    map[string]StageResult // setup/typecheck/lint/build/test/verify/e2e -> result
    Diff      string                 // the branch diff (the "PR")
    Artifacts []string               // screenshots/recordings the agent pointed at via fabrika_EVIDENCE:
}

type StageResult struct {
    Pass     bool
    Output   string
    Skipped  bool
    ExitCode int
}

type Decision struct {
    ID       string
    PlanID   string   // set for plan-level decisions (TaskID empty)
    TaskID   string   // set for task-level escalations (PlanID empty)
    Question string
    Options  []string
    Context  string
    Answer   string
    Promote  bool     // promote answer to a standing Convention
    Status   string   // open|answered
}

type Comment struct {
    ID          string
    TaskID      string
    AuthorType  string   // user|agent
    AuthorID    string   // agent id; empty for user
    Body        string
    Attachments []string // image upload URLs
    CreatedAt   string
}

type Convention struct {
    ID     string
    Rule   string // standing context injected into future specs + agent runs
    Status string // proposed|approved|rejected (reviewer-proposed conventions start as proposed)
}

// Release: a deploy of the base branch at a SHA (Phase 4). Covers all tasks whose
// merge commits fall in prev_sha..sha.
type Release struct {
    ID         string
    SHA        string
    PrevSHA    string
    Status     string // pending|deploying|baking|live|failed|rolled_back
    DeployLog  string
    HealthLog  string
    Error      string
    CreatedAt  string
    DeployedAt string
    LiveAt     string
}
```

**Persistence map.** Global store: `agents`, `conventions`, `settings` (key/value: role assignments, per-tier routing, WIP cap, audit rate, mutation toggle, thresholds). Per-project store: `bigtasks`, `tasks`, `plans`, `decisions`, `attempts`, `comments`, `releases`. Slice/map fields and `Acceptance`/`Evidence` are stored as JSON TEXT columns.

## 6. Project manifest — `fabrika.toml` (lives in the target repo)

This is what makes the tool **stack-agnostic**: it never knows about npm/go/cargo, only abstract verbs the repo maps to commands. **Agents are not defined here** — they are managed in the UI (§7). `fabrika init` auto-detects the stack (Go / Node / Python / Make, in that precedence) and scaffolds the manifest with the detected verbs filled in.

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

[deploy]                      # Phase 4 — empty/absent disables releases
mode         = "manual"       # manual | per-merge | interval (only manual wired today)
command      = "make deploy"  # required to enable releases
health       = "curl -fsS https://app.example.com/health"  # optional
rollback     = ""             # optional; empty = re-run command at prev_sha checkout
bake_minutes = 30             # 0 = skip bake, go straight to live

[ci]                          # Phase 4 — external-CI poller; empty/absent disables it
command      = "gh run list --json ..." # prints a JSON array of CI runs; matched to tasks by SHA
poll_seconds = 60             # tick interval; must be >= 10
```

Verbs are optional; a missing verb means that gate stage is skipped. Config is validated on load: autonomy tiers must be known and not appear in both lists; `deploy.mode` must be one of the three values; `deploy.bake_minutes >= 0`; `ci.poll_seconds >= 10` when a command is set. The manifest is editable from the UI's Settings screen and saved atomically.

## 7. Agents — UI-defined, multiple, role-based

Agents are **first-class entities you create and edit in the UI**, persisted in the global store and reusable across projects. The system supports **any number of agents** at once.

**Each agent has:** a name, optional photo, an invocation `Command` template, an optional `Model` id, one or more **roles**, optional capability **tags**, a **concurrency** limit, timeout, max-attempts, and a routing **priority**. You add/edit/enable/disable them from the UI's Agents screen — no config-file editing. The screen shows live per-agent activity and trust signals (current tasks, throughput, kick-back rate).

**Command template** substitutes `{prompt_file}`, `{worktree}`, and `{model}`. Agents run as subprocesses with a hard `Timeout` (default 30m) and an idle/stall watchdog (default 5m of silence → killed as stalled, recorded as a distinct liveness failure rather than a timeout). A heartbeat (last output line, idle time, byte count) surfaces liveness to the cockpit so a running card shows staleness.

**Agent → engine output markers** (stdout sentinels the agent may emit):
- `fabrika_DECISION: {json}` — a task-level question; pauses the task into `blocked` as a `Decision` instead of failing it.
- `fabrika_COMMENT: …` — a narrative note, saved to the task comment thread.
- `fabrika_EVIDENCE: <worktree-path> [| caption]` — an artifact (screenshot/recording) copied into `.fabrika/uploads` and surfaced as a comment.
- `fabrika_REVIEW: {json}` — a reviewer-role verdict (`approve`, notes, proposed conventions).
- `fabrika_USAGE: {json}` — self-reported token usage, recorded on the attempt and rolled up to the big task.

**Roles** let one agent pool cover the whole pipeline:
- `implementer` — picks up tasks and writes code (the default pool).
- `planner` — turns a BigTask into a task DAG + contracts + open decisions.
- `reviewer` — does first-pass review on green PRs before auto-merge or human accept.

You choose which agent fills the planner/reviewer roles in settings; multiple implementers run in parallel.

**Routing — how a ready task picks an agent** (`agent.Route`):
1. If `Task.PreferredAgentID` is set (you pinned it via steer) → that agent; if it has no free slot, the task waits rather than falling through.
2. Else, among enabled `implementer` agents with a free concurrency slot, prefer those whose `Tags` overlap `Task.Tags`, breaking ties by `Priority`.
3. Else any enabled implementer with a free slot.
4. If none free, the task waits in `ready`.

You can also set per-risk-tier routing in settings (e.g. send `high` tier to a specific, stronger agent), applied when a task isn't pinned.

**Scheduler:** tracks each agent's free slots (`Concurrency` minus running attempts), respects a global WIP cap, and dispatches ready tasks (dependencies merged, no `TouchPaths` collision with a running task) to a matching agent. An agent that fails its last N attempts in a row is **quarantined** (zero free slots) until it recovers, so a misbehaving agent stops draining the queue.

**Per task, the assigned agent run** (`engine.run`):
1. Create a clean git worktree on a fresh `fabrika/task-<id>` branch off the base.
2. Render the prompt file: task spec + acceptance contract + relevant conventions + human comments + a summary of the previous failed attempt + "make commits on this branch; do not edit locked test files."
3. Run the agent's `Command` (subprocess) with the tokens substituted, watching the heartbeat/idle watchdog and honoring in-flight cancellation (steer).
4. Ingest agent comments/evidence; if it escalated a `Decision`, pause into `blocked`.
5. Commit the work, compute the diff, reject an empty diff or a diff touching `LockedGlobs`/held-out paths, then run the gate (§8) against the worktree.

The adapter stays thin so any CLI agent works (`agent.Runner` interface).

## 8. Verification gate

Run stages in fixed order, stop on first hard failure, emit a normalized `Evidence`:
`setup → typecheck → lint → build → test → verify → e2e`

`verify` runs the repo's `verify` verb plus the task's `Contract.VerifyCmds` and any held-out checks. A missing verb skips that stage (`Pass`, `Skipped`).

Integrity rules (the gate must be hard to fool):
- **Acceptance comes from the spec, not the implementer.** `Contract.VerifyCmds` and locked tests are authored by you or the planner agent — the implementing agent may not modify `LockedGlobs`. The gate rejects branches that touch them or the held-out paths.
- **Held-out checks** the implementer never saw are appended to `verify`. The planner-authored `HeldOutFiles` are written into the worktree **after** the branch is committed and **just before** the gate — untracked, overwriting any implementer copy, paths implicitly locked, never merged.
- **The held-out invariant is enforced in code, not just prompted** (`planner.ValidateHeldOut`): a plan whose `HeldOut` command references a file that neither exists in the repo, is covered by the task's `TouchPaths`, nor is authored in `HeldOutFiles` is rejected at plan time — the planner gets one repair attempt with the violations fed back, then the big task errors. A dispatch-time backstop fails any already-persisted task with such a contract before the implementer runs, so no agent tokens are spent on work that can only gate red.
- **Determinism**: gate commands run via the same timeout-bounded runner as everywhere; pin where possible.

A branch only becomes a `review`/auto-merge candidate if every required stage passes.

**Mutation testing** (validator-of-the-validator, toggleable in settings): after a green gate, Fabrika generates conservative textual mutants (`==`↔`!=`, `&&`↔`||`, boundary flips, `true`↔`false`, `+`↔`-`) **scoped to the lines the diff added** in non-test, non-locked files, and re-runs the `test` verb per mutant (budget ~8). A surviving mutant (tests stayed green) blocks auto-merge and sends the work to human `review`.

## 9. Quality gate, merge gate, and release

**Reviewer pass** (on green, before merge): if a `reviewer`-role agent is configured (and isn't the implementer), it runs on the finished branch with the diff and returns a `fabrika_REVIEW:` verdict. A non-approving verdict blocks auto-merge and routes to human `review`; the reviewer may also propose up to a few `Convention`s (stored `proposed` for you to approve).

**Merge gate:**
- Compute the task's **effective** risk tier = max(declared `RiskTier`, tier of the files it actually changed per `[risk]`). This hardens auto-merge so an agent can't sneak high-risk edits under a low declaration.
- If the effective tier is in `auto_merge`, the gate is green, the reviewer approved, and mutation passed → merge to the base branch, record `MergeCommitSHA`, mark `AutoMerged`, re-queue any tasks it unblocks.
- Else → create a `review` item surfaced to the UI ("Accept"). Accepting also records `MergeCommitSHA`.
- Merge is a git merge/rebase of the worktree branch. A conflicting merge is always aborted so the repo is never left half-merged; the work surfaces in Accept and recovery is Retry (rebuilds on the current base).
- Red (failed/blocked) work is mergeable from the UI via an explicit **Merge anyway** (`force`). Every dead-end has a UI exit: failed/blocked → Retry / Merge anyway / Request changes / Kick back; kicked-back tasks land in **Closed** → Retry / Delete; errored plan requests → Retry planning / Delete.

**Audit sampling** (trust calibration): a configurable fraction of auto-merged tasks are flagged `AuditFlagged` and held in an **Audit** queue for post-merge human spot-check — acknowledge (clears the flag) or **Revert**. Unsampled auto-merges go straight to `merged`.

**Push:** merged work lives on the local base branch until you push. The UI shows an unpushed count and a Ship/Push control; `Task.Pushed` is computed from the remote-tracking ref.

**Releases (Phase 4):** when `[deploy]` is configured, the human ships a **release** — a single-flight deploy of the base branch at HEAD. `Ship()` creates a release (`deploying`), runs `deploy.command` (and optional `health`), maps every merged task in `prev_sha..sha` to the release, then enters `baking` (or straight to `live` if `bake_minutes == 0`). A bake timer (derived from `deployed_at`, so it survives restarts) promotes `baking → live`. **Rollback** redeploys the previous SHA (via `deploy.rollback` or a temp worktree at `prev_sha`) — deploy-level, not git-level, so `main` keeps every merge and the rolled-back tasks simply re-appear as unshipped. `Engine.Revert(task)` spawns a high-priority `git revert -m 1 <sha>` task through the normal pipeline (and flags the original as a change-failure).

**External CI (Phase 4):** when `[ci]` is configured, a poller runs the command, parses a JSON array of CI runs, matches them to tasks by `MergeCommitSHA`, and writes `CIStatus`/`CIRunURL`. The first time a task flips to `failure`, the poller spawns a high-priority "Fix CI failure" task (reporter `ci`) referencing the original and the run URL.

> The richer incident/feedback model in [SPECS-PHASE4.md](SPECS-PHASE4.md) §5.6–5.7 (deduplicated incidents, suspect-release correlation, auto-rollback during bake) is **not yet built** — there is no `Incident` type or `internal/feedback` package. The shipped CI poller is the lighter substitute.

## 10. Web UI (the surfaces + observability)

A single-page app with four top-level views (Board, Factory, Agents, Settings) plus modal detail surfaces. Updates live over WebSocket (`/api/events`).

**Board** — a Jira-style kanban (unified scroll, sticky column heads) over the whole lifecycle. Columns: **Backlog · Planning · Approve · Decide · Ready · Running · Verifying · Accept · Audit · Merged · Closed**. `Approve`, `Decide`, `Accept`, and `Audit` are the human gates. Opening a card opens the matching modal:
- **Define** — a box for a big task (intent + constraints + attachments + risk). One submit. Big tasks can sit in **Backlog** and be promoted to Planning.
- **Create task** — author a single task by hand (spec + verify commands), bypassing the planner.
- **Approve** — a proposed `Plan` (task list + dependency shape + open decisions). Approve / revise (with feedback) / reject.
- **Decide** — the decision queue: a question + options; answer with a tap, optional note, optional "save as convention."
- **Accept** — the review queue: a task with its `Evidence` (per-stage results + diff + artifacts). Merge (green, or red via Merge-anyway) / Request changes / Kick back. Supports multi-select **batch** accept/retry with an undo toast.
- **Audit** — auto-merged work sampled for post-merge review: acknowledge or revert.
- **Task / Big-task detail** — full info, comment thread (human + agent, with image attachments), agent assignment, planning status/errors with Replan.
- **Ship / Release detail** — confirm a ship (the unshipped task titles double as release notes); a release strip shows the latest release state with deploy/health logs and a rollback button.

**Steer** — reprioritize the ready queue, pause/redirect in-flight tasks, change autonomy tiers, reassign a task to a different agent (inline on the card + `/api/steer`). To tell the implementer *what to do*, comment on the task and Retry: human comments are injected into the next run's prompt together with a summary of the previous attempt's evidence.

**Factory** (the old "Engine room", promoted to a first-class view) — observability + autonomy dials: throughput (in-flight / ready / review / merged / tokens), trust (touches-per-unit, change-failure rate, auto-merged count, audit queue), per-agent work share, and controls for WIP cap, audit rate, and the mutation-testing toggle. Hosts the **Conventions** panel (approve/reject standing rules).

**Agents** — create/edit/enable/disable agents (name, photo, command, model, roles, tags, concurrency, timeout, priority). Assign which agent holds the planner/reviewer roles and set per-tier routing. Shows live per-agent activity so you can compare agents head to head.

**Settings** — edit and persist the `fabrika.toml` manifest.

Shared components: side-by-side diff view, CI status badge, attachment gallery, board filters (by status / agent / risk), toast + undo.

## 11. API surface

```
# Define / plan
POST   /api/bigtasks                       GET  /api/bigtasks        DELETE /api/bigtasks/{id}
POST   /api/bigtasks/{id}/plan             # promote backlog -> planning
POST   /api/bigtasks/{id}/replan           # retry an errored plan request
POST   /api/bigtasks/{id}/stop             # cancel planning with a reason
GET    /api/plans                          GET  /api/plans/{id}
POST   /api/plans/{id}/approve|reject|revise
GET    /api/decisions                      POST /api/decisions/{id}/answer  # body {answer, promote}

# Tasks / accept / audit / steer
GET    /api/tasks                          POST /api/tasks            GET    /api/tasks/{id}
DELETE /api/tasks/{id}                      # discard a closed task
GET    /api/tasks/{id}/comments            POST /api/tasks/{id}/comments
GET    /api/reviews                         # Accept queue (review|failed|blocked)
POST   /api/tasks/{id}/accept              # body {force} merges red work
POST   /api/tasks/{id}/reject|retry|request-changes
POST   /api/tasks/accept-batch             POST /api/tasks/retry-batch   # body {ids}
GET    /api/audits                         POST /api/tasks/{id}/audit-ok|revert
POST   /api/tasks/{id}/assign              # body {agentId}
POST   /api/steer                          # reprioritize / pause / redirect / reassign

# Uploads
POST   /api/uploads                        GET  /api/uploads/{name}

# Agents / conventions / settings / config
GET    /api/agents                         POST   /api/agents
PUT    /api/agents/{id}                     DELETE /api/agents/{id}
POST   /api/agents/{id}/enable|disable
GET    /api/conventions                    POST   /api/conventions
DELETE /api/conventions/{id}                POST   /api/conventions/{id}/approve|reject
GET    /api/settings                       PUT  /api/settings         # role assignment, routing, WIP, audit rate
GET    /api/config                         PUT  /api/config           # fabrika.toml

# Release / push / CI signal
GET    /api/push/status                    POST /api/push
GET    /api/releases                       POST /api/releases/ship
GET    /api/releases/{id}                  POST /api/releases/{id}/rollback
GET    /api/releases/unshipped

# Observability
GET    /api/attention                      # unified judgment feed: plans + decisions + reviews + audits
GET    /api/metrics                         # throughput, trust, per-agent activity
GET    /api/version
WS     /api/events                          # push: plan/decision/task/release updates + metrics
```

## 12. Repo layout

```
fabrika/
  cmd/fabrika/main.go        # CLI: flags, init, version, server boot, browser open
  internal/
    model/                   # shared domain types (§5)
    config/                  # fabrika.toml parse + scaffold + stack detection + risk tiering
    store/                   # SQLite: global + per-project DBs, numbered migrations, repos
    git/                     # worktree / branch / diff / merge / rev-parse (git CLI)
    gate/                    # runs manifest verbs, normalizes Evidence
    mutate/                  # textual mutation testing (scoped to changed lines)
    agent/                   # registry + subprocess adapter + routing + health/quarantine + reviewer
    planner/                 # BigTask -> Tasks + held-out validation
    engine/                  # dispatch loop, lifecycle state machine, autonomy, decisions, releases
    release/                 # ship / bake timer / rollback manager
    ci/                      # external-CI poller -> CIStatus + auto fix-task
    api/                     # REST + WS handlers, uploads, attention feed
    observability/           # Sentry init
  web/                       # vanilla-TS UI (esbuild), embedded via go:embed
  fabrika.toml               # this repo's own manifest
```

## 13. Build plan (status)

### Phase 0 — Thin slice — **DONE**
`fabrika` starts the UI from a repo with a manifest; register an agent; create one task by hand; worktree → agent → gate → evidence → Accept → merge, all through the UI.

### Phase 1 — Multiple agents, scheduling, parallelism — **DONE**
Several agents with per-agent concurrency; scheduler dispatches across free slots; `DependsOn` + `TouchPaths` + `Tags`; tag-based + per-tier routing; manual reassignment; WIP cap; path-collision avoidance; parallel worktrees; quarantine; live Factory view.

### Phase 2 — Planner, decisions, conventions — **DONE**
Planner role: BigTask → task DAG + contracts + open decisions; approve/revise-plan flow; decision escalation → answer → resume (task- and plan-level); promote answers to Conventions injected into future runs; spec-derived locked acceptance + held-out checks with code-enforced validation.

### Phase 3 — Autonomy, trust, hardening — **DONE**
Risk tiering + auto-merge of low-risk; reviewer role; mutation testing; audit sampling of auto-merged PRs; metrics dashboard; steering of in-flight work; per-agent kick-back/trust signals.

### Phase 4 — Releases & downstream — **PARTIAL**
- **Done:** merge-SHA capture + real `git revert` task; releases (Ship), bake timer (restart-safe), manual rollback; `[deploy]` config; release UI (Ship drawer, release strip, task RELEASE field); external-CI poller (`[ci]`) with auto fix-task on failure.
- **Remaining (per [SPECS-PHASE4.md](SPECS-PHASE4.md)):** the incident/feedback layer — deduplicated `incidents` table, `internal/feedback` poller, suspect-release/task correlation, auto-rollback of a baking release on a correlated incident, and the Incidents UI.

## 14. Metrics

- **Touches per shipped unit** — human interventions per merged change. The anti-bottleneck number; drive it down.
- **Change-failure rate** — share of merged changes later reverted/fixed (and, going forward, rolled back). The trust number; keep it flat as you widen autonomy.
- **Per-agent kick-back rate** — share of an agent's PRs you reject. Lets you compare agents and route work to the ones that earn trust.
- **Throughput & token usage** — in-flight / ready / review / merged counts and rolled-up agent token usage per big task.
- Release/incident metrics (releases shipped, rollbacks, time-to-live, mean time merged→shipped) land with the remaining Phase 4 work.

## 15. Open problems — NOT solved by this tool (keep human)

- **Context provisioning**: reliably getting the right repo context into each agent run. Conventions + explicit `TouchPaths` help; expect to iterate.
- **Architectural coherence across parallel merges**: git merge is textual, not semantic — two branches can merge cleanly yet be architecturally incompatible. This is why you stay the architect; the tool gives you the audit/steer hooks to catch it, not a fix.
- **"Right thing" judgment**: no gate catches "passes tests but wasn't worth building." That's irreducibly yours, by design.
