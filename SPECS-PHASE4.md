# Fabrika — Phase 4 spec: Close the loop downstream (releases, deploy, incidents, self-healing)

Extends `SPECS.md`. Phases 0–3 are complete. This phase makes the binary aware of
production: merged ≠ done. Tasks accumulate on `main`, the human ships a **release**,
the machine watches it **bake**, and production errors flow back in as **incidents**
that auto-rollback bad releases and spawn fix tasks.

## 1. Principle

Human gates the forward direction (the **Ship** button); machine guards the backward
direction (auto-rollback). Rollback is **deploy-level, not git-level**: rolling back
redeploys the previous SHA while `main` keeps all merges, so innocent tasks in a bad
batch are merely un-deployed, never reverted. The fix merges on top; the next Ship
carries everything back out. No cherry-picks, no git surgery.

## 2. Vocabulary

- **Release** — a deploy of the base branch at a SHA. Covers all tasks whose merge
  commits are in `prev_sha..sha`.
- **Bake** — a quiet period after a successful deploy during which Fabrika watches
  incidents before declaring the release `live`.
- **Incident** — a deduplicated production error (one row per fingerprint, with an
  occurrence counter), optionally correlated to a suspect release/task.
- **Rollback** — redeploy `prev_sha` (seconds, no git change). The incident reflex.
- **Revert** — `git revert -m 1 <merge_commit_sha>` of one task, run through gates as
  a normal high-priority task (minutes, calm path).

## 3. Data model changes

### 3.1 `tasks` (per-project DB) — new columns

```
merge_commit_sha TEXT NOT NULL DEFAULT ''   -- captured at merge time
release_id       TEXT NOT NULL DEFAULT ''   -- set when a release covers this task
```

### 3.2 `releases` (per-project DB) — new table

```
id          TEXT PRIMARY KEY
sha         TEXT NOT NULL          -- base branch commit being deployed
prev_sha    TEXT NOT NULL          -- previous live SHA ('' for first release)
status      TEXT NOT NULL          -- pending | deploying | baking | live | failed | rolled_back
deploy_log  TEXT NOT NULL DEFAULT ''
health_log  TEXT NOT NULL DEFAULT ''
error       TEXT NOT NULL DEFAULT ''
created_at  TEXT NOT NULL
deployed_at TEXT NOT NULL DEFAULT ''
live_at     TEXT NOT NULL DEFAULT ''
```

Status machine: `pending → deploying → baking → live`; `deploying → failed`
(deploy/health failure, auto-rollback attempted); `baking → rolled_back`
(correlated incident during bake). `live` releases can also go `rolled_back`
via the manual button.

### 3.3 `incidents` (per-project DB) — new table

```
id           TEXT PRIMARY KEY
fingerprint  TEXT NOT NULL UNIQUE  -- hash(error type + top stack frames)
title        TEXT NOT NULL
stack        TEXT NOT NULL DEFAULT ''
payload      TEXT NOT NULL DEFAULT ''   -- raw source event JSON
count        INTEGER NOT NULL DEFAULT 1
first_seen   TEXT NOT NULL
last_seen    TEXT NOT NULL
status       TEXT NOT NULL          -- open | fixing | resolved | ignored
task_id      TEXT NOT NULL DEFAULT ''   -- spawned fix task
suspect_release_id TEXT NOT NULL DEFAULT ''
suspect_task_id    TEXT NOT NULL DEFAULT ''
```

Repeats of a known fingerprint bump `count` + `last_seen` only — never a second
task. New occurrences of a `resolved` incident reopen it (status → `open`, fresh
fix task allowed).

## 4. Manifest — `fabrika.toml` additions

```toml
[deploy]
mode         = "manual"          # manual | per-merge | interval (manual is default; others later)
command      = "make deploy"     # required to enable releases; empty = feature off
health       = "curl -fsS https://app.example.com/health"  # optional; empty = skip
rollback     = ""                # optional; empty = re-run `command` at prev_sha checkout
bake_minutes = 30                # 0 = skip bake, go straight to live

[feedback]
[[feedback.sources]]
type         = "command"         # "command" | "sentry"
command      = "./scripts/tail-errors.sh"   # prints JSON array of error events; see §7
poll_seconds = 60
```

