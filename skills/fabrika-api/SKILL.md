---
name: fabrika-api
description: Drive a running Fabrika server via its REST API using curl.
---

This skill helps a coding agent interact with a running Fabrika server through its REST API. `curl` is the common denominator — every example below is a plain `curl` one-liner that works in any shell, regardless of which agent or IDE you are using. No SDK or special library is required.

## Base URL

The default base URL is `http://localhost:7777`. The port is `7777` by default and can be changed with the `--port` flag when starting the server:

```
fabrika --port 8080
```

All endpoints are namespaced under `/api`, so every request path starts with `/api/...`.

### Health check — verify Fabrika is running

Before using any other endpoint, confirm the server is up:

```sh
curl -s http://localhost:7777/api/version
```

A successful response returns JSON with at least two fields:

```json
{"version":"0.1.0","project":"/path/to/your/repo"}
```

If the command hangs or returns a connection-refused error, start Fabrika first (`fabrika` or `make run`) before proceeding.

### Data stores

Fabrika uses two separate SQLite databases:

| Store | Location | What lives there |
|---|---|---|
| **Global** | `~/.fabrika/fabrika.db` | Agents, conventions, settings |
| **Per-project** | `<repo>/.fabrika/fabrika.db` | Tasks, big-tasks, plans, decisions, releases |

When an API call reads or writes agent definitions or global settings it targets the global store. When it reads or writes tasks, plans, or decisions it targets the per-project store for whichever repository Fabrika was started in.

## API Reference

Every endpoint is namespaced under `/api`. HTTP methods are stated explicitly (GET/POST/PUT/DELETE). Path parameters use the placeholder `{id}` (or `{name}` for uploads) — substitute the real value when calling. Request and response bodies are JSON unless noted (uploads use multipart form data). Successful mutations return the affected resource or a small `{"status":"..."}` acknowledgement; errors return `{"error":"..."}` with a non-2xx status.

### Version

- **GET `/api/version`** — health check / server identity. No request body. Response: `{"version":"0.1.0","project":"<project name>"}`. Use this to confirm the server is up before issuing other calls.

### Tasks

A task is the unit of work an agent picks up. Core fields: `id`, `title`, `spec`, `acceptance` (`{verifyCmds[],heldOut[],properties[],lockedGlobs[]}`), `dependsOn[]`, `touchPaths[]`, `tags[]`, `attachments[]`, `riskTier` (`low|medium|high`), `priority` (`low|medium|high`), `status` (`ready|claimed|running|verifying|review|merged|blocked|failed`), `branch`, `agentId`, `preferredAgentId`, `reporter` (`user|planner`), `autoMerged`, `auditFlagged`, `reverted`, `mergeCommitSha`, `releaseId`, `pushed`.

- **GET `/api/tasks`** — list tasks (newest-first). Supports additive filter query params: `status`, `agentId`, `riskTier`. Each accepts a single value or a comma-separated list (OR within a param); different params combine with AND. Example: `GET /api/tasks?status=review,blocked&riskTier=high`. A `bigTaskId` filter is also supported — `GET /api/tasks?bigTaskId=BIGTASK_ID` returns only that big task's child tasks (the tasks its plan decomposed into). Response: JSON array of task objects.
- **POST `/api/tasks`** — create a task. Request body is a task object; `title` is required. `riskTier`/`priority`, if set, must be valid; an omitted `riskTier` is derived from `touchPaths` via the manifest. Server assigns `id`, sets `reporter` to `user`. Response: `201` with the created task.
- **GET `/api/tasks/{id}`** — fetch one task with its attempt history. Response: `{"task":{...},"attempts":[...]}`. Each attempt has `id`, `taskId`, `agentId`, `result` (`pass|fail|escalated`), `evidence` (`{stages,diff,artifacts}`), `usage` (`{inputTokens,outputTokens,totalTokens}`), `log`.
- **DELETE `/api/tasks/{id}`** — delete a task. No request body. Response: `{"status":"ok"}`-style acknowledgement.
- **POST `/api/tasks/{id}/accept`** — accept (merge) a task in review. Request body: `{"force":boolean}` (`force` overrides soft gate warnings). Response: acknowledgement.
- **POST `/api/tasks/{id}/reject`** — reject a task. Request body: `{"reason":"..."}`. Response: acknowledgement.
- **POST `/api/tasks/{id}/retry`** — re-queue a task for another attempt. No request body. Response: acknowledgement.
- **POST `/api/tasks/{id}/request-changes`** — send a task back to its agent with guidance instead of merging. Request body: `{"guidance":"..."}`. Response: acknowledgement.
- **POST `/api/tasks/{id}/assign`** — pin a preferred agent for the task. Request body: `{"agentId":"..."}` (empty string clears the preference). Response: acknowledgement.

