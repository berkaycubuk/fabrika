-- Record which planner agent is currently decomposing a big task so the board
-- UI can surface the agent name in the Planning column card (e.g. "planning by
-- Claude Code") instead of just a generic "planning…" pill.

ALTER TABLE bigtasks ADD COLUMN planner_agent_id TEXT NOT NULL DEFAULT '';
