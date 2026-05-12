CREATE TABLE product_external_metadata (
    id                TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    household_id      TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    product_id        TEXT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    product_link_id   TEXT REFERENCES product_links(id) ON DELETE SET NULL,
    source            TEXT NOT NULL,
    source_record_id  TEXT,
    source_url        TEXT,
    lookup_key        TEXT,
    payload_json      TEXT NOT NULL,
    payload_version   INTEGER NOT NULL DEFAULT 1,
    content_hash      TEXT,
    fetched_at        DATETIME,
    expires_at        DATETIME,
    http_status       INTEGER,
    last_error        TEXT,
    confidence        REAL CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_product_external_metadata_product
    ON product_external_metadata(product_id, source, fetched_at);

CREATE UNIQUE INDEX idx_product_external_metadata_source_record
    ON product_external_metadata(product_id, source, source_record_id)
    WHERE source_record_id IS NOT NULL AND source_record_id != '';

ALTER TABLE product_enrichment_suggestions
    ADD COLUMN external_metadata_id TEXT
        REFERENCES product_external_metadata(id) ON DELETE SET NULL;

CREATE UNIQUE INDEX idx_product_enrichment_suggestions_snapshot_unique
    ON product_enrichment_suggestions(product_id, external_metadata_id, field, value)
    WHERE external_metadata_id IS NOT NULL;
