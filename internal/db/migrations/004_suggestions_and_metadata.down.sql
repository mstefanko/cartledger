-- Drop suggestion lookup index
DROP INDEX IF EXISTS idx_line_items_suggested;

-- Drop new line_items columns
ALTER TABLE line_items DROP COLUMN suggested_name;
ALTER TABLE line_items DROP COLUMN suggested_category;
ALTER TABLE line_items DROP COLUMN suggested_product_id;

-- Drop new products columns
ALTER TABLE products DROP COLUMN brand;
ALTER TABLE products DROP COLUMN product_tags;

-- Rebuild original FTS without brand
DROP TRIGGER IF EXISTS products_fts_insert;
DROP TRIGGER IF EXISTS products_fts_update;
DROP TRIGGER IF EXISTS products_fts_delete;
DROP TABLE IF EXISTS products_fts;

CREATE VIRTUAL TABLE products_fts USING fts5(name, category, content=products, content_rowid=rowid);

CREATE TRIGGER products_fts_insert AFTER INSERT ON products BEGIN
    INSERT INTO products_fts(rowid, name, category) VALUES (NEW.rowid, NEW.name, NEW.category);
END;
CREATE TRIGGER products_fts_update AFTER UPDATE ON products BEGIN
    INSERT INTO products_fts(products_fts, rowid, name, category) VALUES ('delete', OLD.rowid, OLD.name, OLD.category);
    INSERT INTO products_fts(rowid, name, category) VALUES (NEW.rowid, NEW.name, NEW.category);
END;
CREATE TRIGGER products_fts_delete AFTER DELETE ON products BEGIN
    INSERT INTO products_fts(products_fts, rowid, name, category) VALUES ('delete', OLD.rowid, OLD.name, OLD.category);
END;
