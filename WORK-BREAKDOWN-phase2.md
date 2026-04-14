# Phase 2 Work Breakdown: Store Enrichment, Payment Tracking, Sale Prices

## Review Status

Reviewed 2026-04-14 by: deep-analysis agent, ux-researcher agent, senior dev consolidation.
All steps verified against source code. Issues from review and UX audit resolved below.

---

## Verified Assumptions

- golang-migrate/migrate with embedded FS, `NNN_name.{up,down}.sql` naming [VERIFIED: migrate.go:8-14]
- Only 001_initial migration exists [VERIFIED]
- `StoreAddress *string` exists in ReceiptExtraction but NOT persisted to stores table [VERIFIED: types.go:6, 001_initial.up.sql has no address column]
- Money values stored as TEXT via shopspring/decimal [VERIFIED: 001_initial.up.sql:98-99, worker/receipt.go:194-198]
- Frontend types mirror backend JSON [VERIFIED]
- modernc.org/sqlite bundles SQLite 3.41.0+ (supports DROP COLUMN, partial indexes) [VERIFIED]

## Approach

Two migrations: 002 fixes the pre-existing receipt status CHECK bug (table recreate required), 003 adds all Phase 2 columns. Then coordinated LLM prompt/types update, worker changes, API changes, and frontend updates.

---

## Work Breakdown (Execution Order)

### Step 1A: Migration — `002_fix_receipt_status.up.sql`

**Why:** The receipt status CHECK constraint only allows `('pending', 'matched', 'reviewed')` but the worker writes `'processing'` (receipt.go:177) and `'error'` (receipt.go:76). SQLite enforces CHECK constraints, so these writes are currently failing. This must be fixed before Phase 2 adds more receipt columns.

**Approach:** SQLite cannot ALTER CHECK constraints — requires table recreate.

```sql
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

-- Recreate indexes from 001_initial
CREATE INDEX idx_receipts_store ON receipts(store_id, receipt_date);
CREATE INDEX idx_receipts_date ON receipts(receipt_date);

DROP TABLE receipts_old;

PRAGMA foreign_keys=ON;
```

**Down migration** (`002_fix_receipt_status.down.sql`): Same pattern but restores original 3-value CHECK.

### Step 1B: Migration — `003_phase2_enrichment.up.sql`

```sql
-- Feature 1: Store Entity Enrichment
ALTER TABLE stores ADD COLUMN address TEXT;
ALTER TABLE stores ADD COLUMN city TEXT;
ALTER TABLE stores ADD COLUMN state TEXT;
ALTER TABLE stores ADD COLUMN zip TEXT;
ALTER TABLE stores ADD COLUMN store_number TEXT;
ALTER TABLE stores ADD COLUMN nickname TEXT;
ALTER TABLE stores ADD COLUMN latitude REAL;
ALTER TABLE stores ADD COLUMN longitude REAL;

-- Feature 2: Payment Method Tracking
ALTER TABLE receipts ADD COLUMN card_type TEXT;
ALTER TABLE receipts ADD COLUMN card_last4 TEXT;

-- Feature 3: Transaction Date/Time
ALTER TABLE receipts ADD COLUMN receipt_time TEXT;

-- Feature 4: Sale/Discount Price Handling
ALTER TABLE line_items ADD COLUMN regular_price TEXT;
ALTER TABLE line_items ADD COLUMN discount_amount TEXT;

ALTER TABLE product_prices ADD COLUMN regular_price TEXT;
ALTER TABLE product_prices ADD COLUMN discount_amount TEXT;
ALTER TABLE product_prices ADD COLUMN is_sale BOOLEAN DEFAULT FALSE;

-- Index for store_number lookups (Feature 1)
CREATE INDEX idx_stores_store_number ON stores(household_id, store_number) WHERE store_number IS NOT NULL;
```

**Down migration** (`003_phase2_enrichment.down.sql`): DROP COLUMN for each (safe on SQLite 3.35.0+, modernc bundles 3.41.0+).

### Step 2: LLM Types — `internal/llm/types.go`

Add to `ReceiptExtraction`:
```go
StoreCity        *string `json:"store_city"`
StoreState       *string `json:"store_state"`
StoreZip         *string `json:"store_zip"`
StoreNumber      *string `json:"store_number"`
PaymentCardType  *string `json:"payment_card_type"`
PaymentCardLast4 *string `json:"payment_card_last4"`
Time             *string `json:"time"`
```

