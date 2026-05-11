package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
)

type compareFixture struct {
	Handler *ReceiptHandler
	HH1     string
	HH2     string
	StoreA  string
	StoreB  string
	cleanup func()
}

func newCompareFixture(t *testing.T) *compareFixture {
	t.Helper()
	ph, _, cleanup := newTestHandler(t)
	h := &ReceiptHandler{DB: ph.DB, Cfg: ph.Cfg}

	var hh1, hh2 string
	if err := h.DB.QueryRow("INSERT INTO households (name) VALUES ('Compare HH1') RETURNING id").Scan(&hh1); err != nil {
		t.Fatalf("insert hh1: %v", err)
	}
	if err := h.DB.QueryRow("INSERT INTO households (name) VALUES ('Compare HH2') RETURNING id").Scan(&hh2); err != nil {
		t.Fatalf("insert hh2: %v", err)
	}

	var storeA, storeB string
	if err := h.DB.QueryRow("INSERT INTO stores (household_id, name) VALUES (?, 'Costco') RETURNING id", hh1).Scan(&storeA); err != nil {
		t.Fatalf("insert storeA: %v", err)
	}
	if err := h.DB.QueryRow("INSERT INTO stores (household_id, name) VALUES (?, 'ShopRite') RETURNING id", hh1).Scan(&storeB); err != nil {
		t.Fatalf("insert storeB: %v", err)
	}

	return &compareFixture{
		Handler: h,
		HH1:     hh1,
		HH2:     hh2,
		StoreA:  storeA,
		StoreB:  storeB,
		cleanup: cleanup,
	}
}

func (f *compareFixture) close() {
	f.cleanup()
}

func compareProduct(t *testing.T, f *compareFixture, householdID, name, category string) string {
	t.Helper()
	var id string
	if err := f.Handler.DB.QueryRow(
		"INSERT INTO products (household_id, name, category) VALUES (?, ?, ?) RETURNING id",
		householdID, name, category,
	).Scan(&id); err != nil {
		t.Fatalf("insert product %q: %v", name, err)
	}
	return id
}

func compareGroup(t *testing.T, f *compareFixture, householdID, name string) string {
	t.Helper()
	var id string
	if err := f.Handler.DB.QueryRow(
		"INSERT INTO product_groups (id, household_id, name) VALUES (?, ?, ?) RETURNING id",
		uuid.New().String(), householdID, name,
	).Scan(&id); err != nil {
		t.Fatalf("insert group %q: %v", name, err)
	}
	return id
}

func assignCompareGroup(t *testing.T, f *compareFixture, productID, groupID string) {
	t.Helper()
	if _, err := f.Handler.DB.Exec("UPDATE products SET product_group_id = ? WHERE id = ?", groupID, productID); err != nil {
		t.Fatalf("assign group: %v", err)
	}
}

func compareReceipt(t *testing.T, f *compareFixture, householdID, storeID, date, total, status string) string {
	t.Helper()
	id := uuid.New().String()
	var storeArg any
	if storeID != "" {
		storeArg = storeID
	}
	if _, err := f.Handler.DB.Exec(
		`INSERT INTO receipts (id, household_id, store_id, receipt_date, total, status)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, householdID, storeArg, date, total, status,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	return id
}

func compareLine(t *testing.T, f *compareFixture, receiptID, productID, rawName, quantity, unit, totalPrice string) string {
	t.Helper()
	id := uuid.New().String()
	var productArg any
	if productID != "" {
		productArg = productID
	}
	var unitArg any
	if strings.TrimSpace(unit) != "" {
		unitArg = unit
	}
	if _, err := f.Handler.DB.Exec(
		`INSERT INTO line_items (id, receipt_id, product_id, raw_name, quantity, unit, total_price, matched)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, receiptID, productArg, rawName, quantity, unitArg, totalPrice, "manual",
	); err != nil {
		t.Fatalf("insert line: %v", err)
	}
	return id
}

func callCompareReceipts(t *testing.T, h *ReceiptHandler, householdID, body string) (int, compareReceiptsResponse, string) {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/receipts/compare", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set(auth.ContextKeyHouseholdID, householdID)
	if err := h.compareReceipts(c); err != nil {
		t.Fatalf("compareReceipts error: %v", err)
	}

	var resp compareReceiptsResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal success: %v\n%s", err, rec.Body.String())
		}
	}
	return rec.Code, resp, rec.Body.String()
}

