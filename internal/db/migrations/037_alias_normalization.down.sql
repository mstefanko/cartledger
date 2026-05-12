DROP INDEX IF EXISTS idx_alias_store_household_norm;
DROP INDEX IF EXISTS idx_alias_global_household_norm;
DROP INDEX IF EXISTS idx_product_aliases_household_alias_normalized;
DROP INDEX IF EXISTS idx_products_household_name_normalized;

CREATE UNIQUE INDEX IF NOT EXISTS idx_alias_global ON product_aliases(alias) WHERE store_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_alias_store ON product_aliases(alias, store_id) WHERE store_id IS NOT NULL;

ALTER TABLE product_aliases DROP COLUMN updated_at;
ALTER TABLE product_aliases DROP COLUMN accepted_at;
ALTER TABLE product_aliases DROP COLUMN confidence;
ALTER TABLE product_aliases DROP COLUMN source;
ALTER TABLE product_aliases DROP COLUMN alias_normalized;
ALTER TABLE product_aliases DROP COLUMN household_id;

ALTER TABLE products DROP COLUMN name_normalized;
