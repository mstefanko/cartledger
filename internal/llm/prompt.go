package llm

import "fmt"

const receiptExtractionPrompt = `Extract all items from this grocery receipt image and call the extract_receipt tool with the results.

Rules:
- raw_name must be EXACTLY as printed on receipt (preserve abbreviations)
- store_item_code: store-printed item number/SKU if visible; Costco commonly prints this before the description
- receipt_description: printed product text with the store item number/SKU removed only when confident
- upc: product barcode/UPC/GTIN if explicitly printed for this line item; digits only; null when absent
- Store item numbers/SKUs are not purchase quantities
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
- suggested_category must be one of: Meat, Produce, Dairy, Bakery, Frozen, Pantry, Snacks, Beverages, Household, Health, Other
- If quantity and unit_price are visible, include both
- If only total_price is visible, set unit_price to null and default packaged-goods purchase quantity to 1
- Package text like 1GAL, 16 CT, 2 x 8 ct, or 2/31.7 is package content, not purchase quantity
- Keep measured sold quantities like 2.34 lb in quantity/unit
- unit should be standardized: lb, oz, gal, qt, pt, each, pack, ct
- Omit non-grocery items (bag fees, bottle deposits) but include tax/total
- Per-item confidence score: 0.95+ for clearly readable, 0.7-0.95 for partially obscured, <0.7 for guesses
- store_number: extract store/location number if printed (often after store name or in header). Return digits only, strip any '#' or 'No.' prefix.
- payment_card_type: extract the card brand/tender from the payment section at bottom of receipt.
- payment_card_last4: extract ONLY the four trailing digits from a masked payment account line (examples: "XXXXXXXXXXXX1234", "**** **** **** 1234", "CARD ********1234"). Prefer a masked account line directly above, below, or on the same line as the card brand.
- Do NOT use member numbers, store numbers, phone/zip codes, totals, AID, sequence numbers, app/auth codes, transaction IDs, reference numbers, or approval codes as payment_card_last4.
- If the card brand is visible but the masked account last 4 is uncertain, set payment_card_type to the brand and payment_card_last4 to null.
- For Cash or Check, set payment_card_last4 to null.
- payment_card_raw: transcribe the exact visible payment-section lines around the masked account, card brand, AID/auth/sequence/transaction details. Preserve line breaks and do not invent masking dots.
- time: extract transaction time if printed (usually near date)
- items_sold_count: if the receipt prints "Items Sold", "Total number of items sold", or similar, extract that integer; otherwise null
- If an item has a discount/savings line immediately following it, combine them:
  - regular_price = the original/higher price
  - discount_amount = the savings amount (positive number)
  - total_price = the final price paid (regular_price - discount_amount)
- Coupon, savings, discount, member price, or instant savings rows are NOT products. Attach them to the affected item when possible.
- For Costco-style coupon references like "000343232/1005641 5.00-", the number after "/" points to the item number that received the discount.
- If no discount applies to an item, set regular_price and discount_amount to null
- Preserve printed item order for real product items only.
- total_price MUST always be the actual amount charged for the item`

func receiptRepairPrompt(currentJSON, note string) string {
	return fmt.Sprintf(`Review this grocery receipt image and repair the existing structured extraction.

Current extraction JSON:
%s

User repair note:
%s

Rules:
- Return a full corrected extraction object using the same schema as extract_receipt.
- Preserve existing correct items.
- Fix only issues supported by the image or the user note.
- Store item numbers/SKUs are not purchase quantities.
- If a product barcode/UPC/GTIN is printed for a line item, include upc as digits only; otherwise null.
- For Costco receipts, the leading numeric field before the description is usually the store_item_code.
- If only total_price is visible, set packaged-goods purchase quantity to 1.
- Package text like 1GAL, 16 CT, 2 x 8 ct, or 2/31.7 is package content, not purchase quantity.
- Coupon, savings, discount, member price, or instant savings rows are adjustments, not products.
- If the receipt prints an items sold count, include items_sold_count.
- Return corrected real product items in printed order.
- Return ONLY the JSON object, no markdown fences or explanation.`, currentJSON, note)
}
