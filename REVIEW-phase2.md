# Phase 2 Work Breakdown Review

Reviewed: 2026-04-14
Reviewer: Deep analysis agent
Source: WORK-BREAKDOWN-phase2.md verified against actual source files

---

## Step 1: Migration (002_phase2_enrichment.up.sql) -- READY

**Verdict: READY**

Verified:
- `stores` table (001_initial.up.sql:20-29) has NO address/city/state/zip/store_number/nickname/lat/lng columns. No conflicts.
- `receipts` table (001_initial.up.sql:74-88) has NO card_type/card_last4/receipt_time columns. No conflicts.
- `line_items` table (001_initial.up.sql:91-104) has NO regular_price/discount_amount columns. No conflicts.
- `product_prices` table (001_initial.up.sql:107-119) has NO regular_price/discount_amount/is_sale columns. No conflicts.
- Migration system uses golang-migrate/migrate with embedded FS (`internal/db/migrate.go:8-14`). File naming convention `NNN_name.{up,down}.sql` is correct.
- Only one migration exists today (001_initial). Numbering 002 is correct.

Notes:
- The partial index `CREATE INDEX idx_stores_store_number ON stores(household_id, store_number) WHERE store_number IS NOT NULL` uses SQLite partial index syntax -- this is valid for SQLite 3.8.0+ which modernc.org/sqlite supports.
- The `is_sale BOOLEAN DEFAULT FALSE` is safe for SQLite (stored as integer 0).

**Pre-existing bug confirmed**: Receipt status CHECK constraint at 001_initial.up.sql:85 is `CHECK (status IN ('pending', 'matched', 'reviewed'))` but worker/receipt.go:76 sets `status = 'error'`. This violates the CHECK constraint. The breakdown correctly identifies this as out-of-scope, but the migration SHOULD add 'error' and 'processing' to the CHECK constraint since the worker also uses 'processing' at receipt.go:177. Recommendation: add an ALTER or recreate for the receipts table status CHECK in this migration, or at minimum document it as a known bug to fix separately. SQLite does not support ALTER COLUMN to change CHECK constraints, so this would require a table recreate -- confirming it should be a separate migration.

---

## Step 2: LLM Types (internal/llm/types.go) -- READY

**Verdict: READY**

Verified:
- `ReceiptExtraction` struct (types.go:4-13) currently has `StoreAddress *string` at line 6. The proposed new fields (`StoreCity`, `StoreState`, `StoreZip`, `StoreNumber`, `PaymentCardType`, `PaymentCardLast4`, `Time`) follow the same `*string` pointer pattern for optional fields. No conflicts.
- `ExtractedItem` struct (types.go:16-26) currently has `UnitPrice *float64` at line 22. The proposed `RegularPrice *float64` and `DiscountAmount *float64` follow the same pattern. No conflicts.
- JSON tag naming is consistent with existing tags (snake_case).

---

## Step 3: LLM Prompt (internal/llm/prompt.go) -- READY

**Verdict: READY**

Verified:
- The prompt is a single const string `receiptExtractionPrompt` (prompt.go:3-38).
- The JSON schema section (lines 6-27) has `"store_address"` at line 8. New store fields go after this line -- correct.
- Per-item schema (lines 10-22) has `"total_price"` at line 18. New `regular_price` and `discount_amount` go after this -- correct.
- Rules section starts at line 29. New rules append naturally.
- The prompt does NOT use a structured JSON schema validator -- it's free-form text. This means the LLM may or may not reliably extract the new fields, but since they're all nullable, this degrades gracefully.

---

## Step 4: Worker Pipeline (internal/worker/receipt.go) -- NEEDS CLARIFICATION

**Verdict: NEEDS CLARIFICATION**

### 4a. Store matching (after line 172)

**Line numbers verified**: Store find-or-create logic is at lines 154-172. The breakdown says "after line 172" which is correct.

