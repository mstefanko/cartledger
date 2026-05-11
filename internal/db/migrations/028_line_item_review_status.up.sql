ALTER TABLE line_items
  ADD COLUMN review_status TEXT NOT NULL DEFAULT 'pending'
  CHECK (review_status IN ('pending', 'accepted'));

UPDATE line_items
   SET review_status = 'accepted'
 WHERE receipt_id IN (
   SELECT id FROM receipts WHERE status = 'reviewed'
 );

CREATE INDEX idx_line_items_review_status
  ON line_items(receipt_id, review_status);
