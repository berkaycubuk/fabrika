-- Persist the token usage the planner agent self-reports for each big task.
-- Integer columns (not JSON) so per-agent totals can be SUM-aggregated in SQL.

ALTER TABLE bigtasks ADD COLUMN input_tokens  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE bigtasks ADD COLUMN output_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE bigtasks ADD COLUMN total_tokens  INTEGER NOT NULL DEFAULT 0;
