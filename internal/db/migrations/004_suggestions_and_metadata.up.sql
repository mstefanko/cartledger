-- Product metadata
ALTER TABLE products ADD COLUMN brand TEXT;
ALTER TABLE products ADD COLUMN product_tags TEXT;

-- Line item suggestions
ALTER TABLE line_items ADD COLUMN suggested_name TEXT;
ALTER TABLE line_items ADD COLUMN suggested_category TEXT;
ALTER TABLE line_items ADD COLUMN suggested_product_id TEXT REFERENCES products(id);

-- Rebuild FTS to include brand
DROP TRIGGER IF EXISTS products_fts_insert;
DROP TRIGGER IF EXISTS products_fts_update;
DROP TRIGGER IF EXISTS products_fts_delete;
DROP TABLE IF EXISTS products_fts;

CREATE VIRTUAL TABLE products_fts USING fts5(name, category, brand, content=products, content_rowid=rowid);

CREATE TRIGGER products_fts_insert AFTER INSERT ON products BEGIN
    INSERT INTO products_fts(rowid, name, category, brand) VALUES (NEW.rowid, NEW.name, NEW.category, NEW.brand);
END;
CREATE TRIGGER products_fts_update AFTER UPDATE ON products BEGIN
    INSERT INTO products_fts(products_fts, rowid, name, category, brand) VALUES ('delete', OLD.rowid, OLD.name, OLD.category, OLD.brand);
    INSERT INTO products_fts(rowid, name, category, brand) VALUES (NEW.rowid, NEW.name, NEW.category, NEW.brand);
END;
CREATE TRIGGER products_fts_delete AFTER DELETE ON products BEGIN
    INSERT INTO products_fts(products_fts, rowid, name, category, brand) VALUES ('delete', OLD.rowid, OLD.name, OLD.category, OLD.brand);
END;

-- Index for suggestion lookups
CREATE INDEX idx_line_items_suggested ON line_items(suggested_product_id) WHERE suggested_product_id IS NOT NULL;
