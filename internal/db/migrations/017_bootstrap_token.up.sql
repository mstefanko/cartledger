-- First-run bootstrap token. Single-row table — the CHECK enforces that only
-- row id=1 can ever exist. On first boot, the server inserts a random token
-- (base64url, 32 bytes of entropy) so the operator has a one-time URL to
-- create the first admin. When /api/v1/setup succeeds, the row is marked
-- consumed so the token cannot be replayed.
--
-- The token persists across restarts: if the server reboots before setup
-- completes, the operator's pasted URL remains valid.
CREATE TABLE bootstrap_token (
    id          INTEGER PRIMARY KEY CHECK (id = 1),
    token       TEXT    NOT NULL,
    consumed_at DATETIME,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
