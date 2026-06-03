-- Add a profile photo (data URI string) to agent definitions.
-- Existing rows default to '' (no photo).

ALTER TABLE agents ADD COLUMN photo TEXT NOT NULL DEFAULT '';
