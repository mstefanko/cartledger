-- Feature 1: Store Entity Enrichment
ALTER TABLE stores ADD COLUMN address TEXT;
ALTER TABLE stores ADD COLUMN city TEXT;
ALTER TABLE stores ADD COLUMN state TEXT;
ALTER TABLE stores ADD COLUMN zip TEXT;
ALTER TABLE stores ADD COLUMN store_number TEXT;
ALTER TABLE stores ADD COLUMN nickname TEXT;
ALTER TABLE stores ADD COLUMN latitude REAL;
ALTER TABLE stores ADD COLUMN longitude REAL;

-- Feature 2: Payment Method Tracking
ALTER TABLE receipts ADD COLUMN card_type TEXT;
ALTER TABLE receipts ADD COLUMN card_last4 TEXT;

-- Feature 3: Transaction Date/Time
ALTER TABLE receipts ADD COLUMN receipt_time TEXT;

-- Feature 4: Sale/Discount Price Handling
ALTER TABLE line_items ADD COLUMN regular_price TEXT;
ALTER TABLE line_items ADD COLUMN discount_amount TEXT;

ALTER TABLE product_prices ADD COLUMN regular_price TEXT;
ALTER TABLE product_prices ADD COLUMN discount_amount TEXT;
ALTER TABLE product_prices ADD COLUMN is_sale BOOLEAN DEFAULT FALSE;

-- Index for store_number lookups (Feature 1)
CREATE INDEX idx_stores_store_number ON stores(household_id, store_number) WHERE store_number IS NOT NULL;