### Reviews & batch operations

- **GET `/api/reviews`** — list tasks awaiting human review, each bundled with its latest attempt. Response: array of `{"task":{...},"attempt":{...}}`.
- **POST `/api/tasks/accept-batch`** — accept several tasks at once. Request body: `{"ids":["...","..."]}`. Response: array of per-id results `{"id":"...","ok":true}` or `{"id":"...","ok":false,"error":"..."}`.
- **POST `/api/tasks/retry-batch`** — retry several tasks at once. Request body: `{"ids":["...","..."]}`. Response: array of per-id results (same shape as accept-batch).

### Big tasks

A big task is a high-level intent that a planner decomposes into tasks. Fields: `id`, `title`, `intent`, `constraints[]`, `attachments[]`, `repoPath`, `status` (`backlog|draft|planning|planned|running|done|error`), `error`, `plannerAgentId`, `planFeedback`.

- **GET `/api/bigtasks`** — list big tasks (newest-first). Response: JSON array.
- **GET `/api/bigtasks/{id}`** — fetch a single big task by id. Response: the big-task object. Returns `404` for an unknown id. Use this to poll a big task's `status`/`plannerAgentId` while planning runs asynchronously.
- **POST `/api/bigtasks`** — create a big task; `title` is required. A `backlog` status parks it as-is; otherwise the repo is preflighted and, if a planner agent is configured, planning runs asynchronously (status → `planning`). Response: `201` with the created big task.
- **POST `/api/bigtasks/reorder`** — reorder the backlog. Request body: `{"ids":["...","..."]}` in the desired order. Response: acknowledgement.
- **DELETE `/api/bigtasks/{id}`** — delete a big task. No request body.
- **POST `/api/bigtasks/{id}/plan`** — promote a backlog big task into the planning flow (decompose into a proposed plan). No request body.
- **POST `/api/bigtasks/{id}/replan`** — re-run planning for a big task. No request body.
- **POST `/api/bigtasks/{id}/stop`** — cancel an in-flight planning run. Request body: `{"reason":"..."}` (optional). Response: acknowledgement.

### Plans & decisions

A plan is a proposed decomposition awaiting approval. Fields: `id`, `bigTaskId`, `tasks[]`, `openDecisions[]`, `status` (`proposed|approved|rejected`); list/detail responses also include the parent `bigTask`. A decision is a question the planner (or a task) couldn't resolve: `id`, `planId`, `taskId`, `question`, `options[]`, `context`, `answer`, `promote`, `status` (`open|answered`).

- **GET `/api/plans`** — list plans, each assembled with its big task, tasks, and open decisions. Response: JSON array of plan views.
- **GET `/api/plans/{id}`** — fetch one assembled plan. Response: a plan view object.
- **POST `/api/plans/{id}/approve`** — approve a proposed plan (its tasks become `ready`). No request body. Response: acknowledgement.
- **POST `/api/plans/{id}/reject`** — reject a proposed plan. No request body. Response: acknowledgement.
- **POST `/api/plans/{id}/revise`** — ask the planner to re-think the plan. Request body: `{"feedback":"..."}`. Response: acknowledgement.
- **GET `/api/decisions`** — list open decisions. Response: JSON array of decision objects.
- **POST `/api/decisions/{id}/answer`** — answer a decision. Request body: `{"answer":"...","promote":boolean}` (`promote:true` turns the answer into a standing convention). Response: acknowledgement.

### Audits

The post-merge trust backstop: a sampled share of auto-merged tasks the human eyeballs after the fact.

