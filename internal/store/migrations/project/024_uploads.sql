-- Per-upload metadata for generalized file attachments. Blobs live on disk
-- under .fabrika/uploads; this row lets them survive restart and be served
-- back with their original filename and content type.
CREATE TABLE uploads (
    name         TEXT PRIMARY KEY,                  -- generated served name, e.g. <uuid>.<ext>
    filename     TEXT NOT NULL DEFAULT '',          -- original client filename, for download
    content_type TEXT NOT NULL DEFAULT '',          -- MIME used when serving
    size         INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT NOT NULL DEFAULT (datetime('now'))
);