Add to `ExtractedItem`:
```go
RegularPrice   *float64 `json:"regular_price"`
DiscountAmount *float64 `json:"discount_amount"`
```

### Step 3: LLM Prompt — `internal/llm/prompt.go`

At the top level, add after `"store_address"`:
```
"store_city": "string or null",
"store_state": "two-letter state code or null",
"store_zip": "string or null",
"store_number": "digits only, no '#' prefix (e.g., '749', '0123') or null",
"payment_card_type": "Visa|Mastercard|Amex|Discover|Debit|EBT|Cash|Check|null",
"payment_card_last4": "string (last 4 digits) or null — omit for Cash/Check",
"time": "HH:MM (24-hour) or null",
```

**store_number normalization:** Prompt explicitly says "digits only, no '#' prefix" so the LLM strips `#` before returning. This prevents the "Store ##749" double-prefix bug on display.

Per item, add after `"total_price"`:
```
"regular_price": 14.99 or null,
"discount_amount": 5.00 or null,
```

Add to Rules section:
```
- store_number: extract store/location number if printed (often after store name or in header). Return digits only, strip any '#' or 'No.' prefix.
- payment_card_type and payment_card_last4: extract from payment section at bottom of receipt. For Cash or Check, set card_last4 to null.
- time: extract transaction time if printed (usually near date)
- If an item has a discount/savings line immediately following it, combine them:
  - regular_price = the original/higher price
  - discount_amount = the savings amount (positive number)
  - total_price = the final price paid (regular_price - discount_amount)
- If no discount applies to an item, set regular_price and discount_amount to null
- total_price MUST always be the actual amount charged for the item
```

### Step 4: Worker Pipeline — `internal/worker/receipt.go`

**4a. Store matching enhancement (lines 154-172)**

Replace current single-query match with two-phase match:

```go
// Phase 1: Exact name match (existing behavior)
var storeID string
err := tx.QueryRow(
    `SELECT id FROM stores WHERE household_id = ? AND LOWER(name) = LOWER(?)`,
    job.HouseholdID, *extraction.StoreName,
).Scan(&storeID)

// Phase 2: Store number + name prefix match (new)
if err == sql.ErrNoRows && extraction.StoreNumber != nil {
    // Extract first word of store name as base name (e.g., "Costco" from "Costco Mt Laurel #749")
    baseName := strings.Fields(*extraction.StoreName)[0]
    err = tx.QueryRow(
        `SELECT id FROM stores WHERE household_id = ? AND store_number = ? AND LOWER(name) LIKE LOWER(? || '%')`,
        job.HouseholdID, *extraction.StoreNumber, baseName,
    ).Scan(&storeID)
}
```