- **GET `/api/audits`** — list auto-merged, audit-flagged, non-reverted tasks, each with its latest attempt's evidence. Response: array of `{"task":{...},"attempt":{...}}`.
- **POST `/api/tasks/{id}/audit-ok`** — clear a task's audit flag ("looks good"), removing it from the audit queue. No request body. Response: `{"status":"ok"}`.
- **POST `/api/tasks/{id}/revert`** — record a merged task as a change-failure (feeds the change-failure-rate metric; the git revert itself is left to the human). No request body. Response: `{"status":"reverted"}`.

### Agents

An agent is a registered coding program. Fields: `id`, `name`, `photo` (data URI), `command` (template substituting `{prompt_file} {worktree} {model}`), `model`, `roles[]` (`implementer|planner|reviewer`), `tags[]`, `concurrency`, `timeout` (e.g. `"20m"`), `maxAttempts`, `enabled`. Agents live in the global store.

- **GET `/api/agents`** — list registered agents. Response: JSON array.
- **POST `/api/agents`** — create an agent. Request body is an agent object (validated; `photo` capped at 2 MiB). Server assigns `id`. Response: `201` with the created agent.
- **PUT `/api/agents/{id}`** — replace an agent's definition. Request body is the full agent object. Response: the updated agent.
- **DELETE `/api/agents/{id}`** — delete an agent. No request body.
- **POST `/api/agents/{id}/enable`** — mark an agent enabled. No request body.
- **POST `/api/agents/{id}/disable`** — mark an agent disabled. No request body.

### Conventions

A convention is a standing rule agents must follow. Fields: `id`, `rule`, `status` (`proposed|approved|rejected`). Conventions live in the global store.

- **GET `/api/conventions`** — list conventions; optional `status` query param filters by status (e.g. `GET /api/conventions?status=approved`). Response: JSON array.
- **POST `/api/conventions`** — create a convention. Request body: `{"rule":"..."}` (`rule` required). Response: `201` with the created convention.
- **DELETE `/api/conventions/{id}`** — delete a convention. No request body.
- **POST `/api/conventions/{id}/approve`** — approve a proposed convention. No request body.
- **POST `/api/conventions/{id}/reject`** — reject a proposed convention. No request body.

### Metrics & steering

- **GET `/api/metrics`** — board-wide and per-agent metrics. Response includes `agents[]` (each with `agentId`, `name`, `enabled`, `concurrency`, `running`, `planning`, `merged`, `planned`, `kickedBack`, `kickbackRate`, token counts), plus board fields: `wip`, `planning`, `wipCap`, `ready`, `inReview`, `blocked`, `merged`, `throughput`, `autoMerged`, `manualMerged`, `reverted`, `auditQueue`, `autoMergeShare`, `touchesPerUnit`, `changeFailRate`, `auditRate`, `mutationTesting`, `totalTokens`.
- **POST `/api/steer`** — imperative control over a single task. Request body: `{"action":"assign|cancel","taskId":"...","agentId":"...","reason":"..."}`. `taskId` is required. `action:"assign"` pins `agentId` as the preferred agent (empty clears it); `action:"cancel"` rejects the task with `reason`. Response: `{"status":"ok"}`.

### Attention

- **GET `/api/attention`** — a single bundle of everything awaiting the human, for the unified inbox. Response: `{"cursor":<number>,"reviews":[...],"decisions":[...],"audits":[...],"plans":[...]}` — `reviews` and `audits` are `{task,attempt}` items, `decisions` are decision objects, `plans` are plan views. The numeric `cursor` is a monotonic board-change counter. Supports **long-poll**: pass `since=<cursor>` and `wait=<seconds>` to block until the board changes — e.g. `GET /api/attention?since=42&wait=30`. If the cursor has already advanced past `since`, the call returns immediately with the current bundle; otherwise it blocks up to `wait` seconds (capped at 60) and wakes on any board change, returning the fresh bundle (and new `cursor`) the moment something happens — or the unchanged bundle if the wait elapses first. Typical loop: read the `cursor` from one response, then re-request with `?since=<cursor>&wait=30` to stream changes without busy-polling.

