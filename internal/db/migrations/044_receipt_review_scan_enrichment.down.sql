PRAGMA foreign_keys = OFF;

DROP TRIGGER IF EXISTS aliases_fts_insert;
DROP TRIGGER IF EXISTS aliases_fts_update;
DROP TRIGGER IF EXISTS aliases_fts_delete;

CREATE TABLE product_enrichment_jobs_old (
    id                   TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    household_id         TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    product_id           TEXT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    requested_by_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    trigger              TEXT NOT NULL CHECK (trigger IN (
        'receipt_scan',
        'manual_lookup',
        'manual_refresh',
        'scheduled_refresh',
        'batch_backfill'
    )),
    lookup_key           TEXT NOT NULL DEFAULT '',
    requested_sources    TEXT,
    status               TEXT NOT NULL DEFAULT 'queued' CHECK (status IN (
        'queued',
        'running',
        'succeeded',
        'partial',
        'failed',
        'cancelled'
    )),
    attempt_count        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at      DATETIME,
    last_error           TEXT,
    queued_at            DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at           DATETIME,
    finished_at          DATETIME,
    updated_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO product_enrichment_jobs_old (
    id, household_id, product_id, requested_by_user_id, trigger, lookup_key,
    requested_sources, status, attempt_count, next_attempt_at, last_error,
    queued_at, started_at, finished_at, updated_at
)
SELECT
    id, household_id, product_id, requested_by_user_id,
    CASE WHEN trigger = 'receipt_review_scan' THEN 'manual_lookup' ELSE trigger END,
    CASE WHEN trigger = 'receipt_review_scan' THEN 'receipt_review_scan:' || lookup_key ELSE lookup_key END,
    requested_sources, status, attempt_count, next_attempt_at,
    last_error, queued_at, started_at, finished_at, updated_at
FROM product_enrichment_jobs;

DROP INDEX IF EXISTS idx_product_enrichment_jobs_active;
DROP INDEX IF EXISTS idx_product_enrichment_jobs_product;
DROP INDEX IF EXISTS idx_product_enrichment_jobs_status;
DROP TABLE product_enrichment_jobs;
ALTER TABLE product_enrichment_jobs_old RENAME TO product_enrichment_jobs;

CREATE INDEX idx_product_enrichment_jobs_status
    ON product_enrichment_jobs(status, queued_at);

CREATE INDEX idx_product_enrichment_jobs_product
    ON product_enrichment_jobs(product_id, queued_at);

CREATE UNIQUE INDEX idx_product_enrichment_jobs_active
    ON product_enrichment_jobs(household_id, product_id, trigger, lookup_key)
    WHERE status IN ('queued', 'running');

CREATE TABLE product_identifiers_old (
    id                TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    household_id      TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    product_id        TEXT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    kind              TEXT NOT NULL CHECK (kind IN ('gtin', 'plu', 'external_id')),
    authority         TEXT NOT NULL DEFAULT '',
    value             TEXT NOT NULL,
    normalized_value  TEXT NOT NULL,
    source            TEXT NOT NULL DEFAULT 'manual'
        CHECK (source IN ('manual', 'line_item', 'legacy_upc', 'enrichment', 'import', 'user_accept')),
    confidence        REAL CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
    first_seen_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO product_identifiers_old (
    id, household_id, product_id, kind, authority, value, normalized_value,
    source, confidence, first_seen_at, last_seen_at, created_at, updated_at
)
SELECT
    id, household_id, product_id, kind, authority, value, normalized_value,
    CASE WHEN source = 'receipt_review_scan' THEN 'manual' ELSE source END,
    confidence, first_seen_at, last_seen_at, created_at, updated_at
FROM product_identifiers;

DROP INDEX IF EXISTS idx_product_identifiers_household_kind_authority_value;
DROP INDEX IF EXISTS idx_product_identifiers_product;
DROP INDEX IF EXISTS idx_product_identifiers_lookup;
DROP TABLE product_identifiers;
ALTER TABLE product_identifiers_old RENAME TO product_identifiers;

CREATE UNIQUE INDEX idx_product_identifiers_household_kind_authority_value
    ON product_identifiers(household_id, kind, authority, normalized_value);

CREATE INDEX idx_product_identifiers_product
    ON product_identifiers(product_id);

CREATE INDEX idx_product_identifiers_lookup
    ON product_identifiers(household_id, kind, authority, normalized_value, product_id);

CREATE TABLE line_item_identifier_observations_old (
    id                TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    line_item_id      TEXT NOT NULL REFERENCES line_items(id) ON DELETE CASCADE,
    kind              TEXT NOT NULL CHECK (kind IN ('gtin', 'plu', 'external_id')),
    authority         TEXT NOT NULL DEFAULT '',
    raw_value         TEXT NOT NULL,
    normalized_value  TEXT NOT NULL,
    source            TEXT NOT NULL DEFAULT 'receipt'
        CHECK (source IN ('receipt', 'manual', 'import', 'repair')),
    confidence        REAL CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO line_item_identifier_observations_old (
    id, line_item_id, kind, authority, raw_value, normalized_value,
    source, confidence, created_at
)
SELECT
    id, line_item_id, kind, authority, raw_value, normalized_value,
    CASE WHEN source = 'receipt_review_scan' THEN 'manual' ELSE source END,
    confidence, created_at
FROM line_item_identifier_observations;

DROP INDEX IF EXISTS idx_line_item_identifier_observations_line;
DROP INDEX IF EXISTS idx_line_item_identifier_observations_lookup;
DROP TABLE line_item_identifier_observations;
ALTER TABLE line_item_identifier_observations_old RENAME TO line_item_identifier_observations;

CREATE INDEX idx_line_item_identifier_observations_line
    ON line_item_identifier_observations(line_item_id);

CREATE INDEX idx_line_item_identifier_observations_lookup
    ON line_item_identifier_observations(kind, authority, normalized_value);

CREATE TABLE product_aliases_old (
    id               TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    product_id       TEXT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    alias            TEXT NOT NULL,
    store_id         TEXT REFERENCES stores(id),
    created_at       DATETIME DEFAULT CURRENT_TIMESTAMP,
    household_id     TEXT REFERENCES households(id) ON DELETE CASCADE,
    alias_normalized TEXT,
    source           TEXT NOT NULL DEFAULT 'legacy'
        CHECK (source IN ('legacy', 'receipt_match', 'manual_match', 'user_alias', 'import', 'enrichment')),
    confidence       REAL CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
    accepted_at      DATETIME,
    updated_at       DATETIME,
    UNIQUE(alias, store_id)
);

INSERT INTO product_aliases_old (
    id, product_id, alias, store_id, created_at, household_id, alias_normalized,
    source, confidence, accepted_at, updated_at
)
SELECT
    id, product_id, alias, store_id, created_at, household_id, alias_normalized,
    CASE WHEN source = 'receipt_review_scan' THEN 'manual_match' ELSE source END,
    confidence, accepted_at, updated_at
FROM product_aliases;

DROP INDEX IF EXISTS idx_product_aliases_product;
DROP INDEX IF EXISTS idx_product_aliases_alias;
DROP INDEX IF EXISTS idx_product_aliases_household_alias_normalized;
DROP INDEX IF EXISTS idx_alias_global_household_norm;
DROP INDEX IF EXISTS idx_alias_store_household_norm;
DROP TABLE product_aliases;
ALTER TABLE product_aliases_old RENAME TO product_aliases;

CREATE INDEX idx_product_aliases_alias ON product_aliases(alias);
CREATE INDEX idx_product_aliases_product ON product_aliases(product_id);
CREATE INDEX idx_product_aliases_household_alias_normalized
    ON product_aliases(household_id, alias_normalized);

CREATE UNIQUE INDEX idx_alias_global_household_norm
    ON product_aliases(household_id, alias_normalized)
    WHERE store_id IS NULL AND household_id IS NOT NULL AND alias_normalized IS NOT NULL;

CREATE UNIQUE INDEX idx_alias_store_household_norm
    ON product_aliases(household_id, store_id, alias_normalized)
    WHERE store_id IS NOT NULL AND household_id IS NOT NULL AND alias_normalized IS NOT NULL;

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

INSERT INTO product_aliases_fts(product_aliases_fts) VALUES('rebuild');

PRAGMA foreign_key_check;
PRAGMA foreign_keys = ON;
