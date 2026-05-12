CREATE TABLE product_identifiers (
    id                TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    household_id      TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    product_id        TEXT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    kind              TEXT NOT NULL CHECK (kind IN ('gtin', 'plu', 'external_id')),
    authority         TEXT NOT NULL DEFAULT '',
    value             TEXT NOT NULL,
    normalized_value  TEXT NOT NULL,
    source            TEXT NOT NULL DEFAULT 'manual'
        CHECK (source IN ('manual', 'line_item', 'legacy_upc', 'enrichment', 'import', 'user_accept')),
    confidence        REAL CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
    first_seen_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX idx_product_identifiers_household_kind_authority_value
    ON product_identifiers(household_id, kind, authority, normalized_value);

CREATE INDEX idx_product_identifiers_product
    ON product_identifiers(product_id);

CREATE INDEX idx_product_identifiers_lookup
    ON product_identifiers(household_id, kind, authority, normalized_value, product_id);

CREATE TABLE line_item_identifier_observations (
    id                TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    line_item_id      TEXT NOT NULL REFERENCES line_items(id) ON DELETE CASCADE,
    kind              TEXT NOT NULL CHECK (kind IN ('gtin', 'plu', 'external_id')),
    authority         TEXT NOT NULL DEFAULT '',
    raw_value         TEXT NOT NULL,
    normalized_value  TEXT NOT NULL,
    source            TEXT NOT NULL DEFAULT 'receipt'
        CHECK (source IN ('receipt', 'manual', 'import', 'repair')),
    confidence        REAL CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_line_item_identifier_observations_line
    ON line_item_identifier_observations(line_item_id);

CREATE INDEX idx_line_item_identifier_observations_lookup
    ON line_item_identifier_observations(kind, authority, normalized_value);

INSERT OR IGNORE INTO product_identifiers (
    household_id, product_id, kind, authority, value, normalized_value,
    source, confidence, first_seen_at, last_seen_at, created_at, updated_at
)
SELECT household_id, id, 'gtin', '', upc, upc,
       'legacy_upc', 0.8, created_at, updated_at, created_at, updated_at
FROM products
WHERE upc IS NOT NULL AND TRIM(upc) != '';

PRAGMA foreign_keys = OFF;

CREATE TABLE line_items_new (
    id                   TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    receipt_id           TEXT NOT NULL REFERENCES receipts(id) ON DELETE CASCADE,
    product_id           TEXT REFERENCES products(id),
    raw_name             TEXT NOT NULL,
    quantity             TEXT NOT NULL DEFAULT '1',
    unit                 TEXT,
    unit_price           TEXT,
    total_price          TEXT NOT NULL,
    matched              TEXT DEFAULT 'unmatched' CHECK (matched IN ('unmatched','auto','manual','rule','alias','fuzzy','code','identifier')),
    confidence           REAL,
    line_number          INTEGER,
    created_at           DATETIME DEFAULT CURRENT_TIMESTAMP,
    regular_price        TEXT,
    discount_amount      TEXT,
    suggested_name       TEXT,
    suggested_category   TEXT,
    suggested_product_id TEXT REFERENCES products(id),
    suggested_brand      TEXT,
    import_batch_id      TEXT REFERENCES import_batches(id) ON DELETE SET NULL,
    count_contribution   TEXT NOT NULL DEFAULT '1',
    pack_quantity_override TEXT,
    pack_unit_override   TEXT,
    pack_override_source TEXT,
    review_status        TEXT NOT NULL DEFAULT 'pending' CHECK (review_status IN ('pending', 'accepted')),
    store_item_code      TEXT,
    receipt_description  TEXT,
    upc                  TEXT
);

INSERT INTO line_items_new (
    id,
    receipt_id,
    product_id,
    raw_name,
    quantity,
    unit,
    unit_price,
    total_price,
    matched,
    confidence,
    line_number,
    created_at,
    regular_price,
    discount_amount,
    suggested_name,
    suggested_category,
    suggested_product_id,
    suggested_brand,
    import_batch_id,
    count_contribution,
    pack_quantity_override,
    pack_unit_override,
    pack_override_source,
    review_status,
    store_item_code,
    receipt_description,
    upc
)
SELECT
    id,
    receipt_id,
    product_id,
    raw_name,
    quantity,
    unit,
    unit_price,
    total_price,
    matched,
    confidence,
    line_number,
    created_at,
    regular_price,
    discount_amount,
    suggested_name,
    suggested_category,
    suggested_product_id,
    suggested_brand,
    import_batch_id,
    count_contribution,
    pack_quantity_override,
    pack_unit_override,
    pack_override_source,
    review_status,
    store_item_code,
    receipt_description,
    upc
FROM line_items;

DROP TABLE line_items;
ALTER TABLE line_items_new RENAME TO line_items;

CREATE INDEX idx_line_items_receipt ON line_items(receipt_id);
CREATE INDEX idx_line_items_product ON line_items(product_id);
CREATE INDEX idx_line_items_suggested ON line_items(suggested_product_id) WHERE suggested_product_id IS NOT NULL;
CREATE INDEX idx_line_items_unmatched
  ON line_items(receipt_id)
  WHERE product_id IS NULL OR matched = 'unmatched';
CREATE INDEX idx_line_items_import_batch ON line_items(import_batch_id) WHERE import_batch_id IS NOT NULL;
CREATE INDEX idx_line_items_review_status
  ON line_items(receipt_id, review_status);
CREATE INDEX idx_line_items_store_item_code
    ON line_items(receipt_id, store_item_code)
    WHERE store_item_code IS NOT NULL AND store_item_code != '';
CREATE INDEX idx_line_items_upc
    ON line_items(receipt_id, upc)
    WHERE upc IS NOT NULL AND upc != '';

PRAGMA foreign_keys = ON;
