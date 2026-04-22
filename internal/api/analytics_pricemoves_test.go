package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
)

// callPriceMoves invokes GET /analytics/price-moves with a fixed household id.
func callPriceMoves(t *testing.T, ah *AnalyticsHandler, householdID string) priceMovesResponse {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/price-moves", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set(auth.ContextKeyHouseholdID, householdID)
	if err := ah.PriceMoves(c); err != nil {
		t.Fatalf("PriceMoves error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp priceMovesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp
}

// newPMHousehold creates a household + store and returns (householdID, storeID).
func newPMHousehold(t *testing.T, ah *AnalyticsHandler, name string) (string, string) {
	t.Helper()
	d := ah.DB
	var householdID string
	if err := d.QueryRow("INSERT INTO households (name) VALUES (?) RETURNING id", name).Scan(&householdID); err != nil {
		t.Fatalf("insert household: %v", err)
	}
	var storeID string
	if err := d.QueryRow(
		"INSERT INTO stores (household_id, name) VALUES (?, 'PMStore') RETURNING id", householdID,
	).Scan(&storeID); err != nil {
		t.Fatalf("insert store: %v", err)
	}
	return householdID, storeID
}

// insertPMProduct creates a product for price-moves tests.
func insertPMProduct(t *testing.T, ah *AnalyticsHandler, householdID, name string) string {
	t.Helper()
	var productID string
	if err := ah.DB.QueryRow(
		"INSERT INTO products (household_id, name) VALUES (?, ?) RETURNING id",
		householdID, name,
	).Scan(&productID); err != nil {
		t.Fatalf("insert product %q: %v", name, err)
	}
	return productID
}

// insertPMPrice inserts a product_prices row at a specific date offset with a
// normalized_price value, using unit "each" by default.
func insertPMPrice(t *testing.T, ah *AnalyticsHandler, householdID, storeID, productID, daysOffset, normalizedPrice, unit string) {
	t.Helper()
	d := ah.DB

	var receiptID string
	if err := d.QueryRow("SELECT lower(hex(randomblob(16)))").Scan(&receiptID); err != nil {
		t.Fatalf("gen receipt id: %v", err)
	}
	var receiptDate string
	if err := d.QueryRow("SELECT date('now', ?)", daysOffset).Scan(&receiptDate); err != nil {
		t.Fatalf("compute date: %v", err)
	}
	if _, err := d.Exec(
		"INSERT INTO receipts (id, household_id, store_id, receipt_date, total, status) VALUES (?, ?, ?, ?, '10.00', 'matched')",
		receiptID, householdID, storeID, receiptDate,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	if _, err := d.Exec(
		`INSERT INTO product_prices (product_id, store_id, receipt_id, receipt_date, quantity, unit, unit_price, normalized_price)
		 VALUES (?, ?, ?, ?, '1', ?, ?, ?)`,
		productID, storeID, receiptID, receiptDate, unit, normalizedPrice, normalizedPrice,
	); err != nil {
		t.Fatalf("insert product_prices: %v", err)
	}
}

// insertPMPriceNullNorm inserts a product_prices row with normalized_price = NULL
// and the provided unit_price. Used to test COALESCE fallback behaviour.
func insertPMPriceNullNorm(t *testing.T, ah *AnalyticsHandler, householdID, storeID, productID string, daysOffset int, unitPrice, unit string) {
	t.Helper()
	d := ah.DB

	var receiptID string
	if err := d.QueryRow("SELECT lower(hex(randomblob(16)))").Scan(&receiptID); err != nil {
		t.Fatalf("gen receipt id: %v", err)
	}
	offset := fmt.Sprintf("%d days", daysOffset)
	var receiptDate string
	if err := d.QueryRow("SELECT date('now', ?)", offset).Scan(&receiptDate); err != nil {
		t.Fatalf("compute date: %v", err)
	}
	if _, err := d.Exec(
		"INSERT INTO receipts (id, household_id, store_id, receipt_date, total, status) VALUES (?, ?, ?, ?, '10.00', 'matched')",
		receiptID, householdID, storeID, receiptDate,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	if _, err := d.Exec(
		`INSERT INTO product_prices (product_id, store_id, receipt_id, receipt_date, quantity, unit, unit_price, normalized_price)
		 VALUES (?, ?, ?, ?, '1', ?, ?, NULL)`,
		productID, storeID, receiptID, receiptDate, unit, unitPrice,
	); err != nil {
		t.Fatalf("insert product_prices: %v", err)
	}
}

// findPMItem returns the priceMoveItem matching productID from a slice, or nil.
func findPMItem(items []priceMoveItem, productID string) *priceMoveItem {
	for i := range items {
		if items[i].ProductID == productID {
			return &items[i]
		}
	}
	return nil
}

// Case 1: price went up — 3 recent @ $2.00, 3 prior @ $1.00 → up list, pct_change=100.
func TestPriceMoves_PriceUp(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newPMHousehold(t, ah, "PM1")
	productID := insertPMProduct(t, ah, householdID, "Butter")

	insertPMPrice(t, ah, householdID, storeID, productID, "-5 days", "2.00", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-10 days", "2.00", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-15 days", "2.00", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-40 days", "1.00", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-50 days", "1.00", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-60 days", "1.00", "each")

	resp := callPriceMoves(t, ah, householdID)

	it := findPMItem(resp.Up, productID)
	if it == nil {
		t.Fatalf("expected product in Up list; Up=%v Down=%v", resp.Up, resp.Down)
	}
	if it.PctChange != 100.00 {
		t.Errorf("pct_change: expected 100.00, got %f", it.PctChange)
	}
	if findPMItem(resp.Down, productID) != nil {
		t.Error("product should not appear in Down list")
	}
}

// Case 2: price went down — 3 recent @ $1.00, 3 prior @ $2.00 → down list, pct_change=-50.
func TestPriceMoves_PriceDown(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newPMHousehold(t, ah, "PM2")
	productID := insertPMProduct(t, ah, householdID, "Cheese")

	insertPMPrice(t, ah, householdID, storeID, productID, "-5 days", "1.00", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-10 days", "1.00", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-15 days", "1.00", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-40 days", "2.00", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-50 days", "2.00", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-60 days", "2.00", "each")

	resp := callPriceMoves(t, ah, householdID)

	it := findPMItem(resp.Down, productID)
	if it == nil {
		t.Fatalf("expected product in Down list; Up=%v Down=%v", resp.Up, resp.Down)
	}
	if it.PctChange != -50.00 {
		t.Errorf("pct_change: expected -50.00, got %f", it.PctChange)
	}
	if findPMItem(resp.Up, productID) != nil {
		t.Error("product should not appear in Up list")
	}
}

// Case 3: no change — 3 recent @ $1.50, 3 prior @ $1.50 → excluded (pct_change=0).
func TestPriceMoves_NoChange(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newPMHousehold(t, ah, "PM3")
	productID := insertPMProduct(t, ah, householdID, "Eggs")

	insertPMPrice(t, ah, householdID, storeID, productID, "-5 days", "1.50", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-10 days", "1.50", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-15 days", "1.50", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-40 days", "1.50", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-50 days", "1.50", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-60 days", "1.50", "each")

	resp := callPriceMoves(t, ah, householdID)

	if findPMItem(resp.Up, productID) != nil || findPMItem(resp.Down, productID) != nil {
		t.Error("zero-change product should be excluded from both lists")
	}
}

// Case 4: missing recent — 0 recent, 3 prior → excluded by HAVING.
func TestPriceMoves_MissingRecent(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newPMHousehold(t, ah, "PM4")
	productID := insertPMProduct(t, ah, householdID, "Olive Oil")

	insertPMPrice(t, ah, householdID, storeID, productID, "-40 days", "5.00", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-50 days", "5.00", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-60 days", "5.00", "each")

	resp := callPriceMoves(t, ah, householdID)

	if findPMItem(resp.Up, productID) != nil || findPMItem(resp.Down, productID) != nil {
		t.Error("no-recent product should be excluded")
	}
}

// Case 5: missing prior — 3 recent, 0 prior → excluded by HAVING.
func TestPriceMoves_MissingPrior(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newPMHousehold(t, ah, "PM5")
	productID := insertPMProduct(t, ah, householdID, "Bread")

	insertPMPrice(t, ah, householdID, storeID, productID, "-5 days", "3.00", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-10 days", "3.00", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-15 days", "3.00", "each")

	resp := callPriceMoves(t, ah, householdID)

	if findPMItem(resp.Up, productID) != nil || findPMItem(resp.Down, productID) != nil {
		t.Error("no-prior product should be excluded")
	}
}

// Case 6: prior avg = 0 — prior @ $0.00 → excluded by avg_prior > 0 guard.
func TestPriceMoves_PriorAvgZero(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newPMHousehold(t, ah, "PM6")
	productID := insertPMProduct(t, ah, householdID, "Sample")

	// normalized_price = 0 is filtered by WHERE normalized_price > 0, so
	// insert a prior-window row with normalized_price NULL to force prior
	// observation count = 0 (effectively, no valid prior data).
	d := ah.DB
	var receiptID string
	if err := d.QueryRow("SELECT lower(hex(randomblob(16)))").Scan(&receiptID); err != nil {
		t.Fatalf("gen id: %v", err)
	}
	var receiptDate string
	if err := d.QueryRow("SELECT date('now', '-45 days')").Scan(&receiptDate); err != nil {
		t.Fatalf("compute date: %v", err)
	}
	if _, err := d.Exec(
		"INSERT INTO receipts (id, household_id, store_id, receipt_date, total, status) VALUES (?, ?, ?, ?, '10.00', 'matched')",
		receiptID, householdID, storeID, receiptDate,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	// Insert prior row with NULL normalized_price — WHERE clause excludes it.
	if _, err := d.Exec(
		`INSERT INTO product_prices (product_id, store_id, receipt_id, receipt_date, quantity, unit, unit_price, normalized_price)
		 VALUES (?, ?, ?, ?, '1', 'each', '0.00', NULL)`,
		productID, storeID, receiptID, receiptDate,
	); err != nil {
		t.Fatalf("insert product_prices: %v", err)
	}

	insertPMPrice(t, ah, householdID, storeID, productID, "-5 days", "3.00", "each")

	resp := callPriceMoves(t, ah, householdID)

	if findPMItem(resp.Up, productID) != nil || findPMItem(resp.Down, productID) != nil {
		t.Error("product with null/zero prior should be excluded")
	}
}

// Case 7: NULL normalized_price falls back to unit_price via COALESCE — product is INCLUDED.
func TestPriceMoves_NullNormalizedPrice(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newPMHousehold(t, ah, "PM7")
	productID := insertPMProduct(t, ah, householdID, "Milk")

	// Recent window: 3 rows at ~2.00 (unit_price, normalized_price=NULL).
	insertPMPriceNullNorm(t, ah, householdID, storeID, productID, -5, "2.00", "each")
	insertPMPriceNullNorm(t, ah, householdID, storeID, productID, -10, "2.00", "each")
	insertPMPriceNullNorm(t, ah, householdID, storeID, productID, -15, "2.00", "each")
	// Prior window: 3 rows at ~1.00 (unit_price, normalized_price=NULL) → expect price UP.
	insertPMPriceNullNorm(t, ah, householdID, storeID, productID, -40, "1.00", "each")
	insertPMPriceNullNorm(t, ah, householdID, storeID, productID, -50, "1.00", "each")
	insertPMPriceNullNorm(t, ah, householdID, storeID, productID, -60, "1.00", "each")

	resp := callPriceMoves(t, ah, householdID)

	item := findPMItem(resp.Up, productID)
	if item == nil {
		t.Fatal("product with null normalized_price but valid unit_price should appear in Up via COALESCE fallback")
	}
	if item.PctChange <= 0 {
		t.Errorf("expected positive pct_change, got %v", item.PctChange)
	}
}

// Case 8: unit mismatch — recent unit='lb', prior unit='each' → excluded.
func TestPriceMoves_UnitMismatch(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newPMHousehold(t, ah, "PM8")
	productID := insertPMProduct(t, ah, householdID, "Flour")

	insertPMPrice(t, ah, householdID, storeID, productID, "-5 days", "2.00", "lb")
	insertPMPrice(t, ah, householdID, storeID, productID, "-10 days", "2.00", "lb")
	insertPMPrice(t, ah, householdID, storeID, productID, "-15 days", "2.00", "lb")
	insertPMPrice(t, ah, householdID, storeID, productID, "-40 days", "1.00", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-50 days", "1.00", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-60 days", "1.00", "each")

	resp := callPriceMoves(t, ah, householdID)

	if findPMItem(resp.Up, productID) != nil || findPMItem(resp.Down, productID) != nil {
		t.Error("unit-mismatch product should be excluded")
	}
}

// Case 9: unit consistent (all 'each') with valid normalized_price → INCLUDED.
func TestPriceMoves_UnitConsistentIncluded(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newPMHousehold(t, ah, "PM9")
	productID := insertPMProduct(t, ah, householdID, "Yogurt")

	insertPMPrice(t, ah, householdID, storeID, productID, "-5 days", "3.00", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-10 days", "3.00", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-15 days", "3.00", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-40 days", "2.00", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-50 days", "2.00", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-60 days", "2.00", "each")

	resp := callPriceMoves(t, ah, householdID)

	it := findPMItem(resp.Up, productID)
	if it == nil {
		t.Fatalf("expected product in Up list; Up=%v Down=%v", resp.Up, resp.Down)
	}
	if it.PctChange != 50.00 {
		t.Errorf("pct_change: expected 50.00, got %f", it.PctChange)
	}
}

// Case 10: top-5 cap — 8 products all going up → only top 5 by |pct_change| returned.
func TestPriceMoves_Top5Cap(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newPMHousehold(t, ah, "PM10")

	// Create 8 products with increasing % changes (10%, 20%, 30%, ..., 80%).
	type entry struct {
		id        string
		pctChange float64
	}
	entries := make([]entry, 8)
	for i := 0; i < 8; i++ {
		pct := float64((i + 1) * 10)
		recentPrice := 1.0 + pct/100.0 // e.g. for 10%: prior=1.00, recent=1.10
		priorPrice := 1.0
		productID := insertPMProduct(t, ah, householdID, "Product"+string(rune('A'+i)))
		// 3 recent rows on distinct dates, 3 prior rows on distinct dates
		insertPMPrice(t, ah, householdID, storeID, productID, "-5 days", formatFloatStr(recentPrice), "each")
		insertPMPrice(t, ah, householdID, storeID, productID, "-6 days", formatFloatStr(recentPrice), "each")
		insertPMPrice(t, ah, householdID, storeID, productID, "-7 days", formatFloatStr(recentPrice), "each")
		insertPMPrice(t, ah, householdID, storeID, productID, "-40 days", formatFloatStr(priorPrice), "each")
		insertPMPrice(t, ah, householdID, storeID, productID, "-41 days", formatFloatStr(priorPrice), "each")
		insertPMPrice(t, ah, householdID, storeID, productID, "-42 days", formatFloatStr(priorPrice), "each")
		entries[i] = entry{id: productID, pctChange: pct}
	}

	resp := callPriceMoves(t, ah, householdID)

	if len(resp.Up) != 5 {
		t.Fatalf("expected 5 items in Up list, got %d", len(resp.Up))
	}
	// Verify the top 5 have the highest |pct_change| (i.e. 80%, 70%, 60%, 50%, 40%).
	expectedTop5Pcts := []float64{80, 70, 60, 50, 40}
	for i, expected := range expectedTop5Pcts {
		if resp.Up[i].PctChange != expected {
			t.Errorf("Up[%d].pct_change: expected %.2f, got %.2f", i, expected, resp.Up[i].PctChange)
		}
	}
	// Items with 10% and 20% change should NOT appear.
	if len(resp.Up) > 5 {
		t.Errorf("should not have more than 5 items in Up list")
	}
}

// formatFloatStr formats a float with 2 decimal places for SQL insertion.
func formatFloatStr(f float64) string {
	return fmt.Sprintf("%.2f", f)
}

// TestPriceMoves_TenPercentThreshold verifies the 10% noise threshold:
// products with |pct_change| < 10% are excluded; those at exactly 10% or above
// are included and sorted by |pct_change| DESC.
func TestPriceMoves_TenPercentThreshold(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newPMHousehold(t, ah, "PMThresh")

	// Product A: prior=1.00, recent=1.09 (+9%) — must NOT appear.
	idA := insertPMProduct(t, ah, householdID, "Product A")
	insertPMPrice(t, ah, householdID, storeID, idA, "-5 days", "1.09", "each")
	insertPMPrice(t, ah, householdID, storeID, idA, "-6 days", "1.09", "each")
	insertPMPrice(t, ah, householdID, storeID, idA, "-7 days", "1.09", "each")
	insertPMPrice(t, ah, householdID, storeID, idA, "-40 days", "1.00", "each")
	insertPMPrice(t, ah, householdID, storeID, idA, "-41 days", "1.00", "each")
	insertPMPrice(t, ah, householdID, storeID, idA, "-42 days", "1.00", "each")

	// Product B: prior=1.00, recent=1.10 (+10%) — must appear in Up.
	idB := insertPMProduct(t, ah, householdID, "Product B")
	insertPMPrice(t, ah, householdID, storeID, idB, "-5 days", "1.10", "each")
	insertPMPrice(t, ah, householdID, storeID, idB, "-6 days", "1.10", "each")
	insertPMPrice(t, ah, householdID, storeID, idB, "-7 days", "1.10", "each")
	insertPMPrice(t, ah, householdID, storeID, idB, "-40 days", "1.00", "each")
	insertPMPrice(t, ah, householdID, storeID, idB, "-41 days", "1.00", "each")
	insertPMPrice(t, ah, householdID, storeID, idB, "-42 days", "1.00", "each")

	// Product C: prior=1.00, recent=1.25 (+25%) — must appear in Up.
	idC := insertPMProduct(t, ah, householdID, "Product C")
	insertPMPrice(t, ah, householdID, storeID, idC, "-5 days", "1.25", "each")
	insertPMPrice(t, ah, householdID, storeID, idC, "-6 days", "1.25", "each")
	insertPMPrice(t, ah, householdID, storeID, idC, "-7 days", "1.25", "each")
	insertPMPrice(t, ah, householdID, storeID, idC, "-40 days", "1.00", "each")
	insertPMPrice(t, ah, householdID, storeID, idC, "-41 days", "1.00", "each")
	insertPMPrice(t, ah, householdID, storeID, idC, "-42 days", "1.00", "each")

	// Product D: prior=1.00, recent=0.91 (-9%) — must NOT appear.
	idD := insertPMProduct(t, ah, householdID, "Product D")
	insertPMPrice(t, ah, householdID, storeID, idD, "-5 days", "0.91", "each")
	insertPMPrice(t, ah, householdID, storeID, idD, "-6 days", "0.91", "each")
	insertPMPrice(t, ah, householdID, storeID, idD, "-7 days", "0.91", "each")
	insertPMPrice(t, ah, householdID, storeID, idD, "-40 days", "1.00", "each")
	insertPMPrice(t, ah, householdID, storeID, idD, "-41 days", "1.00", "each")
	insertPMPrice(t, ah, householdID, storeID, idD, "-42 days", "1.00", "each")

	// Product E: prior=1.00, recent=0.90 (-10%) — must appear in Down.
	idE := insertPMProduct(t, ah, householdID, "Product E")
	insertPMPrice(t, ah, householdID, storeID, idE, "-5 days", "0.90", "each")
	insertPMPrice(t, ah, householdID, storeID, idE, "-6 days", "0.90", "each")
	insertPMPrice(t, ah, householdID, storeID, idE, "-7 days", "0.90", "each")
	insertPMPrice(t, ah, householdID, storeID, idE, "-40 days", "1.00", "each")
	insertPMPrice(t, ah, householdID, storeID, idE, "-41 days", "1.00", "each")
	insertPMPrice(t, ah, householdID, storeID, idE, "-42 days", "1.00", "each")

	// Product F: prior=1.00, recent=0.75 (-25%) — must appear in Down.
	idF := insertPMProduct(t, ah, householdID, "Product F")
	insertPMPrice(t, ah, householdID, storeID, idF, "-5 days", "0.75", "each")
	insertPMPrice(t, ah, householdID, storeID, idF, "-6 days", "0.75", "each")
	insertPMPrice(t, ah, householdID, storeID, idF, "-7 days", "0.75", "each")
	insertPMPrice(t, ah, householdID, storeID, idF, "-40 days", "1.00", "each")
	insertPMPrice(t, ah, householdID, storeID, idF, "-41 days", "1.00", "each")
	insertPMPrice(t, ah, householdID, storeID, idF, "-42 days", "1.00", "each")

	_ = idA
	_ = idD

	resp := callPriceMoves(t, ah, householdID)

	// Up list: exactly 2 items (C at 25%, B at 10%), sorted by |pct_change| DESC.
	if len(resp.Up) != 2 {
		t.Fatalf("expected 2 items in Up list, got %d: %v", len(resp.Up), resp.Up)
	}
	if resp.Up[0].Name != "Product C" {
		t.Errorf("Up[0]: expected 'Product C', got %q", resp.Up[0].Name)
	}
	if resp.Up[1].Name != "Product B" {
		t.Errorf("Up[1]: expected 'Product B', got %q", resp.Up[1].Name)
	}

	// Down list: exactly 2 items (F at -25%, E at -10%), sorted by |pct_change| DESC.
	if len(resp.Down) != 2 {
		t.Fatalf("expected 2 items in Down list, got %d: %v", len(resp.Down), resp.Down)
	}
	if resp.Down[0].Name != "Product F" {
		t.Errorf("Down[0]: expected 'Product F', got %q", resp.Down[0].Name)
	}
	if resp.Down[1].Name != "Product E" {
		t.Errorf("Down[1]: expected 'Product E', got %q", resp.Down[1].Name)
	}

	// Sub-threshold products A and D must not appear anywhere.
	for _, item := range resp.Up {
		if item.Name == "Product A" || item.Name == "Product D" {
			t.Errorf("sub-threshold product %q must not appear in Up list", item.Name)
		}
	}
	for _, item := range resp.Down {
		if item.Name == "Product A" || item.Name == "Product D" {
			t.Errorf("sub-threshold product %q must not appear in Down list", item.Name)
		}
	}
}

// TestPriceMoves_ErrorStatusExcluded — receipt with status='error' should be excluded
// even when it has 3+ product_prices rows with consistent unit and normalized_price.
func TestPriceMoves_ErrorStatusExcluded(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newPMHousehold(t, ah, "PMErr")
	productID := insertPMProduct(t, ah, householdID, "ErrorProduct")

	d := ah.DB
	// Insert 6 receipts with status='error', 3 recent and 3 prior, each on distinct dates.
	type rowSpec struct {
		offset string
		price  string
	}
	specs := []rowSpec{
		{"-5 days", "2.00"}, {"-6 days", "2.00"}, {"-7 days", "2.00"},
		{"-40 days", "1.00"}, {"-41 days", "1.00"}, {"-42 days", "1.00"},
	}
	for _, s := range specs {
		var receiptID string
		if err := d.QueryRow("SELECT lower(hex(randomblob(16)))").Scan(&receiptID); err != nil {
			t.Fatalf("gen receipt id: %v", err)
		}
		var receiptDate string
		if err := d.QueryRow("SELECT date('now', ?)", s.offset).Scan(&receiptDate); err != nil {
			t.Fatalf("compute date: %v", err)
		}
		if _, err := d.Exec(
			"INSERT INTO receipts (id, household_id, store_id, receipt_date, total, status) VALUES (?, ?, ?, ?, '10.00', 'error')",
			receiptID, householdID, storeID, receiptDate,
		); err != nil {
			t.Fatalf("insert receipt: %v", err)
		}
		if _, err := d.Exec(
			`INSERT INTO product_prices (product_id, store_id, receipt_id, receipt_date, quantity, unit, unit_price, normalized_price)
			 VALUES (?, ?, ?, ?, '1', 'each', ?, ?)`,
			productID, storeID, receiptID, receiptDate, s.price, s.price,
		); err != nil {
			t.Fatalf("insert product_prices: %v", err)
		}
	}

	resp := callPriceMoves(t, ah, householdID)

	if findPMItem(resp.Up, productID) != nil || findPMItem(resp.Down, productID) != nil {
		t.Error("product from error-status receipts should be excluded from both lists")
	}
}

// TestPriceMoves_NonProductExcluded — product with is_non_product=1 should be excluded
// even with 3+ rows across both windows.
func TestPriceMoves_NonProductExcluded(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newPMHousehold(t, ah, "PMNonProd")
	// Insert a product with is_non_product=1.
	var productID string
	if err := ah.DB.QueryRow(
		"INSERT INTO products (household_id, name, is_non_product) VALUES (?, 'Tax', 1) RETURNING id",
		householdID,
	).Scan(&productID); err != nil {
		t.Fatalf("insert non-product: %v", err)
	}

	// 3 recent and 3 prior rows on distinct dates.
	insertPMPrice(t, ah, householdID, storeID, productID, "-5 days", "2.00", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-6 days", "2.00", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-7 days", "2.00", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-40 days", "1.00", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-41 days", "1.00", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-42 days", "1.00", "each")

	resp := callPriceMoves(t, ah, householdID)

	if findPMItem(resp.Up, productID) != nil || findPMItem(resp.Down, productID) != nil {
		t.Error("is_non_product=1 product should be excluded from both lists")
	}
}

// TestPriceMoves_TwoDistinctDatesExcluded — product with only 2 distinct receipt dates
// (both in the 90-day window) should be excluded by the HAVING >= 3 distinct dates guard.
func TestPriceMoves_TwoDistinctDatesExcluded(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newPMHousehold(t, ah, "PM2Dates")
	productID := insertPMProduct(t, ah, householdID, "TwoDates")

	// Only 2 distinct dates: one recent, one prior.
	insertPMPrice(t, ah, householdID, storeID, productID, "-5 days", "2.00", "each")
	insertPMPrice(t, ah, householdID, storeID, productID, "-40 days", "1.00", "each")

	resp := callPriceMoves(t, ah, householdID)

	if findPMItem(resp.Up, productID) != nil || findPMItem(resp.Down, productID) != nil {
		t.Error("product with only 2 distinct receipt dates should be excluded")
	}
}

// TestPriceMoves_MixedNullNonNull — a single product with 4 rows mixing NULL and non-NULL
// normalized_price. COALESCE should blend unit_price for NULL rows with normalized_price
// for non-NULL rows, producing a correct blended average and appearing in the result.
//
// Seed (all unit "each"):
//
//	Prior window (~30-90 days ago): row A normalized=1.00, row B normalized=NULL/unit_price=1.50
//	  → blended prior avg = (1.00 + 1.50) / 2 = 1.25
//	Recent window (~0-30 days ago): row C normalized=2.00, row D normalized=NULL/unit_price=2.50
//	  → blended recent avg = (2.00 + 2.50) / 2 = 2.25
//	Expected pct_change = (2.25 - 1.25) / 1.25 * 100 = 80.00%
func TestPriceMoves_MixedNullNonNull(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newPMHousehold(t, ah, "PMMix")
	productID := insertPMProduct(t, ah, householdID, "MixedProduct")

	// Recent window: 1 non-NULL normalized + 1 NULL normalized (distinct dates).
	insertPMPrice(t, ah, householdID, storeID, productID, "-5 days", "2.00", "each")
	insertPMPriceNullNorm(t, ah, householdID, storeID, productID, -10, "2.50", "each")
	// Prior window: 1 non-NULL normalized + 1 NULL normalized (distinct dates).
	insertPMPrice(t, ah, householdID, storeID, productID, "-40 days", "1.00", "each")
	insertPMPriceNullNorm(t, ah, householdID, storeID, productID, -50, "1.50", "each")

	resp := callPriceMoves(t, ah, householdID)

	item := findPMItem(resp.Up, productID)
	if item == nil {
		t.Fatalf("expected mixed-null product in Up list; Up=%v Down=%v", resp.Up, resp.Down)
	}
	// Blended recent avg = 2.25, blended prior avg = 1.25 → pct_change = 80.00
	if item.PctChange != 80.00 {
		t.Errorf("pct_change: expected 80.00, got %f", item.PctChange)
	}
}
