ALTER TABLE unit_conversions
    ADD COLUMN household_id TEXT REFERENCES households(id) ON DELETE CASCADE;

ALTER TABLE unit_conversions
    ADD COLUMN product_group_id TEXT REFERENCES product_groups(id) ON DELETE CASCADE;

CREATE INDEX idx_unit_conversions_product
    ON unit_conversions(product_id, from_unit, to_unit)
    WHERE product_id IS NOT NULL;

CREATE INDEX idx_unit_conversions_group
    ON unit_conversions(product_group_id, from_unit, to_unit)
    WHERE product_group_id IS NOT NULL;

CREATE INDEX idx_unit_conversions_household
    ON unit_conversions(household_id, from_unit, to_unit)
    WHERE household_id IS NOT NULL AND product_id IS NULL AND product_group_id IS NULL;

CREATE UNIQUE INDEX idx_unit_conversions_product_unique
    ON unit_conversions(product_id, from_unit, to_unit)
    WHERE product_id IS NOT NULL;

CREATE UNIQUE INDEX idx_unit_conversions_group_unique
    ON unit_conversions(product_group_id, from_unit, to_unit)
    WHERE product_group_id IS NOT NULL;

CREATE UNIQUE INDEX idx_unit_conversions_household_unique
    ON unit_conversions(household_id, from_unit, to_unit)
    WHERE household_id IS NOT NULL AND product_id IS NULL AND product_group_id IS NULL;
