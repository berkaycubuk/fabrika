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
