package llm

const receiptExtractionPrompt = `Extract all items from this grocery receipt image.
Return a JSON object with this exact structure:

{
  "store_name": "string",
  "store_address": "string or null",
  "store_city": "string or null",
  "store_state": "two-letter state code or null",
  "store_zip": "string or null",
  "store_number": "digits only, no '#' prefix (e.g., '749', '0123') or null",
  "date": "YYYY-MM-DD",
  "payment_card_type": "Visa|Mastercard|Amex|Discover|Debit|EBT|Cash|Check|null",
  "payment_card_last4": "string (last 4 digits) or null — omit for Cash/Check",
  "time": "HH:MM (24-hour) or null",
  "items": [
    {
      "raw_name": "exact text from receipt",
      "suggested_name": "Human-readable canonical product name",
      "suggested_brand": "Brand name or null if generic/store-brand not identifiable",
      "suggested_tags": "comma-separated attributes: organic, frozen, canned, fresh, gluten-free, etc.",
      "suggested_category": "Meat|Produce|Dairy|Bakery|Frozen|Pantry|Snacks|Beverages|Household|Health|Other",
      "quantity": 1.0,
      "unit": "lb" | "oz" | "gal" | "each" | "pack" | null,
      "unit_price": 3.49 or null,
      "total_price": 3.49,
      "regular_price": 14.99,
      "discount_amount": 5.00,
      "line_number": 1,
      "confidence": 0.95
    }
  ],
  "subtotal": 0.00,
  "tax": 0.00,
  "total": 0.00,
  "confidence": 0.95
}

Rules:
- raw_name must be EXACTLY as printed on receipt (preserve abbreviations)
- suggested_name: clean, human-readable canonical product name
  - Include brand when identifiable (e.g., "Kirkland Organic Broccoli Florets")
  - Expand store-brand abbreviations: KS = Kirkland Signature, GV = Great Value, 365 = 365 by Whole Foods
  - Include relevant qualifiers: organic, boneless, skinless, frozen, etc.
  - Do NOT include package size (that goes in quantity/unit)
  - Format: "[Brand] [Qualifiers] Product [Form]" — e.g., "Kirkland Organic Broccoli Florets"
- suggested_brand: the brand name expanded fully, or null for generic products
  - "KS" → "Kirkland Signature", "GV" → "Great Value"
- suggested_tags: comma-separated lowercase attributes extracted from the item
  - Types: organic, conventional, gluten-free, sugar-free, low-fat, etc.
  - Forms: fresh, frozen, canned, dried, whole, sliced, florets, ground, etc.
- suggested_category must be one of the listed categories
- If quantity and unit_price are visible, include both
- If only total_price is visible, set unit_price to null and quantity to 1
- If quantity/weight is embedded in the item name (e.g., "3LB" in "BNLS CHKN BRST 3LB"), extract it
- unit should be standardized: lb, oz, gal, qt, pt, each, pack, ct
- Omit non-grocery items (bag fees, bottle deposits) but include tax/total
- Per-item confidence score: 0.95+ for clearly readable, 0.7-0.95 for partially obscured, <0.7 for guesses
- store_number: extract store/location number if printed (often after store name or in header). Return digits only, strip any '#' or 'No.' prefix.
- payment_card_type and payment_card_last4: extract from payment section at bottom of receipt. For Cash or Check, set card_last4 to null.
- time: extract transaction time if printed (usually near date)
- If an item has a discount/savings line immediately following it, combine them:
  - regular_price = the original/higher price
  - discount_amount = the savings amount (positive number)
  - total_price = the final price paid (regular_price - discount_amount)
- If no discount applies to an item, set regular_price and discount_amount to null
- total_price MUST always be the actual amount charged for the item`