func compareBody(ids []string, minOverlap *int) string {
	if minOverlap == nil {
		return fmt.Sprintf(`{"receipt_ids":%s}`, mustCompareJSON(ids))
	}
	return fmt.Sprintf(`{"receipt_ids":%s,"min_overlap":%d}`, mustCompareJSON(ids), *minOverlap)
}

func mustCompareJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestCompareReceipts_NormalizesMixedUnitsAndStars(t *testing.T) {
	f := newCompareFixture(t)
	defer f.close()

	cheerios := compareProduct(t, f, f.HH1, "Cheerios", "cereal")
	milk := compareProduct(t, f, f.HH1, "Milk", "dairy")
	r1 := compareReceipt(t, f, f.HH1, f.StoreA, "2026-04-12", "142.55", "matched")
	r2 := compareReceipt(t, f, f.HH1, f.StoreB, "2026-04-15", "82.10", "reviewed")
	cheeriosA := compareLine(t, f, r1, cheerios, "GM CHEERIOS 32OZ", "32", "oz", "4.99")
	compareLine(t, f, r2, cheerios, "CHEERIOS 1LB", "1", "lb", "2.99")
	milkA := compareLine(t, f, r1, milk, "MILK GALLON", "1", "gal", "4.00")
	compareLine(t, f, r2, milk, "MILK HALF GAL", "64", "fl oz", "3.00")

	code, resp, body := callCompareReceipts(t, f.Handler, f.HH1, compareBody([]string{r1, r2}, nil))
	if code != http.StatusOK {
		t.Fatalf("want 200 got %d: %s", code, body)
	}
	if len(resp.Receipts) != 2 || resp.Receipts[0].ID != r1 || resp.Receipts[1].ID != r2 {
		t.Fatalf("receipts not returned in requested order: %+v", resp.Receipts)
	}
	if resp.Receipts[0].LineCount != 2 || resp.Receipts[1].LineCount != 2 {
		t.Fatalf("line counts = %d/%d, want 2/2", resp.Receipts[0].LineCount, resp.Receipts[1].LineCount)
	}
	if resp.MissingUnitCount != 0 {
		t.Fatalf("missing_unit_count = %d, want 0", resp.MissingUnitCount)
	}
	if len(resp.Products) != 2 {
		t.Fatalf("products = %d, want 2: %s", len(resp.Products), body)
	}

	gotCheerios := resp.Products[0]
	if gotCheerios.Name != "Cheerios" || gotCheerios.ComparableUnit == nil || *gotCheerios.ComparableUnit != "oz" {
		t.Fatalf("cheerios row mismatch: %+v", gotCheerios)
	}
	if gotCheerios.BestAppearance == nil || *gotCheerios.BestAppearance != cheeriosA {
		t.Fatalf("cheerios best appearance = %v, want %s", gotCheerios.BestAppearance, cheeriosA)
	}
	if !gotCheerios.Appearances[0].SizeKnown || gotCheerios.Appearances[0].NormalizedPrice == nil {
		t.Fatalf("cheerios first appearance should be normalized: %+v", gotCheerios.Appearances[0])
	}

	gotMilk := resp.Products[1]
	if gotMilk.Name != "Milk" || gotMilk.ComparableUnit == nil || *gotMilk.ComparableUnit != "fl_oz" {
		t.Fatalf("milk row mismatch: %+v", gotMilk)
	}
	if gotMilk.BestAppearance == nil || *gotMilk.BestAppearance != milkA {
		t.Fatalf("milk best appearance = %v, want %s", gotMilk.BestAppearance, milkA)
	}
}

