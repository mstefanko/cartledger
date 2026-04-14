# Phase 2: Store Enrichment, Payment Tracking, Sale Prices

## Overview

Four features sharing a single migration and coordinated LLM prompt update.

## Feature 1: Store Entity Enrichment

**Migration** — Add to `stores`: `address`, `city`, `state`, `zip`, `store_number`, `nickname`, `latitude`, `longitude`

**LLM prompt** — Extract `store_city`, `store_state`, `store_zip`, `store_number`

**Store matching** (in worker):
1. Exact match on `LOWER(name)` (existing)
2. Match by `store_number` + base name (e.g., "Costco" from "Costco Mt Laurel #749")
3. On match, progressively enrich address fields if previously NULL

**Frontend** — Show address/store number in store detail, nickname in sidebar

## Feature 2: Payment Method Tracking

**Migration** — Add to `receipts`: `card_type`, `card_last4`

**LLM prompt** — Extract `payment_card_type` (Visa/MC/Amex/etc), `payment_card_last4`

**Frontend** — Payment badge on receipt detail (e.g., "Visa ...0388")

## Feature 3: Transaction Date/Time

**Migration** — Add to `receipts`: `receipt_time`

**LLM prompt** — Extract `time` as "HH:MM" 24-hour or null

**Frontend** — Display time alongside date: "Mar 11, 2025 at 7:59 PM"

## Feature 4: Sale/Discount Price Handling

**Migration** — Add to `line_items`: `regular_price`, `discount_amount`. Add to `product_prices`: `regular_price`, `discount_amount`, `is_sale`

**Key design decisions:**
- `total_price` remains the **final paid price** (invariant: `total_price = regular_price - discount_amount`)
- When no discount: `regular_price` and `discount_amount` are NULL
- LLM must combine item + its discount line into a single entry

**LLM prompt** — Extract per item: `regular_price`, `discount_amount`, `total_price`

**Analytics:**
- Sparklines/trends use `total_price` (actual paid price) — this is what matters to the user
- Sale data points marked with `is_sale` flag for visual differentiation (green dot)
- Product detail shows "You saved $X.XX" summary
- Deals endpoint enhanced to distinguish genuine sales from just cheap stores

**Frontend display for line items with discount:**
```
$9.99  (was $14.99, saved $5.00)
```
Strikethrough on regular price, green "saved" badge.

## Implementation Order

1. **Phase A** — Single migration + LLM types/prompt (all 4 features)
2. **Phase B** — Worker: store matching → payment/time → discount handling
3. **Phase C** — API handlers (additive, all 4 features)
4. **Phase D** — Frontend types → UI per feature

## Critical Files

- `internal/llm/prompt.go` — all 4 features modify this
- `internal/llm/types.go` — Go structs matching prompt schema
- `internal/worker/receipt.go` — processing pipeline
- `internal/api/receipts.go` — response types + queries
- `web/src/types/index.ts` — frontend types mirror backend
