DROP INDEX IF EXISTS idx_receipt_duplicate_candidates_receipt;
DROP INDEX IF EXISTS idx_receipt_duplicate_candidates_household_status;
DROP TABLE IF EXISTS receipt_duplicate_candidates;

DROP INDEX IF EXISTS idx_receipts_household_source_fingerprint;
ALTER TABLE receipts DROP COLUMN source_fingerprint;
