CREATE TABLE product_enrichment_suggestions (
    id              TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    product_id      TEXT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    product_link_id TEXT REFERENCES product_links(id) ON DELETE CASCADE,
    source          TEXT NOT NULL,
    source_url      TEXT NOT NULL,
    field           TEXT NOT NULL,
    value           TEXT NOT NULL,
    evidence        TEXT,
    confidence      REAL,
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'rejected')),
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(product_id, product_link_id, field, value)
);

CREATE INDEX idx_product_enrichment_suggestions_product
    ON product_enrichment_suggestions(product_id, status, created_at);
