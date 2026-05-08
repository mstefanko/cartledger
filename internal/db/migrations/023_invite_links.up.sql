CREATE TABLE invite_links (
    id            TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    household_id  TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    inviter_id    TEXT NOT NULL REFERENCES users(id),
    email         TEXT,
    token_hash    TEXT NOT NULL UNIQUE,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at    DATETIME NOT NULL,
    consumed_at   DATETIME,
    revoked_at    DATETIME
);

CREATE INDEX invite_links_household_idx ON invite_links(household_id);
