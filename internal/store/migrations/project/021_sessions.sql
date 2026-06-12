CREATE TABLE IF NOT EXISTS sessions (
  id          TEXT PRIMARY KEY,
  title       TEXT NOT NULL DEFAULT '',
  agent_id    TEXT NOT NULL,
  model       TEXT NOT NULL DEFAULT '',
  base_branch TEXT NOT NULL DEFAULT '',
  branch      TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT 'active',
  evidence    TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS session_messages (
  id         TEXT PRIMARY KEY,
  session_id TEXT NOT NULL,
  role       TEXT NOT NULL,
  body       TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_session_messages_session
  ON session_messages (session_id, created_at);