### Settings & config

- **GET `/api/settings`** — flat map of string settings (role assignment, per-tier routing, WIP cap). Response: `{"<key>":"<value>",...}`.
- **PUT `/api/settings`** — upsert settings. Request body: a flat `{"<key>":"<value>"}` map; keys are merged, not replaced wholesale. Response: the full settings map after the merge.
- **GET `/api/config`** — the in-memory project manifest (`fabrika.toml`) the engine uses. Response: the config object.
- **PUT `/api/config`** — persist an updated manifest to `fabrika.toml` and apply it to the running engine. Request body: the full config object; an invalid config (e.g. bad autonomy policy) yields `400`. Response: the saved config.

### Push

- **GET `/api/push/status`** — whether the integration branch has unpushed commits. Response: a status object indicating whether there is something to ship.
- **POST `/api/push`** — push the integration branch to its remote. No request body. Response: `{"status":"pushed","detail":"<git summary>"}`. Failures (no remote, non-fast-forward, network) return `409` with git's message.

### Releases

A release is a shipped integration-branch snapshot. Fields: `id`, `sha`, `prevSha`, `status` (`pending|deploying|baking|live|failed|rolled_back`), `deployLog`, `healthLog`, `error`, `createdAt`, `deployedAt`, `liveAt`.

- **GET `/api/releases`** — list releases (newest-first). Response: JSON array.
- **POST `/api/releases/ship`** — cut and deploy a new release from unshipped merged work. No request body. Response: the created release.
- **GET `/api/releases/unshipped`** — merged tasks not yet covered by a release. Response: JSON array.
- **GET `/api/releases/{id}`** — fetch one release with detail. Response: a release detail object.
- **POST `/api/releases/{id}/rollback`** — roll a release back to its `prevSha`. No request body. Response: the updated release.

### Comments & uploads

A comment is a note on a task or big task. Fields: `id`, `taskId`, `bigTaskId`, `authorType` (`user|agent`), `authorId`, `body`, `attachments[]`, `createdAt`.

- **GET `/api/tasks/{id}/comments`** — list a task's comments. Response: JSON array.
- **POST `/api/tasks/{id}/comments`** — add a comment to a task. Request body: `{"body":"...","attachments":["/api/uploads/<name>",...]}`. Attachments must be upload URLs. Response: the created comment.
- **GET `/api/bigtasks/{id}/comments`** — list a big task's comments. Response: JSON array.
- **POST `/api/bigtasks/{id}/comments`** — add a comment to a big task. Request body: `{"body":"...","attachments":[...]}`. Response: the created comment.
- **POST `/api/uploads`** — upload an image. Request body: `multipart/form-data` with a `file` field (png/jpeg/gif/webp, max 10 MiB). Response: `201` with `{"url":"/api/uploads/<name>"}`. Example: `curl -s -F file=@shot.png http://localhost:7777/api/uploads`.
- **GET `/api/uploads/{name}`** — serve a previously uploaded image by its generated name. No request body; returns the raw image bytes (cacheable).

### Events (WebSocket)

- **GET `/api/events`** — a WebSocket endpoint streaming live board changes. Connect with a WebSocket client (e.g. `websocat ws://localhost:7777/api/events`). Each message is a JSON envelope `{"type":"<event>","payload":<object>}`. Event types:
  - `task.created`, `task.updated` — a task appeared or changed (payload: the task).
  - `task.comment.added` — a comment was added to a task (payload: the comment).
  - `bigtask.created`, `bigtask.updated`, `bigtask.reordered` — big task lifecycle (payload: the big task, or the new order).
  - `bigtask.comment.added` — a comment was added to a big task (payload: the comment).
  - `agent.created`, `agent.updated`, `agent.deleted` — agent registry changes (payload: the agent).

  Use the events stream to react to board changes without polling; fall back to the GET list endpoints above for the current snapshot.

## Workflows

Copy-pasteable curl recipes for the most common end-to-end flows. Replace placeholder values like `TASK_ID`, `PLAN_ID`, `BIGTASK_ID`, and `DECISION_ID` with real IDs returned by earlier calls.

---

