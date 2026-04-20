package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
	"github.com/mstefanko/cartledger/internal/ws"
)

// reviewTestFixture mirrors importTestFixture but wires only the ReviewHandler,
// plus a few helpers for inserting receipts/line_items/batches by hand. The
// plan calls for the "lighter path" over spreadsheet.Commit when a minimal
// fixture is sufficient — this is that fixture.
type reviewTestFixture struct {
	Handler *ReviewHandler
	Cfg     *config.Config
	DB      *sql.DB
	Echo    *echo.Echo
	HH1     string
	HH2     string
}

func newReviewFixture(t *testing.T) (*reviewTestFixture, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := db.RunMigrations(database); err != nil {
		database.Close()
		t.Fatalf("RunMigrations: %v", err)
	}

	cfg := &config.Config{
		DataDir:           dir,
		BackupRetainCount: 5,
		RateLimitEnabled:  false,
	}

	var hh1, hh2 string
	if err := database.QueryRow(
		"INSERT INTO households (name) VALUES ('ReviewHH1') RETURNING id",
	).Scan(&hh1); err != nil {
		t.Fatalf("seed household 1: %v", err)
	}
	if err := database.QueryRow(
		"INSERT INTO households (name) VALUES ('ReviewHH2') RETURNING id",
	).Scan(&hh2); err != nil {
		t.Fatalf("seed household 2: %v", err)
	}

	h := &ReviewHandler{DB: database, Cfg: cfg, Hub: (*ws.Hub)(nil)}
	e := echo.New()
	protected := e.Group("", func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if hid := c.Request().Header.Get("X-Test-Household-ID"); hid != "" {
				c.Set(auth.ContextKeyHouseholdID, hid)
				c.Set(auth.ContextKeyUserID, "test-user")
			}
			return next(c)
		}
	})
	h.RegisterRoutes(protected)

	cleanup := func() { database.Close() }
	return &reviewTestFixture{
		Handler: h,
		Cfg:     cfg,
		DB:      database,
		Echo:    e,
		HH1:     hh1,
		HH2:     hh2,
	}, cleanup
}

// insertBatch creates an import_batches row and returns its id.
func insertBatch(t *testing.T, database *sql.DB, householdID, filename string, receipts, items int) string {
	t.Helper()
	id := uuid.New().String()
	if _, err := database.Exec(
		`INSERT INTO import_batches (id, household_id, source_type, filename, receipts_count, items_count, unmatched_count)
		 VALUES (?, ?, 'spreadsheet', ?, ?, ?, 0)`,
		id, householdID, filename, receipts, items,
	); err != nil {
		t.Fatalf("insert batch: %v", err)
	}
	return id
}

// insertReceiptWithLineItem inserts a receipt + one unmatched line_item for
// the given household. batchID may be empty to create a scan-path receipt
// (no import_batch_id) — the plan's Test 1 needs both shapes.
func insertReceiptWithLineItem(t *testing.T, database *sql.DB, householdID, batchID, rawName string) (receiptID, lineItemID string) {
	t.Helper()
	receiptID = uuid.New().String()
	lineItemID = uuid.New().String()

	// Receipt: when batchID is empty, use a null import_batch_id + source='scan'
	// so the row matches real scan-path shape.
	var bid interface{}
	source := "scan"
	if batchID != "" {
		bid = batchID
		source = "import"
	}
	if _, err := database.Exec(
		`INSERT INTO receipts (id, household_id, receipt_date, total, status, source, import_batch_id)
		 VALUES (?, ?, '2026-03-12', '1.00', 'pending', ?, ?)`,
		receiptID, householdID, source, bid,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}

	if _, err := database.Exec(
		`INSERT INTO line_items (id, receipt_id, raw_name, quantity, total_price, matched, import_batch_id)
		 VALUES (?, ?, ?, '1', '1.00', 'unmatched', ?)`,
		lineItemID, receiptID, rawName, bid,
	); err != nil {
		t.Fatalf("insert line_item: %v", err)
	}
	return
}

