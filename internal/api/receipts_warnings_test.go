package api

import "testing"

func TestReceiptReviewWarningsItemCountMismatch(t *testing.T) {
	expected := 17
	accounted, warnings := receiptReviewWarnings(&expected, []lineItemResponse{
		{CountContribution: "2"},
		{CountContribution: "14"},
	})

	if accounted != "16" {
		t.Fatalf("accounted = %q, want 16", accounted)
	}
	if len(warnings) != 1 {
		t.Fatalf("len(warnings) = %d, want 1", len(warnings))
	}
	if warnings[0].Code != "item_count_mismatch" {
		t.Fatalf("warning code = %q, want item_count_mismatch", warnings[0].Code)
	}
}

func TestReceiptReviewWarningsItemCountMatch(t *testing.T) {
	expected := 3
	accounted, warnings := receiptReviewWarnings(&expected, []lineItemResponse{
		{CountContribution: "2"},
		{CountContribution: "1"},
	})

	if accounted != "3" {
		t.Fatalf("accounted = %q, want 3", accounted)
	}
	if len(warnings) != 0 {
		t.Fatalf("len(warnings) = %d, want 0", len(warnings))
	}
}
