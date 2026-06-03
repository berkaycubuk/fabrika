-- Tasks and big tasks can carry image attachments at creation time: a JSON
-- array of upload URLs (e.g. ["/api/uploads/<uuid>.png"]) served from
-- <repo>/.fabrika/uploads, same scheme as comment attachments.
ALTER TABLE tasks ADD COLUMN attachments TEXT NOT NULL DEFAULT '[]';
ALTER TABLE bigtasks ADD COLUMN attachments TEXT NOT NULL DEFAULT '[]';
