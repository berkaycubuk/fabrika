-- Relay mode: per-project daemon identities, paired phone devices, and their
-- Web Push subscriptions. Global because identities are machine-level secrets
-- that must never live inside a repo's .fabrika/ (risk of being committed).

CREATE TABLE IF NOT EXISTS relay_identities (
    project_root TEXT PRIMARY KEY,
    private_key  BLOB NOT NULL,                          -- 32-byte X25519 scalar
    created_at   TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS relay_devices (
    id         TEXT PRIMARY KEY,
    daemon_id  TEXT NOT NULL,                            -- hex pubkey fingerprint (per project)
    pubkey     BLOB NOT NULL,                            -- phone static X25519 public key
    name       TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    last_seen  TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE (daemon_id, pubkey)
);

CREATE TABLE IF NOT EXISTS relay_push_subs (
    endpoint   TEXT PRIMARY KEY,                         -- Web Push endpoint URL
    daemon_id  TEXT NOT NULL,
    device_id  TEXT NOT NULL,
    p256dh     TEXT NOT NULL,
    auth       TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
