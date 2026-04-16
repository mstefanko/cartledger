package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
)

// TestBuyAgain_LastPriceAndStore verifies that the BuyAgain response is
// enriched with `last_price` (PRINTF'%.2f') and `last_store_name` drawn from
// the most-recent product_prices row per product (tiebreaker:
// receipt_date DESC, created_at DESC, id DESC).
//
// Setup: one product with three product_price rows across two stores. The
// most recent row is at the second store with unit_price 7.25. BuyAgain
// requires at least two rows per product (HAVING COUNT(*) >= 2) with a
// non-null LEAD gap, which this seed satisfies.
func TestBuyAgain_LastPriceAndStore(t *testing.T) {
	ph, _, cleanup := newTestHandler(t)
	defer cleanup()

	householdID, storeID, receiptID, productID := seedTestData(t, ph)
	d := ph.DB

	// Second store in the same household.
	var storeID2 string
	if err := d.QueryRow(
		"INSERT INTO stores (household_id, name) VALUES (?, 'FreshMart') RETURNING id", householdID,
	).Scan(&storeID2); err != nil {
		t.Fatalf("insert store2: %v", err)
	}

	// Second receipt at store2, later date.
	var receiptID2 string
	if err := d.QueryRow("SELECT lower(hex(randomblob(16)))").Scan(&receiptID2); err != nil {
		t.Fatalf("gen receipt2 id: %v", err)
	}
	if _, err := d.Exec(
		"INSERT INTO receipts (id, household_id, store_id, receipt_date, total, status) VALUES (?, ?, ?, '2024-03-01', '14.50', 'matched')",
		receiptID2, householdID, storeID2,
	); err != nil {
		t.Fatalf("insert receipt2: %v", err)
	}

	// Three price rows. The most recent (2024-03-01 @ FreshMart, 7.25) is what
	// last_price/last_store_name should surface.
	if _, err := d.Exec(
		"INSERT INTO product_prices (product_id, store_id, receipt_id, receipt_date, quantity, unit, unit_price) VALUES (?, ?, ?, '2024-01-01', '1', 'each', '5.00')",
		productID, storeID, receiptID,
	); err != nil {
		t.Fatalf("insert price1: %v", err)
	}
	if _, err := d.Exec(
		"INSERT INTO product_prices (product_id, store_id, receipt_id, receipt_date, quantity, unit, unit_price) VALUES (?, ?, ?, '2024-02-01', '1', 'each', '6.00')",
		productID, storeID, receiptID,
	); err != nil {
		t.Fatalf("insert price2: %v", err)
	}
	if _, err := d.Exec(
		"INSERT INTO product_prices (product_id, store_id, receipt_id, receipt_date, quantity, unit, unit_price) VALUES (?, ?, ?, '2024-03-01', '1', 'each', '7.25')",
		productID, storeID2, receiptID2,
	); err != nil {
		t.Fatalf("insert price3: %v", err)
	}

	ah := &AnalyticsHandler{DB: d, Cfg: ph.Cfg}

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/buy-again", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set(auth.ContextKeyHouseholdID, householdID)
	if err := ah.BuyAgain(c); err != nil {
		t.Fatalf("BuyAgain error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var items []buyAgainItem
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d: %s", len(items), rec.Body.String())
	}
	got := items[0]
	if got.ProductID != productID {
		t.Errorf("product_id: expected %q, got %q", productID, got.ProductID)
	}
	if got.LastPrice == nil {
		t.Fatalf("expected last_price to be populated, got nil")
	}
	if *got.LastPrice != "7.25" {
		t.Errorf("last_price: expected %q, got %q", "7.25", *got.LastPrice)
	}
	if got.LastStoreName == nil {
		t.Fatalf("expected last_store_name to be populated, got nil")
	}
	if *got.LastStoreName != "FreshMart" {
		t.Errorf("last_store_name: expected %q, got %q", "FreshMart", *got.LastStoreName)
	}
}
