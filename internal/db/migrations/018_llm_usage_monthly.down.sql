DROP INDEX IF EXISTS idx_llm_usage_household_month;
DROP TABLE IF EXISTS llm_usage_monthly;
-- SQLite can't drop a column without a full table rebuild before 3.35.
-- Leaving error_message in place on down is acceptable for a self-host
-- tool; the column is nullable and has no default.
