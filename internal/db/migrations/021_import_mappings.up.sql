-- Spreadsheet import (CSV/XLSX) scaffolding.
-- Adds three new tables (import_mappings, import_batches, not_duplicate_pairs)
-- plus tracking columns on receipts/line_items so imported rows can be
-- traced back to their source batch and distinguished from scanned receipts.

CREATE TABLE import_mappings (
    id                 TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    household_id       TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    name               TEXT NOT NULL,
    source_type        TEXT NOT NULL CHECK (source_type IN ('csv','xlsx')),
    source_fingerprint TEXT,
    config_json        TEXT NOT NULL,
    created_at         DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_used_at       DATETIME
);

CREATE INDEX idx_import_mappings_household ON import_mappings(household_id, last_used_at DESC);
CREATE INDEX idx_import_mappings_fingerprint ON import_mappings(household_id, source_fingerprint);

CREATE TABLE import_batches (
    id              TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    household_id    TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    source_type     TEXT NOT NULL,
    filename        TEXT,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    completed_at    DATETIME,
    receipts_count  INTEGER NOT NULL DEFAULT 0,
    items_count     INTEGER NOT NULL DEFAULT 0,
    unmatched_count INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_import_batches_household ON import_batches(household_id, created_at DESC);

-- Pair ordering is enforced at INSERT time: callers canonicalize so product_a_id < product_b_id.
CREATE TABLE not_duplicate_pairs (
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    product_a_id TEXT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    product_b_id TEXT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    dismissed_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (household_id, product_a_id, product_b_id),
    CHECK (product_a_id < product_b_id)
);

-- Distinguish scanned (LLM) receipts from manually-entered and imported-from-spreadsheet
-- receipts. SQLite 3.25+ accepts inline CHECK on ADD COLUMN; the DEFAULT 'scan'
-- backfills all existing rows atomically.
ALTER TABLE receipts ADD COLUMN source TEXT NOT NULL DEFAULT 'scan' CHECK (source IN ('scan','manual','import'));

-- Nullable FKs with ON DELETE SET NULL: deleting an import_batches row must
-- preserve the imported receipts/line_items (users may want to purge the batch
-- record without losing the data it brought in). SET NULL gives that behavior;
-- the default NO ACTION would block deletion once anything references the batch.
ALTER TABLE receipts ADD COLUMN import_batch_id TEXT REFERENCES import_batches(id) ON DELETE SET NULL;
ALTER TABLE line_items ADD COLUMN import_batch_id TEXT REFERENCES import_batches(id) ON DELETE SET NULL;

CREATE INDEX idx_receipts_import_batch ON receipts(import_batch_id) WHERE import_batch_id IS NOT NULL;
CREATE INDEX idx_line_items_import_batch ON line_items(import_batch_id) WHERE import_batch_id IS NOT NULL;
