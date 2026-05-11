ALTER TABLE products ADD COLUMN upc TEXT;
ALTER TABLE line_items ADD COLUMN upc TEXT;

CREATE UNIQUE INDEX idx_products_household_upc
    ON products(household_id, upc)
    WHERE upc IS NOT NULL AND upc != '';

CREATE INDEX idx_line_items_upc
    ON line_items(receipt_id, upc)
    WHERE upc IS NOT NULL AND upc != '';
