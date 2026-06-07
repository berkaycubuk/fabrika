CREATE TABLE releases (
	id TEXT PRIMARY KEY,
	sha TEXT NOT NULL,
	prev_sha TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL,
	deploy_log TEXT NOT NULL DEFAULT '',
	health_log TEXT NOT NULL DEFAULT '',
	error TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT (datetime('now')),
	deployed_at TEXT NOT NULL DEFAULT '',
	live_at TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_releases_status ON releases(status);
CREATE INDEX idx_releases_created_at ON releases(created_at DESC);
