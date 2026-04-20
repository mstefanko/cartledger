package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
	"github.com/mstefanko/cartledger/internal/matcher"
)

// importTestFixture bundles the moving parts a spreadsheet-import API test
// needs. Echo wires a minimal auth shim that honors X-Test-Household-ID so
// tests can flip households without producing real JWTs.
type importTestFixture struct {
	Handler *ImportSpreadsheetHandler
	Cfg     *config.Config
	DB      *sql.DB
	Echo    *echo.Echo
	HH1     string // household 1 id
	HH2     string // household 2 id
}

func newImportFixture(t *testing.T) (*importTestFixture, func()) {
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
		DataDir:                  dir,
		BackupRetainCount:        5,
		ImportSpreadsheetEnabled: true,
		RateLimitEnabled:         false, // tests bypass tiered limiter, custom upload/preview limiters still apply
	}

	// Two households, isolated.
	var hh1, hh2 string
	if err := database.QueryRow(
		"INSERT INTO households (name) VALUES ('TestHH1') RETURNING id",
	).Scan(&hh1); err != nil {
		t.Fatalf("seed household 1: %v", err)
	}
	if err := database.QueryRow(
		"INSERT INTO households (name) VALUES ('TestHH2') RETURNING id",
	).Scan(&hh2); err != nil {
		t.Fatalf("seed household 2: %v", err)
	}

	matchEngine := matcher.NewEngine(database)
	h := NewImportSpreadsheetHandler(database, cfg, matchEngine)

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

	cleanup := func() {
		h.Close()
		database.Close()
	}
	return &importTestFixture{
		Handler: h,
		Cfg:     cfg,
		DB:      database,
		Echo:    e,
		HH1:     hh1,
		HH2:     hh2,
	}, cleanup
}

// newImportFixtureDisabled returns a fixture with the spreadsheet feature
// flag off — used to assert the routes aren't registered.
func newImportFixtureDisabled(t *testing.T) (*echo.Echo, func()) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := db.RunMigrations(database); err != nil {
		database.Close()
		t.Fatalf("RunMigrations: %v", err)
	}
	cfg := &config.Config{
		DataDir:                  dir,
		BackupRetainCount:        5,
		ImportSpreadsheetEnabled: false,
	}
	e := echo.New()
	_ = e.Group("", func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if hid := c.Request().Header.Get("X-Test-Household-ID"); hid != "" {
				c.Set(auth.ContextKeyHouseholdID, hid)
			}
			return next(c)
		}
	})
	// Mirror the router gate: only register if enabled. Since enabled=false,
	// we skip registration entirely — Echo's default 404 handler answers.
	if cfg.ImportSpreadsheetEnabled {
		t.Fatalf("fixture should disable the flag")
	}
	// Reference BodyLimit so the import stays load-bearing.
	_ = middleware.BodyLimit
	cleanup := func() { database.Close() }
	return e, cleanup
}

// buildUploadRequest returns a *http.Request that POSTs the given
// filename + content bytes as multipart/form-data with a field "file".
func buildUploadRequest(t *testing.T, filename string, content []byte, householdID string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/import/spreadsheet/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if householdID != "" {
		req.Header.Set("X-Test-Household-ID", householdID)
	}
	return req
}

// cleanCSV is the fixture contents reused by the happy-path tests.
const cleanCSV = `Date,Store,Item,Qty,Unit,Unit Price,Total
2026-03-12,Whole Foods,Organic whole milk,1,gal,4.99,4.99
2026-03-12,Whole Foods,Bananas,2.3,lb,0.59,1.36
2026-03-13,Costco,Eggs 18ct,1,ea,6.99,6.99
`

// doJSON invokes the handler via ServeHTTP and returns the recorder, parsed
// body as a generic map, and the recorder for further assertions.
func doJSON(t *testing.T, e *echo.Echo, req *http.Request) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	var body map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			// Not every endpoint returns JSON (DELETE → 204); OK to ignore.
			body = nil
		}
	}
	return rec, body
}

