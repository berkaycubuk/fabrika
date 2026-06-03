# Fabrika

Single Go binary orchestrating local coding agents. See `SPECS.md` for the full design.

## Commands
- `make build` — build UI + binary
- `make run` — build and run
- `make test` — `go test ./...`

## Stack
- Go 1.26, stdlib `net/http` (ServeMux routing), `coder/websocket`
- SQLite via `modernc.org/sqlite` (pure Go, no cgo)
- Git: shell out to system `git` CLI (`internal/git`)
- UI: vanilla TS + esbuild (`web/`), embedded via `go:embed` from `web/dist`

## Layout
- `cmd/fabrika` — entrypoint
- `internal/engine` — dispatch loop (route → worktree → run agent → gate → review/merge)
- `internal/{agent,gate,git,store,api,model,config}` — components
- Two stores: global `~/.fabrika/fabrika.db` (agents/settings), per-project `<repo>/.fabrika/fabrika.db` (tasks)
