-- Reverse of 021_import_mappings.up.sql.
-- Drops indexes/tables. Per repo convention (see 019_user_is_admin.down.sql),
-- we leave the added columns on receipts and line_items in place: SQLite
-- before 3.35 cannot DROP COLUMN, and a full table rebuild risks data loss
-- on a self-host tool. The columns are nullable (or have safe defaults) and
-- break nothing when unused.

DROP INDEX IF EXISTS idx_line_items_import_batch;
DROP INDEX IF EXISTS idx_receipts_import_batch;

DROP TABLE IF EXISTS not_duplicate_pairs;

DROP INDEX IF EXISTS idx_import_batches_household;
DROP TABLE IF EXISTS import_batches;

DROP INDEX IF EXISTS idx_import_mappings_fingerprint;
DROP INDEX IF EXISTS idx_import_mappings_household;
DROP TABLE IF EXISTS import_mappings;
