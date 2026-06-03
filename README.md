# Fabrika

A local, single-binary tool that turns one person into a software factory. You
define tasks and make decisions; agents do the work; the tool handles
build/test/verify and only surfaces what needs your judgment. See
[SPECS.md](SPECS.md) for the full design.

> **Status: Phase 0 foundation (skeleton + plumbing).** The binary boots, serves
> the cockpit UI, and supports agent + task CRUD across the two-store
> architecture. The live `worktree → run agent → gate → evidence → merge` loop is
> scaffolded behind interfaces and lands in the next pass.

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
- **Tasks** screen — create a task (spec + verify commands) and watch the list.
  Persisted in the **per-project store** (`<repo>/.fabrika/fabrika.db`).
- Live updates over WebSocket (`/api/events`).
- The remaining cockpit surfaces (Define / Approve / Decide / Accept / Engine
  room) are present as placeholders; their API endpoints return `501` until the
  live loop is built.

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
| `internal/engine`   | task lifecycle + scheduler — stub (next pass)               |
| `internal/planner`  | BigTask → Tasks (Phase 0 passthrough)                       |
| `web`               | vanilla-TS UI, built with esbuild, embedded via `go:embed`  |
