package worker

import (
	"testing"

	"github.com/mstefanko/cartledger/internal/llm"
)

func TestNormalizeExtractedItems_AttachesCostcoCouponToReferencedItem(t *testing.T) {
	items := []llm.ExtractedItem{
		{RawName: "1005641 CHOBANI YGRT", SuggestedName: "Chobani Yogurt", Quantity: 1, Unit: strPtr("each"), TotalPrice: 14.99, LineNumber: 3, Confidence: 0.95},
		{RawName: "000343232/1005641", SuggestedName: "Discount/Coupon", Quantity: 1, Unit: strPtr("each"), TotalPrice: -5, LineNumber: 4, Confidence: 0.90},
	}

	got := normalizeExtractedItems(items)
	if len(got) != 1 {
		t.Fatalf("len(normalized) = %d, want 1", len(got))
	}
	if got[0].RegularPrice == nil || *got[0].RegularPrice != 14.99 {
		t.Fatalf("regular_price = %v, want 14.99", got[0].RegularPrice)
	}
	if got[0].DiscountAmount == nil || *got[0].DiscountAmount != 5 {
		t.Fatalf("discount_amount = %v, want 5", got[0].DiscountAmount)
	}
	if !nearlyEqual(got[0].TotalPrice, 9.99) {
		t.Fatalf("total_price = %v, want 9.99", got[0].TotalPrice)
	}
}

func TestNormalizeExtractedItems_MergesAdjacentCountableDuplicates(t *testing.T) {
	items := []llm.ExtractedItem{
		{RawName: "47826 GREEN GRAPES", SuggestedName: "Green Grapes", Quantity: 1, Unit: strPtr("each"), TotalPrice: 6.39, LineNumber: 10, Confidence: 0.95},
		{RawName: "47826 GREEN GRAPES", SuggestedName: "Green Grapes", Quantity: 1, Unit: strPtr("each"), TotalPrice: 6.39, LineNumber: 11, Confidence: 0.95},
	}

	got := normalizeExtractedItems(items)
	if len(got) != 1 {
		t.Fatalf("len(normalized) = %d, want 1", len(got))
	}
	if got[0].Quantity != 2 {
		t.Fatalf("quantity = %v, want 2", got[0].Quantity)
	}
	if !nearlyEqual(got[0].TotalPrice, 12.78) {
		t.Fatalf("total_price = %v, want 12.78", got[0].TotalPrice)
	}
	if got[0].CountContribution != 2 {
		t.Fatalf("count_contribution = %v, want 2", got[0].CountContribution)
	}
}

func TestNormalizeExtractedItems_DoesNotMergeWeightedRows(t *testing.T) {
	items := []llm.ExtractedItem{
		{RawName: "22967 ORGNC BS THG", Quantity: 1.24, Unit: strPtr("lb"), TotalPrice: 12.40, LineNumber: 1, Confidence: 0.95},
		{RawName: "22967 ORGNC BS THG", Quantity: 1.31, Unit: strPtr("lb"), TotalPrice: 13.10, LineNumber: 2, Confidence: 0.95},
	}

	got := normalizeExtractedItems(items)
	if len(got) != 2 {
		t.Fatalf("len(normalized) = %d, want 2", len(got))
	}
}

func strPtr(s string) *string {
	return &s
}
