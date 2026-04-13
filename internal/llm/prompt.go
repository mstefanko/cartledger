package llm

const receiptExtractionPrompt = `Extract all items from this grocery receipt image.
Return a JSON object with this exact structure:

{
  "store_name": "string",
  "store_address": "string or null",
  "date": "YYYY-MM-DD",
  "items": [
    {
      "raw_name": "exact text from receipt",
      "suggested_name": "Human-readable canonical product name",
      "suggested_category": "Meat|Produce|Dairy|Bakery|Frozen|Pantry|Snacks|Beverages|Household|Health|Other",
      "quantity": 1.0,
      "unit": "lb" | "oz" | "gal" | "each" | "pack" | null,
      "unit_price": 3.49 or null,
      "total_price": 3.49,
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
- suggested_name should be a clean, human-readable product name (e.g., "BNLS CHKN BRST" → "Chicken Breast, Boneless")
- suggested_category must be one of the listed categories
- If quantity and unit_price are visible, include both
- If only total_price is visible, set unit_price to null and quantity to 1
- If quantity/weight is embedded in the item name (e.g., "3LB" in "BNLS CHKN BRST 3LB"), extract it
- unit should be standardized: lb, oz, gal, qt, pt, each, pack, ct
- Omit non-grocery items (bag fees, bottle deposits) but include tax/total
- Per-item confidence score: 0.95+ for clearly readable, 0.7-0.95 for partially obscured, <0.7 for guesses`
