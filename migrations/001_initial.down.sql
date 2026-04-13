-- Drop triggers first
DROP TRIGGER IF EXISTS aliases_fts_delete;
DROP TRIGGER IF EXISTS aliases_fts_update;
DROP TRIGGER IF EXISTS aliases_fts_insert;
DROP TRIGGER IF EXISTS products_fts_delete;
DROP TRIGGER IF EXISTS products_fts_update;
DROP TRIGGER IF EXISTS products_fts_insert;

-- Drop FTS virtual tables
DROP TABLE IF EXISTS product_aliases_fts;
DROP TABLE IF EXISTS products_fts;

-- Drop tables in reverse dependency order
DROP TABLE IF EXISTS product_links;
DROP TABLE IF EXISTS product_images;
DROP TABLE IF EXISTS unit_conversions;
DROP TABLE IF EXISTS shopping_list_items;
DROP TABLE IF EXISTS shopping_lists;
DROP TABLE IF EXISTS product_prices;
DROP TABLE IF EXISTS line_items;
DROP TABLE IF EXISTS receipts;
DROP TABLE IF EXISTS matching_rules;
DROP TABLE IF EXISTS product_aliases;
DROP TABLE IF EXISTS products;
DROP TABLE IF EXISTS stores;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS households;
