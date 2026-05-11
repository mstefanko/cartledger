ALTER TABLE line_items ADD COLUMN store_item_code TEXT;
ALTER TABLE line_items ADD COLUMN receipt_description TEXT;

CREATE INDEX idx_line_items_store_item_code
    ON line_items(receipt_id, store_item_code)
    WHERE store_item_code IS NOT NULL AND store_item_code != '';
