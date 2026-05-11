package llm

import (
	"encoding/json"
	"testing"
)

func TestReceiptExtractionUnmarshalAcceptsQuotedNumbers(t *testing.T) {
	raw := []byte(`{
		"store_name": "Costco Wholesale",
		"store_address": null,
		"store_city": null,
		"store_state": null,
		"store_zip": null,
		"store_number": "749",
		"date": "2026-04-20",
		"payment_card_type": null,
		"payment_card_last4": null,
		"payment_card_raw": null,
		"time": null,
		"items_sold_count": 23,
		"items": [{
			"raw_name": "1407506 LC SOFT TACO",
			"suggested_name": "Soft Taco Shells",
			"suggested_brand": "",
			"suggested_tags": "taco,shells",
			"suggested_category": "Pantry",
			"quantity": "1",
			"unit": "each",
			"unit_price": "$6.99",
			"total_price": "6.99",
			"regular_price": null,
			"discount_amount": "3.00-",
			"count_contribution": "1",
			"line_number": 1,
			"confidence": "0.91"
		}],
		"subtotal": "$183.97",
		"tax": "0.33",
		"total": "184.30",
		"confidence": "0.88"
	}`)

	var got ReceiptExtraction
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if got.Subtotal != 183.97 || got.Tax != 0.33 || got.Total != 184.30 || got.Confidence != 0.88 {
		t.Fatalf("totals = subtotal %.2f tax %.2f total %.2f confidence %.2f", got.Subtotal, got.Tax, got.Total, got.Confidence)
	}
	if len(got.Items) != 1 {
		t.Fatalf("len(Items) = %d, want 1", len(got.Items))
	}
	item := got.Items[0]
	if item.Quantity != 1 || item.TotalPrice != 6.99 || item.CountContribution != 1 || item.Confidence != 0.91 {
		t.Fatalf("item numeric fields = %+v", item)
	}
	if item.UnitPrice == nil || *item.UnitPrice != 6.99 {
		t.Fatalf("UnitPrice = %v, want 6.99", item.UnitPrice)
	}
	if item.DiscountAmount == nil || *item.DiscountAmount != -3 {
		t.Fatalf("DiscountAmount = %v, want -3", item.DiscountAmount)
	}
}
