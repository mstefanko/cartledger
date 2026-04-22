package spreadsheet

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/mstefanko/cartledger/internal/matcher"
)

// MatchEngine is the minimal surface this package needs from matcher.Engine.
// Defined as an interface so commit tests can inject a fake that errors on
// demand without spinning up the full fuzzy-match pipeline. The real
// *matcher.Engine satisfies this interface by virtue of its own
// MatchWithSuggestion method and NewSession factory.
//
// NewSession returns a batched match session — see internal/matcher/session.go
// for the contract. Callers SHOULD prefer the session path for in-loop
// matching (it collapses the per-item fuzzy queries to a per-batch preload),
// and MUST gracefully fall back to per-call MatchWithSuggestion when
// NewSession returns an error. Test fakes typically return an error from
// NewSession to force the fallback path and keep existing tests green.
type MatchEngine interface {
	MatchWithSuggestion(rawName, suggestedName, storeID, householdID string) matcher.MatchResult
	NewSession(householdID, storeID string) (*matcher.Session, error)
}

// CommitInput carries everything a commit needs. The handler (Phase 5)
// assembles this from the staged state + current config + transform chain;
// Commit itself stays pure (no HTTP concerns, no filesystem I/O).
type CommitInput struct {
	HouseholdID string

	// Sheet is the post-transform ParsedSheet produced by ApplyTransforms.
	// Commit re-runs normalization + grouping so the persisted result is
	// exactly what the preview showed the user.
	Sheet *ParsedSheet

	Mapping     Mapping
	DateFormat  DateFormat
	CSVOptions  CSVOptions
	UnitOptions UnitOptions
	Grouping    Grouping

	// SourceFilename is recorded on the import_batches row for audit.
	SourceFilename string
	// SourceType mirrors the mapping's source_type field ("csv" | "xlsx").
	SourceType string

	// IncludedGroupIDs is a whitelist. If nil, every produced group is
	// committed. If non-nil, only group IDs present in the map (with true
	// value) are committed — used when the user deselects error / duplicate
	// groups in the preview before clicking Commit.
	IncludedGroupIDs map[string]bool

	// ConfirmedDuplicates enumerates group IDs the user explicitly opted in
	// to commit despite matching an existing receipt. Groups that Duplicates
	// flagged as duplicates but are NOT in this set are silently skipped.
	ConfirmedDuplicates map[string]bool

	// Duplicates is the result of a prior CheckDuplicates call — groupID ->
	// existing receipt_id. A missing entry means "not a duplicate". Commit
	// consults this together with ConfirmedDuplicates to decide skip-vs-
	// commit for each group.
	Duplicates DuplicateMap

	// Now is the timestamp stamped onto created_at columns. Injected for
	// deterministic tests. When zero, Commit calls time.Now().UTC() once.
	Now time.Time

	// MapRecorder, when non-nil, is invoked after each per-receipt tx commits
	// with (groupID, receiptID, lineItemIDs). The Phase 5 handler uses it to
	// build the success-screen payload without re-querying the DB.
	MapRecorder func(groupID, receiptID string, lineItemIDs []string)
}

// CommitResult summarizes the outcome of a Commit call. BatchID is present
// even when per-group errors occurred — a partial batch still produces a
// real import_batches row.
type CommitResult struct {
	BatchID            string
	ReceiptsCreated    int
	LineItemsCreated   int
	UnmatchedLineItems int
	DuplicatesSkipped  int
	Errors             []CommitError
}

// CommitError is a per-group/per-row failure captured during commit. Fatal=
// true when the error prevented the import_batches row itself from being
// written; in that case Commit returns an error. Non-fatal errors are
// collected and surfaced in CommitResult.Errors without aborting the batch.
type CommitError struct {
	GroupID string
	Message string
	Fatal   bool
}

