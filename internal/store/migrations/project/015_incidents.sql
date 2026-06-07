CREATE TABLE incidents (
	id TEXT PRIMARY KEY,
	fingerprint TEXT NOT NULL UNIQUE,
	title TEXT NOT NULL,
	stack TEXT NOT NULL DEFAULT '',
	payload TEXT NOT NULL DEFAULT '',
	count INTEGER NOT NULL DEFAULT 1,
	first_seen TEXT NOT NULL,
	last_seen TEXT NOT NULL,
	status TEXT NOT NULL,
	task_id TEXT NOT NULL DEFAULT '',
	suspect_release_id TEXT NOT NULL DEFAULT '',
	suspect_task_id TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_incidents_status ON incidents(status);