Config validation: `deploy.mode` must be one of the three values; sources require
`poll_seconds >= 10`; `type=command` requires non-empty `command`. All of
`[deploy]`/`[feedback]` optional — absent sections disable the features cleanly.

## 5. Engine changes

### 5.1 Capture merge SHA (both merge sites)

After `repo.Merge()` succeeds in `finishGreen()` (internal/engine/engine.go ~:682)
and `Accept()` (~:839), resolve `git rev-parse HEAD` on the base branch and persist
it to `tasks.merge_commit_sha`. Add `RevParse(ref string)` to `internal/git` if absent.

### 5.2 Real revert

`Engine.Revert(taskID)` (internal/engine/engine.go:1076) currently only flags
`task.Reverted`. Change it to:

1. Refuse if `merge_commit_sha` is empty (pre-Phase-4 merges) — keep flag-only
   behavior for those, with a comment explaining.
2. Otherwise create a new task: title `Revert: <orig title>`, spec instructing
   `git revert -m 1 <sha>` plus the original task's spec for context, priority
   `high`, risk tier inherited from the original. It flows through the normal
   dispatch → gates → review/auto-merge pipeline. If the revert conflicts, the
   agent resolves it like any other task.
3. Set `Reverted=true` on the original immediately (metrics unchanged).

### 5.3 Release runner (new: `internal/release`)

A `Manager` owned by the engine, single-flight (one release in motion at a time):

- `Ship()` — collect base-branch SHA, create release (`pending`), run
  `deploy.command` via the same command-runner used by gates (capture output to
  `deploy_log`), then `deploy.health` if set (`health_log`). On success →
  `baking` (or `live` if `bake_minutes == 0`), stamp `deployed_at`, and set
  `release_id` on every task whose `merge_commit_sha` is in `prev_sha..sha`
  (`git rev-list prev_sha..sha`). On failure → `failed`, attempt rollback (§5.4),
  surface `error`.
- Ship is a no-op error if no unshipped merged tasks exist or a release is already
  in motion.
- `prev_sha` = SHA of the latest `live` or `baking` release; `''` if none (first
  release skips rollback on failure — nothing to roll back to).

### 5.4 Rollback

`Rollback(releaseID)`:
- If `deploy.rollback` set: run it with env `FABRIKA_ROLLBACK_SHA=<prev_sha>`.
- Else: `git worktree add` a temp checkout at `prev_sha`, run `deploy.command`
  inside it, remove the worktree.
- On success: release → `rolled_back`. Tasks keep `release_id` (history), and the
  board's "unshipped" count includes them again so the next Ship re-carries them.
- Manual endpoint always available for `baking`/`live` releases.

### 5.5 Bake timer

When a release enters `baking`, start a timer (`bake_minutes`). On expiry with no
correlated incident → `live`, stamp `live_at`, broadcast. Timer state is derived
(recomputed from `deployed_at` on startup), not persisted — surviving restarts
mid-bake must work.

### 5.6 Feedback poller (new: `internal/feedback`)

One goroutine per source, ticking every `poll_seconds`:

- `type=command`: run the command; stdout must be a JSON array of events
  `{"title": str, "stack": str, "fingerprint": str?, ...}` (extra fields preserved
  in `payload`). Non-zero exit or bad JSON → log and skip the tick (never crash
  the loop).
- `type=sentry`: stub acceptable in this phase — define the source interface so
  it slots in later.
- Fingerprint: use the event's own if provided, else
  `sha256(title + first 5 stack-frame file:line pairs)[:16]`.
- Known fingerprint → bump `count`/`last_seen`. New fingerprint → insert incident
  (`open`) and run correlation (§5.7). Resolved fingerprint reappearing → reopen.

### 5.7 Correlation + incident reflex

On a **new** incident:

1. If a release is `baking`: mark it suspect (`suspect_release_id`), then
   **auto-rollback** that release and post a System comment on each covered task.
2. Suspect task: intersect stack-trace file paths with the diffs of the release's
   tasks (diff lives in `attempts.evidence`); single best overlap →
   `suspect_task_id`.