### 1. Create a task

Submit a unit of work for an agent to pick up. `title` is required; `spec` describes what to do; `acceptance.verifyCmds` lists shell commands the gate runs to verify the result.

```sh
curl -X POST http://localhost:7777/api/tasks \
  -H 'Content-Type: application/json' \
  -d '{
    "title": "Add dark-mode toggle to the settings page",
    "spec": "Implement a dark/light mode toggle in web/src/settings.ts. Persist the preference in localStorage. The toggle must appear in the page header.",
    "acceptance": {
      "verifyCmds": ["make test"]
    },
    "riskTier": "low",
    "priority": "medium"
  }'
```

Expected response: `201 Created` with the full task object. The task starts with `status: "ready"` and is picked up by the next available agent.

---

### 2. Check what needs attention

Fetch the unified inbox — everything awaiting a human decision in one call.

```sh
curl -s http://localhost:7777/api/attention | jq .
```

Response shape:

```json
{
  "reviews":   [ { "task": {...}, "attempt": {...} } ],
  "decisions": [ { "id": "...", "question": "...", "options": [...] } ],
  "audits":    [ { "task": {...}, "attempt": {...} } ],
  "plans":     [ { "id": "...", "status": "proposed", "tasks": [...], "openDecisions": [...] } ]
}
```

- **`reviews`** — tasks in `review` status whose diffs are ready for merge or rejection.
- **`decisions`** — open questions the planner could not resolve on its own (answer these to unblock planning).
- **`audits`** — auto-merged tasks sampled for spot-check (each has `attempt.evidence` with the diff and artifacts).
- **`plans`** — proposed plans awaiting approval before their child tasks are released to agents.

---

### 3. Accept a single task

Merge a task that is in `review` status.

```sh
curl -s -X POST http://localhost:7777/api/tasks/TASK_ID/accept \
  -H 'Content-Type: application/json' \
  -d '{}'
```

The task transitions from `review` → `merged`. Omit the body or pass `{}` for a normal accept.

#### Accept a batch

