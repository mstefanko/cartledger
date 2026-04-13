-- Household (the shared space — "The Stefankos")
-- Single household per deployment for v1. Multi-household is a future additive change.
CREATE TABLE households (
    id          TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    name        TEXT NOT NULL,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Users
CREATE TABLE users (
    id            TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    household_id  TEXT NOT NULL REFERENCES households(id),
    email         TEXT NOT NULL UNIQUE,
    name          TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Stores (sidebar navigation — like Actual accounts)
CREATE TABLE stores (
    id            TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    household_id  TEXT NOT NULL REFERENCES households(id),
    name          TEXT NOT NULL,
    display_order INTEGER DEFAULT 0,
    icon          TEXT,
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(household_id, name)
);

-- Canonical Products (the normalized product database)
CREATE TABLE products (
    id                TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    household_id      TEXT NOT NULL REFERENCES households(id),
    name              TEXT NOT NULL,
    category          TEXT,
    default_unit      TEXT,
    notes             TEXT,
    last_purchased_at DATE,
    purchase_count    INTEGER DEFAULT 0,
    created_at        DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(household_id, name)
);

-- Product Aliases (maps receipt abbreviations to canonical products)
CREATE TABLE product_aliases (
    id          TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    product_id  TEXT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    alias       TEXT NOT NULL,
    store_id    TEXT REFERENCES stores(id),
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(alias, store_id)
);

-- Partial unique indexes for aliases (handles NULL store_id correctly)
CREATE UNIQUE INDEX idx_alias_global ON product_aliases(alias) WHERE store_id IS NULL;
CREATE UNIQUE INDEX idx_alias_store ON product_aliases(alias, store_id) WHERE store_id IS NOT NULL;

-- Matching Rules (like Actual Budget's rules engine)
CREATE TABLE matching_rules (
    id            TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    household_id  TEXT NOT NULL REFERENCES households(id),
    priority      INTEGER DEFAULT 0,
    condition_op  TEXT NOT NULL,
    condition_val TEXT NOT NULL,
    store_id      TEXT REFERENCES stores(id),
    product_id    TEXT NOT NULL REFERENCES products(id),
    category      TEXT,
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Receipts
CREATE TABLE receipts (
    id            TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    household_id  TEXT NOT NULL REFERENCES households(id),
    store_id      TEXT REFERENCES stores(id),
    scanned_by    TEXT REFERENCES users(id),
    receipt_date  DATE NOT NULL,
    subtotal      TEXT,
    tax           TEXT,
    total         TEXT,
    image_paths   TEXT,
    raw_llm_json  TEXT,
    status        TEXT DEFAULT 'pending' CHECK (status IN ('pending', 'matched', 'reviewed')),
    llm_provider  TEXT,
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Line Items (individual items on a receipt)
CREATE TABLE line_items (
    id            TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    receipt_id    TEXT NOT NULL REFERENCES receipts(id) ON DELETE CASCADE,
    product_id    TEXT REFERENCES products(id),
    raw_name      TEXT NOT NULL,
    quantity      TEXT NOT NULL DEFAULT '1',
    unit          TEXT,
    unit_price    TEXT,
    total_price   TEXT NOT NULL,
    matched       TEXT DEFAULT 'unmatched' CHECK (matched IN ('unmatched', 'auto', 'manual', 'rule')),
    confidence    REAL,
    line_number   INTEGER,
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Product Prices (denormalized for fast analytics)
CREATE TABLE product_prices (
    id               TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    product_id       TEXT NOT NULL REFERENCES products(id),
    store_id         TEXT NOT NULL REFERENCES stores(id),
    receipt_id       TEXT NOT NULL REFERENCES receipts(id),
    receipt_date     DATE NOT NULL,
    quantity         TEXT NOT NULL,
    unit             TEXT NOT NULL,
    unit_price       TEXT NOT NULL,
    normalized_price TEXT,
    normalized_unit  TEXT,
    created_at       DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Shopping Lists
CREATE TABLE shopping_lists (
    id            TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    household_id  TEXT NOT NULL REFERENCES households(id),
    name          TEXT NOT NULL,
    created_by    TEXT REFERENCES users(id),
    status        TEXT DEFAULT 'active' CHECK (status IN ('active', 'completed', 'archived')),
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Shopping List Items
CREATE TABLE shopping_list_items (
    id            TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    list_id       TEXT NOT NULL REFERENCES shopping_lists(id) ON DELETE CASCADE,
    product_id    TEXT REFERENCES products(id),
    name          TEXT NOT NULL,
    quantity      TEXT NOT NULL DEFAULT '1',
    unit          TEXT,
    checked       BOOLEAN DEFAULT FALSE,
    checked_by    TEXT REFERENCES users(id),
    sort_order    INTEGER DEFAULT 0,
    notes         TEXT,
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Unit Conversions (food-specific density conversions)
CREATE TABLE unit_conversions (
    id            TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    product_id    TEXT REFERENCES products(id),
    from_unit     TEXT NOT NULL,
    to_unit       TEXT NOT NULL,
    factor        TEXT NOT NULL,
    UNIQUE(product_id, from_unit, to_unit)
);

-- Product Images (user-uploaded photos)
CREATE TABLE product_images (
    id            TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    product_id    TEXT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    image_path    TEXT NOT NULL,
    type          TEXT DEFAULT 'photo' CHECK (type IN ('photo', 'nutrition', 'packaging')),
    caption       TEXT,
    is_primary    BOOLEAN DEFAULT FALSE,
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Product Links (back-references to Mealie, URLs, external sources)
CREATE TABLE product_links (
    id            TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    product_id    TEXT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    source        TEXT NOT NULL,
    external_id   TEXT,
    url           TEXT NOT NULL,
    label         TEXT,
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- FTS5 virtual tables for search
CREATE VIRTUAL TABLE products_fts USING fts5(name, category, content=products, content_rowid=rowid);
CREATE VIRTUAL TABLE product_aliases_fts USING fts5(alias, content=product_aliases, content_rowid=rowid);

-- FTS5 sync triggers (REQUIRED — without these, search index goes stale)
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

CREATE TRIGGER aliases_fts_insert AFTER INSERT ON product_aliases BEGIN
    INSERT INTO product_aliases_fts(rowid, alias) VALUES (NEW.rowid, NEW.alias);
END;
CREATE TRIGGER aliases_fts_update AFTER UPDATE ON product_aliases BEGIN
    INSERT INTO product_aliases_fts(product_aliases_fts, rowid, alias) VALUES ('delete', OLD.rowid, OLD.alias);
    INSERT INTO product_aliases_fts(rowid, alias) VALUES (NEW.rowid, NEW.alias);
END;
CREATE TRIGGER aliases_fts_delete AFTER DELETE ON product_aliases BEGIN
    INSERT INTO product_aliases_fts(product_aliases_fts, rowid, alias) VALUES ('delete', OLD.rowid, OLD.alias);
END;

-- Indexes
CREATE INDEX idx_line_items_receipt ON line_items(receipt_id);
CREATE INDEX idx_line_items_product ON line_items(product_id);
CREATE INDEX idx_product_prices_product ON product_prices(product_id, receipt_date);
CREATE INDEX idx_product_prices_store ON product_prices(store_id, receipt_date);
CREATE INDEX idx_product_aliases_alias ON product_aliases(alias);
CREATE INDEX idx_receipts_store ON receipts(store_id, receipt_date);
CREATE INDEX idx_receipts_date ON receipts(receipt_date);
CREATE INDEX idx_matching_rules_priority ON matching_rules(household_id, priority DESC);
CREATE INDEX idx_product_images_product ON product_images(product_id);
CREATE INDEX idx_product_links_product ON product_links(product_id);
CREATE INDEX idx_product_links_source ON product_links(source, external_id);