// Commit runs normalization + grouping on in.Sheet and writes the resulting
// groups to the database. Flow:
//
//  1. One tx creates the import_batches row (so the FK exists before any
//     receipt references it). This tx is committed immediately.
//  2. For each included, non-skipped group: one transaction creates the
//     receipts row, N line_items rows, and — for matched items — the
//     product_aliases / product_prices rows and the product stats bump.
//     Per-receipt tx isolation means a single failure doesn't roll back
//     the whole import (see PLAN §Backend "per imported receipt, not per
//     whole import").
//  3. A final tx updates import_batches with receipts_count / items_count /
//     unmatched_count / completed_at.
//
// Callers MUST pre-validate that in.Sheet, in.HouseholdID, and in.SourceType
// are set. Commit returns an error for setup failures (missing household,
// DB unavailable, batch INSERT failed); per-group failures are reported in
// CommitResult.Errors.
func Commit(ctx context.Context, db *sql.DB, matchEngine MatchEngine, in CommitInput) (*CommitResult, error) {
	if db == nil {
		return nil, fmt.Errorf("commit: nil db")
	}
	if matchEngine == nil {
		return nil, fmt.Errorf("commit: nil match engine")
	}
	if in.HouseholdID == "" {
		return nil, fmt.Errorf("commit: missing household_id")
	}
	if in.Sheet == nil {
		return nil, fmt.Errorf("commit: nil sheet")
	}
	if in.SourceType != "csv" && in.SourceType != "xlsx" {
		return nil, fmt.Errorf("commit: source_type must be csv or xlsx, got %q", in.SourceType)
	}

	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	// Re-run normalization + grouping on the already-transformed sheet. This
	// is deliberately the *same* code path the preview handler will use in
	// Phase 5, so commit never drifts from what the user saw.
	parsed := make([]ParsedValue, 0, len(in.Sheet.Rows))
	for _, raw := range in.Sheet.Rows {
		pv := NormalizeRow(raw, in.Mapping, in.UnitOptions, in.Grouping, in.DateFormat)
		parsed = append(parsed, pv)
	}
	groups := GroupRows(parsed, in.Grouping)

	// Build a row-index -> ParsedValue lookup for the per-receipt pass.
	pvByIdx := make(map[int]ParsedValue, len(parsed))
	for _, pv := range parsed {
		pvByIdx[pv.RowIndex] = pv
	}

	// Step 1: create the import_batches row in its own small tx.
	batchID := uuid.New().String()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO import_batches
		 (id, household_id, source_type, filename, created_at, receipts_count, items_count, unmatched_count)
		 VALUES (?, ?, ?, ?, ?, 0, 0, 0)`,
		batchID, in.HouseholdID, in.SourceType, nullableString(in.SourceFilename), now,
	); err != nil {
		return nil, fmt.Errorf("commit: insert import_batches: %w", err)
	}

	result := &CommitResult{BatchID: batchID}

	// Step 2: per-group commits. Each group runs in its own tx; failures are
	// captured in result.Errors but don't abort the batch.
	for _, g := range groups {
		if in.IncludedGroupIDs != nil && !in.IncludedGroupIDs[g.ID] {
			continue
		}
		// Skip duplicates the user didn't confirm. An empty Duplicates map
		// means CheckDuplicates wasn't run, which is fine — no groups are
		// treated as duplicates.
		if existingID, isDup := in.Duplicates[g.ID]; isDup && existingID != "" {
			if !in.ConfirmedDuplicates[g.ID] {
				result.DuplicatesSkipped++
				continue
			}
		}

		receiptID, lineItemIDs, created, unmatched, err := commitGroup(ctx, db, matchEngine, in, g, pvByIdx, batchID, now)
		if err != nil {
			result.Errors = append(result.Errors, CommitError{
				GroupID: g.ID,
				Message: err.Error(),
				Fatal:   false,
			})
			slog.Warn("spreadsheet commit: group failed", "group_id", g.ID, "err", err)
			continue
		}
		result.ReceiptsCreated++
		result.LineItemsCreated += created
		result.UnmatchedLineItems += unmatched
		if in.MapRecorder != nil {
			in.MapRecorder(g.ID, receiptID, lineItemIDs)
		}
	}

	// Step 3: finalize import_batches counters.
	if _, err := db.ExecContext(ctx,
		`UPDATE import_batches
		 SET receipts_count = ?, items_count = ?, unmatched_count = ?, completed_at = ?
		 WHERE id = ?`,
		result.ReceiptsCreated, result.LineItemsCreated, result.UnmatchedLineItems, now, batchID,
	); err != nil {
		// Non-fatal: the batch row exists; counters are just stale. Log it
		// and surface as a (non-fatal) error in the result.
		slog.Warn("spreadsheet commit: finalize batch failed", "batch_id", batchID, "err", err)
		result.Errors = append(result.Errors, CommitError{
			Message: fmt.Sprintf("finalize batch counters: %v", err),
			Fatal:   false,
		})
	}

	return result, nil
}

// commitGroup writes one receipts row + its line_items (and matched-product
// side-effects) inside a single tx. Returns (receiptID, lineItemIDs,
// lineItemsCreated, unmatchedCount, error). The whole tx rolls back on the
// first failure.
func commitGroup(
	ctx context.Context,
	db *sql.DB,
	matchEngine MatchEngine,
	in CommitInput,
	g Group,
	pvByIdx map[int]ParsedValue,
	batchID string,
	now time.Time,
) (string, []string, int, int, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", nil, 0, 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Resolve (or create) the store by normalized name. Matches the scan
	// worker's "exact LOWER(name) match first, create on miss" policy —
	// see internal/worker/receipt.go:481-509. Spreadsheet imports don't
	// carry store address/city/zip in v1, so the new row is name-only.
	storeID, err := findOrCreateStore(ctx, tx, in.HouseholdID, g.Store, now)
	if err != nil {
		return "", nil, 0, 0, err
	}

	// Open a per-group matcher session. Matcher reads target e.db (NOT the
	// in-flight tx — see matcher/engine.go:30), so a session built here sees
	// the same committed aliases/products the per-call path would see. Scope
	// is per-group (not per-Commit) to preserve the exact semantics of the
	// prior per-call path: aliases written by earlier groups' txs are visible
	// to this group because we open the session AFTER those txs committed.
	//
	// On NewSession error we fall through to the one-shot MatchWithSuggestion
	// path — test fakes deliberately return an error here to exercise the
	// fallback and keep their fixture behavior intact. The group itself
	// continues either way.
	sess, sessErr := matchEngine.NewSession(in.HouseholdID, storeID)
	if sessErr != nil {
		slog.Warn("spreadsheet commit: session open failed, falling back to per-call",
			"group_id", g.ID, "err", sessErr)
		sess = nil
	}

	// Receipt date: the grouping step guarantees every row in a group shares
	// the same date string. Empty-date groups are already segregated by
	// GroupRows into their own group; those shouldn't reach commit (the
	// preview UI deselects them), but if one does we reject with a clear
	// error rather than inserting a blank DATE NOT NULL value.
	if g.Date == "" {
		return "", nil, 0, 0, fmt.Errorf("group %s has no date", g.ID)
	}
	receiptDate, err := time.Parse("2006-01-02", g.Date)
	if err != nil {
		return "", nil, 0, 0, fmt.Errorf("parse receipt date %q: %w", g.Date, err)
	}

	receiptID := uuid.New().String()
	totalStr := centsToDecimalString(g.TotalCents)

	// INSERT into receipts. Columns explicit so the insert is loud when a
	// future migration adds a NOT NULL column without a default.
	//
	// source='import', image_paths/raw_llm_json/llm_provider all NULL per
	// PLAN §Backend. status defaults to 'pending'; we bump it to 'matched'
	// below iff every line item resolved.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO receipts
		 (id, household_id, store_id, receipt_date, subtotal, tax, total,
		  status, source, import_batch_id, created_at)
		 VALUES (?, ?, ?, ?, NULL, NULL, ?, 'pending', 'import', ?, ?)`,
		receiptID, in.HouseholdID, nullableString(storeID), receiptDate, totalStr, batchID, now,
	); err != nil {
		return "", nil, 0, 0, fmt.Errorf("insert receipt: %w", err)
	}

	// Preserve row order within the group (GroupRows already sorts by
	// RowIndex, but we re-sort defensively — a later grouping strategy
	// might not guarantee the invariant).
	rowIndices := make([]int, len(g.RowIndices))
	copy(rowIndices, g.RowIndices)
	sort.Ints(rowIndices)

	var lineItemIDs []string
	lineItemsCreated := 0
	unmatched := 0
	allMatched := true

	for lineNum, ri := range rowIndices {
		pv, ok := pvByIdx[ri]
		if !ok {
			// Shouldn't happen — GroupRows references pvs by index.
			continue
		}
		// Skip rows that clearly aren't items: blank raw name AND zero
		// money (a stray subtotal placeholder left in after transforms).
		// A non-zero total is enough signal to keep the row — the
		// user may genuinely have an unnamed line.
		if strings.TrimSpace(pv.Item) == "" && pv.TotalCents == 0 && pv.UnitPriceCents == 0 {
			continue
		}

		lineItemID := uuid.New().String()
		rawName := pv.Item
		qtyStr := decimal.NewFromFloat(pv.Qty).String()
		unit := pv.Unit
		if unit == "" {
			unit = "ea"
		}

		var unitPriceStr *string
		if pv.UnitPriceCents != 0 {
			s := centsToDecimalString(pv.UnitPriceCents)
			unitPriceStr = &s
		}
		totalPriceStr := centsToDecimalString(pv.TotalCents)

		// Matcher: spreadsheet rows don't carry a "suggested name" from an
		// LLM — pass "" for suggestedName. MatchWithSuggestion degenerates
		// to the 3-stage raw-name pipeline in that case (see
		// matcher/engine.go:60-67). Prefer the per-group session when it
		// opened cleanly; fall back to the per-call engine path otherwise.
		var matchResult matcher.MatchResult
		if strings.TrimSpace(rawName) != "" {
			if sess != nil {
				matchResult = sess.MatchWithSuggestion(rawName, "")
			} else {
				matchResult = matchEngine.MatchWithSuggestion(rawName, "", storeID, in.HouseholdID)
			}
		} else {
			matchResult = matcher.MatchResult{Method: "unmatched"}
		}

		var productID *string
		var confidence *float64
		matched := matchResult.Method

		// Only stages 1-3 (rule/alias/fuzzy) finalize a product_id on a
		// spreadsheet row. Suggestion-class matches (stages 4-5) produce
		// method="suggested" or "cross_store_match"; those are proposals
		// awaiting user confirmation, so we keep matched="unmatched" and
		// don't set product_id — mirroring the scan worker's behavior at
		// internal/worker/receipt.go:619-627.
		switch matchResult.Method {
		case "unmatched", "suggested", "cross_store_match":
			matched = "unmatched"
			unmatched++
			allMatched = false
		default:
			pid := matchResult.ProductID
			conf := matchResult.Confidence
			productID = &pid
			confidence = &conf
		}

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO line_items
			 (id, receipt_id, product_id, raw_name, quantity, unit,
			  unit_price, total_price, matched, confidence, line_number,
			  import_batch_id, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			lineItemID, receiptID, productID, rawName, qtyStr, unit,
			unitPriceStr, totalPriceStr, matched, confidence, lineNum+1,
			batchID, now,
		); err != nil {
			return "", nil, 0, 0, fmt.Errorf("insert line_item row %d: %w", ri, err)
		}

		lineItemIDs = append(lineItemIDs, lineItemID)
		lineItemsCreated++

		// If we assigned a product, perform the same side-effects the scan
		// worker performs: backfill brand/category (no-op in v1 since we
		// pass empty strings — kept so the contract is symmetric across
		// ingestion paths; see PLAN §Optional Fields & the Matcher), insert
		// product_alias if missing, insert product_prices row, bump
		// product purchase stats.
		if productID != nil && storeID != "" {
			if err := matcher.BackfillProductMetadata(tx, *productID, "", ""); err != nil {
				// Non-fatal per helper contract (see matcher/backfill.go
				// doc comment). Log and continue.
				slog.Warn("spreadsheet commit: backfill failed", "product_id", *productID, "err", err)
			}

			normalized := matcher.Normalize(rawName)

			var aliasExists int
			if err := tx.QueryRowContext(ctx,
				"SELECT COUNT(*) FROM product_aliases WHERE product_id = ? AND alias = ?",
				*productID, normalized,
			).Scan(&aliasExists); err != nil {
				return "", nil, 0, 0, fmt.Errorf("check alias: %w", err)
			}
			if aliasExists == 0 {
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO product_aliases (id, product_id, alias, store_id, created_at)
					 VALUES (?, ?, ?, ?, ?)`,
					uuid.New().String(), *productID, normalized, storeID, now,
				); err != nil {
					return "", nil, 0, 0, fmt.Errorf("insert alias: %w", err)
				}
			}

			// product_prices: unit_price per-unit (total/qty). Uses the
			// same math as worker/receipt.go:702 — total.Div(quantity) —
			// to keep analytics comparable between scan and import paths.
			qtyDec := decimal.NewFromFloat(pv.Qty)
			if qtyDec.IsZero() {
				qtyDec = decimal.NewFromInt(1)
			}
			totalDec := decimal.New(pv.TotalCents, -2)
			up := totalDec.Div(qtyDec)

			priceID := uuid.New().String()
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO product_prices
				 (id, product_id, store_id, receipt_id, receipt_date,
				  quantity, unit, unit_price, is_sale, created_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?)`,
				priceID, *productID, storeID, receiptID, receiptDate,
				qtyDec.String(), unit, up.String(), now,
			); err != nil {
				return "", nil, 0, 0, fmt.Errorf("insert product_price: %w", err)
			}

			if _, err := tx.ExecContext(ctx,
				`UPDATE products
				 SET last_purchased_at = ?, purchase_count = purchase_count + 1, updated_at = ?
				 WHERE id = ?`,
				receiptDate, now, *productID,
			); err != nil {
				return "", nil, 0, 0, fmt.Errorf("update product stats: %w", err)
			}
		}
	}

	// Bump status to 'matched' when every line resolved. If no line items
	// were created at all (empty group after filtering), leave 'pending'
	// — the UI will show it as needing review.
	if allMatched && lineItemsCreated > 0 {
		if _, err := tx.ExecContext(ctx,
			`UPDATE receipts SET status = 'matched' WHERE id = ?`,
			receiptID,
		); err != nil {
			return "", nil, 0, 0, fmt.Errorf("update receipt status: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return "", nil, 0, 0, fmt.Errorf("commit tx: %w", err)
	}
	return receiptID, lineItemIDs, lineItemsCreated, unmatched, nil
}

// findOrCreateStore returns an existing store's id for (householdID,
// normalizedName), or creates a new name-only row. storeName should have
// already been passed through NormalizeStoreName during parse/normalize.
// An empty storeName is legal — returns "" so the caller can persist a
// receipt with store_id=NULL (PLAN §Store Handling case 3).
func findOrCreateStore(ctx context.Context, tx *sql.Tx, householdID, storeName string, now time.Time) (string, error) {
	if strings.TrimSpace(storeName) == "" {
		return "", nil
	}

	var storeID string
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM stores WHERE household_id = ? AND LOWER(name) = LOWER(?)`,
		householdID, storeName,
	).Scan(&storeID)
	if err == nil {
		return storeID, nil
	}
	if err != sql.ErrNoRows {
		return "", fmt.Errorf("lookup store: %w", err)
	}

	// Create a name-only store. Spreadsheet imports carry no address/city/
	// zip in v1 (PLAN §Risks — "we intentionally don't infer store
	// metadata from a spreadsheet").
	storeID = uuid.New().String()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO stores (id, household_id, name, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)`,
		storeID, householdID, storeName, now, now,
	); err != nil {
		return "", fmt.Errorf("create store: %w", err)
	}
	return storeID, nil
}

// centsToDecimalString formats an integer-cents value as the repo's
// canonical decimal string ("499" -> "4.99"). Uses decimal.New(cents, -2)
// rather than decimal.NewFromFloat to avoid float-drift surprises like
// 4.99 rendering as "4.990000000000001". Money columns are TEXT
// (PLAN §Risks).
func centsToDecimalString(cents int64) string {
	return decimal.New(cents, -2).String()
}

// nullableString returns nil for empty strings and a string value otherwise.
// Used when passing optional columns to SQL driver placeholders — the driver
// translates nil to NULL.
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
