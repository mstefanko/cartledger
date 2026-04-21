package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
)

// callInflation invokes GET /analytics/inflation with a fixed household id.
func callInflation(t *testing.T, ah *AnalyticsHandler, householdID string) inflationResponse {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/inflation", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set(auth.ContextKeyHouseholdID, householdID)
	if err := ah.Inflation(c); err != nil {
		t.Fatalf("Inflation error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp inflationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp
}

// newInflHousehold creates one household + store and returns (householdID, storeID).
func newInflHousehold(t *testing.T, ah *AnalyticsHandler, name string) (string, string) {
	t.Helper()
	d := ah.DB
	var householdID string
	if err := d.QueryRow("INSERT INTO households (name) VALUES (?) RETURNING id", name).Scan(&householdID); err != nil {
		t.Fatalf("insert household: %v", err)
	}
	var storeID string
	if err := d.QueryRow(
		"INSERT INTO stores (household_id, name) VALUES (?, 'InflStore') RETURNING id", householdID,
	).Scan(&storeID); err != nil {
		t.Fatalf("insert store: %v", err)
	}
	return householdID, storeID
}

// insertInflProduct creates a product for inflation tests.
func insertInflProduct(t *testing.T, ah *AnalyticsHandler, householdID, name string) string {
	t.Helper()
	var productID string
	if err := ah.DB.QueryRow(
		"INSERT INTO products (household_id, name, is_non_product) VALUES (?, ?, 0) RETURNING id",
		householdID, name,
	).Scan(&productID); err != nil {
		t.Fatalf("insert product %q: %v", name, err)
	}
	return productID
}

// insertInflPrice inserts a receipt + product_prices row at date('now', daysOffset).
// normalizedPrice may be empty string to insert a NULL normalized_price (tests unit_price fallback).
func insertInflPrice(t *testing.T, ah *AnalyticsHandler, householdID, storeID, productID, daysOffset, unitPrice, normalizedPrice, qty string) {
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
	var normVal interface{}
	if normalizedPrice == "" {
		normVal = nil
	} else {
		normVal = normalizedPrice
	}
	if _, err := d.Exec(
		`INSERT INTO product_prices (product_id, store_id, receipt_id, receipt_date, quantity, unit, unit_price, normalized_price)
		 VALUES (?, ?, ?, ?, ?, 'each', ?, ?)`,
		productID, storeID, receiptID, receiptDate, qty, unitPrice, normVal,
	); err != nil {
		t.Fatalf("insert product_prices: %v", err)
	}
}

// seedStapleProduct seeds enough purchases for a product to appear in qStaplesList
// (requires distinct_dates >= 2, cadence <= 60d) AND to have prices in all three
// windows: W_current [now-30,now), W_3mo [now-120,now-90), W_6mo [now-210,now-180).
// Also ensures span >= 200 days so change_6mo_pct is not span-suppressed.
func seedStapleProduct(t *testing.T, ah *AnalyticsHandler, householdID, storeID, productID, curPrice, priorPrice, price6mo string) {
	t.Helper()
	// 6 purchases spread over ~200d with cadence ~40d (≤60) — satisfies qStaplesList.
	// W_current [now-30, now): -10d ✓
	insertInflPrice(t, ah, householdID, storeID, productID, "-10 days", curPrice, curPrice, "1")
	// Q_90d [now-90, now): -50d (2nd obs for median, still in Q_90d window)
	insertInflPrice(t, ah, householdID, storeID, productID, "-50 days", curPrice, curPrice, "1")
	// W_3mo [now-120, now-90): -95d ✓
	insertInflPrice(t, ah, householdID, storeID, productID, "-95 days", priorPrice, priorPrice, "1")
	// Gap purchase to maintain cadence
	insertInflPrice(t, ah, householdID, storeID, productID, "-140 days", priorPrice, priorPrice, "1")
	// W_6mo [now-210, now-180): -190d ✓
	insertInflPrice(t, ah, householdID, storeID, productID, "-190 days", price6mo, price6mo, "1")
	// Final anchor purchase to give span ~200d
	insertInflPrice(t, ah, householdID, storeID, productID, "-200 days", price6mo, price6mo, "1")
}

// Case 1: Happy path — 3 staple products, +10% inflation in current vs 3mo and 6mo.
func TestInflation_HappyPath10Pct(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newInflHousehold(t, ah, "Infl1")
	for _, name := range []string{"Milk", "Eggs", "Bread"} {
		productID := insertInflProduct(t, ah, householdID, name)
		// W_current: $1.10, W_3mo: $1.00, W_6mo: $1.00 → +10%
		seedStapleProduct(t, ah, householdID, storeID, productID, "1.10", "1.00", "1.00")
	}

	resp := callInflation(t, ah, householdID)

	if resp.Suppressed {
		t.Fatalf("expected suppressed=false, got suppressed=true (reason=%v)", resp.SuppressionReason)
	}
	if resp.Change3moPct == nil {
		t.Fatal("expected change_3mo_pct non-nil")
	}
	if resp.Change6moPct == nil {
		t.Fatal("expected change_6mo_pct non-nil")
	}
	if *resp.Change3moPct < 9.9 || *resp.Change3moPct > 10.1 {
		t.Errorf("change_3mo_pct: expected ~10.00, got %f", *resp.Change3moPct)
	}
	if *resp.Change6moPct < 9.9 || *resp.Change6moPct > 10.1 {
		t.Errorf("change_6mo_pct: expected ~10.00, got %f", *resp.Change6moPct)
	}
}

// Case 2: Deflation — prices -5% between W_3mo and W_current.
func TestInflation_Deflation(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newInflHousehold(t, ah, "Infl2")
	for _, name := range []string{"Juice", "Butter", "Cheese"} {
		productID := insertInflProduct(t, ah, householdID, name)
		// W_current: $0.95, W_3mo: $1.00 → -5%
		seedStapleProduct(t, ah, householdID, storeID, productID, "0.95", "1.00", "1.00")
	}

	resp := callInflation(t, ah, householdID)

	if resp.Change3moPct == nil {
		t.Fatal("expected change_3mo_pct non-nil")
	}
	if *resp.Change3moPct > -4.9 || *resp.Change3moPct < -5.1 {
		t.Errorf("change_3mo_pct: expected ~-5.00, got %f", *resp.Change3moPct)
	}
}

// Case 3: Empty household — no receipts. basket_size=0, suppressed=true.
func TestInflation_EmptyHousehold(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	var householdID string
	if err := ah.DB.QueryRow("INSERT INTO households (name) VALUES ('InflEmpty') RETURNING id").Scan(&householdID); err != nil {
		t.Fatalf("insert household: %v", err)
	}

	resp := callInflation(t, ah, householdID)

	if !resp.Suppressed {
		t.Error("expected suppressed=true for empty household")
	}
	if resp.Change3moPct != nil {
		t.Error("expected change_3mo_pct=nil")
	}
	if resp.Change6moPct != nil {
		t.Error("expected change_6mo_pct=nil")
	}
	if resp.SuppressionReason == nil || *resp.SuppressionReason != "Not enough overlap yet." {
		t.Errorf("expected suppression_reason='Not enough overlap yet.', got %v", resp.SuppressionReason)
	}
}

// Case 4: Insufficient history (span<90d) — both pcts nil, suppressed=true.
func TestInflation_InsufficientHistory(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newInflHousehold(t, ah, "Infl4")
	productID := insertInflProduct(t, ah, householdID, "Oatmeal")
	// Two purchases within last 45 days → span<90, but satisfies qStaplesList cadence.
	insertInflPrice(t, ah, householdID, storeID, productID, "-5 days", "1.00", "1.00", "1")
	insertInflPrice(t, ah, householdID, storeID, productID, "-40 days", "1.00", "1.00", "1")

	resp := callInflation(t, ah, householdID)

	if !resp.Suppressed {
		t.Error("expected suppressed=true for span<90d")
	}
	if resp.Change3moPct != nil {
		t.Error("expected change_3mo_pct=nil for span<90d")
	}
	if resp.Change6moPct != nil {
		t.Error("expected change_6mo_pct=nil for span<90d")
	}
}

// Case 5: Mid-span (span ~100d) — change_3mo_pct non-nil, change_6mo_pct nil.
func TestInflation_MidSpan(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newInflHousehold(t, ah, "Infl5")
	for _, name := range []string{"Rice", "Pasta", "Oats"} {
		productID := insertInflProduct(t, ah, householdID, name)
		// Purchases in W_current and W_3mo only; span ~110d.
		// We need span>=90 and data in W_current + W_3mo, but NOT in W_6mo.
		insertInflPrice(t, ah, householdID, storeID, productID, "-10 days", "1.10", "1.10", "1")
		insertInflPrice(t, ah, householdID, storeID, productID, "-50 days", "1.10", "1.10", "1")
		insertInflPrice(t, ah, householdID, storeID, productID, "-100 days", "1.00", "1.00", "1")
		insertInflPrice(t, ah, householdID, storeID, productID, "-110 days", "1.00", "1.00", "1")
	}

	resp := callInflation(t, ah, householdID)

	if resp.Suppressed {
		t.Errorf("expected suppressed=false for mid-span, got true (reason=%v)", resp.SuppressionReason)
	}
	if resp.Change3moPct == nil {
		t.Error("expected change_3mo_pct non-nil for mid-span ≥90d")
	}
	if resp.Change6moPct != nil {
		t.Errorf("expected change_6mo_pct nil for span<180d, got %f", *resp.Change6moPct)
	}
}

// Case 6: Low basket overlap (<50%) — change_3mo_pct nil.
// W_current = [now-30, now), W_3mo = [now-120, now-90).
// We create 6 staple products. Only 2 get purchases in W_3mo; the other 4 do not.
// overlap_3 = 2, basketSize = 6 → 2/6 = 0.33 < 0.5 → change_3mo_pct must be nil.
func TestInflation_LowOverlap(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newInflHousehold(t, ah, "Infl6")

	for i, name := range []string{"P1", "P2", "P3", "P4", "P5", "P6"} {
		productID := insertInflProduct(t, ah, householdID, name)
		// All 6 products must satisfy qStaplesList (cadence<=60d, distinct_dates>=2).
		// Base purchases: W_current (-10d), then spread over long span avoiding W_3mo
		// window [now-120, now-90) for products i>=2.
		// Use -10, -65, -130, -190 for span ~180d, cadence ~60d — avoids W_3mo gap.
		insertInflPrice(t, ah, householdID, storeID, productID, "-10 days", "1.00", "1.00", "1")
		insertInflPrice(t, ah, householdID, storeID, productID, "-65 days", "1.00", "1.00", "1")
		insertInflPrice(t, ah, householdID, storeID, productID, "-130 days", "1.00", "1.00", "1")
		insertInflPrice(t, ah, householdID, storeID, productID, "-190 days", "1.00", "1.00", "1")
		// Only first 2 products get a W_3mo purchase: [now-120, now-90) → -100d.
		if i < 2 {
			insertInflPrice(t, ah, householdID, storeID, productID, "-100 days", "1.00", "1.00", "1")
		}
	}

	resp := callInflation(t, ah, householdID)

	// With 6-product basket and overlap of only 2 (33%) < 50%, change_3mo_pct must be nil.
	if resp.Change3moPct != nil {
		t.Errorf("expected change_3mo_pct nil when overlap<50%%, got %f", *resp.Change3moPct)
	}
}

// Case 7: Quantity median fallback — 1 obs in last 90d, 4 all-time obs.
func TestInflation_QtyMedianFallback(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newInflHousehold(t, ah, "Infl7")
	productID := insertInflProduct(t, ah, householdID, "Yogurt")
	// 1 obs in Q_90d, 4 all-time obs → fallback to all-time median.
	// All-time obs: qty={1,2,3,4} → median=2.5.
	insertInflPrice(t, ah, householdID, storeID, productID, "-10 days", "1.00", "1.00", "4") // Q_90d only
	insertInflPrice(t, ah, householdID, storeID, productID, "-100 days", "1.00", "1.00", "1")
	insertInflPrice(t, ah, householdID, storeID, productID, "-110 days", "1.00", "1.00", "2")
	insertInflPrice(t, ah, householdID, storeID, productID, "-120 days", "1.00", "1.00", "3")

	// Should not panic; just verify the handler returns without error.
	resp := callInflation(t, ah, householdID)
	_ = resp
}

// Case 8: Even-count median — product has 4 qty obs {1,2,3,4} → median=2.5.
func TestInflation_EvenCountMedian(t *testing.T) {
	// This is a unit test of the median helper.
	input := []float64{1, 2, 3, 4}
	got := median(input)
	if got != 2.5 {
		t.Errorf("median({1,2,3,4}): expected 2.5, got %f", got)
	}
	input3 := []float64{1, 2, 3}
	got3 := median(input3)
	if got3 != 2 {
		t.Errorf("median({1,2,3}): expected 2, got %f", got3)
	}
}

// Case 9: Rounding boundary — normalized_price NULL falls back to unit_price.
func TestInflation_NullNormalizedPriceFallback(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newInflHousehold(t, ah, "Infl9")
	for _, name := range []string{"SpreadA", "SpreadB", "SpreadC"} {
		productID := insertInflProduct(t, ah, householdID, name)
		// normalizedPrice="" → NULL; handler must fall back to unit_price via COALESCE.
		insertInflPrice(t, ah, householdID, storeID, productID, "-10 days", "1.10", "", "1")
		insertInflPrice(t, ah, householdID, storeID, productID, "-67 days", "1.10", "", "1")
		insertInflPrice(t, ah, householdID, storeID, productID, "-105 days", "1.00", "", "1")
		insertInflPrice(t, ah, householdID, storeID, productID, "-165 days", "1.00", "", "1")
	}

	resp := callInflation(t, ah, householdID)

	if resp.Suppressed {
		t.Errorf("expected suppressed=false (normalized_price NULL fallback), reason=%v", resp.SuppressionReason)
	}
	if resp.Change3moPct == nil {
		t.Error("expected change_3mo_pct non-nil when unit_price fallback used")
	}
}

// Case 10: Staleness sanity — receipts stop 70d ago; pipeline returns values, no panic.
func TestInflation_StalenessSanity(t *testing.T) {
	ah, cleanup := newAnalyticsHandler(t)
	defer cleanup()

	householdID, storeID := newInflHousehold(t, ah, "Infl10")
	for _, name := range []string{"OldMilk", "OldEggs", "OldBread"} {
		productID := insertInflProduct(t, ah, householdID, name)
		// All purchases stopped 70+ days ago (no W_current data).
		insertInflPrice(t, ah, householdID, storeID, productID, "-75 days", "1.00", "1.00", "1")
		insertInflPrice(t, ah, householdID, storeID, productID, "-100 days", "1.00", "1.00", "1")
		insertInflPrice(t, ah, householdID, storeID, productID, "-115 days", "1.00", "1.00", "1")
		insertInflPrice(t, ah, householdID, storeID, productID, "-165 days", "1.00", "1.00", "1")
	}

	// Should not panic; values may be nil due to no W_current data (overlap=0).
	resp := callInflation(t, ah, householdID)
	_ = resp
}
