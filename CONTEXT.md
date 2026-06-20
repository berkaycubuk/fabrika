# Fabrika

Single Go binary that orchestrates local coding agents from a browser UI. Defines big tasks, agents do the work; the tool handles build/test/verify and surfaces only what needs human judgment.

## Stack

- Go 1.26 backend, `net/http` ServeMux, `coder/websocket`
- SQLite via `modernc.org/sqlite` (pure Go, no cgo)
- Vanilla TypeScript UI (esbuild), embedded via `go:embed` into the binary
- Git: shell out to system `git` CLI (`internal/git`)

## Package map

| Package | Role |
|---|---|
| `cmd/fabrika` | CLI entrypoint: flags, `init`, `version`, serve loop |
| `internal/engine` | Dispatch loop: route → worktree → run agent → gate → review/merge |
| `internal/agent` | Registry, subprocess adapter, routing, prompt rendering |
| `internal/planner` | BigTask → task DAG + contracts + decisions |
| `internal/gate` | Runs manifest verbs (setup/typecheck/lint/build/test/verify/e2e) |
| `internal/mutate` | Mutation testing (scoped to changed lines) |
| `internal/git` | Worktree, branch, diff, merge (shells to git CLI) |
| `internal/store` | SQLite persistence (global + per-project DBs, migrations) |
| `internal/config` | `fabrika.toml` parse/validate/scaffold/stack-detect |
| `internal/api` | REST + WebSocket handlers, uploads |
| `internal/model` | Shared domain types (BigTask, Task, Agent, Plan, etc.) |
| `internal/release` | Ship/bake/rollback |
| `internal/ci` | External CI poller |
| `web/` | Vanilla TS UI, embedded via `web/embed.go` |

## Core data flow

```
BigTask (human intent) → Planner → Plan (tasks + contracts + decisions)
  → approve → Tasks dispatched to agents → worktree → agent subprocess
  → gate (setup→typecheck→lint→build→test→verify→e2e) → review/auto-merge
```

Human gates are: Approve plan, Answer decisions, Accept/reject work, Audit samples.

## Two stores

| Store | Path | Contents |
|---|---|---|
| Global | `~/.fabrika/fabrika.db` | Agents, conventions, settings |
| Per-project | `<repo>/.fabrika/fabrika.db` | Big tasks, tasks, plans, decisions, attempts, comments |

## Key design decisions

- **Agents are subprocesses**, not APIs. Any CLI coding tool works. The adapter stays thin.
- **Acceptance contracts come from the planner (or human), never the implementer.** Held-out checks are written into the worktree *after* the branch is committed so the implementer never sees them.
- **Risk-tiered auto-merge**: low-risk passes (auto-merge), medium/high waits for human accept. Effective tier = max(declared tier, tier of actually-changed paths).
- **Mutants scoped to changed lines only** (budget ~8), in non-test/non-locked files.
- **Conventions** are standing rules injected into every agent prompt (global, not per-task).
- **Project knowledge** (`[knowledge]` in `fabrika.toml`) is injected as architectural context into planner + implementer prompts — separate from short conventions.
- **Merge commit SHA** is captured at merge time for CI correlation and release mapping.
- **Sessions** are interactive chat with an agent in its own worktree, exiting through Finish (commit → gate → merge).

## Code conventions

- Model types in `internal/model/model.go` — no methods, plain structs with JSON tags
- Settings are key/value strings in the global store, read by the engine at dispatch time
- Store package uses `Repo` interfaces per domain (e.g. `ConventionRepo`, `TaskRepo`)
- REST handlers in `internal/api/` — one file per domain, wired in `server.go`
- The full spec is `SPECS.md`; `SPECS-PHASE4.md` covers the remaining incident/feedback work
- The engine emits typed events (`task.updated`, `convention.created`, etc.) consumed by the WebSocket hub
