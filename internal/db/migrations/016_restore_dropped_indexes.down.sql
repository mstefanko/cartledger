-- Down: drop the restored indexes (reverses 016_restore_dropped_indexes.up.sql).
DROP INDEX IF EXISTS idx_line_items_receipt;
DROP INDEX IF EXISTS idx_line_items_product;
DROP INDEX IF EXISTS idx_product_prices_product;
DROP INDEX IF EXISTS idx_product_prices_store;
