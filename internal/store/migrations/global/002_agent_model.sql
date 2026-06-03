-- Add an explicit program-specific model identifier to agent definitions.
-- Existing rows default to '' (no explicit model).

ALTER TABLE agents ADD COLUMN model TEXT NOT NULL DEFAULT '';
