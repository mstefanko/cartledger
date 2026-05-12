DROP INDEX IF EXISTS idx_unit_conversions_household_unique;
DROP INDEX IF EXISTS idx_unit_conversions_group_unique;
DROP INDEX IF EXISTS idx_unit_conversions_product_unique;
DROP INDEX IF EXISTS idx_unit_conversions_household;
DROP INDEX IF EXISTS idx_unit_conversions_group;
DROP INDEX IF EXISTS idx_unit_conversions_product;

ALTER TABLE unit_conversions DROP COLUMN product_group_id;
ALTER TABLE unit_conversions DROP COLUMN household_id;
