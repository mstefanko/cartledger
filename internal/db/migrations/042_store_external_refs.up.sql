CREATE TABLE store_external_refs (
    id            TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    household_id  TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    store_id      TEXT NOT NULL REFERENCES stores(id) ON DELETE CASCADE,
    source        TEXT NOT NULL,
    external_id   TEXT NOT NULL,
    label         TEXT,
    confidence    REAL CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
    metadata_json TEXT,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(store_id, source)
);

CREATE INDEX idx_store_external_refs_household_source
    ON store_external_refs(household_id, source, external_id);
