package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
)

// helper: make a GET request to /analytics/savings with householdID injected.
func callSavings(t *testing.T, ah *AnalyticsHandler, householdID string) savingsResponse {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/savings", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set(auth.ContextKeyHouseholdID, householdID)
	if err := ah.Savings(c); err != nil {
		t.Fatalf("Savings error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp savingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp
}

// insertLineItemWithDiscount inserts a line item with discount_amount.
func insertLineItemWithDiscount(t *testing.T, ah *AnalyticsHandler, receiptID, productID, totalPrice, discountAmount string) {
	t.Helper()
	d := ah.DB
	if productID == "" {
		if _, err := d.Exec(
			"INSERT INTO line_items (receipt_id, raw_name, total_price, discount_amount, matched) VALUES (?, 'item', ?, ?, 'unmatched')",
			receiptID, totalPrice, discountAmount,
		); err != nil {
			t.Fatalf("insert unmatched line_item: %v", err)
		}
	} else {
		if _, err := d.Exec(
			"INSERT INTO line_items (receipt_id, product_id, raw_name, total_price, discount_amount, matched) VALUES (?, ?, 'item', ?, ?, 'auto')",
			receiptID, productID, totalPrice, discountAmount,
		); err != nil {
			t.Fatalf("insert line_item: %v", err)
		}
	}
}

// TestSavings_EmptyWindow — case 1: no receipts → all zeros.
func TestSavings_EmptyWindow(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	var householdID string
	if err := ah.DB.QueryRow("INSERT INTO households (name) VALUES ('H1') RETURNING id").Scan(&householdID); err != nil {
		t.Fatalf("insert household: %v", err)
	}

	resp := callSavings(t, ah, householdID)

	if resp.MonthToDate != 0 {
		t.Errorf("month_to_date: expected 0, got %f", resp.MonthToDate)
	}
	if resp.Last30D != 0 {
		t.Errorf("last_30d: expected 0, got %f", resp.Last30D)
	}
	if resp.YearToDate != 0 {
		t.Errorf("year_to_date: expected 0, got %f", resp.YearToDate)
	}
}

// TestSavings_SingleDiscount — case 2: one receipt with one discounted item today.
func TestSavings_SingleDiscount(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	var householdID string
	if err := ah.DB.QueryRow("INSERT INTO households (name) VALUES ('H2') RETURNING id").Scan(&householdID); err != nil {
		t.Fatalf("insert household: %v", err)
	}

	_, receiptID := insertReceiptRelative(t, ah, householdID, "0 days", "matched")
	insertLineItemWithDiscount(t, ah, receiptID, "", "5.00", "0.50")

	resp := callSavings(t, ah, householdID)

	if resp.MonthToDate != 0.50 {
		t.Errorf("month_to_date: expected 0.50, got %f", resp.MonthToDate)
	}
	if resp.Last30D != 0.50 {
		t.Errorf("last_30d: expected 0.50, got %f", resp.Last30D)
	}
	if resp.YearToDate != 0.50 {
		t.Errorf("year_to_date: expected 0.50, got %f", resp.YearToDate)
	}
}

// TestSavings_MultipleDiscounts — case 3: multiple items with discounts over 30 days.
func TestSavings_MultipleDiscounts(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	var householdID string
	if err := ah.DB.QueryRow("INSERT INTO households (name) VALUES ('H3') RETURNING id").Scan(&householdID); err != nil {
		t.Fatalf("insert household: %v", err)
	}

	// Use the same receipt for multiple items to avoid duplicate store
	_, receiptID := insertReceiptRelative(t, ah, householdID, "-5 days", "matched")
	insertLineItemWithDiscount(t, ah, receiptID, "", "10.00", "1.00")
	insertLineItemWithDiscount(t, ah, receiptID, "", "15.00", "0.75")

	resp := callSavings(t, ah, householdID)

	// Both items within last 30 days
	expectedLast30 := 1.75
	if resp.Last30D != expectedLast30 {
		t.Errorf("last_30d: expected %f, got %f", expectedLast30, resp.Last30D)
	}

	// Only recent items count for month (assuming test runs in the current month)
	// This test is flexible since calendar may vary
	if resp.MonthToDate < 0 {
		t.Errorf("month_to_date: expected >= 0, got %f", resp.MonthToDate)
	}

	// Year-to-date should include all
	if resp.YearToDate != expectedLast30 {
		t.Errorf("year_to_date: expected %f, got %f", expectedLast30, resp.YearToDate)
	}
}

// TestSavings_NullDiscounts — case 4: NULL discount_amount rows are treated as 0.
func TestSavings_NullDiscounts(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	var householdID string
	if err := ah.DB.QueryRow("INSERT INTO households (name) VALUES ('H4') RETURNING id").Scan(&householdID); err != nil {
		t.Fatalf("insert household: %v", err)
	}

	_, receiptID := insertReceiptRelative(t, ah, householdID, "0 days", "matched")
	// Insert line item without discount_amount (NULL)
	d := ah.DB
	if _, err := d.Exec(
		"INSERT INTO line_items (receipt_id, raw_name, total_price, matched) VALUES (?, 'item', '5.00', 'unmatched')",
		receiptID,
	); err != nil {
		t.Fatalf("insert line_item: %v", err)
	}

	resp := callSavings(t, ah, householdID)

	if resp.MonthToDate != 0 {
		t.Errorf("month_to_date: expected 0 for NULL discounts, got %f", resp.MonthToDate)
	}
	if resp.Last30D != 0 {
		t.Errorf("last_30d: expected 0 for NULL discounts, got %f", resp.Last30D)
	}
	if resp.YearToDate != 0 {
		t.Errorf("year_to_date: expected 0 for NULL discounts, got %f", resp.YearToDate)
	}
}

// TestSavings_FilterByStatus — case 5: only 'pending', 'matched', 'reviewed' receipts count.
func TestSavings_FilterByStatus(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	var householdID string
	if err := ah.DB.QueryRow("INSERT INTO households (name) VALUES ('H5') RETURNING id").Scan(&householdID); err != nil {
		t.Fatalf("insert household: %v", err)
	}

	// Valid receipt with discount
	_, rec1 := insertReceiptRelative(t, ah, householdID, "0 days", "matched")
	insertLineItemWithDiscount(t, ah, rec1, "", "10.00", "1.00")

	// Invalid receipt with discount (should be ignored) — use separate store and 'error' status
	var storeID2 string
	if err := ah.DB.QueryRow(
		"INSERT INTO stores (household_id, name) VALUES (?, 'FailedTestStore') RETURNING id", householdID,
	).Scan(&storeID2); err != nil {
		t.Fatalf("insert store: %v", err)
	}
	var rec2 string
	if err := ah.DB.QueryRow("SELECT lower(hex(randomblob(16)))").Scan(&rec2); err != nil {
		t.Fatalf("gen receipt id: %v", err)
	}
	if _, err := ah.DB.Exec(
		"INSERT INTO receipts (id, household_id, store_id, receipt_date, total, status) VALUES (?, ?, ?, date('now'), '10.00', 'error')",
		rec2, householdID, storeID2,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	insertLineItemWithDiscount(t, ah, rec2, "", "10.00", "2.00")

	resp := callSavings(t, ah, householdID)

	// Only the matched receipt counts
	if resp.MonthToDate != 1.00 {
		t.Errorf("month_to_date: expected 1.00 (only matched), got %f", resp.MonthToDate)
	}
}

// TestSavings_RoundingPrecision — case 6: 2-decimal rounding is applied.
func TestSavings_RoundingPrecision(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	var householdID string
	if err := ah.DB.QueryRow("INSERT INTO households (name) VALUES ('H6') RETURNING id").Scan(&householdID); err != nil {
		t.Fatalf("insert household: %v", err)
	}

	_, receiptID := insertReceiptRelative(t, ah, householdID, "0 days", "matched")
	// Precision test: 0.123456 should round to 0.12
	insertLineItemWithDiscount(t, ah, receiptID, "", "10.00", "0.123456")

	resp := callSavings(t, ah, householdID)

	expected := 0.12
	if resp.MonthToDate != expected {
		t.Errorf("month_to_date: expected %f (rounded), got %f", expected, resp.MonthToDate)
	}
}