// doReviewJSON is a tiny helper that sends a GET with the X-Test-Household-ID
// header and decodes the JSON body.
func doReviewJSON(t *testing.T, e *echo.Echo, path, householdID string) (*httptest.ResponseRecorder, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if householdID != "" {
		req.Header.Set("X-Test-Household-ID", householdID)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec, rec.Body.Bytes()
}

// ----------------------------------------------------------------------------
// Test 1: batch filter narrows the unmatched list.
// ----------------------------------------------------------------------------

func TestReview_BatchFilter_NarrowsList(t *testing.T) {
	f, cleanup := newReviewFixture(t)
	defer cleanup()

	batchID := insertBatch(t, f.DB, f.HH1, "grocery.csv", 1, 1)
	_, batchLineID := insertReceiptWithLineItem(t, f.DB, f.HH1, batchID, "batch-item")
	_, scanLineID := insertReceiptWithLineItem(t, f.DB, f.HH1, "", "scan-item")

	// Unfiltered: both line items returned.
	rec, body := doReviewJSON(t, f.Echo, "/line-items/unmatched", f.HH1)
	if rec.Code != http.StatusOK {
		t.Fatalf("unfiltered: want 200 got %d body=%s", rec.Code, string(body))
	}
	var all []map[string]any
	if err := json.Unmarshal(body, &all); err != nil {
		t.Fatalf("unmarshal unfiltered: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("unfiltered: want 2 items got %d (%v)", len(all), all)
	}

	// Filtered: only the batch item.
	rec, body = doReviewJSON(t, f.Echo, "/line-items/unmatched?batch_id="+batchID, f.HH1)
	if rec.Code != http.StatusOK {
		t.Fatalf("filtered: want 200 got %d body=%s", rec.Code, string(body))
	}
	var filtered []map[string]any
	if err := json.Unmarshal(body, &filtered); err != nil {
		t.Fatalf("unmarshal filtered: %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("filtered: want 1 item got %d (%v)", len(filtered), filtered)
	}
	if filtered[0]["id"] != batchLineID {
		t.Errorf("filtered: want id=%s got %v", batchLineID, filtered[0]["id"])
	}
	// Sanity: the scan line item id should NOT appear.
	if filtered[0]["id"] == scanLineID {
		t.Errorf("filtered: unexpectedly returned scan line item %s", scanLineID)
	}
}

// ----------------------------------------------------------------------------
// Test 2: batch belongs to a different household → 404.
// ----------------------------------------------------------------------------

func TestReview_BatchFilter_CrossHousehold404(t *testing.T) {
	f, cleanup := newReviewFixture(t)
	defer cleanup()

	hh2Batch := insertBatch(t, f.DB, f.HH2, "hh2.csv", 0, 0)

	rec, body := doReviewJSON(t, f.Echo, "/line-items/unmatched?batch_id="+hh2Batch, f.HH1)
	if rec.Code != http.StatusNotFound {
		t.Errorf("cross-household: want 404 got %d body=%s", rec.Code, string(body))
	}

	// Count endpoint should behave identically.
	rec, body = doReviewJSON(t, f.Echo, "/line-items/unmatched/count?batch_id="+hh2Batch, f.HH1)
	if rec.Code != http.StatusNotFound {
		t.Errorf("cross-household count: want 404 got %d body=%s", rec.Code, string(body))
	}

	// GET /import/batches/:id must also 404 cross-household.
	rec, body = doReviewJSON(t, f.Echo, "/import/batches/"+hh2Batch, f.HH1)
	if rec.Code != http.StatusNotFound {
		t.Errorf("cross-household header: want 404 got %d body=%s", rec.Code, string(body))
	}
}

// ----------------------------------------------------------------------------
// Test 3: unknown batch id → 404.
// ----------------------------------------------------------------------------

func TestReview_BatchFilter_UnknownID404(t *testing.T) {
	f, cleanup := newReviewFixture(t)
	defer cleanup()

	unknown := uuid.New().String()
	rec, body := doReviewJSON(t, f.Echo, "/line-items/unmatched?batch_id="+unknown, f.HH1)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown id: want 404 got %d body=%s", rec.Code, string(body))
	}
}

// ----------------------------------------------------------------------------
// Test 4: malformed batch id → 400 (empty / too long).
// ----------------------------------------------------------------------------

func TestReview_BatchFilter_Malformed400(t *testing.T) {
	f, cleanup := newReviewFixture(t)
	defer cleanup()

	// An empty ?batch_id= is indistinguishable from "no query param" because
	// c.QueryParam returns the same "" for both — so we skip the "empty"
	// arm here and rely on the direct validateBatchID coverage from the
	// length-bound test below, plus the GET /import/batches/ case in
	// TestReview_GetBatchHeader_Malformed400.

	tooLong := ""
	for i := 0; i < maxBatchIDLen+5; i++ {
		tooLong += "a"
	}
	rec, body := doReviewJSON(t, f.Echo, "/line-items/unmatched?batch_id="+tooLong, f.HH1)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("too-long id: want 400 got %d body=%s", rec.Code, string(body))
	}

	rec, body = doReviewJSON(t, f.Echo, "/line-items/unmatched/count?batch_id="+tooLong, f.HH1)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("too-long id count: want 400 got %d body=%s", rec.Code, string(body))
	}
}

