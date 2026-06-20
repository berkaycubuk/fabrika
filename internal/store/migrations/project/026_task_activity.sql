-- Per-task implementation activity log: a bounded timeline of what the agent did
-- while working on a task. Trimmed to the most-recent rows per task by
-- TaskActivityRepo so a chatty agent can't bloat the per-project store.
CREATE TABLE task_activity (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id TEXT NOT NULL,
    type    TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    ts      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_task_activity_task ON task_activity(task_id, id);
