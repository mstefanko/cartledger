ALTER TABLE receipts ADD COLUMN source_fingerprint TEXT;

CREATE INDEX idx_receipts_household_source_fingerprint
    ON receipts(household_id, source_fingerprint)
    WHERE source_fingerprint IS NOT NULL AND source_fingerprint != '';

CREATE TABLE receipt_duplicate_candidates (
    id             TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    household_id   TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    receipt_id     TEXT NOT NULL REFERENCES receipts(id) ON DELETE CASCADE,
    candidate_id   TEXT NOT NULL REFERENCES receipts(id) ON DELETE CASCADE,
    kind           TEXT NOT NULL CHECK (kind IN ('exact_image')),
    confidence     REAL CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
    status         TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'dismissed', 'confirmed')),
    evidence_json  TEXT,
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(receipt_id, candidate_id, kind)
);

CREATE INDEX idx_receipt_duplicate_candidates_household_status
    ON receipt_duplicate_candidates(household_id, status, created_at);

CREATE INDEX idx_receipt_duplicate_candidates_receipt
    ON receipt_duplicate_candidates(receipt_id, status);
