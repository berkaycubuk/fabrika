-- Persist the token usage an agent self-reports for each attempt. Integer
-- columns (not JSON) so per-agent totals can be SUM-aggregated in SQL.

ALTER TABLE attempts ADD COLUMN input_tokens  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE attempts ADD COLUMN output_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE attempts ADD COLUMN total_tokens  INTEGER NOT NULL DEFAULT 0;
