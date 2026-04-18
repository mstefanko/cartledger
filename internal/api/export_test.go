package api

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
)

// newExportHandler opens an in-memory-style SQLite DB, runs all migrations,
// and returns an ExportHandler wired to it. Mirrors newTestHandler to keep
// test style consistent with the rest of internal/api/.
func newExportHandler(t *testing.T) (*ExportHandler, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := db.RunMigrations(database); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	cfg := &config.Config{DataDir: dir}
	h := &ExportHandler{DB: database, Cfg: cfg}
	cleanup := func() {
		database.Close()
		os.RemoveAll(dir)
	}
	return h, cleanup
}

// seedExportHousehold inserts a household + store and returns their ids.
func seedExportHousehold(t *testing.T, h *ExportHandler, name, storeName string) (householdID, storeID string) {
	t.Helper()
	if err := h.DB.QueryRow("INSERT INTO households (name) VALUES (?) RETURNING id", name).Scan(&householdID); err != nil {
		t.Fatalf("insert household %s: %v", name, err)
	}
	if err := h.DB.QueryRow(
		"INSERT INTO stores (household_id, name) VALUES (?, ?) RETURNING id",
		householdID, storeName,
	).Scan(&storeID); err != nil {
		t.Fatalf("insert store %s: %v", storeName, err)
	}
	return
}

// seedExportReceipt inserts a receipt row and returns its id. receipt_date is
// YYYY-MM-DD. storeID may be empty to leave store_id NULL.
func seedExportReceipt(t *testing.T, h *ExportHandler, householdID, storeID, date, total, subtotal, tax string) string {
	t.Helper()
	var receiptID string
	if err := h.DB.QueryRow("SELECT lower(hex(randomblob(16)))").Scan(&receiptID); err != nil {
		t.Fatalf("gen receiptID: %v", err)
	}

	var storeArg interface{}
	if storeID == "" {
		storeArg = nil
	} else {
		storeArg = storeID
	}
	var subArg, taxArg, totalArg interface{}
	if subtotal != "" {
		subArg = subtotal
	}
	if tax != "" {
		taxArg = tax
	}
	if total != "" {
		totalArg = total
	}
	if _, err := h.DB.Exec(
		`INSERT INTO receipts (id, household_id, store_id, receipt_date, subtotal, tax, total, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 'matched')`,
		receiptID, householdID, storeArg, date, subArg, taxArg, totalArg,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	return receiptID
}

// seedExportLineItem inserts one line_items row. productID may be empty for
// unmatched items.
func seedExportLineItem(t *testing.T, h *ExportHandler, receiptID, productID, rawName, qty, unit, unitPrice, totalPrice string, lineNo int) {
	t.Helper()
	var prodArg, unitArg, unitPriceArg interface{}
	if productID != "" {
		prodArg = productID
	}
	if unit != "" {
		unitArg = unit
	}
	if unitPrice != "" {
		unitPriceArg = unitPrice
	}
	if _, err := h.DB.Exec(
		`INSERT INTO line_items (receipt_id, product_id, raw_name, quantity, unit, unit_price, total_price, matched, line_number)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		receiptID, prodArg, rawName, qty, unitArg, unitPriceArg, totalPrice,
		func() string {
			if productID != "" {
				return "auto"
			}
			return "unmatched"
		}(),
		lineNo,
	); err != nil {
		t.Fatalf("insert line_item: %v", err)
	}
}

// seedExportProduct inserts a product with optional category.
func seedExportProduct(t *testing.T, h *ExportHandler, householdID, name, category string) string {
	t.Helper()
	var id string
	var catArg interface{}
	if category != "" {
		catArg = category
	}
	if err := h.DB.QueryRow(
		"INSERT INTO products (household_id, name, category) VALUES (?, ?, ?) RETURNING id",
		householdID, name, catArg,
	).Scan(&id); err != nil {
		t.Fatalf("insert product: %v", err)
	}
	return id
}

// callExport drives the ExportReceipts handler and returns the recorder.
func callExport(t *testing.T, h *ExportHandler, householdID, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	target := "/api/v1/export/receipts"
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set(auth.ContextKeyHouseholdID, householdID)
	if err := h.ExportReceipts(c); err != nil {
		t.Fatalf("ExportReceipts: %v", err)
	}
	return rec
}

// TestExport_FormatRequired ensures a missing/invalid format is a 400.
func TestExport_FormatRequired(t *testing.T) {
	h, cleanup := newExportHandler(t)
	defer cleanup()
	hh, _ := seedExportHousehold(t, h, "HH", "Store")

	for _, q := range []string{"", "format=xyz", "format="} {
		rec := callExport(t, h, hh, q)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("query %q: expected 400, got %d: %s", q, rec.Code, rec.Body.String())
		}
	}
}

// TestExport_CSVQuoting verifies RFC 4180 escaping for commas and double
// quotes in item_name.
func TestExport_CSVQuoting(t *testing.T) {
	h, cleanup := newExportHandler(t)
	defer cleanup()
	hh, store := seedExportHousehold(t, h, "HH", "Whole Foods")
	r := seedExportReceipt(t, h, hh, store, "2026-03-14", "10.00", "", "")
	seedExportLineItem(t, h, r, "", `Bananas, "Organic"`, "2", "lb", "0.69", "1.38", 1)

	rec := callExport(t, h, hh, "format=csv")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get(echo.HeaderContentType); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("content-type: expected text/csv, got %q", ct)
	}
	if cd := rec.Header().Get(echo.HeaderContentDisposition); !strings.Contains(cd, "receipts-export-") || !strings.HasSuffix(cd, `.csv"`) {
		t.Errorf("content-disposition: got %q", cd)
	}

	// Round-trip through csv.NewReader to confirm the escaping is valid.
	reader := csv.NewReader(rec.Body)
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("csv decode: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected header + 1 row, got %d records", len(records))
	}
	want := []string{"receipt_id", "receipt_date", "store_name", "item_name", "matched_product", "quantity", "unit", "unit_price", "total_price", "category"}
	if len(records[0]) != len(want) {
		t.Fatalf("header length mismatch: got %v", records[0])
	}
	for i, h := range want {
		if records[0][i] != h {
			t.Errorf("header[%d]: expected %q, got %q", i, h, records[0][i])
		}
	}
	if records[1][3] != `Bananas, "Organic"` {
		t.Errorf("item_name round-trip: got %q", records[1][3])
	}
	if records[1][2] != "Whole Foods" {
		t.Errorf("store_name: got %q", records[1][2])
	}
}

