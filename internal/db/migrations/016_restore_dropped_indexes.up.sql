-- Restore indexes silently dropped by migrations 005 and 006.
-- SQLite DROP TABLE cascades index loss; the table-rebuild migrations
-- (005_fix_fk_references, 006_fix_matched_check) did not recreate these.
-- Uses IF NOT EXISTS so deployed DBs that manually restored them won't break.
CREATE INDEX IF NOT EXISTS idx_line_items_receipt ON line_items(receipt_id);
CREATE INDEX IF NOT EXISTS idx_line_items_product ON line_items(product_id);
CREATE INDEX IF NOT EXISTS idx_product_prices_product ON product_prices(product_id, receipt_date);
CREATE INDEX IF NOT EXISTS idx_product_prices_store ON product_prices(store_id, receipt_date);
