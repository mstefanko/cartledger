DROP INDEX IF EXISTS idx_product_prices_line_item;

ALTER TABLE line_items DROP COLUMN pack_override_source;
ALTER TABLE line_items DROP COLUMN pack_unit_override;
ALTER TABLE line_items DROP COLUMN pack_quantity_override;

ALTER TABLE product_prices DROP COLUMN line_item_id;