// TestExport_EmptyCSV verifies an empty dataset still produces a valid CSV
// with only the header row (not a 500, not a 204).
func TestExport_EmptyCSV(t *testing.T) {
	h, cleanup := newExportHandler(t)
	defer cleanup()
	hh, _ := seedExportHousehold(t, h, "HH", "Store")

	rec := callExport(t, h, hh, "format=csv")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	reader := csv.NewReader(rec.Body)
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("csv decode: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("empty export: expected 1 record (header), got %d", len(records))
	}
}

// TestExport_EmptyZip verifies an empty dataset still produces a valid zip.
func TestExport_EmptyZip(t *testing.T) {
	h, cleanup := newExportHandler(t)
	defer cleanup()
	hh, _ := seedExportHousehold(t, h, "HH", "Store")

	rec := callExport(t, h, hh, "format=markdown")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("zip open: %v", err)
	}
	if len(zr.File) != 0 {
		t.Errorf("empty export: expected 0 zip entries, got %d", len(zr.File))
	}
}

// TestExport_MarkdownRendering verifies frontmatter + table, including pipe
// escaping in item names.
func TestExport_MarkdownRendering(t *testing.T) {
	h, cleanup := newExportHandler(t)
	defer cleanup()
	hh, store := seedExportHousehold(t, h, "HH", "Whole Foods Market")
	prod := seedExportProduct(t, h, hh, "Bananas", "produce")
	r := seedExportReceipt(t, h, hh, store, "2026-03-14", "10.48", "9.00", "1.48")
	seedExportLineItem(t, h, r, prod, "Organic Bananas", "2.14", "lb", "0.69", "1.48", 1)
	seedExportLineItem(t, h, r, "", "Weird | Item", "1", "", "", "3.00", 2)

	rec := callExport(t, h, hh, "format=markdown")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("zip open: %v", err)
	}
	if len(zr.File) != 1 {
		t.Fatalf("expected 1 zip entry, got %d", len(zr.File))
	}

	entry := zr.File[0]
	if entry.Name != "2026-03-14-whole-foods-market.md" {
		t.Errorf("filename: got %q", entry.Name)
	}

	f, err := entry.Open()
	if err != nil {
		t.Fatalf("open zip entry: %v", err)
	}
	raw, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := string(raw)

	mustContain := []string{
		"---\n",
		"date: 2026-03-14\n",
		"store: Whole Foods Market\n",
		"total: 10.48\n",
		"subtotal: 9.00\n",
		"tax: 1.48\n",
		"receipt_id: " + r,
		"# Whole Foods Market \u2014 2026-03-14",
		"| Item | Matched | Qty | Unit | Unit Price | Total |",
		"| Organic Bananas | Bananas | 2.14 | lb | 0.69 | 1.48 |",
		`| Weird \| Item |`,
	}
	for _, m := range mustContain {
		if !strings.Contains(content, m) {
			t.Errorf("markdown body missing %q\n---\n%s", m, content)
		}
	}
}

