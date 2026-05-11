CREATE TABLE product_nutrition (
    id                       TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    product_id               TEXT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    product_link_id           TEXT REFERENCES product_links(id) ON DELETE SET NULL,
    serving_quantity          REAL,
    serving_unit              TEXT,
    serving_label             TEXT,
    servings_per_container    REAL,
    calories                  REAL,
    total_fat_g               REAL,
    saturated_fat_g           REAL,
    trans_fat_g               REAL,
    cholesterol_mg            REAL,
    sodium_mg                 REAL,
    total_carbohydrate_g      REAL,
    dietary_fiber_g           REAL,
    total_sugars_g            REAL,
    added_sugars_g            REAL,
    protein_g                 REAL,
    ingredients               TEXT,
    allergens_json            TEXT,
    source_confidence         REAL,
    accepted_by_user          INTEGER NOT NULL DEFAULT 0,
    created_at                DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at                DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(product_id, product_link_id)
);

CREATE INDEX idx_product_nutrition_product
    ON product_nutrition(product_id);
