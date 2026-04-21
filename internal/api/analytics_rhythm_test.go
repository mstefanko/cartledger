package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
)

// rhythmCall fires GET /analytics/rhythm for the given householdID and returns
// the parsed response and HTTP status code.
func rhythmCall(t *testing.T, ah *AnalyticsHandler, householdID string) (rhythmResponse, int) {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/rhythm", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set(auth.ContextKeyHouseholdID, householdID)
	if err := ah.Rhythm(c); err != nil {
		t.Fatalf("Rhythm error: %v", err)
	}
	var resp rhythmResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v %s", err, rec.Body.String())
	}
	return resp, rec.Code
}

// genID generates a random hex ID using SQLite.
func genID(t *testing.T, ah *AnalyticsHandler) string {
	t.Helper()
	var id string
	if err := ah.DB.QueryRow("SELECT lower(hex(randomblob(16)))").Scan(&id); err != nil {
		t.Fatalf("genID: %v", err)
	}
	return id
}

// insertReceipt inserts a receipt and returns its id.
func insertReceipt(t *testing.T, ah *AnalyticsHandler, householdID, date, total, status string) string {
	t.Helper()
	id := genID(t, ah)
	if _, err := ah.DB.Exec(
		"INSERT INTO receipts (id, household_id, receipt_date, total, status) VALUES (?, ?, ?, ?, ?)",
		id, householdID, date, total, status,
	); err != nil {
		t.Fatalf("insertReceipt: %v", err)
	}
	return id
}

// TestRhythm_Empty: seedTestData provides one matched receipt at 2024-01-01
// (well outside the current 30-day window for any reasonable "today"). No
// current-window receipts → trips.current=0, delta_pct=nil, avg_basket=0.
func TestRhythm_Empty(t *testing.T) {
	ph, _, cleanup := newTestHandler(t)
	defer cleanup()
	householdID, _, _, _ := seedTestData(t, ph)
	ah := &AnalyticsHandler{DB: ph.DB, Cfg: ph.Cfg}

	resp, code := rhythmCall(t, ah, householdID)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if resp.Trips.Current != 0 {
		t.Errorf("trips.current: expected 0, got %d", resp.Trips.Current)
	}
	if resp.Trips.DeltaPct != nil {
		t.Errorf("delta_pct: expected nil, got %v", *resp.Trips.DeltaPct)
	}
	if resp.AvgBasket.Current != 0 {
		t.Errorf("avg_basket.current: expected 0, got %f", resp.AvgBasket.Current)
	}
	if resp.AvgItemsPerTrip != 0 {
		t.Errorf("avg_items_per_trip: expected 0, got %f", resp.AvgItemsPerTrip)
	}
}

// TestRhythm_SingleCurrentPending: a pending receipt in the current window
// with 3 line items should appear in counts.
func TestRhythm_SingleCurrentPending(t *testing.T) {
	ph, _, cleanup := newTestHandler(t)
	defer cleanup()
	householdID, _, _, _ := seedTestData(t, ph)
	ah := &AnalyticsHandler{DB: ph.DB, Cfg: ph.Cfg}

	inCurrent := time.Now().UTC().AddDate(0, 0, -15).Format("2006-01-02")
	receiptID := insertReceipt(t, ah, householdID, inCurrent, "55.00", "pending")

	for i := 0; i < 3; i++ {
		if _, err := ah.DB.Exec(
			"INSERT INTO line_items (receipt_id, raw_name, total_price) VALUES (?, 'item', '1.00')",
			receiptID,
		); err != nil {
			t.Fatalf("insert line_item: %v", err)
		}
	}

	resp, code := rhythmCall(t, ah, householdID)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if resp.Trips.Current != 1 {
		t.Errorf("trips.current: expected 1, got %d", resp.Trips.Current)
	}
	if resp.AvgBasket.Current != 55.0 {
		t.Errorf("avg_basket.current: expected 55.0, got %f", resp.AvgBasket.Current)
	}
	if resp.AvgItemsPerTrip != 3.0 {
		t.Errorf("avg_items_per_trip: expected 3.0, got %f", resp.AvgItemsPerTrip)
	}
}

// TestRhythm_ProcessingErrorExcluded: receipts with status 'processing' or
// 'error' must not be counted in any window.
func TestRhythm_ProcessingErrorExcluded(t *testing.T) {
	ph, _, cleanup := newTestHandler(t)
	defer cleanup()
	householdID, _, _, _ := seedTestData(t, ph)
	ah := &AnalyticsHandler{DB: ph.DB, Cfg: ph.Cfg}

	inCurrent := time.Now().UTC().AddDate(0, 0, -15).Format("2006-01-02")
	insertReceipt(t, ah, householdID, inCurrent, "20.00", "processing")
	insertReceipt(t, ah, householdID, inCurrent, "30.00", "error")

	resp, code := rhythmCall(t, ah, householdID)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if resp.Trips.Current != 0 {
		t.Errorf("trips.current: expected 0, got %d (processing/error must be excluded)", resp.Trips.Current)
	}
}

