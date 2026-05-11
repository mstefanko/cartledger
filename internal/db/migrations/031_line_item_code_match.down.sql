PRAGMA foreign_keys = OFF;

UPDATE line_items SET matched = 'alias' WHERE matched = 'code';

CREATE TABLE line_items_new (
    id                   TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    receipt_id           TEXT NOT NULL REFERENCES receipts(id) ON DELETE CASCADE,
    product_id           TEXT REFERENCES products(id),
    raw_name             TEXT NOT NULL,
    quantity             TEXT NOT NULL DEFAULT '1',
    unit                 TEXT,
    unit_price           TEXT,
    total_price          TEXT NOT NULL,
    matched              TEXT DEFAULT 'unmatched' CHECK (matched IN ('unmatched','auto','manual','rule','alias','fuzzy')),
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
    receipt_description  TEXT
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
    receipt_description
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
    receipt_description
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

PRAGMA foreign_keys = ON;
