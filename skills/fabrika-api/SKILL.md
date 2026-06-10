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

- **GET `/api/tasks`** — list tasks (newest-first). Supports additive filter query params: `status`, `agentId`, `riskTier`. Each accepts a single value or a comma-separated list (OR within a param); different params combine with AND. Example: `GET /api/tasks?status=review,blocked&riskTier=high`. Response: JSON array of task objects.
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

- **GET `/api/attention`** — a single bundle of everything awaiting the human, for the unified inbox. Response: `{"reviews":[...],"decisions":[...],"audits":[...],"plans":[...]}` — `reviews` and `audits` are `{task,attempt}` items, `decisions` are decision objects, `plans` are plan views.

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