// TestRhythm_PriorWindowCounted: a matched receipt in the prior 30-day window
// (31-60 days ago) should appear in trips.prior; delta_pct should be -100.
func TestRhythm_PriorWindowCounted(t *testing.T) {
	ph, _, cleanup := newTestHandler(t)
	defer cleanup()
	householdID, _, _, _ := seedTestData(t, ph)
	ah := &AnalyticsHandler{DB: ph.DB, Cfg: ph.Cfg}

	inPrior := time.Now().UTC().AddDate(0, 0, -45).Format("2006-01-02")
	insertReceipt(t, ah, householdID, inPrior, "40.00", "matched")

	resp, code := rhythmCall(t, ah, householdID)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if resp.Trips.Prior != 1 {
		t.Errorf("trips.prior: expected 1, got %d", resp.Trips.Prior)
	}
	if resp.Trips.Current != 0 {
		t.Errorf("trips.current: expected 0, got %d", resp.Trips.Current)
	}
	if resp.Trips.DeltaPct == nil {
		t.Fatal("delta_pct: expected -100.0, got nil")
	}
	if *resp.Trips.DeltaPct != -100.0 {
		t.Errorf("delta_pct: expected -100.0, got %f", *resp.Trips.DeltaPct)
	}
}

// TestRhythm_DeltaPctNullWhenPriorZero: no prior-window receipts means
// delta_pct cannot be computed and must be JSON null.
func TestRhythm_DeltaPctNullWhenPriorZero(t *testing.T) {
	ph, _, cleanup := newTestHandler(t)
	defer cleanup()
	householdID, _, _, _ := seedTestData(t, ph)
	ah := &AnalyticsHandler{DB: ph.DB, Cfg: ph.Cfg}

	inCurrent := time.Now().UTC().AddDate(0, 0, -15).Format("2006-01-02")
	insertReceipt(t, ah, householdID, inCurrent, "30.00", "matched")

	resp, code := rhythmCall(t, ah, householdID)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if resp.Trips.DeltaPct != nil {
		t.Errorf("delta_pct: expected nil (prior=0), got %v", *resp.Trips.DeltaPct)
	}
}

// TestRhythm_MostShoppedDOW_ClearWinner: insert 5 Saturdays and 2 Sundays
// spanning > 14 days; most_shopped_dow must be "Saturday".
func TestRhythm_MostShoppedDOW_ClearWinner(t *testing.T) {
	ph, _, cleanup := newTestHandler(t)
	defer cleanup()
	householdID, _, _, _ := seedTestData(t, ph)
	ah := &AnalyticsHandler{DB: ph.DB, Cfg: ph.Cfg}

	// Find the most recent Saturday at or before today.
	now := time.Now().UTC()
	daysUntilSat := int(now.Weekday()+1) % 7 // days back to Saturday (Weekday: Sun=0,...,Sat=6)
	lastSat := now.AddDate(0, 0, -daysUntilSat)

	// Insert 5 Saturdays going back.
	for i := 0; i < 5; i++ {
		d := lastSat.AddDate(0, 0, -7*i).Format("2006-01-02")
		insertReceipt(t, ah, householdID, d, "50.00", "matched")
	}
	// Insert 2 Sundays (the day after each of the last two Saturdays, going back one more week).
	for i := 0; i < 2; i++ {
		sun := lastSat.AddDate(0, 0, -7*(i+1)+1)
		insertReceipt(t, ah, householdID, sun.Format("2006-01-02"), "40.00", "matched")
	}

	resp, code := rhythmCall(t, ah, householdID)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if resp.MostShoppedDOW == nil {
		t.Fatal("most_shopped_dow: expected 'Saturday', got nil")
	}
	if *resp.MostShoppedDOW != "Saturday" {
		t.Errorf("most_shopped_dow: expected 'Saturday', got %q", *resp.MostShoppedDOW)
	}
}

// TestRhythm_MostShoppedDOW_TieReturnsString: exact two-way tie between
// Saturday and Sunday across >= 14 days → "Saturday/Sunday".
func TestRhythm_MostShoppedDOW_TieReturnsString(t *testing.T) {
	ph, _, cleanup := newTestHandler(t)
	defer cleanup()
	householdID, _, _, _ := seedTestData(t, ph)
	ah := &AnalyticsHandler{DB: ph.DB, Cfg: ph.Cfg}

	now := time.Now().UTC()
	daysUntilSat := int(now.Weekday()+1) % 7
	lastSat := now.AddDate(0, 0, -daysUntilSat)

	// 3 Saturdays.
	for i := 0; i < 3; i++ {
		d := lastSat.AddDate(0, 0, -7*i).Format("2006-01-02")
		insertReceipt(t, ah, householdID, d, "50.00", "matched")
	}
	// 3 Sundays (day after each Saturday going back).
	for i := 0; i < 3; i++ {
		sun := lastSat.AddDate(0, 0, -7*i+1)
		insertReceipt(t, ah, householdID, sun.Format("2006-01-02"), "40.00", "matched")
	}

	resp, code := rhythmCall(t, ah, householdID)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if resp.MostShoppedDOW == nil {
		t.Fatal("most_shopped_dow: expected tie string, got nil")
	}
	// ORDER BY dow ASC puts Sunday(0) before Saturday(6): "Sunday/Saturday"
	if *resp.MostShoppedDOW != "Sunday/Saturday" {
		t.Errorf("most_shopped_dow: expected 'Sunday/Saturday', got %q", *resp.MostShoppedDOW)
	}
}
