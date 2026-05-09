ALTER TABLE receipts ADD COLUMN items_sold_count INTEGER;

ALTER TABLE line_items ADD COLUMN count_contribution TEXT NOT NULL DEFAULT '1';