// TestExport_MarkdownOmitsMissingFrontmatter verifies subtotal/tax lines are
// omitted (not `subtotal: null`) when those columns are NULL.
func TestExport_MarkdownOmitsMissingFrontmatter(t *testing.T) {
	h, cleanup := newExportHandler(t)
	defer cleanup()
	hh, store := seedExportHousehold(t, h, "HH", "Trader Joes")
	r := seedExportReceipt(t, h, hh, store, "2026-03-12", "5.00", "", "")
	seedExportLineItem(t, h, r, "", "Widget", "1", "", "", "5.00", 1)

	rec := callExport(t, h, hh, "format=markdown")
	body := rec.Body.Bytes()
	zr, _ := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	f, _ := zr.File[0].Open()
	raw, _ := io.ReadAll(f)
	f.Close()
	content := string(raw)
	if strings.Contains(content, "subtotal:") {
		t.Errorf("missing subtotal should be omitted, got:\n%s", content)
	}
	if strings.Contains(content, "tax:") {
		t.Errorf("missing tax should be omitted, got:\n%s", content)
	}
	if strings.Contains(content, "null") {
		t.Errorf("no 'null' literal should appear, got:\n%s", content)
	}
}

// TestExport_DateFilter verifies receipts outside the [from, to] window are
// excluded from the CSV.
func TestExport_DateFilter(t *testing.T) {
	h, cleanup := newExportHandler(t)
	defer cleanup()
	hh, store := seedExportHousehold(t, h, "HH", "Store")

	r1 := seedExportReceipt(t, h, hh, store, "2026-01-10", "1.00", "", "")
	seedExportLineItem(t, h, r1, "", "Old", "1", "", "", "1.00", 1)
	r2 := seedExportReceipt(t, h, hh, store, "2026-03-14", "2.00", "", "")
	seedExportLineItem(t, h, r2, "", "Mid", "1", "", "", "2.00", 1)
	r3 := seedExportReceipt(t, h, hh, store, "2026-06-01", "3.00", "", "")
	seedExportLineItem(t, h, r3, "", "New", "1", "", "", "3.00", 1)

	rec := callExport(t, h, hh, "format=csv&from=2026-02-01&to=2026-04-30")
	records, err := csv.NewReader(rec.Body).ReadAll()
	if err != nil {
		t.Fatalf("csv: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected header + 1 row, got %d: %v", len(records), records)
	}
	if records[1][3] != "Mid" {
		t.Errorf("expected only 'Mid' row, got %q", records[1][3])
	}
}

// TestExport_StoreFilter verifies ?store_id=... narrows to that store only.
func TestExport_StoreFilter(t *testing.T) {
	h, cleanup := newExportHandler(t)
	defer cleanup()
	hh, s1 := seedExportHousehold(t, h, "HH", "Store1")
	var s2 string
	if err := h.DB.QueryRow("INSERT INTO stores (household_id, name) VALUES (?, 'Store2') RETURNING id", hh).Scan(&s2); err != nil {
		t.Fatalf("insert store2: %v", err)
	}

	r1 := seedExportReceipt(t, h, hh, s1, "2026-03-14", "1.00", "", "")
	seedExportLineItem(t, h, r1, "", "FromStore1", "1", "", "", "1.00", 1)
	r2 := seedExportReceipt(t, h, hh, s2, "2026-03-14", "2.00", "", "")
	seedExportLineItem(t, h, r2, "", "FromStore2", "1", "", "", "2.00", 1)

	rec := callExport(t, h, hh, "format=csv&store_id="+s1)
	records, err := csv.NewReader(rec.Body).ReadAll()
	if err != nil {
		t.Fatalf("csv: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected header + 1 row, got %d", len(records))
	}
	if records[1][3] != "FromStore1" {
		t.Errorf("expected FromStore1, got %q", records[1][3])
	}
}

