CREATE TABLE product_groups (
    id TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    comparison_unit TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(household_id, name)
);

ALTER TABLE products ADD COLUMN product_group_id TEXT REFERENCES product_groups(id) ON DELETE SET NULL;

CREATE INDEX idx_products_group ON products(product_group_id) WHERE product_group_id IS NOT NULL;
