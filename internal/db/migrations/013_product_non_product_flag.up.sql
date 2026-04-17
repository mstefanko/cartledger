ALTER TABLE products ADD COLUMN is_non_product BOOLEAN NOT NULL DEFAULT 0;

UPDATE products SET is_non_product = 1
  WHERE LOWER(name) IN ('instant savings', 'instant savings coupon', 'coupon', 'discount', 'manufacturer coupon')
     OR LOWER(name) LIKE 'instant savings%';
