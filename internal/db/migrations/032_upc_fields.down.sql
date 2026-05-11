DROP INDEX IF EXISTS idx_line_items_upc;
DROP INDEX IF EXISTS idx_products_household_upc;

ALTER TABLE line_items DROP COLUMN upc;
ALTER TABLE products DROP COLUMN upc;
