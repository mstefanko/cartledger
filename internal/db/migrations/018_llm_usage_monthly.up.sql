-- Per-household monthly LLM token usage tracking.
-- Used by the budget cap + admin usage endpoint (see internal/llm/usage.go).
-- year_month is "YYYY-MM" (UTC); one row per household per calendar month.
CREATE TABLE llm_usage_monthly (
    household_id  TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    year_month    TEXT NOT NULL,
    input_tokens  INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    call_count    INTEGER NOT NULL DEFAULT 0,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (household_id, year_month)
);

CREATE INDEX idx_llm_usage_household_month ON llm_usage_monthly(household_id, year_month);

-- Add error_message to receipts so the worker can surface specific
-- failure reasons (budget exceeded, circuit open, etc.) in the UI.
ALTER TABLE receipts ADD COLUMN error_message TEXT;
