-- Phase 3: autonomy + trust. Distinguish machine-merged work from human-accepted
-- work, sample auto-merged PRs for post-merge audit, and record change-failures.
-- See SPECS.md §9, §13, §14.

ALTER TABLE tasks ADD COLUMN auto_merged   INTEGER NOT NULL DEFAULT 0; -- merged without a human accept
ALTER TABLE tasks ADD COLUMN audit_flagged INTEGER NOT NULL DEFAULT 0; -- sampled for post-merge human audit
ALTER TABLE tasks ADD COLUMN reverted      INTEGER NOT NULL DEFAULT 0; -- merged then reverted/fixed (change-failure)
