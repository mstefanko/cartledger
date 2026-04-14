# Sale/Discount Price Storage & Display — Research & Recommendations

**Date**: 2026-04-14
**Context**: CartLedger currently has no discount/sale price fields. This document proposes schema changes based on industry patterns.

---

## 1. Recommended Data Model Changes

### Current Gap
`LineItem` and `ProductPrice` have only `unit_price` and `total_price` — no way to distinguish regular vs. sale pricing or track discounts.

### Proposed Schema: LineItem additions

```sql
ALTER TABLE line_items ADD COLUMN regular_price    DECIMAL(10,2) NULL;  -- shelf/regular price before discount
ALTER TABLE line_items ADD COLUMN discount_amount   DECIMAL(10,2) NULL;  -- total discount on this line
ALTER TABLE line_items ADD COLUMN discount_type     TEXT NULL;           -- 'instant_savings', 'coupon', 'bogo', 'member', 'markdown', 'loyalty', 'manager'
ALTER TABLE line_items ADD COLUMN discount_name     TEXT NULL;           -- raw text from receipt, e.g. "INSTANT SAVINGS", "MFR COUPON"
ALTER TABLE line_items ADD COLUMN is_on_sale        BOOLEAN DEFAULT FALSE;
```

**Key principle** (from Veryfi's receipt API and e-commerce standards):
- `total_price` = what was actually paid (net of discounts) — this is already correct
- `regular_price` = the non-discounted shelf price (NULL if no discount)
- `discount_amount` = `regular_price - total_price` (stored for quick access)
- `unit_price` = stays as-is, represents the effective per-unit price paid

### Proposed Schema: ProductPrice additions

```sql
ALTER TABLE product_prices ADD COLUMN regular_price  DECIMAL(10,2) NULL;
ALTER TABLE product_prices ADD COLUMN discount_amount DECIMAL(10,2) NULL;
ALTER TABLE product_prices ADD COLUMN is_on_sale     BOOLEAN DEFAULT FALSE;
```

### Why this pattern (industry evidence)

| Source | Pattern |
|--------|---------|
| **Veryfi Receipt OCR API** | `line_items[].total`, `line_items[].discount_amount`, `line_items[].discount_percent` |
| **Schema.org / Google** | `Offer.price` (current), `UnitPriceSpecification` with `priceType: StrikethroughPrice` for original |
| **Shopify** | `price` (sale), `compare_at_price` (regular/original) |
| **E-commerce DB pattern** | `price` (current), `compare_at_price` (was), stored as integers in cents |
| **Red Gate pricing model** | Separate `product_pricing` table with `create_date`, `expiry_date`, treats price as slowly-changing dimension |

---

## 2. Analytics: How to Handle Sale Prices in Trend Lines

### Industry approach (Keepa / CamelCamelCamel / price trackers)

**Track what was actually paid** — this is the universal standard.

- **Keepa**: Shows the actual transaction price. Multiple colored lines represent different seller types (Amazon, 3rd-party, used), NOT regular-vs-sale. Every data point is what you'd actually pay.
- **CamelCamelCamel**: Same — plots actual prices over time, which naturally shows dips during sales.
- **Google Shopping**: Price history shows actual listed prices; sale dips are visible as valleys.

### Recommendation for CartLedger

**Primary line**: Plot `unit_price` (what was paid) — users care about their actual spending.

**Sale annotations**: Mark sale-price data points with a visual indicator (small dot, different color, or tag). This lets users see the trend while knowing which points were discounted.

**Optional "regular price" overlay**: A lighter/dashed line showing `regular_price` when available, so users can see the true shelf-price trend separately.

```
Price
$15 ┤ · · · · · ·  ← regular price (dashed, lighter)
$12 ┤         ○     ← sale point marked
$10 ┤───●───●───●── ← actual paid price (solid line)
     Jan  Feb  Mar
```

**For aggregate stats** (avg, min, max):
- Default: Include sale prices (reflects actual cost of living)
- Optional filter: "Exclude sale prices" toggle for users who want to see baseline trends

---

## 3. Frontend Display Patterns

### Line item display (receipt review)

Best practice from Baymard Institute and e-commerce UX research:

```
Chobani Yogurt                    $9.99
  Was $14.99 · Saved $5.00         ↑ paid price, prominent
```

**Rules**:
1. **Paid price** is always the largest/boldest — it's what matters
2. **Original price** in smaller text with strikethrough: ~~$14.99~~
3. **Savings** shown as positive value in green/accent color
4. Use the **anchoring effect**: showing the original price makes the savings feel tangible
5. **Discount type badge** when available: `COUPON`, `MEMBER PRICE`, `BOGO`

### Shopping list estimated prices

When showing estimated prices on shopping lists, use the **most recent non-sale price** as the estimate (more conservative/realistic), but note if a sale was recently seen.

---

## 4. Receipt Parsing: Handling All Discount Formats

### Discount appearance patterns on receipts

| Pattern | Example | Parsing Strategy |
|---------|---------|-----------------|
| **Line below item** | `INSTANT SAVINGS  -5.00` | Associate with preceding item; look for negative amount |
| **Same line suffix** | `CHOBANI YGRT 14.99 SC 5.00-` | Parse `SC` or trailing negative as discount |
| **Coupon block at bottom** | `MFR COUPON  -1.00` | Match coupon to item by keyword/SKU; may need user help |
| **BOGO** | `BUY 1 GET 1 FREE` or `BOGO -4.99` | Detect BOGO keywords; discount = item price; split across qty |
| **Percentage** | `20% OFF  -2.00` | Capture both percent and dollar amount |
| **Member/loyalty price** | `MEMBER PRICE 9.99` or `CARD SAVINGS -2.00` | Look for member/card/loyalty keywords |
| **Manager/ad hoc** | `MGR MARKDOWN -1.50` | Capture as `manager` discount type |
| **Weight-based** | `2.5 lb @ 3.99/lb  9.98` then `SALE -2.00` | Discount applies to computed total |

### LLM Prompt Enhancement for Receipt Parsing

Update the receipt parsing prompt to extract these additional fields per line item:

```json
{
  "raw_name": "CHOBANI YGRT",
  "quantity": "1",
  "unit_price": "9.99",
  "total_price": "9.99",
  "regular_price": "14.99",
  "discount_amount": "5.00",
  "discount_type": "instant_savings",
  "discount_name": "INSTANT SAVINGS"
}
```

### Association rules for the LLM/parser:

1. A discount line immediately following an item line applies to that item
2. "SC" (store coupon), "MC" (manufacturer coupon) suffixes on item lines are inline discounts
3. Bottom-of-receipt coupons: try to match by product keyword; if ambiguous, flag for manual review
4. BOGO: if two identical items and one discount equal to item price, split discount evenly or mark both
5. Member pricing: when receipt shows both member and non-member price, use member as `total_price` and non-member as `regular_price`

---

## 5. TypeScript Type Changes

```typescript
export interface LineItem {
  // ... existing fields ...
  regular_price: string | null;     // shelf price before discount
  discount_amount: string | null;   // how much was saved
  discount_type: string | null;     // 'instant_savings' | 'coupon' | 'bogo' | 'member' | 'markdown' | 'loyalty' | 'manager'
  discount_name: string | null;     // raw discount label from receipt
  is_on_sale: boolean;
}

export interface ProductPrice {
  // ... existing fields ...
  regular_price: string | null;
  discount_amount: string | null;
  is_on_sale: boolean;
}

export interface PriceHistoryEntry {
  // ... existing fields ...
  regular_price: string | null;
  discount_amount: string | null;
  is_on_sale: boolean;
}

export interface SparklinePoint {
  date: string;
  price: string;               // what was paid
  regular_price: string | null; // shelf price, for overlay
  store: string;
  is_on_sale: boolean;
}
```

---

## 6. Migration Notes

- All new fields are nullable — backward compatible with existing data
- `is_on_sale` defaults to `false`
- Existing `total_price` and `unit_price` semantics are unchanged (they already represent what was paid)
- No data migration needed for existing records; they simply have NULL discount fields

---

## Sources

- [Veryfi Receipt OCR API — Field Documentation](https://faq.veryfi.com/en/articles/5571268-data-extraction-fields-explained-for-receipts-invoices-api)
- [Mindee Receipt OCR API](https://developers.mindee.com/docs/receipt-ocr)
- [Schema.org UnitPriceSpecification](https://schema.org/UnitPriceSpecification)
- [Red Gate — Offers, Deals, and Discounts: A Product Pricing Data Model](https://www.red-gate.com/blog/offers-deals-and-discounts-a-product-pricing-data-model/)
- [Red Gate — Designing A Price History Database Model](https://www.red-gate.com/blog/price-history-database-model/)
- [Baymard Institute — How to Display Price Discounts on the Product Page](https://baymard.com/blog/product-page-price-discounts)
- [How to Read Keepa Graphs](https://www.fulltimefba.com/read-understand-keepa-graphs/)
- [Keepa vs CamelCamelCamel comparison](https://goaura.com/blog/camelcamelcamel-vs-keepa)
- [Strikethrough pricing UX](https://www.voucherify.io/blog/strikethrough-pricing-why-a-single-line-still-drives-conversions)
- [Andersen — UI/UX Design of a Grocery Price App](https://andersenlab.com/project-cases/ui-ux-app-grocery-prices-comparison)
- [Azure Document Intelligence — Receipt Schema](https://github.com/Azure-Samples/document-intelligence-code-samples/blob/main/schema/2024-11-30-ga/receipt.md)
- [E-commerce Database Schema Guide](https://erflow.io/en/blog/ecommerce-database-schema)
