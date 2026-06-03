-- Surface planner failures: a big task that fails to plan moves to status
-- 'error' with a human-readable reason here, instead of silently reverting to
-- 'draft'. The UI reads this to show what went wrong.

ALTER TABLE bigtasks ADD COLUMN error TEXT NOT NULL DEFAULT '';