// TestExport_CrossHouseholdIsolation verifies user A cannot see user B's
// receipts. This is the most load-bearing security assertion.
func TestExport_CrossHouseholdIsolation(t *testing.T) {
	h, cleanup := newExportHandler(t)
	defer cleanup()

	hhA, storeA := seedExportHousehold(t, h, "A", "StoreA")
	hhB, storeB := seedExportHousehold(t, h, "B", "StoreB")

	rA := seedExportReceipt(t, h, hhA, storeA, "2026-03-14", "1.00", "", "")
	seedExportLineItem(t, h, rA, "", "ItemA", "1", "", "", "1.00", 1)
	rB := seedExportReceipt(t, h, hhB, storeB, "2026-03-14", "9.00", "", "")
	seedExportLineItem(t, h, rB, "", "ItemB", "1", "", "", "9.00", 1)

	// Export as A — must see only ItemA.
	recA := callExport(t, h, hhA, "format=csv")
	recordsA, err := csv.NewReader(recA.Body).ReadAll()
	if err != nil {
		t.Fatalf("csv A: %v", err)
	}
	if len(recordsA) != 2 || recordsA[1][3] != "ItemA" {
		t.Fatalf("user A export leaked data: %v", recordsA)
	}

	// Even when A passes B's store_id, the household predicate filters it out.
	recCross := callExport(t, h, hhA, "format=csv&store_id="+storeB)
	crossRecs, err := csv.NewReader(recCross.Body).ReadAll()
	if err != nil {
		t.Fatalf("csv cross: %v", err)
	}
	if len(crossRecs) != 1 {
		t.Errorf("cross-household store_id leak: got %d rows", len(crossRecs)-1)
	}

	// Same check via markdown path.
	recMd := callExport(t, h, hhA, "format=markdown")
	body := recMd.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("zip: %v", err)
	}
	if len(zr.File) != 1 {
		t.Errorf("markdown cross-household leak: got %d entries", len(zr.File))
	}
	_ = rB // silence unused
}

// TestExport_FilenameCollision verifies two receipts with the same date and
// same store get distinct zip filenames (hash suffix on the loser).
func TestExport_FilenameCollision(t *testing.T) {
	h, cleanup := newExportHandler(t)
	defer cleanup()
	hh, store := seedExportHousehold(t, h, "HH", "Whole Foods")

	r1 := seedExportReceipt(t, h, hh, store, "2026-03-14", "1.00", "", "")
	seedExportLineItem(t, h, r1, "", "A", "1", "", "", "1.00", 1)
	r2 := seedExportReceipt(t, h, hh, store, "2026-03-14", "2.00", "", "")
	seedExportLineItem(t, h, r2, "", "B", "1", "", "", "2.00", 1)

	rec := callExport(t, h, hh, "format=markdown")
	body := rec.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("zip: %v", err)
	}
	if len(zr.File) != 2 {
		t.Fatalf("expected 2 zip entries, got %d", len(zr.File))
	}
	names := map[string]struct{}{}
	for _, f := range zr.File {
		names[f.Name] = struct{}{}
	}
	if len(names) != 2 {
		t.Errorf("filenames collided: %v", names)
	}
	// The first entry wins the plain name; the second gets a hash suffix.
	// Sort order: receipt_date DESC then r.id ASC — whichever receipt_id sorts
	// first is the winner, the other gets the hash suffix. Assert one of each.
	hasPlain := false
	hasHashed := false
	for n := range names {
		if n == "2026-03-14-whole-foods.md" {
			hasPlain = true
		} else if strings.HasPrefix(n, "2026-03-14-whole-foods-") && strings.HasSuffix(n, ".md") {
			hasHashed = true
		}
	}
	if !hasPlain || !hasHashed {
		t.Errorf("expected one plain + one hashed filename, got %v", names)
	}
}

// TestSlugify covers the slug helper directly so edge cases are locked down
// outside the zip round-trip (empty, punctuation-only, mixed runs).
func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Whole Foods Market": "whole-foods-market",
		"Trader Joe's":       "trader-joe-s",
		"":                   "unknown-store",
		"   ":                "unknown-store",
		"---":                "unknown-store",
		"ABC 123":            "abc-123",
		"  café  ":           "caf",
	}
	for in, want := range cases {
		got := slugify(in)
		if got != want {
			t.Errorf("slugify(%q): got %q, want %q", in, got, want)
		}
	}
}
