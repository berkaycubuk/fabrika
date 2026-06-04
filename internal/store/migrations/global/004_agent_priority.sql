-- Add a user-set routing priority to agent definitions.
-- Higher integer = higher priority; existing rows default to 0 (normal).

ALTER TABLE agents ADD COLUMN priority INTEGER NOT NULL DEFAULT 0;