func TestCompareReceipts_MinOverlapFiltersAndSorts(t *testing.T) {
	f := newCompareFixture(t)
	defer f.close()

	apples := compareProduct(t, f, f.HH1, "Apples", "produce")
	bananas := compareProduct(t, f, f.HH1, "Bananas", "produce")
	carrots := compareProduct(t, f, f.HH1, "Carrots", "produce")
	receipts := []string{
		compareReceipt(t, f, f.HH1, f.StoreA, "2026-04-01", "10.00", "matched"),
		compareReceipt(t, f, f.HH1, f.StoreA, "2026-04-08", "11.00", "matched"),
		compareReceipt(t, f, f.HH1, f.StoreB, "2026-04-15", "12.00", "matched"),
		compareReceipt(t, f, f.HH1, f.StoreB, "2026-04-22", "13.00", "matched"),
	}
	for i, receiptID := range receipts {
		compareLine(t, f, receiptID, apples, "APPLES", "16", "oz", "3.00")
		if i < 3 {
			compareLine(t, f, receiptID, bananas, "BANANAS", "16", "oz", "2.00")
		}
		if i < 2 {
			compareLine(t, f, receiptID, carrots, "CARROTS", "16", "oz", "1.00")
		}
	}

	minOverlap := 3
	code, resp, body := callCompareReceipts(t, f.Handler, f.HH1, compareBody(receipts, &minOverlap))
	if code != http.StatusOK {
		t.Fatalf("want 200 got %d: %s", code, body)
	}
	if len(resp.Products) != 2 {
		t.Fatalf("products = %d, want 2: %s", len(resp.Products), body)
	}
	if resp.Products[0].Name != "Apples" || len(resp.Products[0].Appearances) != 4 {
		t.Fatalf("first row = %+v, want Apples with 4 appearances", resp.Products[0])
	}
	if resp.Products[1].Name != "Bananas" || len(resp.Products[1].Appearances) != 3 {
		t.Fatalf("second row = %+v, want Bananas with 3 appearances", resp.Products[1])
	}
}

func TestCompareReceipts_UnknownUnitsDoNotNormalizeOrStar(t *testing.T) {
	f := newCompareFixture(t)
	defer f.close()

	eggs := compareProduct(t, f, f.HH1, "Eggs", "dairy")
	r1 := compareReceipt(t, f, f.HH1, f.StoreA, "2026-04-01", "3.00", "matched")
	r2 := compareReceipt(t, f, f.HH1, f.StoreB, "2026-04-08", "2.50", "matched")
	compareLine(t, f, r1, eggs, "EGGS DOZEN", "12", "each", "3.00")
	compareLine(t, f, r2, eggs, "EGGS", "1", "", "2.50")

	code, resp, body := callCompareReceipts(t, f.Handler, f.HH1, compareBody([]string{r1, r2}, nil))
	if code != http.StatusOK {
		t.Fatalf("want 200 got %d: %s", code, body)
	}
	if resp.MissingUnitCount != 2 {
		t.Fatalf("missing_unit_count = %d, want 2", resp.MissingUnitCount)
	}
	if len(resp.Products) != 1 {
		t.Fatalf("products = %d, want 1", len(resp.Products))
	}
	row := resp.Products[0]
	if row.ComparableUnit != nil || row.BestAppearance != nil {
		t.Fatalf("unknown/count units should not produce star metadata: %+v", row)
	}
	for _, app := range row.Appearances {
		if app.SizeKnown || app.NormalizedPrice != nil || app.NormalizedUnit != nil {
			t.Fatalf("appearance should not be normalized: %+v", app)
		}
	}
}

func TestCompareReceipts_UsesProductPackageSizeForCountLines(t *testing.T) {
	f := newCompareFixture(t)
	defer f.close()

	cheerios := compareProduct(t, f, f.HH1, "Cheerios", "cereal")
	if _, err := f.Handler.DB.Exec("UPDATE products SET pack_quantity = 32, pack_unit = 'oz' WHERE id = ?", cheerios); err != nil {
		t.Fatalf("update product pack: %v", err)
	}
	r1 := compareReceipt(t, f, f.HH1, f.StoreA, "2026-04-01", "8.99", "matched")
	r2 := compareReceipt(t, f, f.HH1, f.StoreB, "2026-04-08", "7.99", "matched")
	firstID := compareLine(t, f, r1, cheerios, "GM CHEERIOS", "1", "each", "8.99")
	compareLine(t, f, r2, cheerios, "CHEERIOS", "1", "", "7.99")

	code, resp, body := callCompareReceipts(t, f.Handler, f.HH1, compareBody([]string{r1, r2}, nil))
	if code != http.StatusOK {
		t.Fatalf("want 200 got %d: %s", code, body)
	}
	if resp.MissingUnitCount != 0 {
		t.Fatalf("missing_unit_count = %d, want 0", resp.MissingUnitCount)
	}
	row := resp.Products[0]
	if row.ComparableUnit == nil || *row.ComparableUnit != "oz" {
		t.Fatalf("comparable unit = %v, want oz", row.ComparableUnit)
	}
	if row.Appearances[0].NormalizedPrice == nil || *row.Appearances[0].NormalizedPrice != "0.2809375" {
		t.Fatalf("first normalized price = %+v, want 0.2809375", row.Appearances[0])
	}
	if row.BestAppearance == nil || *row.BestAppearance == firstID {
		t.Fatalf("best appearance = %v, want second receipt", row.BestAppearance)
	}
}

