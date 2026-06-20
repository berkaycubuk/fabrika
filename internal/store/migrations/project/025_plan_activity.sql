-- Per-big-task planner activity log: a bounded timeline of what the planner did
-- while decomposing a big task. Trimmed to the most-recent rows per big task by
-- PlanActivityRepo so a chatty agent can't bloat the store.
CREATE TABLE plan_activity (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    big_task_id TEXT NOT NULL,
    type        TEXT NOT NULL DEFAULT '',
    summary     TEXT NOT NULL DEFAULT '',
    ts          INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_plan_activity_bigtask ON plan_activity(big_task_id, id);