// ----------------------------------------------------------------------------
// Test 5: count matches list length.
// ----------------------------------------------------------------------------

func TestReview_BatchFilter_CountMatchesList(t *testing.T) {
	f, cleanup := newReviewFixture(t)
	defer cleanup()

	batchID := insertBatch(t, f.DB, f.HH1, "g.csv", 2, 2)
	insertReceiptWithLineItem(t, f.DB, f.HH1, batchID, "item-a")
	insertReceiptWithLineItem(t, f.DB, f.HH1, batchID, "item-b")
	// Also add an unrelated scan item to make sure the scope holds.
	insertReceiptWithLineItem(t, f.DB, f.HH1, "", "scan-only")

	rec, body := doReviewJSON(t, f.Echo, "/line-items/unmatched?batch_id="+batchID, f.HH1)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: want 200 got %d body=%s", rec.Code, string(body))
	}
	var items []map[string]any
	if err := json.Unmarshal(body, &items); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}

	rec, body = doReviewJSON(t, f.Echo, "/line-items/unmatched/count?batch_id="+batchID, f.HH1)
	if rec.Code != http.StatusOK {
		t.Fatalf("count: want 200 got %d body=%s", rec.Code, string(body))
	}
	var countResp map[string]int
	if err := json.Unmarshal(body, &countResp); err != nil {
		t.Fatalf("unmarshal count: %v", err)
	}
	if countResp["count"] != len(items) {
		t.Errorf("count %d != list len %d", countResp["count"], len(items))
	}
	if countResp["count"] != 2 {
		t.Errorf("want count=2 got %d", countResp["count"])
	}
}

// ----------------------------------------------------------------------------
// Test 6: batch header returned; cross-household 404 (covered above too).
// ----------------------------------------------------------------------------

