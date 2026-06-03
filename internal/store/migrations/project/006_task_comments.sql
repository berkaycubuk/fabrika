-- Comments on a task, authored by a human (author_type 'user') or an agent
-- (author_type 'agent', author_id naming the agent). Ordered by created_at.

CREATE TABLE comments (
	id TEXT PRIMARY KEY,
	task_id TEXT NOT NULL,
	author_type TEXT NOT NULL DEFAULT 'user',
	author_id TEXT NOT NULL DEFAULT '',
	body TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_comments_task ON comments(task_id);
