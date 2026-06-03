-- Phase 2: the planner turns a BigTask into a proposed Plan (a DAG of tasks)
-- plus OpenDecisions it couldn't resolve. Decisions also capture mid-run
-- escalations from implementer agents. See SPECS.md §5, §13.

CREATE TABLE IF NOT EXISTS plans (
    id          TEXT PRIMARY KEY,
    big_task_id TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'proposed', -- proposed|approved|rejected
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_plans_bigtask ON plans(big_task_id);

CREATE TABLE IF NOT EXISTS decisions (
    id         TEXT PRIMARY KEY,
    plan_id    TEXT NOT NULL DEFAULT '', -- set for plan-level decisions
    task_id    TEXT NOT NULL DEFAULT '', -- set for task-level escalations
    question   TEXT NOT NULL,
    options    TEXT NOT NULL DEFAULT '[]',
    context    TEXT NOT NULL DEFAULT '',
    answer     TEXT NOT NULL DEFAULT '',
    promote    INTEGER NOT NULL DEFAULT 0,
    status     TEXT NOT NULL DEFAULT 'open', -- open|answered
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_decisions_status ON decisions(status);
CREATE INDEX IF NOT EXISTS idx_decisions_plan ON decisions(plan_id);
CREATE INDEX IF NOT EXISTS idx_decisions_task ON decisions(task_id);
