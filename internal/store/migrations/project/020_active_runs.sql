CREATE TABLE IF NOT EXISTS active_runs (
  task_id    TEXT PRIMARY KEY,
  pgid       INTEGER NOT NULL,
  agent_id   TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL DEFAULT (datetime('now'))
);
