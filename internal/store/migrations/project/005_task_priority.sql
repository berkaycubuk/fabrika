-- Let the human set a task priority (low|medium|high) at creation time, used as
-- an ordering hint in the UI. Defaults to medium for existing and new tasks.

ALTER TABLE tasks ADD COLUMN priority TEXT NOT NULL DEFAULT 'medium';
