ALTER TABLE product_prices ADD COLUMN line_item_id TEXT REFERENCES line_items(id) ON DELETE CASCADE;

CREATE UNIQUE INDEX idx_product_prices_line_item
    ON product_prices(line_item_id)
    WHERE line_item_id IS NOT NULL;

ALTER TABLE line_items ADD COLUMN pack_quantity_override TEXT;
ALTER TABLE line_items ADD COLUMN pack_unit_override TEXT;
ALTER TABLE line_items ADD COLUMN pack_override_source TEXT;
