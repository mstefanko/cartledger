-- Expand matched CHECK constraint to include all matcher methods.
-- SQLite doesn't support ALTER CHECK, so recreate the table.

PRAGMA foreign_keys = OFF;

CREATE TABLE line_items_new (
    id                  TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    receipt_id          TEXT NOT NULL REFERENCES receipts(id) ON DELETE CASCADE,
    product_id          TEXT REFERENCES products(id),
    raw_name            TEXT NOT NULL,
    quantity            TEXT NOT NULL DEFAULT '1',
    unit                TEXT,
    unit_price          TEXT,
    total_price         TEXT NOT NULL,
    matched             TEXT DEFAULT 'unmatched' CHECK (matched IN ('unmatched', 'auto', 'manual', 'rule', 'alias', 'fuzzy')),
    confidence          REAL,
    line_number         INTEGER,
    created_at          DATETIME DEFAULT CURRENT_TIMESTAMP,
    regular_price       TEXT,
    discount_amount     TEXT,
    suggested_name      TEXT,
    suggested_category  TEXT,
    suggested_product_id TEXT REFERENCES products(id)
);

INSERT INTO line_items_new SELECT * FROM line_items;
DROP TABLE line_items;
ALTER TABLE line_items_new RENAME TO line_items;

CREATE INDEX IF NOT EXISTS idx_line_items_suggested ON line_items(suggested_product_id) WHERE suggested_product_id IS NOT NULL;

PRAGMA foreign_keys = ON;
