package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
)

// callStaples invokes GET /analytics/staples with a fixed household id and
// returns the decoded response.
func callStaples(t *testing.T, ah *AnalyticsHandler, householdID string) []stapleItem {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/staples", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set(auth.ContextKeyHouseholdID, householdID)
	if err := ah.Staples(c); err != nil {
		t.Fatalf("Staples error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp []stapleItem
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp
}

// newStaplesHousehold creates one household and returns (householdID, storeID).
func newStaplesHousehold(t *testing.T, ah *AnalyticsHandler, name string) (string, string) {
	t.Helper()
	d := ah.DB
	var householdID string
	if err := d.QueryRow("INSERT INTO households (name) VALUES (?) RETURNING id", name).Scan(&householdID); err != nil {
		t.Fatalf("insert household: %v", err)
	}
	var storeID string
	if err := d.QueryRow(
		"INSERT INTO stores (household_id, name) VALUES (?, 'TestStore') RETURNING id", householdID,
	).Scan(&storeID); err != nil {
		t.Fatalf("insert store: %v", err)
	}
	return householdID, storeID
}

// insertStapleReceipt inserts a receipt at date('now', offset) and returns its id.
// offset is a SQLite modifier like "-30 days".
func insertStapleReceipt(t *testing.T, ah *AnalyticsHandler, householdID, storeID, daysOffset, status string) string {
	t.Helper()
	d := ah.DB
	var receiptID string
	if err := d.QueryRow("SELECT lower(hex(randomblob(16)))").Scan(&receiptID); err != nil {
		t.Fatalf("gen receipt id: %v", err)
	}
	if _, err := d.Exec(
		"INSERT INTO receipts (id, household_id, store_id, receipt_date, total, status) VALUES (?, ?, ?, date('now', ?), '10.00', ?)",
		receiptID, householdID, storeID, daysOffset, status,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	return receiptID
}

// insertStapleProduct creates a product. nonProduct=true flips is_non_product.
func insertStapleProduct(t *testing.T, ah *AnalyticsHandler, householdID, name, category string, nonProduct bool) string {
	t.Helper()
	flag := 0
	if nonProduct {
		flag = 1
	}
	var productID string
	if err := ah.DB.QueryRow(
		"INSERT INTO products (household_id, name, category, is_non_product) VALUES (?, ?, ?, ?) RETURNING id",
		householdID, name, category, flag,
	).Scan(&productID); err != nil {
		t.Fatalf("insert product: %v", err)
	}
	return productID
}

// insertStaplePrice inserts a product_prices row whose receipt_date is copied
// from the receipt (consistent with the worker's behavior).
func insertStaplePrice(t *testing.T, ah *AnalyticsHandler, productID, storeID, receiptID, unitPrice, quantity string) {
	t.Helper()
	if _, err := ah.DB.Exec(
		`INSERT INTO product_prices (product_id, store_id, receipt_id, receipt_date, quantity, unit, unit_price)
		 SELECT ?, ?, ?, r.receipt_date, ?, 'ea', ?
		 FROM receipts r WHERE r.id = ?`,
		productID, storeID, receiptID, quantity, unitPrice, receiptID,
	); err != nil {
		t.Fatalf("insert product_prices: %v", err)
	}
}

// findStaple returns the item with matching product_id or nil.
func findStaple(staples []stapleItem, productID string) *stapleItem {
	for i := range staples {
		if staples[i].ProductID == productID {
			return &staples[i]
		}
	}
	return nil
}

// --- Test cases ---

// Case 1: one product with exactly 2 purchases at -45d and now → INCLUDED,
// cadence=45, times_bought=2. Household span = 45d (< 60) so projections null.
func TestStaples_TwoPurchases45DaysApart(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newStaplesHousehold(t, ah, "H1")
	productID := insertStapleProduct(t, ah, householdID, "Milk", "Dairy", false)

	r1 := insertStapleReceipt(t, ah, householdID, storeID, "-45 days", "matched")
	r2 := insertStapleReceipt(t, ah, householdID, storeID, "0 days", "matched")
	insertStaplePrice(t, ah, productID, storeID, r1, "4.00", "1")
	insertStaplePrice(t, ah, productID, storeID, r2, "5.00", "1")

	resp := callStaples(t, ah, householdID)
	it := findStaple(resp, productID)
	if it == nil {
		t.Fatalf("expected product in response, got: %+v", resp)
	}
	if it.TimesBought != 2 {
		t.Errorf("times_bought: expected 2, got %d", it.TimesBought)
	}
	if it.CadenceDays != 45.0 {
		t.Errorf("cadence_days: expected 45.0, got %f", it.CadenceDays)
	}
	// Household span 45d → projections must be null.
	if it.WeeklySpend != nil || it.MonthlySpend != nil || it.YearlyProjection != nil {
		t.Errorf("expected nil projections (household span < 60d), got w=%v m=%v y=%v",
			it.WeeklySpend, it.MonthlySpend, it.YearlyProjection)
	}
}

// Case 2: 5 purchases 90 days apart → EXCLUDED (cadence > 60).
func TestStaples_CadenceTooLong(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newStaplesHousehold(t, ah, "H2")
	productID := insertStapleProduct(t, ah, householdID, "Spice", "Pantry", false)

	for _, off := range []string{"-360 days", "-270 days", "-180 days", "-90 days", "0 days"} {
		r := insertStapleReceipt(t, ah, householdID, storeID, off, "matched")
		insertStaplePrice(t, ah, productID, storeID, r, "3.00", "1")
	}

	resp := callStaples(t, ah, householdID)
	if findStaple(resp, productID) != nil {
		t.Errorf("expected product excluded (cadence=90d > 60d), got it in response")
	}
}

// Case 3: 2 line items on the SAME receipt at -30d, 1 line item at 0d.
// times_bought must count DISTINCT receipts (2), not line_items (3).
func TestStaples_SameReceiptDistinctCount(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newStaplesHousehold(t, ah, "H3")
	productID := insertStapleProduct(t, ah, householdID, "Eggs", "Dairy", false)

	r1 := insertStapleReceipt(t, ah, householdID, storeID, "-30 days", "matched")
	r2 := insertStapleReceipt(t, ah, householdID, storeID, "0 days", "matched")
	// Two split line-item rows under receipt 1.
	insertStaplePrice(t, ah, productID, storeID, r1, "3.00", "1")
	insertStaplePrice(t, ah, productID, storeID, r1, "3.00", "1")
	insertStaplePrice(t, ah, productID, storeID, r2, "3.00", "1")

	resp := callStaples(t, ah, householdID)
	it := findStaple(resp, productID)
	if it == nil {
		t.Fatalf("expected product in response")
	}
	if it.TimesBought != 2 {
		t.Errorf("times_bought: expected 2 (distinct receipts), got %d", it.TimesBought)
	}
	if it.CadenceDays != 30.0 {
		t.Errorf("cadence_days: expected 30.0, got %f", it.CadenceDays)
	}
}

// Case 4: is_non_product=1 with 5 weekly purchases → EXCLUDED.
func TestStaples_NonProductExcluded(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newStaplesHousehold(t, ah, "H4")
	productID := insertStapleProduct(t, ah, householdID, "Bag Fee", "Fees", true)

	for _, off := range []string{"-28 days", "-21 days", "-14 days", "-7 days", "0 days"} {
		r := insertStapleReceipt(t, ah, householdID, storeID, off, "matched")
		insertStaplePrice(t, ah, productID, storeID, r, "0.10", "1")
	}

	resp := callStaples(t, ah, householdID)
	if findStaple(resp, productID) != nil {
		t.Errorf("expected is_non_product filtered out, but got it")
	}
}

// Case 5: Household earliest receipt -40d (span<60), staple product INCLUDED
// but projections must be null.
func TestStaples_ShortHouseholdSpan(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newStaplesHousehold(t, ah, "H5")
	productID := insertStapleProduct(t, ah, householdID, "Bread", "Bakery", false)

	r1 := insertStapleReceipt(t, ah, householdID, storeID, "-40 days", "matched")
	r2 := insertStapleReceipt(t, ah, householdID, storeID, "-10 days", "matched")
	insertStaplePrice(t, ah, productID, storeID, r1, "3.50", "1")
	insertStaplePrice(t, ah, productID, storeID, r2, "3.50", "1")

	resp := callStaples(t, ah, householdID)
	it := findStaple(resp, productID)
	if it == nil {
		t.Fatalf("expected product in response")
	}
	if it.WeeklySpend != nil {
		t.Errorf("weekly_spend: expected nil (span<60), got %v", *it.WeeklySpend)
	}
	if it.MonthlySpend != nil {
		t.Errorf("monthly_spend: expected nil, got %v", *it.MonthlySpend)
	}
	if it.YearlyProjection != nil {
		t.Errorf("yearly_projection: expected nil, got %v", *it.YearlyProjection)
	}
}

// Case 6: Household span 120d but latest receipt -50d (stale, > 45d); staple
// INCLUDED (cadence within 60d) but all projections null.
func TestStaples_StaleHousehold(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newStaplesHousehold(t, ah, "H6")
	productID := insertStapleProduct(t, ah, householdID, "Cereal", "Pantry", false)

	// Household-wide earliest receipt (different product, establishes span).
	rHouseholdEarly := insertStapleReceipt(t, ah, householdID, storeID, "-120 days", "matched")
	otherProd := insertStapleProduct(t, ah, householdID, "Other", "Other", false)
	insertStaplePrice(t, ah, otherProd, storeID, rHouseholdEarly, "1.00", "1")

	// Staple product: two purchases 30d apart, latest at -50d (stale).
	r1 := insertStapleReceipt(t, ah, householdID, storeID, "-80 days", "matched")
	r2 := insertStapleReceipt(t, ah, householdID, storeID, "-50 days", "matched")
	insertStaplePrice(t, ah, productID, storeID, r1, "4.00", "1")
	insertStaplePrice(t, ah, productID, storeID, r2, "4.00", "1")

	resp := callStaples(t, ah, householdID)
	it := findStaple(resp, productID)
	if it == nil {
		t.Fatalf("expected product in response")
	}
	if it.WeeklySpend != nil || it.MonthlySpend != nil || it.YearlyProjection != nil {
		t.Errorf("expected nil projections (stale household), got w=%v m=%v y=%v",
			it.WeeklySpend, it.MonthlySpend, it.YearlyProjection)
	}
}

// Case 7: Spreadsheet import style — normalized_price NULL, unit_price=4.99,
// quantity=1 — 3 purchases 20d apart. total_spent must be 14.97.
func TestStaples_SpreadsheetImportUnitPrice(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newStaplesHousehold(t, ah, "H7")
	productID := insertStapleProduct(t, ah, householdID, "Yogurt", "Dairy", false)

	// Build enough household history so that the staple is found, but keep
	// the test stable by only asserting on total_spent.
	for _, off := range []string{"-40 days", "-20 days", "0 days"} {
		r := insertStapleReceipt(t, ah, householdID, storeID, off, "matched")
		// normalized_price stays NULL (we do not insert it).
		insertStaplePrice(t, ah, productID, storeID, r, "4.99", "1")
	}

	resp := callStaples(t, ah, householdID)
	it := findStaple(resp, productID)
	if it == nil {
		t.Fatalf("expected product in response")
	}
	if it.TotalSpent != 14.97 {
		t.Errorf("total_spent: expected 14.97 (unit_price*quantity), got %f", it.TotalSpent)
	}
	// AvgPrice = 14.97 / 3 = 4.99.
	if it.AvgPrice != 4.99 {
		t.Errorf("avg_price: expected 4.99, got %f", it.AvgPrice)
	}
}

// Case 8: two purchases 80 days apart → EXCLUDED.
func TestStaples_TwoPurchasesEightyDays(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newStaplesHousehold(t, ah, "H8")
	productID := insertStapleProduct(t, ah, householdID, "Detergent", "Cleaning", false)

	r1 := insertStapleReceipt(t, ah, householdID, storeID, "-80 days", "matched")
	r2 := insertStapleReceipt(t, ah, householdID, storeID, "0 days", "matched")
	insertStaplePrice(t, ah, productID, storeID, r1, "12.00", "1")
	insertStaplePrice(t, ah, productID, storeID, r2, "12.00", "1")

	resp := callStaples(t, ah, householdID)
	if findStaple(resp, productID) != nil {
		t.Errorf("expected exclusion (cadence=80d > 60d)")
	}
}

// Case 9: 10 purchases at 7-day cadence → INCLUDED; sparkline max 8 and ASC
// by date.
func TestStaples_SparklineCappedAtEightAscending(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newStaplesHousehold(t, ah, "H9")
	productID := insertStapleProduct(t, ah, householdID, "Coffee", "Beverage", false)

	// Seed 10 purchases at 7-day cadence with monotonically increasing prices.
	offsets := []string{
		"-63 days", "-56 days", "-49 days", "-42 days", "-35 days",
		"-28 days", "-21 days", "-14 days", "-7 days", "0 days",
	}
	prices := []string{"10.00", "11.00", "12.00", "13.00", "14.00", "15.00", "16.00", "17.00", "18.00", "19.00"}
	for i, off := range offsets {
		r := insertStapleReceipt(t, ah, householdID, storeID, off, "matched")
		insertStaplePrice(t, ah, productID, storeID, r, prices[i], "1")
	}

	resp := callStaples(t, ah, householdID)
	it := findStaple(resp, productID)
	if it == nil {
		t.Fatalf("expected product in response")
	}
	if len(it.SparklinePoints) != 8 {
		t.Fatalf("sparkline_points length: expected 8, got %d (%v)", len(it.SparklinePoints), it.SparklinePoints)
	}
	// Verify ASC order — each point >= previous (our seed prices rise).
	for i := 1; i < len(it.SparklinePoints); i++ {
		if it.SparklinePoints[i] < it.SparklinePoints[i-1] {
			t.Errorf("sparkline_points not ASC at %d: %v", i, it.SparklinePoints)
			break
		}
	}
	// First retained point should be price #3 (-49d, $12.00); last should be #10 ($19.00).
	if it.SparklinePoints[0] != 12.00 {
		t.Errorf("oldest-of-8 point: expected 12.00, got %f", it.SparklinePoints[0])
	}
	if it.SparklinePoints[7] != 19.00 {
		t.Errorf("newest-of-8 point: expected 19.00, got %f", it.SparklinePoints[7])
	}
}

// Case 10: empty household → [] and HTTP 200.
func TestStaples_EmptyHousehold(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, _ := newStaplesHousehold(t, ah, "H10")

	resp := callStaples(t, ah, householdID)
	if resp == nil {
		t.Fatalf("expected empty slice, got nil")
	}
	if len(resp) != 0 {
		t.Errorf("expected empty, got %d items: %v", len(resp), resp)
	}
}

// Bonus: receipts with status outside the whitelist (e.g. 'processing',
// 'error') must be excluded from staple math. Two valid matched purchases
// 30d apart PLUS one 'error' purchase on a third date — the error one must
// not inflate distinct_dates nor shift the cadence.
func TestStaples_RejectedReceiptExcluded(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newStaplesHousehold(t, ah, "Hrej")
	productID := insertStapleProduct(t, ah, householdID, "Pasta", "Pantry", false)

	r1 := insertStapleReceipt(t, ah, householdID, storeID, "-30 days", "matched")
	r2 := insertStapleReceipt(t, ah, householdID, storeID, "0 days", "matched")
	rBad := insertStapleReceipt(t, ah, householdID, storeID, "-90 days", "error")
	insertStaplePrice(t, ah, productID, storeID, r1, "2.00", "1")
	insertStaplePrice(t, ah, productID, storeID, r2, "2.00", "1")
	insertStaplePrice(t, ah, productID, storeID, rBad, "2.00", "1")

	resp := callStaples(t, ah, householdID)
	it := findStaple(resp, productID)
	if it == nil {
		t.Fatalf("expected product in response")
	}
	if it.TimesBought != 2 {
		t.Errorf("times_bought: expected 2 (rejected excluded), got %d", it.TimesBought)
	}
	if it.CadenceDays != 30.0 {
		t.Errorf("cadence_days: expected 30.0, got %f", it.CadenceDays)
	}
}
