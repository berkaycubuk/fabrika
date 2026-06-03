-- Per-project store: this repo's big tasks, tasks, plans, attempts, decisions.
-- Lives at .fabrika/fabrika.db inside the target repo.

CREATE TABLE IF NOT EXISTS bigtasks (
    id          TEXT PRIMARY KEY,
    title       TEXT NOT NULL,
    intent      TEXT NOT NULL DEFAULT '',
    constraints TEXT NOT NULL DEFAULT '[]',
    repo_path   TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'draft',
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS tasks (
    id                 TEXT PRIMARY KEY,
    big_task_id        TEXT NOT NULL DEFAULT '',
    title              TEXT NOT NULL,
    spec               TEXT NOT NULL DEFAULT '',
    acceptance         TEXT NOT NULL DEFAULT '{}',
    depends_on         TEXT NOT NULL DEFAULT '[]',
    touch_paths        TEXT NOT NULL DEFAULT '[]',
    tags               TEXT NOT NULL DEFAULT '[]',
    risk_tier          TEXT NOT NULL DEFAULT 'low',
    status             TEXT NOT NULL DEFAULT 'ready',
    branch             TEXT NOT NULL DEFAULT '',
    agent_id           TEXT NOT NULL DEFAULT '',
    preferred_agent_id TEXT NOT NULL DEFAULT '',
    created_at         TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_bigtask ON tasks(big_task_id);