**Why store_number + base name:** Prevents false positives across chains (Costco #749 vs Walmart #749). Uses first word of store name as chain identifier.

On match (either path), progressively enrich NULL fields:
```go
if storeID != "" {
    tx.Exec(`UPDATE stores SET
        address = COALESCE(address, ?),
        city = COALESCE(city, ?),
        state = COALESCE(state, ?),
        zip = COALESCE(zip, ?),
        store_number = COALESCE(store_number, ?),
        updated_at = ?
        WHERE id = ?`,
        nilStr(extraction.StoreAddress), nilStr(extraction.StoreCity),
        nilStr(extraction.StoreState), nilStr(extraction.StoreZip),
        nilStr(extraction.StoreNumber), now, storeID)
}
```

When creating a new store (no match), populate all fields from extraction.

**4b. Receipt UPDATE with new fields (lines 175-181)**

Add `card_type`, `card_last4`, and `receipt_time`:
```go
_, err = tx.Exec(
    `UPDATE receipts SET store_id = ?, receipt_date = ?, receipt_time = ?,
     subtotal = ?, tax = ?, total = ?,
     card_type = ?, card_last4 = ?,
     raw_llm_json = ?, llm_provider = ?, status = 'processing'
     WHERE id = ?`,
    nilIfEmpty(storeID), receiptDate, extraction.Time,
    subtotal.String(), tax.String(), total.String(),
    extraction.PaymentCardType, extraction.PaymentCardLast4,
    rawJSONStr, provider, job.ReceiptID,
)
```

**4c. Line item INSERT with discount fields (lines 219-225)**

Compute `regularPrice` and `discountAmount` as `*string` via `decimal.NewFromFloat` (same pattern as existing `unitPrice` at lines 196-199). Add to INSERT.

**4d. Product price INSERT with sale data (lines 261-266)**

Add `regular_price`, `discount_amount`, `is_sale`. Variables from 4c are in scope (same `for item` loop).
```go
isSale := item.RegularPrice != nil && item.DiscountAmount != nil
```

### Step 5: API Handler Changes

**5a. `internal/api/receipts.go`**

Add to `receiptDetailResponse`: `CardType *string`, `CardLast4 *string`, `ReceiptTime *string`.
Add to `lineItemResponse`: `RegularPrice *string`, `DiscountAmount *string`.
Update receipt GET SELECT (line 247) + Scan. Update line items SELECT (line 282) + Scan.

Receipt List query (line 198): **Do NOT add payment/time columns.** Per UX audit, the list view is already at capacity with 4 columns. Payment and time are detail-level info — show only on detail page.

**5b. `internal/api/stores.go`**

Add 8 new fields to `storeResponse`. Update ALL 4 SELECT queries (List:70, Create:124, Update:167, and Create INSERT).
Add `Nickname`, `Address`, `City`, `State`, `Zip` to `updateStoreRequest`.

**5c. `internal/api/products.go`**

Add to `priceHistoryEntry`: `RegularPrice *string`, `DiscountAmount *string`, `IsSale bool`.
Update price history SELECT (line 763) + Scan.
Add `TotalSaved *string` to `purchaseStats` with query:
```sql
SELECT COALESCE(SUM(CAST(discount_amount AS REAL)), 0)
FROM product_prices WHERE product_id = ? AND is_sale = TRUE
```

**5d. `internal/api/analytics.go`**

Add `IsSale bool` to `dealItem` and `pricePoint`.
Update Deals query (line 522) and ProductTrend query (line 196) to include `is_sale`.

**5e. `internal/api/matching.go` — CRITICAL GAP (was missing from original breakdown)**

The ManualMatch handler (lines 102-168) creates a `product_prices` row when a user manually matches a line item. Current INSERT (line 163-168) does NOT include discount columns.

**Fix required:**
1. Update the SELECT at line 102 to also fetch `li.regular_price, li.discount_amount`
2. Update the INSERT at line 163 to include `regular_price, discount_amount, is_sale`
3. Compute `is_sale` as `regularPrice != nil && discountAmount != nil`

Without this fix, manually matched items lose their discount data in product_prices.

### Step 6: Frontend Types — `web/src/types/index.ts`

**Store** — add: `address`, `city`, `state`, `zip`, `store_number`, `nickname`, `latitude`, `longitude` (all `string | null` or `number | null`)

**Receipt** — add: `card_type`, `card_last4`, `receipt_time` (all `string | null`)
Also update `status` union type to include `'processing' | 'error'` (fixes pre-existing type mismatch).

**LineItem** — add: `regular_price`, `discount_amount` (both `string | null`)

**ProductPrice** — add: `regular_price`, `discount_amount` (`string | null`), `is_sale` (`boolean`)

**SparklinePoint** — add: `is_sale: boolean`

**Deal** — add: `is_sale: boolean`

**PriceHistoryEntry** — add: `regular_price`, `discount_amount` (`string | null`), `is_sale` (`boolean`)
**Verify:** frontend field `date` must match backend JSON tag `receipt_date` — check and align.

**UpdateStoreRequest** — add: `nickname?`, `address?`, `city?`, `state?`, `zip?`

### Step 7: Frontend UI Changes

**7a. StoreViewPage — Store detail enrichment**

Add address display and store number badge below the store name/icon header.

**Resolved issues:**
- **Store number prefix:** LLM prompt now strips `#` prefix (Step 3), so display safely uses `Store #{store_number}` without double-prefix risk.
- **Partial address:** Use a single formatted string helper that gracefully handles missing components:
```tsx
const formatAddress = (store: Store) => {
  const parts = [store.address, store.city, store.state].filter(Boolean)
  if (parts.length === 0) return null
  let addr = parts.join(', ')
  if (store.zip) addr += ` ${store.zip}`
  return addr
}
```
- **Nickname editing:** Defer to Phase 3. No inline editing pattern exists in the app yet. Adding it here would require building a new interaction pattern just for one field. Instead, add nickname to the existing store edit form (same pattern as name/icon edit).

**7b. ReceiptReviewPage — Payment badge & time display (REVISED PLACEMENT)**

Per UX audit: the ReceiptReview status bar is already at capacity (3 buttons + 2-3 badges). Payment and time are informational, not actionable — they should NOT go in the action bar.

**Revised approach:** Add a metadata row to `ReceiptReviewPage.tsx` (the parent page), NOT inside ReceiptReview. Place it between the page header and the image/review split:

```tsx
{(receipt.receipt_time || receipt.card_type) && (
  <div className="flex items-center gap-3 text-caption text-neutral-500 mb-4">
    {receipt.receipt_date && (
      <span>
        {formatDate(receipt.receipt_date)}
        {receipt.receipt_time && ` at ${formatTime(receipt.receipt_time)}`}
      </span>
    )}
    {receipt.card_type && (
      <Badge variant="neutral">
        {receipt.card_type}
        {receipt.card_last4 ? ` ····${receipt.card_last4}` : ''}
      </Badge>
    )}
  </div>
)}
```

**Resolved issues:**
- **Date not in ReceiptReview:** Correct — date lives in the parent page. Time goes next to date in the parent.
- **Cash/Check format:** Conditional: if `card_last4` is null (Cash, Check), show just "Cash" or "Check" with no digits.
- **Masking convention:** Use `····` (middle dot U+00B7 or bullet) instead of `...` for standard card masking. Four dots matches the industry 4-digit grouping pattern.

**7c. ReceiptReview — Line item discount display (REVISED)**

**Resolved issues:**
- **Column width:** Increase `size` from `90` to `120` to accommodate two-line display.
- **Edit interaction:** The `cell` renderer is display-only. When user clicks to edit, EditableTable replaces it with an `<input>` that edits `total_price` only. `regular_price` and `discount_amount` are read-only extraction data — users cannot edit them through the table. This is the correct behavior: if the LLM got the discount wrong, the user corrects `total_price` (the actual paid amount).
- **Row height variance:** Accept uneven row heights. The alternative (cramming into one line) is worse for readability. The table already uses `overflow-auto` and variable content in other columns.

```tsx
cell: ({ row }) => {
  const item = row.original
  const price = item.total_price
  const formatted = price != null ? '$' + Number(price).toFixed(2) : '\u2014'

  if (item.regular_price && item.discount_amount) {
    return (
      <div className="text-right">
        <span className="tabular-nums">{formatted}</span>
        <span className="block text-caption text-neutral-400">
          <span className="line-through">
            ${Number(item.regular_price).toFixed(2)}
          </span>
          <span className="text-success-dark ml-1">
            -${Number(item.discount_amount).toFixed(2)}
          </span>
        </span>
      </div>
    )
  }

  return <span className="tabular-nums">{formatted}</span>
}
```

**7d. ReceiptsPage — DEFERRED**

Per UX audit: the receipt list is a 4-column table (Store, Date, Total, Status). Adding payment/time columns would make 6 columns — unusable on mobile (375px). These fields add marginal value to a list view where store + date are the primary identifiers.

**Decision:** Do NOT add payment/time to the receipt list. They are available on the detail page (7b). This can be revisited later if users request it, potentially as a hover tooltip on the date cell rather than additional columns.

**7e. ProductDetailPage — Sale savings summary (REVISED)**

**Resolved issues:**
- **Stats row crowding:** Instead of adding a 5th item to the stats flex row, show savings as a separate callout below the stats row only when `total_saved > 0`:
```tsx
{detail.stats.total_saved && parseFloat(detail.stats.total_saved) > 0 && (
  <div className="mt-2 text-caption text-success-dark">
    Total saved: ${Number(detail.stats.total_saved).toFixed(2)} across {saleCount} sale purchases
  </div>
)}
```
- **"You saved" voice:** Changed to "Total saved" — consistent with the app's impersonal label style ("Total Spent", "Items Tracked", not "Your Total").
- **Context:** Added "across N sale purchases" to provide context for the cumulative number.
- **TransactionsSection:** Add a small green "Sale" badge in the existing Unit Price cell for `is_sale` rows — no additional column.

**7f. Sparkline sale indicators (REVISED)**

**Current reality discovered:**
- `Sparkline` component accepts `number[]` — no structured data support
- ProductDetailPage uses a custom bar chart, NOT the Sparkline component
- DashboardPage has NO sparklines at all
- Sparkline is only used in AnalyticsPage and ProductsPage (small inline polyline, 80x24px)

**Revised approach — two separate changes:**

1. **ProductDetailPage bar chart:** Color individual bars green when `is_sale` is true. This is straightforward — the bars are `<div>` elements, just conditionally apply `bg-success` instead of `bg-brand`. Add a `title` attribute: "Sale price" for hover context.

2. **Sparkline component (AnalyticsPage, ProductsPage):** Extend props to accept optional `highlights`:
```tsx
interface SparklineProps {
  data: number[]
  highlights?: boolean[]  // optional: true = render green dot at this index
  width?: number
  height?: number
  color?: string
}
```
Render small `<circle>` elements at highlighted points. At 80x24px, use `r={2}` and `fill="#16a34a"` (green-600). Only render if `highlights` is provided and has true values.

Add a legend/tooltip note in the table header: "Green = sale price" as a `title` attribute on the Trend column header.

**7g. Analytics & Store API type updates**

Type-only changes — `StoreSummary` inherits enriched `Store` fields automatically. No UI changes needed in analytics views for Phase 2.

---

## Implementation Order (with dependencies)

```
Step 1A: Migration 002 (receipt status CHECK fix)
Step 1B: Migration 003 (Phase 2 columns)
    ↓
Step 2: LLM Types (types.go)
Step 3: LLM Prompt (prompt.go)
    ↓
Step 4: Worker Pipeline (receipt.go) — depends on 2, 3
    ↓
Step 5a-5e: API Handlers — depends on 4
Step 6: Frontend Types — can parallel with Step 5
    ↓
Step 7a-7f: Frontend UI — depends on 5, 6
```

Steps 5 and 6 can be done in parallel. Steps 7a-7f are independent of each other.

---

## Risks

1. **LLM prompt regression**: All new fields nullable — degrades gracefully. Test with 5-10 real receipts after prompt change.

2. **Store matching false positives**: Mitigated by requiring `store_number + LOWER(name) LIKE baseName%`. First word of store name acts as chain identifier.

3. **Discount line combining in LLM**: Inherently imprecise. `total_price` invariant means analytics stay correct even if `regular_price`/`discount_amount` are sometimes null.

4. **Migration 002 table recreate**: Foreign key references to `receipts` from `line_items` and `product_prices` must survive the rename+recreate. `PRAGMA foreign_keys=OFF` handles this. Test with existing data.

5. **store_number normalization**: LLM prompt instructs "digits only, no '#' prefix". If LLM doesn't comply, a backend `strings.TrimLeft(storeNumber, "#")` guard should be added in the worker.

---

## Deferred to Later Phases

- **7d: Receipt list payment/time columns** — deferred per UX audit (mobile density)
- **Nickname inline editing** — no inline edit pattern exists; defer to Phase 3
- **Geocoding/map display** — lat/lng columns added but UI is future work
- **Store deduplication/merge UI** — enrichment helps prevent dupes but doesn't merge existing
- **Historical backfill** — old receipts won't have discount data
- **Payment method analytics** — fields stored but no dedicated analytics view

---

## Test Coverage Needed

1. **Migration 002**: Verify receipts table recreate preserves all data + foreign key references
2. **Migration 003**: Apply to DB with existing data; verify new columns are NULL/FALSE
3. **LLM extraction**: Process receipts with/without discounts, payment, time, store numbers
4. **Worker - store matching**: Test name-only match, store_number+name match, progressive enrichment (COALESCE doesn't overwrite)
5. **Worker - discount handling**: Verify `total_price` = paid price, `regular_price`/`discount_amount` set, `is_sale` true in product_prices
6. **API - receipt detail**: GET returns card_type, card_last4, receipt_time; line items include discount fields
7. **API - store CRUD**: List/get/update return new fields; update can set nickname, address
8. **API - manual match (5e)**: ManualMatch passes discount fields through to product_prices
9. **API - product detail**: price_history includes is_sale/discount; total_saved stat works
10. **Frontend - receipt review**: Discount display at 120px width; payment badge in parent page; Cash/Check format
11. **Frontend - store view**: Address formatting with partial data; store number display
12. **Frontend - product detail**: "Total saved" callout; sale badge in transactions; green bars in chart
13. **Frontend - sparkline**: Green dots render at highlighted points; legend visible