3. Spawn a fix task: title `Fix incident: <title>`, spec = stack trace + occurrence
   info + (if a suspect exists) the suspect task's title and diff summary, priority
   `high`, reporter `incident`. Set `incidents.task_id`, status → `fixing`.
4. If no release is baking (incident arrived while `live` or idle): no rollback —
   just create the incident + fix task. Auto-rollback applies only to baking
   releases; rolling back a long-live release is a human call (manual button).
5. When the fix task merges, incident → `resolved` (hook in the merge paths).

## 6. API additions (internal/api, same mux/auth posture as existing routes)

```
GET    /api/releases                  — list, newest first
POST   /api/releases/ship             — Ship(); 409 if in-flight or nothing to ship
GET    /api/releases/{id}             — detail incl. covered tasks, logs
POST   /api/releases/{id}/rollback    — manual rollback (baking|live only)
GET    /api/releases/unshipped        — merged tasks not yet covered by a release
GET    /api/incidents                 — list (filter by status)
POST   /api/incidents/{id}/ignore     — status → ignored
POST   /api/incidents/{id}/resolve    — status → resolved (manual)
```

WebSocket events: `release.updated`, `incident.created`, `incident.updated`
(same hub as `task.*`).

`POST /api/tasks/{id}/revert` keeps its route; behavior upgraded per §5.2.

## 7. Web UI (web/)

- **Board header**: `Ship · N` button (N = unshipped merged tasks; hidden when
  deploy disabled, disabled when N=0 or release in flight). Click → drawer listing
  the tasks (titles = de-facto release notes) with one confirm.
- **Release strip**: thin always-visible bar showing latest release:
  `#42 · baking · 18m left` / `live` / `rolled back` / `failed`, color-coded; click
  → release detail (covered tasks, deploy/health logs, rollback button).
- **Task card sidebar**: new `RELEASE` field — `#42 · live`, or `unshipped` for
  merged-but-not-released. System comment lines for `shipped in #42`,
  `rolled back`, `reverted`.
- **Incidents view**: list with title, count, first/last seen, status, suspect
  task link, fix-task link, [Ignore] [Resolve] and (when applicable) [Rollback].
  Incident rows surface on the board as a banner while any incident is `open`.

## 8. Build plan (each step independently shippable, in order)

1. **Merge SHA + real revert** — migration (tasks columns), capture SHA at both
   merge sites, upgrade `Revert()` to spawn the revert task. Tests: merge a task,
   assert SHA recorded; revert it, assert revert task created with correct spec
   and original flagged.
2. **Releases + Ship + rollback (manual mode)** — migration (releases table),
   `internal/release`, config `[deploy]`, API routes, Ship button + release strip
   + task-card RELEASE field. Tests: ship with stub deploy command, assert task
   coverage via rev-list, failed health triggers rollback, single-flight enforced,
   bake timer survives restart.
3. **Feedback poller + incidents** — migration (incidents table), `internal/feedback`,
   config `[feedback]`, dedup/reopen logic, fix-task spawning, incidents UI.
   Tests: command source emits two identical events → one incident count=2 and one
   task; resolved fingerprint reopens.
4. **Correlation + auto-rollback** — suspect release/task matching, baking-release
   reflex, resolve-on-merge hook. Tests: new incident during bake rolls back and
   spawns fix task with suspect context; incident while idle creates task without
   rollback.

## 9. Risk notes for the planner

- All migrations touch `internal/store/**` → high risk tier per fabrika.toml;
  expect human review on those tasks.
- Deploy/rollback commands are user code: always run via the existing gate
  command-runner with timeouts; never interpolate into shell strings beyond what
  gates already do.
- The poller and bake timer must be resilient: a panicking source or a restart
  mid-bake must never wedge the engine loop.
- Keep `merged` terminal for tasks. Release/incident state lives on their own
  entities; tasks only gain pointer fields.

## 10. Metrics additions

- `releases_shipped`, `rollbacks` (auto vs manual), time-to-live (deployed→live)
- `incidents_open`, mean time merged→shipped, mean time incident→fix-merged
- Change-failure rate now counts rollbacks alongside reverts.
