-- Comments can carry image attachments: a JSON array of upload URLs
-- (e.g. ["/api/uploads/<uuid>.png"]) served from <repo>/.fabrika/uploads.
ALTER TABLE comments ADD COLUMN attachments TEXT NOT NULL DEFAULT '[]';
