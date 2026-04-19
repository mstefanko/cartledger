package matcher

import (
	"database/sql"
	"log/slog"
	"strings"
)

// Execer is the minimal SQL-execution interface satisfied by both *sql.DB and
// *sql.Tx. BackfillProductMetadata uses it so callers can run the backfill
// inside an existing transaction when one is available.
type Execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// BackfillProductMetadata fills NULL brand/category on an existing product from
// an incoming line-item suggestion. It is safe to call multiple times
// (idempotent) and never overwrites a non-NULL value — that belongs to the
// user. The `AND brand IS NULL` / `AND category IS NULL` clauses on the UPDATE
// statements are load-bearing: they prevent a race between two concurrent
// writers (scan-worker vs. spreadsheet-import) from clobbering user-set data.
//
// The two fields are independent: one can be backfilled without the other.
// Empty-string suggestions are treated as NULL and skipped. Brand values are
// normalized via NormalizeBrand before writing so we don't promote raw
// abbreviations ("KS", "GV") into the canonical column.
//
// Scope: call ONLY when an incoming line item is being assigned to an existing
// product. For newly-created products, the normal INSERT flow writes brand /
// category directly; no backfill is needed.
//
// Confidence gating: the helper intentionally does NOT take a confidence
// parameter. The line_item.suggested_brand / suggested_category columns in
// SQLite do not carry per-field confidence, and the spreadsheet-import path
// (see PLAN-spreadsheet-import.md Phase 5) likewise has no per-field
// confidence signal. Gating purely on non-empty, normalized strings keeps the
// helper's API minimum-surprise across both ingestion directions. If callers
// have a confidence floor to apply (e.g. the scan worker's item.Confidence),
// they should do that check themselves before calling this helper.
//
// Errors are returned to the caller but the scan worker / import commit path
// treat them as non-fatal warnings — a failed backfill must never abort an
// assignment.
func BackfillProductMetadata(db Execer, productID, suggestedBrand, suggestedCategory string) error {
	if productID == "" {
		return nil
	}

	if brand := strings.TrimSpace(suggestedBrand); brand != "" {
		normalized := NormalizeBrand(brand)
		if normalized != "" {
			if _, err := db.Exec(
				`UPDATE products SET brand = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND brand IS NULL`,
				normalized, productID,
			); err != nil {
				slog.Warn("matcher: backfill brand failed", "product_id", productID, "err", err)
				return err
			}
		}
	}

	if category := strings.TrimSpace(suggestedCategory); category != "" {
		if _, err := db.Exec(
			`UPDATE products SET category = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND category IS NULL`,
			category, productID,
		); err != nil {
			slog.Warn("matcher: backfill category failed", "product_id", productID, "err", err)
			return err
		}
	}

	return nil
}
