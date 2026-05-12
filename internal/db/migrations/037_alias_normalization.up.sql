ALTER TABLE products ADD COLUMN name_normalized TEXT;

ALTER TABLE product_aliases ADD COLUMN household_id TEXT REFERENCES households(id) ON DELETE CASCADE;
ALTER TABLE product_aliases ADD COLUMN alias_normalized TEXT;
ALTER TABLE product_aliases ADD COLUMN source TEXT NOT NULL DEFAULT 'legacy'
    CHECK (source IN ('legacy', 'receipt_match', 'manual_match', 'user_alias', 'import', 'enrichment'));
ALTER TABLE product_aliases ADD COLUMN confidence REAL
    CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1));
ALTER TABLE product_aliases ADD COLUMN accepted_at DATETIME;
ALTER TABLE product_aliases ADD COLUMN updated_at DATETIME;

UPDATE products
   SET name_normalized = lower(trim(name))
 WHERE name_normalized IS NULL;

UPDATE product_aliases
   SET household_id = (SELECT p.household_id FROM products p WHERE p.id = product_aliases.product_id),
       alias_normalized = lower(trim(alias)),
       updated_at = COALESCE(created_at, CURRENT_TIMESTAMP)
 WHERE alias_normalized IS NULL;

-- Keep exactly one legacy row per normalized household/store scope indexed.
-- Older rows that normalize to the same value stay readable through matcher
-- fallback, but are excluded from the new uniqueness constraint so migration
-- rollout does not fail on historical case/punctuation duplicates.
WITH ranked AS (
    SELECT id,
           ROW_NUMBER() OVER (
               PARTITION BY household_id, COALESCE(store_id, ''), alias_normalized
               ORDER BY created_at, id
           ) AS rn
      FROM product_aliases
     WHERE household_id IS NOT NULL
       AND alias_normalized IS NOT NULL
       AND TRIM(alias_normalized) != ''
)
UPDATE product_aliases
   SET alias_normalized = NULL
 WHERE id IN (SELECT id FROM ranked WHERE rn > 1);

DROP INDEX IF EXISTS idx_alias_global;
DROP INDEX IF EXISTS idx_alias_store;

CREATE INDEX idx_products_household_name_normalized
    ON products(household_id, name_normalized);

CREATE INDEX idx_product_aliases_household_alias_normalized
    ON product_aliases(household_id, alias_normalized);

CREATE UNIQUE INDEX idx_alias_global_household_norm
    ON product_aliases(household_id, alias_normalized)
    WHERE store_id IS NULL AND household_id IS NOT NULL AND alias_normalized IS NOT NULL;

CREATE UNIQUE INDEX idx_alias_store_household_norm
    ON product_aliases(household_id, store_id, alias_normalized)
    WHERE store_id IS NOT NULL AND household_id IS NOT NULL AND alias_normalized IS NOT NULL;
