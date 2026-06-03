# Fabrika

A local, single-binary tool that turns one person into a software factory. You
define tasks and make decisions; agents do the work; the tool handles
build/test/verify and only surfaces what needs your judgment. See
[SPECS.md](SPECS.md) for the full design.

> **Status: Phase 0 complete (the live loop works).** The binary boots, serves
> the cockpit UI, and runs the full loop: define a task → an agent runs it in an
> isolated git worktree → the verification gate checks it → evidence + diff land
> in the **Accept** queue → you merge (to `main`) or kick back. Dispatch is
> single-flight; parallel scheduling (Phase 1) and a planner (Phase 2) are next.

## Build

Requires Go 1.22+ and Node 18+ (for the esbuild UI bundle).

```sh
make build      # builds the UI (esbuild) then the Go binary, UI embedded
make test       # go test ./...
```

## Use

```sh
cd /path/to/your/repo
fabrika init    # scaffold a fabrika.toml
fabrika         # start the UI at http://localhost:7777 (opens browser)
fabrika --port 8080 --no-open
```

## What works today

- `fabrika init` scaffolds the per-repo `fabrika.toml` manifest (abstract
  build/verify verbs, risk globs, autonomy tiers).
- **Agents** screen — create/edit/enable/disable registered agents. Persisted in
  the **global store** (`~/.fabrika/fabrika.db`), reusable across repos.
- **Tasks** screen — create a task (spec + verify commands). The engine
  automatically routes it to an enabled implementer agent, runs the agent in a
  fresh git worktree (`.fabrika/worktrees/<id>` on a `fabrika/task-*` branch),
  captures its work, and runs the verification gate.
- **Accept** screen — the review queue. Each item shows the gate's per-stage
  results and the branch diff. **Merge** green work (it merges to your current
  branch) or **kick back** the rest. Failing runs surface as `failed` with their
  red output; escalations (`fabrika_DECISION:`) surface as `blocked`.
- Live updates over WebSocket (`/api/events`), including a pending-count badge on
  Accept.
- The remaining cockpit surfaces (Define / Approve / Decide / Engine room) are
  placeholders; their endpoints return `501` until later phases.

## Layout

| Path                | Role                                                        |
| ------------------- | ----------------------------------------------------------- |
| `cmd/fabrika`       | CLI: flags, `init`, server boot, browser open               |
| `internal/config`   | `fabrika.toml` parse + scaffold                             |
| `internal/model`    | shared domain types (SPECS §5)                              |
| `internal/store`    | SQLite: global + per-project DBs, migrations, repos         |
| `internal/api`      | REST + WebSocket surface (SPECS §11)                        |
| `internal/git`      | git-CLI wrappers (worktree/branch/diff/merge) — plumbing    |
| `internal/gate`     | verb runner + Evidence normalization — plumbing             |
| `internal/agent`    | registry + subprocess adapter + routing — plumbing          |
| `internal/engine`   | dispatch loop: route → worktree → agent → gate → merge      |
| `internal/planner`  | BigTask → Tasks (Phase 0 passthrough)                       |
| `web`               | vanilla-TS UI, built with esbuild, embedded via `go:embed`  |
