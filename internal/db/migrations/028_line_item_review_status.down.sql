DROP INDEX IF EXISTS idx_line_items_review_status;

ALTER TABLE line_items DROP COLUMN review_status;
