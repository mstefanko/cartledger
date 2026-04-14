PRAGMA foreign_keys=OFF;

ALTER TABLE receipts RENAME TO receipts_old;

CREATE TABLE receipts (
    id            TEXT PRIMARY KEY,
    household_id  TEXT NOT NULL REFERENCES households(id),
    store_id      TEXT REFERENCES stores(id),
    scanned_by    TEXT REFERENCES users(id),
    receipt_date  DATE NOT NULL,
    subtotal      TEXT,
    tax           TEXT,
    total         TEXT,
    image_paths   TEXT,
    raw_llm_json  TEXT,
    status        TEXT DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'matched', 'reviewed', 'error')),
    llm_provider  TEXT,
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO receipts SELECT * FROM receipts_old;

DROP TABLE receipts_old;

-- Recreate indexes (old indexes were dropped with receipts_old)
CREATE INDEX IF NOT EXISTS idx_receipts_store ON receipts(store_id, receipt_date);
CREATE INDEX IF NOT EXISTS idx_receipts_date ON receipts(receipt_date);

PRAGMA foreign_keys=ON;
