-- Each agent run against a task produces an Attempt with normalized Evidence
-- (gate stage results + branch diff). See SPECS.md §5.

CREATE TABLE IF NOT EXISTS attempts (
    id         TEXT PRIMARY KEY,
    task_id    TEXT NOT NULL,
    agent_id   TEXT NOT NULL DEFAULT '',
    result     TEXT NOT NULL DEFAULT '',  -- pass|fail|escalated
    evidence   TEXT NOT NULL DEFAULT '{}',-- JSON model.Evidence
    log        TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_attempts_task ON attempts(task_id, created_at DESC);
