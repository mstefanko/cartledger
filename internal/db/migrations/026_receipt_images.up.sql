CREATE TABLE receipt_images (
    id          TEXT PRIMARY KEY,
    receipt_id  TEXT NOT NULL REFERENCES receipts(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL CHECK(kind IN ('original','processed')),
    page_number INTEGER NOT NULL,
    storage_key TEXT NOT NULL,
    mime_type   TEXT NOT NULL,
    size_bytes  INTEGER NOT NULL DEFAULT 0,
    sha256      TEXT,
    created_at  DATETIME NOT NULL,
    deleted_at  DATETIME,
    UNIQUE(receipt_id, kind, page_number)
);

CREATE INDEX idx_receipt_images_receipt ON receipt_images(receipt_id);
CREATE INDEX idx_receipt_images_active ON receipt_images(receipt_id, kind, page_number) WHERE deleted_at IS NULL;
