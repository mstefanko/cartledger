-- Drop index BEFORE dropping the columns it references
DROP INDEX IF EXISTS idx_stores_store_number;

ALTER TABLE stores DROP COLUMN address;
ALTER TABLE stores DROP COLUMN city;
ALTER TABLE stores DROP COLUMN state;
ALTER TABLE stores DROP COLUMN zip;
ALTER TABLE stores DROP COLUMN store_number;
ALTER TABLE stores DROP COLUMN nickname;
ALTER TABLE stores DROP COLUMN latitude;
ALTER TABLE stores DROP COLUMN longitude;

ALTER TABLE receipts DROP COLUMN card_type;
ALTER TABLE receipts DROP COLUMN card_last4;
ALTER TABLE receipts DROP COLUMN receipt_time;

ALTER TABLE line_items DROP COLUMN regular_price;
ALTER TABLE line_items DROP COLUMN discount_amount;

ALTER TABLE product_prices DROP COLUMN regular_price;
ALTER TABLE product_prices DROP COLUMN discount_amount;
ALTER TABLE product_prices DROP COLUMN is_sale;
