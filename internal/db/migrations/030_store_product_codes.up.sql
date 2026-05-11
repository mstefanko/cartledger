CREATE TABLE store_product_codes (
    id               TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    household_id     TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    store_id         TEXT NOT NULL REFERENCES stores(id) ON DELETE CASCADE,
    product_id       TEXT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    store_item_code  TEXT NOT NULL CHECK (length(store_item_code) <= 64),
    label            TEXT,
    source           TEXT NOT NULL DEFAULT 'receipt'
        CHECK (source IN ('receipt', 'manual', 'import', 'backfill')),
    confidence       REAL CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
    first_seen_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (store_id, store_item_code)
);

CREATE INDEX idx_store_product_codes_product
    ON store_product_codes(product_id);
CREATE INDEX idx_store_product_codes_household_store
    ON store_product_codes(household_id, store_id);
