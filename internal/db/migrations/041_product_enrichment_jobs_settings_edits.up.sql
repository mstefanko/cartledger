CREATE TABLE product_enrichment_jobs (
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

CREATE INDEX idx_product_enrichment_jobs_status
    ON product_enrichment_jobs(status, queued_at);

CREATE INDEX idx_product_enrichment_jobs_product
    ON product_enrichment_jobs(product_id, queued_at);

CREATE UNIQUE INDEX idx_product_enrichment_jobs_active
    ON product_enrichment_jobs(product_id, trigger, lookup_key)
    WHERE status IN ('queued', 'running');

CREATE TABLE product_enrichment_settings (
    household_id                   TEXT PRIMARY KEY REFERENCES households(id) ON DELETE CASCADE,
    manual_lookup_enabled          INTEGER NOT NULL DEFAULT 1,
    auto_on_scan_enabled           INTEGER NOT NULL DEFAULT 0,
    scheduled_sweep_enabled        INTEGER NOT NULL DEFAULT 0,
    provider_openfoodfacts_enabled INTEGER NOT NULL DEFAULT 1,
    provider_usda_fdc_enabled      INTEGER NOT NULL DEFAULT 0,
    provider_kroger_enabled        INTEGER NOT NULL DEFAULT 0,
    first_run_backfill_limit       INTEGER NOT NULL DEFAULT 200,
    created_at                     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at                     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE product_field_edits (
    product_id         TEXT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    field              TEXT NOT NULL,
    edited_by_user_id  TEXT REFERENCES users(id) ON DELETE SET NULL,
    edit_source        TEXT NOT NULL DEFAULT 'manual' CHECK (edit_source IN (
        'manual',
        'suggestion_accept',
        'merge',
        'import'
    )),
    edited_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(product_id, field)
);

CREATE INDEX idx_product_field_edits_product
    ON product_field_edits(product_id, edited_at);