Accept multiple tasks in one call (useful after reviewing a sprint's worth of work).

```sh
curl -s -X POST http://localhost:7777/api/tasks/accept-batch \
  -H 'Content-Type: application/json' \
  -d '{"ids": ["TASK_ID_1", "TASK_ID_2", "TASK_ID_3"]}'
```

Response: an array of per-id results. Each entry is either `{"id":"...","ok":true}` or `{"id":"...","ok":false,"error":"..."}`.

---

### 4. Retry failed work as a batch

Re-queue multiple `failed` or `blocked` tasks for another agent attempt.

```sh
curl -s -X POST http://localhost:7777/api/tasks/retry-batch \
  -H 'Content-Type: application/json' \
  -d '{"ids": ["TASK_ID_1", "TASK_ID_2"]}'
```

Each listed task transitions back to `ready`. Response shape is the same as accept-batch.

To find which tasks need retrying, filter by status first:

```sh
curl -s 'http://localhost:7777/api/tasks?status=failed,blocked' | jq '[.[].id]'
```

---

### 5. Force-merge a failed or blocked task

Override soft gate warnings and merge a task even if the verifier flagged issues. Use with care — this bypasses the normal quality gate.

```sh
curl -s -X POST http://localhost:7777/api/tasks/TASK_ID/accept \
  -H 'Content-Type: application/json' \
  -d '{"force": true}'
```

`force: true` tells the engine to ignore non-fatal gate failures and proceed with the merge. The task must still be in a state that allows acceptance (e.g. `review` or `failed`/`blocked` with surviving worktree). Status transitions to `merged`.

---

### 6. Create and plan a big task, then approve the plan

A big task is a high-level intent that a planner agent decomposes into concrete tasks.

**Step 1 — create the big task** (planning starts asynchronously if a planner agent is configured):

```sh
curl -s -X POST http://localhost:7777/api/bigtasks \
  -H 'Content-Type: application/json' \
  -d '{
    "title": "Migrate authentication to JWT",
    "intent": "Replace the current session-cookie auth with stateless JWT tokens. Keep backward compat for 30 days via a dual-read middleware.",
    "constraints": ["Must not break existing /api/auth tests", "No new external dependencies"]
  }'
```

Response: `201 Created` with the big-task object. `status` will be `planning` if a planner is configured, or `backlog` if none is set.

**Step 2 — trigger planning for a backlog task** (skip if already `planning`/`planned`):

```sh
curl -s -X POST http://localhost:7777/api/bigtasks/BIGTASK_ID/plan \
  -H 'Content-Type: application/json'
```

**Step 3 — list proposed plans** and inspect the decomposition:

```sh
curl -s http://localhost:7777/api/plans | jq '.[] | {id, status, taskCount: (.tasks | length), openDecisions: (.openDecisions | length)}'
```

**Step 4 — approve a proposed plan** (releases child tasks to `ready`):

```sh
curl -s -X POST http://localhost:7777/api/plans/PLAN_ID/approve \
  -H 'Content-Type: application/json'
```

After approval the plan's `status` moves to `approved` and each child task becomes `ready` for agents to pick up.

---

### 7. Answer an open decision

Decisions are questions the planner raised that it could not resolve autonomously. Answering them unblocks planning or task execution.

```sh
curl -s http://localhost:7777/api/decisions | jq '.[] | {id, question, options}'
```

Pick the relevant decision ID, then answer it:

```sh
curl -s -X POST http://localhost:7777/api/decisions/DECISION_ID/answer \
  -H 'Content-Type: application/json' \
  -d '{
    "answer": "Use PostgreSQL — the team already operates it in production.",
    "promote": true
  }'
```

`promote: true` turns the answer into a standing convention so future planners and agents follow it automatically. Use `promote: false` (or omit) for one-off answers that should not become policy. The decision's `status` transitions from `open` → `answered`.

---

### 8. Ship / push merged work

Check whether there are unpushed commits on the integration branch, then push:

```sh
# See what's waiting to be pushed
curl -s http://localhost:7777/api/push/status | jq .

# Push the integration branch to its remote
curl -s -X POST http://localhost:7777/api/push \
  -H 'Content-Type: application/json'
```

A successful push returns:

```json
{"status": "pushed", "detail": "origin/main: abc1234..def5678"}
```

Failures (no remote configured, non-fast-forward, network error) return `409 Conflict` with git's error message in `detail`.

## Pitfalls

Non-obvious behaviors that burn agents (and humans) the first time.

---

### 1. Port is not always 7777

`7777` is the default, but anyone who started Fabrika with `--port 8080` (or any other value) will see connection-refused errors if you hardcode `7777`. **Always confirm the live base URL before issuing other calls:**

```sh
curl -s http://localhost:7777/api/version
# if that fails, try the port Fabrika was actually started on
```

The `/api/version` response also returns `"project"`, which tells you which repo you are talking to — useful when multiple Fabrika instances are running for different projects.

---

### 2. Force-merge semantics: `failed`/`blocked` tasks need `{"force":true}`

`POST /api/tasks/{id}/accept` without a body (or with `{}`) **only succeeds when the task is in `review` status.** Calling it on a `failed` or `blocked` task returns `409 Conflict`. To merge those states you must explicitly opt in:

```sh
curl -s -X POST http://localhost:7777/api/tasks/TASK_ID/accept \
  -H 'Content-Type: application/json' \
  -d '{"force": true}'
```

`force: true` signals deliberate intent — it overrides soft gate warnings and bypasses the normal quality check. The worktree from the failed attempt must still exist; if it has been cleaned up, the accept will fail regardless of `force`.

---

### 3. `closed` is not the same as `failed` or `blocked`

Full task status set (in lifecycle order):

| Status | Meaning |
|---|---|
| `planned` | Created by a planner, not yet released |
| `ready` | Queued, waiting for an agent |
| `claimed` | An agent has picked it up |
| `running` | Agent is actively working |
| `verifying` | Gate is running acceptance checks |
| `review` | Gate passed; awaiting human merge or rejection |
| `merged` | Accepted and merged into the integration branch |
| `blocked` | Agent escalated — needs human input before proceeding |
| `failed` | Attempt(s) exhausted or gate hard-failed |
| `closed` | Dismissed by a human (kicked back, won't-fix, duplicate) |

**`failed` and `blocked` are still in the attention/review queue** — they are awaiting a human decision (retry, force-merge, or close). **`closed` is a terminal dismissal** — the task is removed from the queue. Only `closed` tasks can be `DELETE`d; trying to delete a task in any other status returns an error.

---

### 4. Empty diff = automatic verification failure

If an agent's attempt produces **no file changes** (empty diff), the gate treats it as a failure regardless of whether `verifyCmds` pass. An agent that writes only to temp files, only modifies files outside the worktree, or exits without committing will always fail. Ensure the agent commits at least one meaningful change before the gate runs.

---

### 5. Global store vs per-project store

Fabrika has two separate SQLite databases:

| What | Store | Scope |
|---|---|---|
| Agents, conventions, settings | **Global** (`~/.fabrika/fabrika.db`) | Shared across all repos |
| Tasks, big tasks, plans, decisions, releases | **Per-project** (`<repo>/.fabrika/fabrika.db`) | Isolated to one repo |

An agent registered in the global store can be dispatched to tasks in any project. If you delete an agent or update a convention, the change is reflected everywhere. Conversely, tasks and plans from repo A are completely invisible when Fabrika is started in repo B.

---

### 6. Batch endpoints return per-item results — they never fail wholesale

`POST /api/tasks/accept-batch` and `POST /api/tasks/retry-batch` always return `200` with an array of per-item results, even when individual items fail:

```json
[
  {"id": "abc", "ok": true},
  {"id": "def", "ok": false, "error": "task not in review status"}
]
```

Do **not** assume a `200` response means all items succeeded. Always iterate the array and check `ok` on each entry.

---

### 7. `promote: true` on a decision answer creates a standing Convention

When you answer a decision with `"promote": true`, Fabrika does more than record the answer — it creates a new entry in the global **Conventions** store:

```sh
curl -s -X POST http://localhost:7777/api/decisions/DECISION_ID/answer \
  -H 'Content-Type: application/json' \
  -d '{"answer": "Use PostgreSQL.", "promote": true}'
```

That convention is then injected into every future agent prompt across all projects. Use `promote: true` only for policy-level decisions you want to apply permanently; use `promote: false` (or omit) for one-off answers. To remove an elevated convention later, delete it via `DELETE /api/conventions/{id}`.

---

### 8. Push and Ship can return 409

`POST /api/push` and `POST /api/releases/ship` both push to a git remote, and both return **`409 Conflict`** when git refuses the push:

- **No remote configured** — the repo has no `origin` (or the configured remote is missing).
- **Non-fast-forward** — the remote branch has commits that are not in the local integration branch (someone pushed directly, or a force-push happened upstream).
- **Network / auth error** — git's error message is forwarded verbatim in the `detail` field.

On a `409`, read `detail` to diagnose. A non-fast-forward requires a manual `git pull --rebase` (or equivalent) on the integration branch before retrying.

---

### 9. `POST /api/bigtasks/{id}/plan` is asynchronous — `draft` is not a failure

`POST /api/bigtasks/{id}/plan` does **not** return the finished plan. It returns `200` immediately with the big task in the queued `draft` state and `plannerAgentId` populated to the planner it assigned — this is the expected success response, **not** an error. The actual decomposition runs in the background afterward, so the plan only appears once the planner finishes.

Do not treat the `draft` status (or the absence of a plan in the response) as a failure. Instead, **`poll GET /api/bigtasks/{id}`** for the big task's `status` to track progress — it advances through `draft` → `planning` → `planned` (or `error`). Once it reaches `planned`, fetch the proposed plan from `GET /api/plans` (or filter child tasks with `GET /api/tasks?bigTaskId=BIGTASK_ID`).

```sh
# Kick off planning (returns immediately, status: "draft")
curl -s -X POST http://localhost:7777/api/bigtasks/BIGTASK_ID/plan \
  -H 'Content-Type: application/json'

# Then poll for status until it leaves the queued state
curl -s http://localhost:7777/api/bigtasks/BIGTASK_ID | jq '{status, plannerAgentId}'
```