func TestCompareReceipts_AggregatesDuplicateLines(t *testing.T) {
	f := newCompareFixture(t)
	defer f.close()

	oats := compareProduct(t, f, f.HH1, "Oats", "pantry")
	coffee := compareProduct(t, f, f.HH1, "Coffee", "pantry")
	r1 := compareReceipt(t, f, f.HH1, f.StoreA, "2026-04-01", "20.00", "matched")
	r2 := compareReceipt(t, f, f.HH1, f.StoreB, "2026-04-08", "20.00", "matched")

	oatsPrimary := compareLine(t, f, r1, oats, "OATS 8OZ", "8", "oz", "1.00")
	compareLine(t, f, r1, oats, "OATS 4OZ", "4", "oz", "0.75")
	compareLine(t, f, r2, oats, "OATS 16OZ", "16", "oz", "2.50")

	compareLine(t, f, r1, coffee, "COFFEE 8OZ", "8", "oz", "4.00")
	compareLine(t, f, r1, coffee, "COFFEE 1LB", "1", "lb", "8.00")
	compareLine(t, f, r2, coffee, "COFFEE 8OZ", "8", "oz", "5.00")

	code, resp, body := callCompareReceipts(t, f.Handler, f.HH1, compareBody([]string{r1, r2}, nil))
	if code != http.StatusOK {
		t.Fatalf("want 200 got %d: %s", code, body)
	}
	if len(resp.Products) != 2 {
		t.Fatalf("products = %d, want 2: %s", len(resp.Products), body)
	}

	coffeeRow := resp.Products[0]
	if coffeeRow.Name != "Coffee" {
		t.Fatalf("first row = %s, want Coffee", coffeeRow.Name)
	}
	if coffeeRow.Appearances[0].TotalPrice != "12" || coffeeRow.Appearances[0].Quantity != nil || coffeeRow.Appearances[0].Unit != nil {
		t.Fatalf("conflicting-unit aggregate should keep total only: %+v", coffeeRow.Appearances[0])
	}
	if len(coffeeRow.Appearances[0].Lines) != 2 {
		t.Fatalf("conflicting-unit aggregate lines = %+v, want two selectable lines", coffeeRow.Appearances[0].Lines)
	}
	if coffeeRow.ComparableUnit != nil || coffeeRow.BestAppearance != nil {
		t.Fatalf("only one normalized coffee cell should not star: %+v", coffeeRow)
	}

	oatsRow := resp.Products[1]
	if oatsRow.Name != "Oats" {
		t.Fatalf("second row = %s, want Oats", oatsRow.Name)
	}
	first := oatsRow.Appearances[0]
	if first.LineItemID != oatsPrimary || first.TotalPrice != "1.75" || first.Quantity == nil || *first.Quantity != "12" || first.Unit == nil || *first.Unit != "oz" {
		t.Fatalf("same-unit aggregate mismatch: %+v", first)
	}
	lineNames := map[string]bool{}
	for _, line := range first.Lines {
		lineNames[line.RawName] = true
	}
	if len(first.Lines) != 2 || !lineNames["OATS 8OZ"] || !lineNames["OATS 4OZ"] {
		t.Fatalf("same-unit aggregate line picker mismatch: %+v", first.Lines)
	}
	if oatsRow.BestAppearance == nil || *oatsRow.BestAppearance != oatsPrimary {
		t.Fatalf("oats best appearance = %v, want %s", oatsRow.BestAppearance, oatsPrimary)
	}
}

