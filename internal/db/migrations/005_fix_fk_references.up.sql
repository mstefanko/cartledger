-- Fix corrupted FK references: "receipts_old" → receipts
-- SQLite doesn't support ALTER FK, so recreate the tables.

PRAGMA foreign_keys = OFF;

-- Fix line_items
CREATE TABLE line_items_new (
    id                  TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    receipt_id          TEXT NOT NULL REFERENCES receipts(id) ON DELETE CASCADE,
    product_id          TEXT REFERENCES products(id),
    raw_name            TEXT NOT NULL,
    quantity            TEXT NOT NULL DEFAULT '1',
    unit                TEXT,
    unit_price          TEXT,
    total_price         TEXT NOT NULL,
    matched             TEXT DEFAULT 'unmatched' CHECK (matched IN ('unmatched', 'auto', 'manual', 'rule')),
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

-- Fix product_prices
CREATE TABLE product_prices_new (
    id               TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    product_id       TEXT NOT NULL REFERENCES products(id),
    store_id         TEXT NOT NULL REFERENCES stores(id),
    receipt_id       TEXT NOT NULL REFERENCES receipts(id),
    receipt_date     DATE NOT NULL,
    quantity         TEXT NOT NULL,
    unit             TEXT NOT NULL,
    unit_price       TEXT NOT NULL,
    normalized_price TEXT,
    normalized_unit  TEXT,
    created_at       DATETIME DEFAULT CURRENT_TIMESTAMP,
    regular_price    TEXT,
    discount_amount  TEXT,
    is_sale          BOOLEAN DEFAULT FALSE
);

INSERT INTO product_prices_new SELECT * FROM product_prices;
DROP TABLE product_prices;
ALTER TABLE product_prices_new RENAME TO product_prices;

-- Recreate indexes
CREATE INDEX IF NOT EXISTS idx_line_items_suggested ON line_items(suggested_product_id) WHERE suggested_product_id IS NOT NULL;

PRAGMA foreign_keys = ON;
