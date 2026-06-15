CREATE TABLE task_transitions (
	id TEXT PRIMARY KEY,
	task_id TEXT NOT NULL,
	from_status TEXT NOT NULL DEFAULT '',
	to_status TEXT NOT NULL,
	actor TEXT NOT NULL DEFAULT 'engine',
	reason TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_task_transitions_task ON task_transitions(task_id);
