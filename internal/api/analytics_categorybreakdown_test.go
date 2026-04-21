package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
)

// helper: create an AnalyticsHandler backed by the shared test DB.
func newAnalyticsHandler(t *testing.T) (*AnalyticsHandler, func()) {
	t.Helper()
	ph, _, cleanup := newTestHandler(t)
	return &AnalyticsHandler{DB: ph.DB, Cfg: ph.Cfg}, cleanup
}

// helper: make a GET request to /analytics/category-breakdown with householdID injected.
func callCategoryBreakdown(t *testing.T, ah *AnalyticsHandler, householdID string) categoryBreakdownResponse {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/category-breakdown", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set(auth.ContextKeyHouseholdID, householdID)
	if err := ah.CategoryBreakdown(c); err != nil {
		t.Fatalf("CategoryBreakdown error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp categoryBreakdownResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp
}

// insertReceiptRelative inserts a receipt at `date('now', offset)` (e.g. "-1 days").
func insertReceiptRelative(t *testing.T, ah *AnalyticsHandler, householdID, daysOffset, status string) (storeID, receiptID string) {
	t.Helper()
	d := ah.DB
	if err := d.QueryRow(
		"INSERT INTO stores (household_id, name) VALUES (?, 'TestStore') RETURNING id", householdID,
	).Scan(&storeID); err != nil {
		t.Fatalf("insert store: %v", err)
	}
	if err := d.QueryRow("SELECT lower(hex(randomblob(16)))").Scan(&receiptID); err != nil {
		t.Fatalf("gen receipt id: %v", err)
	}
	var receiptDate string
	if err := d.QueryRow("SELECT date('now', ?)", daysOffset).Scan(&receiptDate); err != nil {
		t.Fatalf("compute date: %v", err)
	}
	if _, err := d.Exec(
		"INSERT INTO receipts (id, household_id, store_id, receipt_date, total, status) VALUES (?, ?, ?, ?, '10.00', ?)",
		receiptID, householdID, storeID, receiptDate, status,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	return
}

// insertProductNamed inserts a product with the given name and category.
func insertProductNamed(t *testing.T, ah *AnalyticsHandler, householdID, name, category string) string {
	t.Helper()
	var productID string
	if err := ah.DB.QueryRow(
		"INSERT INTO products (household_id, name, category) VALUES (?, ?, ?) RETURNING id",
		householdID, name, category,
	).Scan(&productID); err != nil {
		t.Fatalf("insert product %q: %v", name, err)
	}
	return productID
}

// insertLineItemForReceipt inserts a line item. Pass empty productID for unmatched.
func insertLineItemForReceipt(t *testing.T, ah *AnalyticsHandler, receiptID, productID, totalPrice string) {
	t.Helper()
	d := ah.DB
	if productID == "" {
		if _, err := d.Exec(
			"INSERT INTO line_items (receipt_id, raw_name, total_price, matched) VALUES (?, 'item', ?, 'unmatched')",
			receiptID, totalPrice,
		); err != nil {
			t.Fatalf("insert unmatched line_item: %v", err)
		}
	} else {
		if _, err := d.Exec(
			"INSERT INTO line_items (receipt_id, product_id, raw_name, total_price, matched) VALUES (?, ?, 'item', ?, 'auto')",
			receiptID, productID, totalPrice,
		); err != nil {
			t.Fatalf("insert line_item: %v", err)
		}
	}
}

// TestCategoryBreakdown_EmptyWindow — case 1: no receipts → empty categories, total=0.
func TestCategoryBreakdown_EmptyWindow(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	var householdID string
	if err := ah.DB.QueryRow("INSERT INTO households (name) VALUES ('H1') RETURNING id").Scan(&householdID); err != nil {
		t.Fatalf("insert household: %v", err)
	}

	resp := callCategoryBreakdown(t, ah, householdID)

	if resp.WindowDays != 30 {
		t.Errorf("window_days: expected 30, got %d", resp.WindowDays)
	}
	if resp.Total != 0 {
		t.Errorf("total: expected 0, got %f", resp.Total)
	}
	if len(resp.Categories) != 0 {
		t.Errorf("categories: expected empty, got %v", resp.Categories)
	}
}

// TestCategoryBreakdown_TwoItemsSameCategory — case 2: two line items in "Dairy" → single bucket.
func TestCategoryBreakdown_TwoItemsSameCategory(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	var householdID string
	if err := ah.DB.QueryRow("INSERT INTO households (name) VALUES ('H2') RETURNING id").Scan(&householdID); err != nil {
		t.Fatalf("insert household: %v", err)
	}

	_, receiptID := insertReceiptRelative(t, ah, householdID, "-1 days", "matched")
	productID := insertProductNamed(t, ah, householdID, "Milk", "Dairy")
	insertLineItemForReceipt(t, ah, receiptID, productID, "3.50")
	insertLineItemForReceipt(t, ah, receiptID, productID, "4.50")

	resp := callCategoryBreakdown(t, ah, householdID)

	if len(resp.Categories) != 1 {
		t.Fatalf("expected 1 category, got %d: %v", len(resp.Categories), resp.Categories)
	}
	if resp.Categories[0].Name != "Dairy" {
		t.Errorf("name: expected Dairy, got %q", resp.Categories[0].Name)
	}
	if resp.Categories[0].Current != 8.00 {
		t.Errorf("current: expected 8.00, got %f", resp.Categories[0].Current)
	}
	if resp.Total != 8.00 {
		t.Errorf("total: expected 8.00, got %f", resp.Total)
	}
}

// TestCategoryBreakdown_NullProductID — case 3: line item with NULL product_id → "Unmatched".
func TestCategoryBreakdown_NullProductID(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	var householdID string
	if err := ah.DB.QueryRow("INSERT INTO households (name) VALUES ('H3') RETURNING id").Scan(&householdID); err != nil {
		t.Fatalf("insert household: %v", err)
	}

	_, receiptID := insertReceiptRelative(t, ah, householdID, "-1 days", "matched")
	insertLineItemForReceipt(t, ah, receiptID, "", "5.00")

	resp := callCategoryBreakdown(t, ah, householdID)

	if len(resp.Categories) != 1 {
		t.Fatalf("expected 1 category, got %d", len(resp.Categories))
	}
	if resp.Categories[0].Name != "Unmatched" {
		t.Errorf("expected Unmatched, got %q", resp.Categories[0].Name)
	}
}

// TestCategoryBreakdown_NullCategory — case 4: matched product with empty category → "Uncategorized".
func TestCategoryBreakdown_NullCategory(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	var householdID string
	if err := ah.DB.QueryRow("INSERT INTO households (name) VALUES ('H4') RETURNING id").Scan(&householdID); err != nil {
		t.Fatalf("insert household: %v", err)
	}

	_, receiptID := insertReceiptRelative(t, ah, householdID, "-1 days", "matched")
	productID := insertProductNamed(t, ah, householdID, "Mystery Item", "")
	insertLineItemForReceipt(t, ah, receiptID, productID, "7.00")

	resp := callCategoryBreakdown(t, ah, householdID)

	if len(resp.Categories) != 1 {
		t.Fatalf("expected 1 category, got %d", len(resp.Categories))
	}
	if resp.Categories[0].Name != "Uncategorized" {
		t.Errorf("expected Uncategorized, got %q", resp.Categories[0].Name)
	}
}

// TestCategoryBreakdown_MixedBuckets — case 5: Dairy $40, Produce $25, Unmatched $10.
func TestCategoryBreakdown_MixedBuckets(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	var householdID string
	if err := ah.DB.QueryRow("INSERT INTO households (name) VALUES ('H5') RETURNING id").Scan(&householdID); err != nil {
		t.Fatalf("insert household: %v", err)
	}

	_, receiptID := insertReceiptRelative(t, ah, householdID, "-1 days", "matched")
	dairyID := insertProductNamed(t, ah, householdID, "Milk H5", "Dairy")
	produceID := insertProductNamed(t, ah, householdID, "Apple H5", "Produce")
	insertLineItemForReceipt(t, ah, receiptID, dairyID, "40.00")
	insertLineItemForReceipt(t, ah, receiptID, produceID, "25.00")
	insertLineItemForReceipt(t, ah, receiptID, "", "10.00")

	resp := callCategoryBreakdown(t, ah, householdID)

	if len(resp.Categories) != 3 {
		t.Fatalf("expected 3 categories, got %d: %v", len(resp.Categories), resp.Categories)
	}
	if resp.Total != 75.00 {
		t.Errorf("total: expected 75.00, got %f", resp.Total)
	}

	var pctSum float64
	for _, cat := range resp.Categories {
		pctSum += cat.PctOfTotal
	}
	if pctSum < 99.99 || pctSum > 100.01 {
		t.Errorf("pct_of_total sum: expected ~100, got %f", pctSum)
	}

	// amount DESC: Dairy(40) > Produce(25) > Unmatched(10)
	if resp.Categories[0].Name != "Dairy" {
		t.Errorf("first bucket: expected Dairy, got %q", resp.Categories[0].Name)
	}
}

// TestCategoryBreakdown_PriorWindowOnly — case 6: prior-window spending, current empty → current=0, delta=-100%.
func TestCategoryBreakdown_PriorWindowOnly(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	var householdID string
	if err := ah.DB.QueryRow("INSERT INTO households (name) VALUES ('H6') RETURNING id").Scan(&householdID); err != nil {
		t.Fatalf("insert household: %v", err)
	}

	// Receipt in prior window: 45 days ago = in [now-60d, now-30d)
	_, receiptID := insertReceiptRelative(t, ah, householdID, "-45 days", "matched")
	productID := insertProductNamed(t, ah, householdID, "Cheese", "Dairy")
	insertLineItemForReceipt(t, ah, receiptID, productID, "20.00")

	resp := callCategoryBreakdown(t, ah, householdID)

	if len(resp.Categories) != 1 {
		t.Fatalf("expected 1 category (from prior), got %d: %v", len(resp.Categories), resp.Categories)
	}
	cat := resp.Categories[0]
	if cat.Current != 0 {
		t.Errorf("current: expected 0, got %f", cat.Current)
	}
	if cat.Prior != 20.00 {
		t.Errorf("prior: expected 20.00, got %f", cat.Prior)
	}
	if cat.DeltaPct == nil {
		t.Fatalf("delta_pct: expected non-nil for prior>0 case")
	}
	if *cat.DeltaPct != -100.00 {
		t.Errorf("delta_pct: expected -100.00, got %f", *cat.DeltaPct)
	}
}

// TestCategoryBreakdown_OutsideWindow — case 7: receipt > 60 days ago not counted.
func TestCategoryBreakdown_OutsideWindow(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	var householdID string
	if err := ah.DB.QueryRow("INSERT INTO households (name) VALUES ('H7') RETURNING id").Scan(&householdID); err != nil {
		t.Fatalf("insert household: %v", err)
	}

	// 90 days ago — outside both current and prior windows
	_, receiptID := insertReceiptRelative(t, ah, householdID, "-90 days", "matched")
	productID := insertProductNamed(t, ah, householdID, "OldItem", "Dairy")
	insertLineItemForReceipt(t, ah, receiptID, productID, "30.00")

	resp := callCategoryBreakdown(t, ah, householdID)

	if len(resp.Categories) != 0 {
		t.Errorf("expected no categories for very old receipt, got %d", len(resp.Categories))
	}
	if resp.Total != 0 {
		t.Errorf("total: expected 0, got %f", resp.Total)
	}
}

// TestCategoryBreakdown_HouseholdScoping — case 8: two households, each sees only its own totals.
func TestCategoryBreakdown_HouseholdScoping(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	var h1, h2 string
	if err := ah.DB.QueryRow("INSERT INTO households (name) VALUES ('H8a') RETURNING id").Scan(&h1); err != nil {
		t.Fatalf("insert h1: %v", err)
	}
	if err := ah.DB.QueryRow("INSERT INTO households (name) VALUES ('H8b') RETURNING id").Scan(&h2); err != nil {
		t.Fatalf("insert h2: %v", err)
	}

	_, r1 := insertReceiptRelative(t, ah, h1, "-1 days", "matched")
	p1 := insertProductNamed(t, ah, h1, "Milk", "Dairy")
	insertLineItemForReceipt(t, ah, r1, p1, "50.00")

	_, r2 := insertReceiptRelative(t, ah, h2, "-1 days", "matched")
	p2 := insertProductNamed(t, ah, h2, "Milk", "Dairy")
	insertLineItemForReceipt(t, ah, r2, p2, "30.00")

	resp1 := callCategoryBreakdown(t, ah, h1)
	resp2 := callCategoryBreakdown(t, ah, h2)

	if resp1.Total != 50.00 {
		t.Errorf("h1 total: expected 50.00, got %f", resp1.Total)
	}
	if resp2.Total != 30.00 {
		t.Errorf("h2 total: expected 30.00, got %f", resp2.Total)
	}
}
