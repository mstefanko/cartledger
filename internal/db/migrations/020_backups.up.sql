-- Tracks both in-progress and completed backup archive runs. Populated by
-- internal/backup.Runner and surfaced via /api/v1/backups for the admin UI.
-- missing_images counts image files referenced by DB rows that weren't on
-- disk at backup time (expected non-zero once image retention prunes originals).
CREATE TABLE backups (
    id              TEXT PRIMARY KEY,
    status          TEXT NOT NULL CHECK(status IN ('running','complete','failed')),
    filename        TEXT NOT NULL,
    size_bytes      INTEGER,
    schema_version  INTEGER NOT NULL,
    missing_images  INTEGER NOT NULL DEFAULT 0,
    error           TEXT,
    created_at      DATETIME NOT NULL,
    completed_at    DATETIME
);

CREATE INDEX idx_backups_created_at ON backups(created_at DESC);
