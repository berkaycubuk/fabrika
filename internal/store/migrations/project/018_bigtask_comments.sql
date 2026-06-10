ALTER TABLE comments ADD COLUMN big_task_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_comments_bigtask ON comments(big_task_id);