**Issue**: The breakdown proposes a secondary match by `store_number` alone (`WHERE household_id = ? AND store_number = ?`). The Risks section (risk #2) correctly identifies this could cause false positives across chains, and suggests using `store_number + base name`. However, the Step 4a code example does NOT implement the base-name fuzzy match -- it only does `store_number` match. The implementation should match the risk mitigation, not the step description.

**Clarification needed**: Define what "base name" matching means concretely. Is it `LOWER(name) LIKE ?` with the first word of `extraction.StoreName`? Or an exact match on the name? The worker should implement whichever is chosen.

### 4b. Receipt UPDATE (lines 175-184)

**Line numbers verified**: The UPDATE is at lines 175-181. The breakdown says "line 175-184" -- close enough (actual block ends at 184 with error handling).

**Issue**: The worker currently sets `status = 'processing'` at line 177. This value violates the CHECK constraint (see Step 1 note). The breakdown preserves this behavior. This is fine as an out-of-scope pre-existing bug, but the writer should be aware.

### 4c. Line item INSERT (lines 219-226)

**Line numbers verified**: The INSERT is at lines 219-225. Accurate.

**Correct approach**: Converting `*float64` to `*string` via `decimal.NewFromFloat` before INSERT is consistent with the existing pattern for `unitPrice` at lines 196-199.

### 4d. Product price INSERT (lines 261-268)

**Line numbers verified**: The INSERT is at lines 261-266. Close enough.

**Correct approach**: Adding `regular_price`, `discount_amount`, `is_sale` to the insert is straightforward.

**MISSING**: The breakdown needs to specify where `regularPrice` and `discountAmount` string variables (computed in 4c) are available in scope. They are computed inside the `for _, item := range extraction.Items` loop, and the product_prices INSERT is also inside that loop (line 261 is inside the `if productID != nil && storeID != ""` block starting at line 231). So the variables from 4c ARE in scope for 4d. This is fine, but worth confirming the writer understands the scoping.

---

## Step 5: API Handler Changes -- NEEDS CLARIFICATION

**Verdict: NEEDS CLARIFICATION**

### 5a. receipts.go -- Verified

- `receiptDetailResponse` (line 64-78): Adding `CardType`, `CardLast4`, `ReceiptTime` is correct.
- `lineItemResponse` (line 48-62): Adding `RegularPrice`, `DiscountAmount` is correct.
- Receipt GET query (line 247): Currently selects 12 columns. Adding 3 more (`card_type`, `card_last4`, `receipt_time`) and updating Scan is straightforward.
- Line items GET query (line 282): Currently selects 13 columns. Adding 2 more (`regular_price`, `discount_amount`) and updating Scan is straightforward.
- Receipt List query (line 198): The breakdown says "optional for list view" -- this is fine; the list view can defer these fields.

### 5b. stores.go -- Verified

- `storeResponse` (line 44-52): Adding all new fields is correct.
- Store List SELECT (line 70): Currently selects 7 columns. Must add 8 new columns + update Scan.
- Store Create RETURNING (line 123-124) and after-create SELECT (line 124): Must add new columns.
- Store Update (line 150-152): Currently only updates `name`, `icon`, `updated_at`. Must add new fields to the UPDATE SET clause AND to the `updateStoreRequest` struct (line 28-31).
- **All 4 SELECT queries that read from stores in this file need updating** (List:70, Create:124, Update:167, and the initial Create INSERT if we want to accept these fields on create).

### 5c. products.go -- Verified

- `priceHistoryEntry` (line 640-651): Adding `RegularPrice`, `DiscountAmount`, `IsSale` is correct.
- Price history SELECT (line 763): Currently selects 10 columns. Adding 3 more is straightforward.
- `purchaseStats` (line 661-666): Adding `TotalSaved` is correct.
- The total_saved query is a new standalone query -- easy to add.

### 5d. analytics.go -- Verified

- `dealItem` (line 106-113): Adding `IsSale` is correct.
- `pricePoint` (line 46-50): Adding `IsSale` for sparkline green dots is correct.
- The Deals query (line 522-545) does not currently join on `is_sale`. Adding it requires modifying the SELECT to include `latest_pp.is_sale`.
- The ProductTrend query (line 196-204) selects from `product_prices` -- adding `is_sale` to the SELECT and pricePoint Scan is straightforward.

### MISSING from breakdown: matching.go

**This is a gap.** `internal/api/matching.go:163-168` has an `INSERT INTO product_prices` that does NOT include the new `regular_price`, `discount_amount`, `is_sale` columns. When a user manually matches a line item, a product_prices row is created. Since the line_items table will now have `regular_price` and `discount_amount`, the ManualMatch handler should:

1. Also SELECT `li.regular_price, li.discount_amount` from line_items (currently at matching.go:103)
2. Pass those values through to the `INSERT INTO product_prices` at matching.go:164
3. Compute `is_sale` as `regular_price IS NOT NULL AND discount_amount IS NOT NULL`

Without this fix, manually matched items will always have NULL discount data in product_prices even if the line_item has discount info.

### MISSING from breakdown: export.go and lists.go

- `export.go:48` queries `pp.unit_price FROM product_prices` -- this does NOT need updating (it only reads unit_price, which is unchanged).
- `lists.go:221` queries `pp.unit_price FROM product_prices` -- same, no update needed.
- `lists.go:284` queries `MAX(pp2.receipt_date) FROM product_prices` -- no update needed.

These are confirmed safe to leave unchanged.

---

## Step 6: Frontend Types (web/src/types/index.ts) -- READY

**Verdict: READY**

Verified:
- `Store` interface (line 23-31): Adding address/city/state/zip/store_number/nickname/lat/lng fields with `| null` is correct.
- `Receipt` interface (line 66-80): Adding card_type/card_last4/receipt_time is correct. Note: the `status` union type at line 77 is `'pending' | 'matched' | 'reviewed'` -- it should probably also include `'error' | 'processing'` to match reality, but this is the same pre-existing bug.
- `LineItem` interface (line 82-95): Adding regular_price/discount_amount is correct.
- `ProductPrice` interface (line 97-109): Adding regular_price/discount_amount/is_sale is correct.
- `SparklinePoint` interface (line 522-526): Adding `is_sale: boolean` is correct.
- `Deal` interface (line 563-570): Adding `is_sale: boolean` is correct.
- `PriceHistoryEntry` interface (line 165-173): Adding regular_price/discount_amount/is_sale is correct. Note: the frontend PriceHistoryEntry does NOT match the backend `priceHistoryEntry` struct perfectly -- frontend has `date`/`store_name` while backend has `receipt_date`/`store_name`. The JSON serialization uses `receipt_date` on the backend. The writer should verify the frontend field names match the backend JSON tags exactly.
- `UpdateStoreRequest` interface (line 270-273): Adding nickname/address/city/state/zip is correct.

---

## Step 7: Frontend UI Changes -- READY (low risk, additive)

**Verdict: READY**

The UI changes are all additive display enhancements. Line number references for frontend components were not verified (they shift frequently), but the component names and approach are sound:

- 7a: StoreViewPage address display -- straightforward conditional rendering.
- 7b: ReceiptReview payment badge and time -- straightforward.
- 7c: ReceiptReview discount display with strikethrough -- the code example is well-structured.
- 7d: ReceiptsPage list additions -- minor.
- 7e: ProductDetailPage savings summary -- depends on backend `total_saved` stat.
- 7f: Sparkline green dots -- depends on `is_sale` field in SparklinePoint.
- 7g: Analytics/stores API type updates -- straightforward.

---

## Summary of Issues Found

### Critical (must fix before implementation)

1. **matching.go gap**: The `INSERT INTO product_prices` in `ManualMatch` handler (matching.go:163-168) is NOT mentioned in the breakdown. It needs `regular_price`, `discount_amount`, `is_sale` columns added, and the preceding SELECT (matching.go:103) needs to also fetch `li.regular_price, li.discount_amount` from line_items.

### Important (should address)

2. **Store matching ambiguity (Step 4a)**: The step describes store_number-only matching but the Risks section says to use store_number + base name. The implementation code should be consistent. Recommend: `WHERE household_id = ? AND store_number = ? AND LOWER(name) LIKE LOWER(? || '%')` using the first word of the store name.

3. **Receipt status CHECK constraint**: Both 'processing' (receipt.go:177) and 'error' (receipt.go:76) violate the CHECK at 001_initial.up.sql:85. While out of scope, the writer should know that these INSERTs/UPDATEs may silently fail or be ignored depending on SQLite's CHECK enforcement behavior. SQLite enforces CHECK constraints and will return an error. This means the worker is currently broken for these status transitions. This should be escalated as a separate bug fix, ideally in the same migration (002).

### Minor

4. **Frontend PriceHistoryEntry field naming**: The frontend type uses `date` but the backend serializes as `receipt_date`. Verify these align or the frontend will get `undefined` for the date field.

5. **Down migration caveat**: SQLite DROP COLUMN requires >= 3.35.0. modernc.org/sqlite bundles SQLite 3.41.0+ so this is safe, but worth a quick verification.

---

## Step Verdicts

| Step | Description | Verdict | Blocking Issues |
|------|-------------|---------|-----------------|
| 1 | Migration 002 | READY | None (CHECK bug is pre-existing) |
| 2 | LLM Types | READY | None |
| 3 | LLM Prompt | READY | None |
| 4 | Worker Pipeline | NEEDS CLARIFICATION | Store matching strategy ambiguity |
| 5 | API Handlers | NEEDS CLARIFICATION | Missing matching.go updates |
| 6 | Frontend Types | READY | None |
| 7 | Frontend UI | READY | None |

## Recommended Action

1. Add a **Step 5e** for matching.go: update ManualMatch to read discount fields from line_items and pass them through to product_prices INSERT.
2. Clarify Step 4a store matching to define the exact SQL for the store_number + name match.
3. Consider adding 'error' and 'processing' to the receipts status CHECK in migration 002 (a single `ALTER TABLE` won't work for CHECK changes in SQLite, but since this is a known bug blocking the worker, it may warrant a table recreate in the migration).