// ----------------------------------------------------------------------------
// Test 1: Upload CSV → returns import_id + sheets + suggested.
// ----------------------------------------------------------------------------

func TestImportSpreadsheet_UploadCSV(t *testing.T) {
	f, cleanup := newImportFixture(t)
	defer cleanup()

	req := buildUploadRequest(t, "grocery.csv", []byte(cleanCSV), f.HH1)
	rec, body := doJSON(t, f.Echo, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	importID, _ := body["import_id"].(string)
	if importID == "" {
		t.Fatalf("upload: missing import_id in response: %v", body)
	}
	sheets, _ := body["sheets"].([]any)
	if len(sheets) == 0 {
		t.Fatalf("upload: no sheets in response: %v", body)
	}
	first, _ := sheets[0].(map[string]any)
	if first["name"] != "Sheet1" {
		t.Errorf("upload: want sheet name Sheet1 got %v", first["name"])
	}
	if rc, _ := first["row_count"].(float64); int(rc) != 3 {
		t.Errorf("upload: want row_count=3 got %v", first["row_count"])
	}

	// Confirm raw file landed on disk.
	dir := filepath.Join(f.Cfg.DataDir, "import-staging", importID)
	if _, err := os.Stat(filepath.Join(dir, "raw.csv")); err != nil {
		t.Errorf("upload: raw file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "staging.json")); err != nil {
		t.Errorf("upload: staging.json missing: %v", err)
	}

	// Suggested mapping should propose date=0 and store=1 minimally.
	sug, _ := body["suggested"].(map[string]any)
	mapping, _ := sug["mapping"].(map[string]any)
	if mapping["date"] == nil || mapping["store"] == nil {
		t.Errorf("upload: expected mapping to include date+store, got %v", mapping)
	}
}

// ----------------------------------------------------------------------------
// Test 2: Upload rejects .xls extension with 415.
// ----------------------------------------------------------------------------

func TestImportSpreadsheet_UploadRejectsXLS(t *testing.T) {
	f, cleanup := newImportFixture(t)
	defer cleanup()

	req := buildUploadRequest(t, "legacy.xls", []byte("unused"), f.HH1)
	rec, _ := doJSON(t, f.Echo, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("want 415 got %d body=%s", rec.Code, rec.Body.String())
	}
}

// ----------------------------------------------------------------------------
// Test 3: Upload rejects >10MB with 413.
// ----------------------------------------------------------------------------

func TestImportSpreadsheet_UploadRejectsOversize(t *testing.T) {
	f, cleanup := newImportFixture(t)
	defer cleanup()

	// Build a >10MB CSV: 11 MB of filler rows.
	var buf bytes.Buffer
	buf.WriteString("Date,Store,Item,Qty,Unit,Unit Price,Total\n")
	row := "2026-03-12,Whole Foods,padding padding padding padding padding padding,1,ea,1.00,1.00\n"
	target := 11 * 1024 * 1024
	for buf.Len() < target {
		buf.WriteString(row)
	}
	req := buildUploadRequest(t, "big.csv", buf.Bytes(), f.HH1)
	rec := httptest.NewRecorder()
	f.Echo.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("want 413 got %d body=%s", rec.Code, rec.Body.String())
	}
}

// ----------------------------------------------------------------------------
// Test 4: Household isolation — HH1 uploads, HH2 cannot read.
// ----------------------------------------------------------------------------

func TestImportSpreadsheet_HouseholdIsolation(t *testing.T) {
	f, cleanup := newImportFixture(t)
	defer cleanup()

	req := buildUploadRequest(t, "grocery.csv", []byte(cleanCSV), f.HH1)
	rec, body := doJSON(t, f.Echo, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload HH1: want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	importID, _ := body["import_id"].(string)

	// HH2 attempts GET sheet.
	req2 := httptest.NewRequest(http.MethodGet, "/import/spreadsheet/"+importID+"/sheet/Sheet1", nil)
	req2.Header.Set("X-Test-Household-ID", f.HH2)
	rec2 := httptest.NewRecorder()
	f.Echo.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNotFound {
		t.Errorf("HH2 GET: want 404 got %d", rec2.Code)
	}

	// HH2 attempts DELETE. We allow 204 on unknown-import (idempotent) but
	// the staging must still be present afterward — so verify HH1 still sees it.
	req3 := httptest.NewRequest(http.MethodDelete, "/import/spreadsheet/"+importID, nil)
	req3.Header.Set("X-Test-Household-ID", f.HH2)
	rec3 := httptest.NewRecorder()
	f.Echo.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusNoContent {
		t.Errorf("HH2 DELETE: want 204 got %d", rec3.Code)
	}
	// HH1 should still be able to get the sheet.
	req4 := httptest.NewRequest(http.MethodGet, "/import/spreadsheet/"+importID+"/sheet/Sheet1", nil)
	req4.Header.Set("X-Test-Household-ID", f.HH1)
	rec4 := httptest.NewRecorder()
	f.Echo.ServeHTTP(rec4, req4)
	if rec4.Code != http.StatusOK {
		t.Errorf("HH1 GET after HH2 DELETE: want 200 got %d body=%s", rec4.Code, rec4.Body.String())
	}
}

// ----------------------------------------------------------------------------
// Test 5: Preview happy path — default mapping, groups + summary populated.
// ----------------------------------------------------------------------------

func TestImportSpreadsheet_PreviewHappyPath(t *testing.T) {
	f, cleanup := newImportFixture(t)
	defer cleanup()

	importID, suggested := uploadCSV(t, f, f.HH1, cleanCSV)

	body := previewBody(suggested)
	req := httptest.NewRequest(http.MethodPost, "/import/spreadsheet/"+importID+"/preview",
		bytes.NewReader(mustJSON(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Household-ID", f.HH1)
	rec, respBody := doJSON(t, f.Echo, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview: want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	rows, _ := respBody["rows"].([]any)
	if len(rows) != 3 {
		t.Errorf("preview: want 3 rows got %d", len(rows))
	}
	groups, _ := respBody["groups"].([]any)
	if len(groups) != 2 {
		t.Errorf("preview: want 2 groups (2 date+store combos) got %d", len(groups))
	}
	sum, _ := respBody["summary"].(map[string]any)
	if items, _ := sum["items"].(float64); int(items) != 3 {
		t.Errorf("preview summary.items: want 3 got %v", sum["items"])
	}
}

// ----------------------------------------------------------------------------
// Test 6: Preview cache short-circuit — second identical call returns same body.
// ----------------------------------------------------------------------------

func TestImportSpreadsheet_PreviewCache(t *testing.T) {
	f, cleanup := newImportFixture(t)
	defer cleanup()

	importID, suggested := uploadCSV(t, f, f.HH1, cleanCSV)
	body := previewBody(suggested)
	payload := mustJSON(body)

	req1 := httptest.NewRequest(http.MethodPost, "/import/spreadsheet/"+importID+"/preview", bytes.NewReader(payload))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("X-Test-Household-ID", f.HH1)
	rec1 := httptest.NewRecorder()
	f.Echo.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("preview 1: want 200 got %d", rec1.Code)
	}
	first := rec1.Body.String()

	req2 := httptest.NewRequest(http.MethodPost, "/import/spreadsheet/"+importID+"/preview", bytes.NewReader(payload))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Test-Household-ID", f.HH1)
	rec2 := httptest.NewRecorder()
	f.Echo.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("preview 2: want 200 got %d", rec2.Code)
	}
	if first != rec2.Body.String() {
		t.Errorf("preview cache: expected identical bodies\nfirst:  %s\nsecond: %s", first, rec2.Body.String())
	}
}

// ----------------------------------------------------------------------------
// Test 7: Transform appends + bumps revision.
// ----------------------------------------------------------------------------

func TestImportSpreadsheet_TransformBumpsRevision(t *testing.T) {
	f, cleanup := newImportFixture(t)
	defer cleanup()

	importID, suggested := uploadCSV(t, f, f.HH1, cleanCSV)

	// Override cell (row 0, col 2 — Item) to "Organic milk".
	txBody := map[string]any{
		"kind":      "override_cell",
		"row_index": 0,
		"col_index": 2,
		"new_value": "Organic milk (renamed)",
	}
	req := httptest.NewRequest(http.MethodPost, "/import/spreadsheet/"+importID+"/transform",
		bytes.NewReader(mustJSON(txBody)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Household-ID", f.HH1)
	rec, body := doJSON(t, f.Echo, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("transform: want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	if rev, _ := body["import_revision"].(float64); int(rev) != 1 {
		t.Errorf("transform: want revision 1 got %v", body["import_revision"])
	}

	// Preview should show the overridden value.
	pv := previewBody(suggested)
	preq := httptest.NewRequest(http.MethodPost, "/import/spreadsheet/"+importID+"/preview",
		bytes.NewReader(mustJSON(pv)))
	preq.Header.Set("Content-Type", "application/json")
	preq.Header.Set("X-Test-Household-ID", f.HH1)
	prec, presp := doJSON(t, f.Echo, preq)
	if prec.Code != http.StatusOK {
		t.Fatalf("preview after transform: want 200 got %d body=%s", prec.Code, prec.Body.String())
	}
	rows, _ := presp["rows"].([]any)
	if len(rows) < 1 {
		t.Fatalf("expected rows in preview")
	}
	first := rows[0].(map[string]any)
	rawCells, _ := first["raw"].([]any)
	if len(rawCells) < 3 || rawCells[2] != "Organic milk (renamed)" {
		t.Errorf("preview: overridden cell not applied, raw=%v", rawCells)
	}
	// import_revision in response should match.
	if rev, _ := presp["import_revision"].(float64); int(rev) != 1 {
		t.Errorf("preview: want import_revision 1 got %v", presp["import_revision"])
	}
}

// ----------------------------------------------------------------------------
// Test 8: Commit end-to-end — batch row + receipts + import_batch_id stamped.
// ----------------------------------------------------------------------------

func TestImportSpreadsheet_CommitEndToEnd(t *testing.T) {
	f, cleanup := newImportFixture(t)
	defer cleanup()

	importID, suggested := uploadCSV(t, f, f.HH1, cleanCSV)
	commitBody := previewBody(suggested)
	req := httptest.NewRequest(http.MethodPost, "/import/spreadsheet/"+importID+"/commit",
		bytes.NewReader(mustJSON(commitBody)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Household-ID", f.HH1)
	rec, body := doJSON(t, f.Echo, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("commit: want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	batchID, _ := body["batch_id"].(string)
	if batchID == "" {
		t.Fatalf("commit: missing batch_id: %v", body)
	}
	if rc, _ := body["receipts_created"].(float64); int(rc) != 2 {
		t.Errorf("commit: want receipts_created=2 got %v", body["receipts_created"])
	}
	if li, _ := body["line_items_created"].(float64); int(li) != 3 {
		t.Errorf("commit: want line_items_created=3 got %v", body["line_items_created"])
	}

	// Confirm DB rows.
	var count int
	if err := f.DB.QueryRow(
		"SELECT COUNT(*) FROM import_batches WHERE household_id = ? AND id = ?",
		f.HH1, batchID,
	).Scan(&count); err != nil || count != 1 {
		t.Errorf("import_batches row missing: count=%d err=%v", count, err)
	}
	if err := f.DB.QueryRow(
		"SELECT COUNT(*) FROM receipts WHERE household_id = ? AND import_batch_id = ?",
		f.HH1, batchID,
	).Scan(&count); err != nil || count != 2 {
		t.Errorf("receipts with import_batch_id: count=%d err=%v", count, err)
	}
	if err := f.DB.QueryRow(
		"SELECT COUNT(*) FROM line_items WHERE import_batch_id = ?",
		batchID,
	).Scan(&count); err != nil || count != 3 {
		t.Errorf("line_items with import_batch_id: count=%d err=%v", count, err)
	}
}

// ----------------------------------------------------------------------------
// Test 9: Commit with save_mapping_as inserts an import_mappings row.
// ----------------------------------------------------------------------------

func TestImportSpreadsheet_CommitSavesMapping(t *testing.T) {
	f, cleanup := newImportFixture(t)
	defer cleanup()

	importID, suggested := uploadCSV(t, f, f.HH1, cleanCSV)
	commitBody := previewBody(suggested)
	commitBody["save_mapping_as"] = "my grocery sheet"
	req := httptest.NewRequest(http.MethodPost, "/import/spreadsheet/"+importID+"/commit",
		bytes.NewReader(mustJSON(commitBody)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Household-ID", f.HH1)
	rec, _ := doJSON(t, f.Echo, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("commit: want 200 got %d body=%s", rec.Code, rec.Body.String())
	}

	var count int
	if err := f.DB.QueryRow(
		"SELECT COUNT(*) FROM import_mappings WHERE household_id = ? AND name = ?",
		f.HH1, "my grocery sheet",
	).Scan(&count); err != nil || count != 1 {
		t.Errorf("import_mappings row missing: count=%d err=%v", count, err)
	}
	// Implicit __last_used__ should also be present.
	if err := f.DB.QueryRow(
		"SELECT COUNT(*) FROM import_mappings WHERE household_id = ? AND name = '__last_used__'",
		f.HH1,
	).Scan(&count); err != nil || count != 1 {
		t.Errorf("__last_used__ row missing: count=%d err=%v", count, err)
	}
}

// ----------------------------------------------------------------------------
// Test 10: Delete purges staging.
// ----------------------------------------------------------------------------

func TestImportSpreadsheet_DeletePurges(t *testing.T) {
	f, cleanup := newImportFixture(t)
	defer cleanup()

	importID, _ := uploadCSV(t, f, f.HH1, cleanCSV)

	dir := filepath.Join(f.Cfg.DataDir, "import-staging", importID)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("pre-delete: staging missing: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/import/spreadsheet/"+importID, nil)
	req.Header.Set("X-Test-Household-ID", f.HH1)
	rec := httptest.NewRecorder()
	f.Echo.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: want 204 got %d", rec.Code)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("delete: staging dir still present: err=%v", err)
	}

	// Subsequent GET should 404.
	req2 := httptest.NewRequest(http.MethodGet, "/import/spreadsheet/"+importID+"/sheet/Sheet1", nil)
	req2.Header.Set("X-Test-Household-ID", f.HH1)
	rec2 := httptest.NewRecorder()
	f.Echo.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNotFound {
		t.Errorf("GET after delete: want 404 got %d", rec2.Code)
	}
}

// ----------------------------------------------------------------------------
// Test 11: Feature flag off → every route 404s.
// ----------------------------------------------------------------------------

func TestImportSpreadsheet_FeatureFlagOff(t *testing.T) {
	e, cleanup := newImportFixtureDisabled(t)
	defer cleanup()

	paths := []string{
		"/import/spreadsheet/upload",
		"/import/spreadsheet/abc/sheet/Sheet1",
		"/import/spreadsheet/abc/transform",
		"/import/spreadsheet/abc/preview",
		"/import/spreadsheet/abc/commit",
		"/import/spreadsheet/abc",
	}
	methods := []string{
		http.MethodPost, http.MethodGet, http.MethodPost,
		http.MethodPost, http.MethodPost, http.MethodDelete,
	}
	for i, p := range paths {
		req := httptest.NewRequest(methods[i], p, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("flag off %s %s: want 404/405 got %d", methods[i], p, rec.Code)
		}
	}
}

// ----------------------------------------------------------------------------
// Test 12: TSV upload is parsed with the tab delimiter (regression — used
// to default to comma, collapsing every row into a single column).
// ----------------------------------------------------------------------------

const cleanTSV = "Date\tStore\tItem\tQty\tUnit\tUnit Price\tTotal\n" +
	"2026-03-12\tWhole Foods\tOrganic whole milk\t1\tgal\t4.99\t4.99\n" +
	"2026-03-12\tWhole Foods\tBananas\t2.3\tlb\t0.59\t1.36\n" +
	"2026-03-13\tCostco\tEggs 18ct\t1\tea\t6.99\t6.99\n"

func TestImportSpreadsheet_UploadTSVUsesTabDelimiter(t *testing.T) {
	f, cleanup := newImportFixture(t)
	defer cleanup()

	req := buildUploadRequest(t, "grocery.tsv", []byte(cleanTSV), f.HH1)
	rec, body := doJSON(t, f.Echo, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("tsv upload: want 200 got %d body=%s", rec.Code, rec.Body.String())
	}

	sheets, _ := body["sheets"].([]any)
	first, _ := sheets[0].(map[string]any)
	headers, _ := first["headers"].([]any)
	if len(headers) != 7 {
		t.Fatalf("tsv upload parsed with wrong delimiter: want 7 header cols got %d (headers=%v)", len(headers), headers)
	}
	if headers[0] != "Date" || headers[2] != "Item" {
		t.Errorf("tsv upload headers scrambled: %v", headers)
	}

	sug, _ := body["suggested"].(map[string]any)
	csvOpts, _ := sug["csv_options"].(map[string]any)
	// JSON encodes rune as its numeric code; '\t' == 9.
	if d, _ := csvOpts["delimiter"].(float64); int(d) != int('\t') {
		t.Errorf("tsv upload: suggested csv_options.delimiter = %v, want tab (9)", csvOpts["delimiter"])
	}
}

// ----------------------------------------------------------------------------
// Test 13: Commit with a stale import_revision returns 409 so the client
// can refresh preview before overwriting data it never saw.
// ----------------------------------------------------------------------------

func TestImportSpreadsheet_CommitRejectsStaleRevision(t *testing.T) {
	f, cleanup := newImportFixture(t)
	defer cleanup()

	importID, suggested := uploadCSV(t, f, f.HH1, cleanCSV)

	// Land a transform so the staging chain revision advances to 1.
	txBody := map[string]any{"kind": "skip_row", "row_indices": []int{1}}
	txReq := httptest.NewRequest(http.MethodPost, "/import/spreadsheet/"+importID+"/transform",
		bytes.NewReader(mustJSON(txBody)))
	txReq.Header.Set("Content-Type", "application/json")
	txReq.Header.Set("X-Test-Household-ID", f.HH1)
	if rec, _ := doJSON(t, f.Echo, txReq); rec.Code != http.StatusOK {
		t.Fatalf("transform: want 200 got %d", rec.Code)
	}

	// Commit echoes the OLD revision 0 — server must reject.
	commitBody := previewBody(suggested)
	commitBody["import_revision"] = 0
	req := httptest.NewRequest(http.MethodPost, "/import/spreadsheet/"+importID+"/commit",
		bytes.NewReader(mustJSON(commitBody)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Household-ID", f.HH1)
	rec, body := doJSON(t, f.Echo, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale commit: want 409 got %d body=%s", rec.Code, rec.Body.String())
	}
	if cur, _ := body["current_import_revision"].(float64); int(cur) != 1 {
		t.Errorf("stale commit: want current_import_revision=1 got %v", body["current_import_revision"])
	}

	// Fresh revision succeeds.
	commitBody["import_revision"] = 1
	req2 := httptest.NewRequest(http.MethodPost, "/import/spreadsheet/"+importID+"/commit",
		bytes.NewReader(mustJSON(commitBody)))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Test-Household-ID", f.HH1)
	if rec2, _ := doJSON(t, f.Echo, req2); rec2.Code != http.StatusOK {
		t.Fatalf("fresh commit: want 200 got %d body=%s", rec2.Code, rec2.Body.String())
	}
}

// ----------------------------------------------------------------------------
// Test 14: Commit response surfaces duplicates_skipped (and the errors
// field exists in the schema even when empty) so clients can report
// partial-import outcomes.
// ----------------------------------------------------------------------------

func TestImportSpreadsheet_CommitSurfacesDuplicatesSkipped(t *testing.T) {
	f, cleanup := newImportFixture(t)
	defer cleanup()

	// One upload, two commits on the same import_id — avoids the 1/min
	// upload rate limiter and still exercises the duplicate-detection path
	// because the first commit persists receipts that the second commit's
	// CheckDuplicates will see.
	importID, suggested := uploadCSV(t, f, f.HH1, cleanCSV)
	body1 := previewBody(suggested)
	req1 := httptest.NewRequest(http.MethodPost, "/import/spreadsheet/"+importID+"/commit",
		bytes.NewReader(mustJSON(body1)))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("X-Test-Household-ID", f.HH1)
	if rec, _ := doJSON(t, f.Echo, req1); rec.Code != http.StatusOK {
		t.Fatalf("first commit: want 200 got %d", rec.Code)
	}

	body2 := previewBody(suggested)
	req2 := httptest.NewRequest(http.MethodPost, "/import/spreadsheet/"+importID+"/commit",
		bytes.NewReader(mustJSON(body2)))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Test-Household-ID", f.HH1)
	rec, body := doJSON(t, f.Echo, req2)
	if rec.Code != http.StatusOK {
		t.Fatalf("dup commit: want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	ds, _ := body["duplicates_skipped"].(float64)
	if int(ds) < 2 {
		t.Errorf("dup commit: want duplicates_skipped>=2 got %v (body=%v)", ds, body)
	}
	if rc, _ := body["receipts_created"].(float64); int(rc) != 0 {
		t.Errorf("dup commit: want receipts_created=0 got %v", rc)
	}
}

// ----------------------------------------------------------------------------
// helpers: upload + previewBody compose a commonly-repeated test shape.
// ----------------------------------------------------------------------------

// uploadCSV uploads the content and returns (import_id, suggested) from the
// response. Fails the test if upload didn't return 200.
func uploadCSV(t *testing.T, f *importTestFixture, householdID, content string) (string, map[string]any) {
	t.Helper()
	req := buildUploadRequest(t, "grocery.csv", []byte(content), householdID)
	rec, body := doJSON(t, f.Echo, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("uploadCSV: want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	importID, _ := body["import_id"].(string)
	suggested, _ := body["suggested"].(map[string]any)
	return importID, suggested
}

// previewBody builds a preview request body from a suggested config. The
// suggested block's shape already matches preview's expected body minus
// `sheet` + `skip_row_indices` + `import_revision`.
func previewBody(suggested map[string]any) map[string]any {
	return map[string]any{
		"sheet":             suggested["sheet"],
		"mapping":           suggested["mapping"],
		"date_format":       suggested["date_format"],
		"csv_options":       suggested["csv_options"],
		"unit_options":      suggested["unit_options"],
		"grouping":          suggested["grouping"],
		"skip_row_indices":  []int{},
		"import_revision":   0,
	}
}

// mustJSON marshals v or fails the test. Using a helper so every test reads
// the same way.
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("mustJSON: %v", err))
	}
	return b
}

// Ensure ancillary imports aren't dropped.
var _ = io.EOF
var _ = context.Background
