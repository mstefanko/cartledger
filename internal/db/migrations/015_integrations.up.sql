CREATE TABLE integrations (
    id           TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    type         TEXT NOT NULL,
    enabled      INTEGER NOT NULL DEFAULT 1,
    config       TEXT NOT NULL,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(household_id, type)
);

CREATE INDEX idx_integrations_household ON integrations(household_id);
