-- Admin gating for destructive operations (restore, promote, etc.).
-- The first user created on a fresh DB is promoted to admin in the
-- /setup path; subsequent users default to non-admin. Operators can
-- promote later via `cartledger promote-admin <email>`.
ALTER TABLE users ADD COLUMN is_admin INTEGER NOT NULL DEFAULT 0;