func TestCompareReceipts_ProductGroupCollapseAndUnmatchedExcluded(t *testing.T) {
	f := newCompareFixture(t)
	defer f.close()

	groupID := compareGroup(t, f, f.HH1, "Cheerios")
	bulk := compareProduct(t, f, f.HH1, "Cheerios Bulk", "cereal")
	box := compareProduct(t, f, f.HH1, "Cheerios Box", "cereal")
	assignCompareGroup(t, f, bulk, groupID)
	assignCompareGroup(t, f, box, groupID)
	r1 := compareReceipt(t, f, f.HH1, f.StoreA, "2026-04-01", "10.00", "matched")
	r2 := compareReceipt(t, f, f.HH1, f.StoreB, "2026-04-08", "10.00", "matched")
	compareLine(t, f, r1, bulk, "CHEERIOS BULK", "32", "oz", "5.00")
	compareLine(t, f, r2, box, "CHEERIOS BOX", "12", "oz", "3.00")
	compareLine(t, f, r1, "", "UNMATCHED COUPON", "1", "", "-1.00")
	compareLine(t, f, r2, "", "UNMATCHED COUPON", "1", "", "-1.00")

	code, resp, body := callCompareReceipts(t, f.Handler, f.HH1, compareBody([]string{r1, r2}, nil))
	if code != http.StatusOK {
		t.Fatalf("want 200 got %d: %s", code, body)
	}
	if len(resp.Products) != 1 {
		t.Fatalf("products = %d, want grouped row only: %s", len(resp.Products), body)
	}
	row := resp.Products[0]
	if row.ComparisonKey != groupID || row.ProductGroupID == nil || *row.ProductGroupID != groupID || row.Name != "Cheerios" {
		t.Fatalf("group row mismatch: %+v", row)
	}
	if len(row.Appearances) != 2 {
		t.Fatalf("group row appearances = %d, want 2", len(row.Appearances))
	}
}

func TestCompareReceipts_Guards(t *testing.T) {
	f := newCompareFixture(t)
	defer f.close()

	product := compareProduct(t, f, f.HH1, "Milk", "dairy")
	r1 := compareReceipt(t, f, f.HH1, f.StoreA, "2026-04-01", "4.00", "matched")
	r2 := compareReceipt(t, f, f.HH1, f.StoreB, "2026-04-08", "4.00", "pending")
	compareLine(t, f, r1, product, "MILK", "1", "gal", "4.00")
	compareLine(t, f, r2, product, "MILK", "1", "gal", "4.00")

	otherStore := compareReceipt(t, f, f.HH2, "", "2026-04-01", "4.00", "matched")
	code, _, body := callCompareReceipts(t, f.Handler, f.HH1, compareBody([]string{r1, otherStore}, nil))
	if code != http.StatusNotFound {
		t.Fatalf("cross-household want 404 got %d: %s", code, body)
	}

	code, _, body = callCompareReceipts(t, f.Handler, f.HH1, compareBody([]string{r1, r2}, nil))
	if code != http.StatusConflict || !strings.Contains(body, `"status":"pending"`) {
		t.Fatalf("status guard want 409 with pending payload, got %d: %s", code, body)
	}

	code, _, body = callCompareReceipts(t, f.Handler, f.HH1, compareBody([]string{r1}, nil))
	if code != http.StatusBadRequest {
		t.Fatalf("single id want 400 got %d: %s", code, body)
	}

	tooMany := []string{r1, r2}
	for len(tooMany) < 13 {
		tooMany = append(tooMany, uuid.New().String())
	}
	code, _, body = callCompareReceipts(t, f.Handler, f.HH1, compareBody(tooMany, nil))
	if code != http.StatusBadRequest {
		t.Fatalf("too many want 400 got %d: %s", code, body)
	}

	minOverlap := 3
	code, _, body = callCompareReceipts(t, f.Handler, f.HH1, compareBody([]string{r1, r1}, &minOverlap))
	if code != http.StatusBadRequest {
		t.Fatalf("min_overlap > unique want 400 got %d: %s", code, body)
	}
}
