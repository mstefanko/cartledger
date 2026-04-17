CREATE INDEX idx_line_items_unmatched
  ON line_items(receipt_id)
  WHERE product_id IS NULL OR matched = 'unmatched';