func TestReview_GetBatchHeader_Shape(t *testing.T) {
	f, cleanup := newReviewFixture(t)
	defer cleanup()

	batchID := insertBatch(t, f.DB, f.HH1, "grocery.csv", 37, 412)
	// Populate line items: two unmatched, one matched via product_id.
	insertReceiptWithLineItem(t, f.DB, f.HH1, batchID, "a")
	insertReceiptWithLineItem(t, f.DB, f.HH1, batchID, "b")
	// Matched row: product_id set + matched='manual'.
	receiptID := uuid.New().String()
	lineItemID := uuid.New().String()
	if _, err := f.DB.Exec(
		`INSERT INTO receipts (id, household_id, receipt_date, total, status, source, import_batch_id)
		 VALUES (?, ?, '2026-03-12', '1.00', 'pending', 'import', ?)`,
		receiptID, f.HH1, batchID,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	// Insert a product so the FK holds; matched row points at it.
	productID := uuid.New().String()
	if _, err := f.DB.Exec(
		`INSERT INTO products (id, household_id, name) VALUES (?, ?, 'test')`,
		productID, f.HH1,
	); err != nil {
		t.Fatalf("insert product: %v", err)
	}
	if _, err := f.DB.Exec(
		`INSERT INTO line_items (id, receipt_id, product_id, raw_name, quantity, total_price, matched, import_batch_id)
		 VALUES (?, ?, ?, 'matched-item', '1', '1.00', 'manual', ?)`,
		lineItemID, receiptID, productID, batchID,
	); err != nil {
		t.Fatalf("insert matched line_item: %v", err)
	}

	rec, body := doReviewJSON(t, f.Echo, "/import/batches/"+batchID, f.HH1)
	if rec.Code != http.StatusOK {
		t.Fatalf("header: want 200 got %d body=%s", rec.Code, string(body))
	}
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["id"] != batchID {
		t.Errorf("id: want %s got %v", batchID, resp["id"])
	}
	if resp["filename"] != "grocery.csv" {
		t.Errorf("filename: want grocery.csv got %v", resp["filename"])
	}
	if resp["source_type"] != "spreadsheet" {
		t.Errorf("source_type: want spreadsheet got %v", resp["source_type"])
	}
	if v, _ := resp["receipts_count"].(float64); int(v) != 37 {
		t.Errorf("receipts_count: want 37 got %v", resp["receipts_count"])
	}
	if v, _ := resp["items_count"].(float64); int(v) != 412 {
		t.Errorf("items_count: want 412 got %v", resp["items_count"])
	}
	// unmatched_count reflects the live COUNT — the two unmatched rows only.
	if v, _ := resp["unmatched_count"].(float64); int(v) != 2 {
		t.Errorf("unmatched_count: want 2 (live count) got %v", resp["unmatched_count"])
	}
	// created_at must be present and RFC3339-shaped (at minimum non-empty).
	if s, _ := resp["created_at"].(string); s == "" {
		t.Errorf("created_at missing: %v", resp)
	}
}

// ----------------------------------------------------------------------------
// Test 7: malformed batch id on GET /import/batches/:id → 400.
// ----------------------------------------------------------------------------

func TestReview_GetBatchHeader_Malformed400(t *testing.T) {
	f, cleanup := newReviewFixture(t)
	defer cleanup()

	tooLong := ""
	for i := 0; i < maxBatchIDLen+5; i++ {
		tooLong += "b"
	}
	rec, body := doReviewJSON(t, f.Echo, "/import/batches/"+tooLong, f.HH1)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("too-long: want 400 got %d body=%s", rec.Code, string(body))
	}
}

// ----------------------------------------------------------------------------
// Test 8: global /review (no batch) still returns identical shape.
// ----------------------------------------------------------------------------

func TestReview_NoBatchParam_Unchanged(t *testing.T) {
	f, cleanup := newReviewFixture(t)
	defer cleanup()

	_, lineID := insertReceiptWithLineItem(t, f.DB, f.HH1, "", "scan-item")

	rec, body := doReviewJSON(t, f.Echo, "/line-items/unmatched", f.HH1)
	if rec.Code != http.StatusOK {
		t.Fatalf("no-batch: want 200 got %d body=%s", rec.Code, string(body))
	}
	var items []map[string]any
	if err := json.Unmarshal(body, &items); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("want 1 item got %d", len(items))
	}
	// The exact keys the frontend relies on must all be present — any addition
	// would break existing consumers (plan §Constraints).
	want := []string{"id", "receipt_id", "receipt_date", "raw_text", "quantity", "total_price"}
	for _, k := range want {
		if _, ok := items[0][k]; !ok {
			t.Errorf("no-batch shape: missing key %q in %v", k, items[0])
		}
	}
	if items[0]["id"] != lineID {
		t.Errorf("id mismatch: want %s got %v", lineID, items[0]["id"])
	}
}
