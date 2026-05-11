DROP INDEX IF EXISTS idx_line_items_store_item_code;

ALTER TABLE line_items DROP COLUMN receipt_description;
ALTER TABLE line_items DROP COLUMN store_item_code;
