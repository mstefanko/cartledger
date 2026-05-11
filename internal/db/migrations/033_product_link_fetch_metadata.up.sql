ALTER TABLE product_links ADD COLUMN fetched_at DATETIME;
ALTER TABLE product_links ADD COLUMN http_status INTEGER;
ALTER TABLE product_links ADD COLUMN content_hash TEXT;
ALTER TABLE product_links ADD COLUMN last_error TEXT;
ALTER TABLE product_links ADD COLUMN source_confidence REAL;
