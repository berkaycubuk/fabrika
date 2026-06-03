-- Global store: agent definitions, conventions, and settings. Reusable across
-- repos; lives at ~/.fabrika/fabrika.db.

CREATE TABLE IF NOT EXISTS agents (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    command      TEXT NOT NULL,
    roles        TEXT NOT NULL DEFAULT '[]',
    tags         TEXT NOT NULL DEFAULT '[]',
    concurrency  INTEGER NOT NULL DEFAULT 1,
    timeout      TEXT NOT NULL DEFAULT '',
    max_attempts INTEGER NOT NULL DEFAULT 1,
    enabled      INTEGER NOT NULL DEFAULT 1,
    created_at   TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at   TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS conventions (
    id   TEXT PRIMARY KEY,
    rule TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
