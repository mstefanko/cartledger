package api

import (
	"encoding/json"
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

// newTestHandler opens an in-memory SQLite DB, runs all migrations, and returns
// a ProductHandler and ReviewHandler wired to it.
func newTestHandler(t *testing.T) (*ProductHandler, *ReviewHandler, func()) {
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
	ph := &ProductHandler{DB: database, Cfg: cfg}
	rh := &ReviewHandler{DB: database, Cfg: cfg}

	cleanup := func() {
		database.Close()
		os.RemoveAll(dir)
	}
	return ph, rh, cleanup
}

// insertHousehold inserts a household row and returns its id.
func insertHousehold(t *testing.T, database interface{ Exec(string, ...interface{}) (interface{}, error) }, name string) string {
	t.Helper()
	// Use raw sql.DB via the handler's DB field.
	return ""
}

// seedHousehold inserts a household and returns its id using *sql.DB directly.
func seedTestData(t *testing.T, h *ProductHandler) (householdID, storeID, receiptID, productID string) {
	t.Helper()
	d := h.DB

	// Household
	if err := d.QueryRow("INSERT INTO households (name) VALUES ('Test') RETURNING id").Scan(&householdID); err != nil {
		t.Fatalf("insert household: %v", err)
	}
	// Store
	if err := d.QueryRow("INSERT INTO stores (household_id, name) VALUES (?, 'TestStore') RETURNING id", householdID).Scan(&storeID); err != nil {
		t.Fatalf("insert store: %v", err)
	}
	// Receipt — migration 002 drops the DEFAULT on receipts.id, so generate it explicitly.
	if err := d.QueryRow("SELECT lower(hex(randomblob(16)))").Scan(&receiptID); err != nil {
		t.Fatalf("gen receipt id: %v", err)
	}
	if _, err := d.Exec(
		"INSERT INTO receipts (id, household_id, store_id, receipt_date, total, status) VALUES (?, ?, ?, '2024-01-01', '10.00', 'matched')",
		receiptID, householdID, storeID,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	// Product
	if err := d.QueryRow(
		"INSERT INTO products (household_id, name) VALUES (?, 'Widget') RETURNING id",
		householdID,
	).Scan(&productID); err != nil {
		t.Fatalf("insert product: %v", err)
	}
	return
}

// makeContext creates a test Echo context with householdID injected.
func makeContext(e *echo.Echo, method, path, body, householdID, productID string) (echo.Context, *httptest.ResponseRecorder) {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set(auth.ContextKeyHouseholdID, householdID)
	if productID != "" {
		c.SetParamNames("id")
		c.SetParamValues(productID)
	}
	return c, rec
}

// TestDelete_WithFullHistory verifies transactional delete cleans up all dependencies.
func TestDelete_WithFullHistory(t *testing.T) {
	h, _, cleanup := newTestHandler(t)
	defer cleanup()

	householdID, storeID, receiptID, productID := seedTestData(t, h)
	d := h.DB

	// Create a product group with only this product.
	var groupID string
	if err := d.QueryRow(
		"INSERT INTO product_groups (id, household_id, name) VALUES (lower(hex(randomblob(16))), ?, 'Group1') RETURNING id",
		householdID,
	).Scan(&groupID); err != nil {
		t.Fatalf("insert product_group: %v", err)
	}
	if _, err := d.Exec("UPDATE products SET product_group_id = ? WHERE id = ?", groupID, productID); err != nil {
		t.Fatalf("assign group: %v", err)
	}

	// Add alias (CASCADE will handle this, but let's confirm it's gone).
	if _, err := d.Exec(
		"INSERT INTO product_aliases (product_id, alias) VALUES (?, 'wdgt')", productID,
	); err != nil {
		t.Fatalf("insert alias: %v", err)
	}

	// Add product_price.
	if _, err := d.Exec(
		"INSERT INTO product_prices (product_id, store_id, receipt_id, receipt_date, quantity, unit, unit_price) VALUES (?, ?, ?, '2024-01-01', '1', 'each', '5.00')",
		productID, storeID, receiptID,
	); err != nil {
		t.Fatalf("insert product_price: %v", err)
	}

	// Add matching_rule.
	if _, err := d.Exec(
		"INSERT INTO matching_rules (household_id, condition_op, condition_val, product_id) VALUES (?, 'contains', 'widget', ?)",
		householdID, productID,
	); err != nil {
		t.Fatalf("insert matching_rule: %v", err)
	}

	// Add shopping list item.
	var listID string
	if err := d.QueryRow(
		"INSERT INTO shopping_lists (household_id, name) VALUES (?, 'MyList') RETURNING id", householdID,
	).Scan(&listID); err != nil {
		t.Fatalf("insert shopping_list: %v", err)
	}
	if _, err := d.Exec(
		"INSERT INTO shopping_list_items (list_id, product_id, name) VALUES (?, ?, 'Widget')",
		listID, productID,
	); err != nil {
		t.Fatalf("insert shopping_list_item: %v", err)
	}

	// Add unit_conversion.
	if _, err := d.Exec(
		"INSERT INTO unit_conversions (product_id, from_unit, to_unit, factor) VALUES (?, 'lb', 'oz', '16')", productID,
	); err != nil {
		t.Fatalf("insert unit_conversion: %v", err)
	}

	// Line item with product_id = productID.
	if _, err := d.Exec(
		"INSERT INTO line_items (receipt_id, product_id, raw_name, total_price, matched) VALUES (?, ?, 'Widget', '5.00', 'auto')",
		receiptID, productID,
	); err != nil {
		t.Fatalf("insert line_item (matched): %v", err)
	}

	// Line item with suggested_product_id = productID.
	if _, err := d.Exec(
		"INSERT INTO line_items (receipt_id, raw_name, total_price, matched, suggested_product_id, suggested_name) VALUES (?, 'wdg', '5.00', 'unmatched', ?, 'Widget')",
		receiptID, productID,
	); err != nil {
		t.Fatalf("insert line_item (suggested): %v", err)
	}

	// DELETE
	e := echo.New()
	c, rec := makeContext(e, http.MethodDelete, "/api/v1/products/"+productID, "", householdID, productID)
	if err := h.Delete(c); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Parse response.
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp["deleted"] != true {
		t.Errorf("expected deleted=true, got %v", resp["deleted"])
	}
	if int(resp["unmatched_line_items"].(float64)) != 1 {
		t.Errorf("expected unmatched_line_items=1, got %v", resp["unmatched_line_items"])
	}

	// Product should be gone.
	var count int
	d.QueryRow("SELECT COUNT(*) FROM products WHERE id = ?", productID).Scan(&count)
	if count != 0 {
		t.Errorf("product not deleted")
	}

	// product_prices deleted.
	d.QueryRow("SELECT COUNT(*) FROM product_prices WHERE product_id = ?", productID).Scan(&count)
	if count != 0 {
		t.Errorf("product_prices not deleted")
	}

	// matching_rules deleted.
	d.QueryRow("SELECT COUNT(*) FROM matching_rules WHERE product_id = ?", productID).Scan(&count)
	if count != 0 {
		t.Errorf("matching_rules not deleted")
	}

	// shopping_list_items deleted.
	d.QueryRow("SELECT COUNT(*) FROM shopping_list_items WHERE product_id = ?", productID).Scan(&count)
	if count != 0 {
		t.Errorf("shopping_list_items not deleted")
	}

	// unit_conversions deleted.
	d.QueryRow("SELECT COUNT(*) FROM unit_conversions WHERE product_id = ?", productID).Scan(&count)
	if count != 0 {
		t.Errorf("unit_conversions not deleted")
	}

	// Line items unmatched (product_id NULL).
	d.QueryRow("SELECT COUNT(*) FROM line_items WHERE product_id IS NULL AND matched = 'unmatched'").Scan(&count)
	if count < 1 {
		t.Errorf("expected at least 1 unmatched line item, got %d", count)
	}

	// suggested_product_id cleared.
	d.QueryRow("SELECT COUNT(*) FROM line_items WHERE suggested_product_id = ?", productID).Scan(&count)
	if count != 0 {
		t.Errorf("suggested_product_id not cleared")
	}

	// Group deleted (was only member).
	d.QueryRow("SELECT COUNT(*) FROM product_groups WHERE id = ?", groupID).Scan(&count)
	if count != 0 {
		t.Errorf("product_group not deleted when last member removed")
	}
}

// TestDelete_NoHistory verifies a product with no dependents is deleted cleanly.
func TestDelete_NoHistory(t *testing.T) {
	h, _, cleanup := newTestHandler(t)
	defer cleanup()

	householdID, _, _, productID := seedTestData(t, h)

	e := echo.New()
	c, rec := makeContext(e, http.MethodDelete, "/api/v1/products/"+productID, "", householdID, productID)
	if err := h.Delete(c); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var count int
	h.DB.QueryRow("SELECT COUNT(*) FROM products WHERE id = ?", productID).Scan(&count)
	if count != 0 {
		t.Errorf("product not deleted")
	}
}

// TestDelete_OnlySuggested verifies that deleting a product only nulls suggested_product_id,
// leaving the other product's product_id intact.
func TestDelete_OnlySuggested(t *testing.T) {
	h, _, cleanup := newTestHandler(t)
	defer cleanup()

	householdID, _, receiptID, productID := seedTestData(t, h)
	d := h.DB

	// Create a second product that owns a line item.
	var otherProductID string
	if err := d.QueryRow(
		"INSERT INTO products (household_id, name) VALUES (?, 'Other') RETURNING id", householdID,
	).Scan(&otherProductID); err != nil {
		t.Fatalf("insert other product: %v", err)
	}

	// Line item: product_id = otherProductID, suggested_product_id = productID (the one being deleted).
	var lineItemID string
	if err := d.QueryRow(
		"INSERT INTO line_items (receipt_id, product_id, raw_name, total_price, matched, suggested_product_id) VALUES (?, ?, 'thing', '3.00', 'auto', ?) RETURNING id",
		receiptID, otherProductID, productID,
	).Scan(&lineItemID); err != nil {
		t.Fatalf("insert line_item: %v", err)
	}

	e := echo.New()
	c, rec := makeContext(e, http.MethodDelete, "/api/v1/products/"+productID, "", householdID, productID)
	if err := h.Delete(c); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// The line item should still reference otherProductID.
	var pid *string
	d.QueryRow("SELECT product_id FROM line_items WHERE id = ?", lineItemID).Scan(&pid)
	if pid == nil || *pid != otherProductID {
		t.Errorf("other product's line item was incorrectly modified; product_id = %v", pid)
	}

	// suggested_product_id should be NULL.
	var suggested *string
	d.QueryRow("SELECT suggested_product_id FROM line_items WHERE id = ?", lineItemID).Scan(&suggested)
	if suggested != nil {
		t.Errorf("suggested_product_id not cleared, got %v", *suggested)
	}
}

// TestDelete_GroupNotEmpty verifies the group survives when a sibling product remains.
func TestDelete_GroupNotEmpty(t *testing.T) {
	h, _, cleanup := newTestHandler(t)
	defer cleanup()

	householdID, _, _, productID := seedTestData(t, h)
	d := h.DB

	// Create a group and assign both productID and a sibling to it.
	var groupID string
	if err := d.QueryRow(
		"INSERT INTO product_groups (id, household_id, name) VALUES (lower(hex(randomblob(16))), ?, 'SharedGroup') RETURNING id",
		householdID,
	).Scan(&groupID); err != nil {
		t.Fatalf("insert product_group: %v", err)
	}

	var siblingID string
	if err := d.QueryRow(
		"INSERT INTO products (household_id, name, product_group_id) VALUES (?, 'Sibling', ?) RETURNING id",
		householdID, groupID,
	).Scan(&siblingID); err != nil {
		t.Fatalf("insert sibling: %v", err)
	}
	if _, err := d.Exec("UPDATE products SET product_group_id = ? WHERE id = ?", groupID, productID); err != nil {
		t.Fatalf("assign group: %v", err)
	}

	e := echo.New()
	c, rec := makeContext(e, http.MethodDelete, "/api/v1/products/"+productID, "", householdID, productID)
	if err := h.Delete(c); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Group must still exist because sibling remains.
	var count int
	d.QueryRow("SELECT COUNT(*) FROM product_groups WHERE id = ?", groupID).Scan(&count)
	if count != 1 {
		t.Errorf("product_group deleted even though sibling remains")
	}
}

// TestUsageEndpoint verifies GetProductUsage returns correct counts.
func TestUsageEndpoint(t *testing.T) {
	h, _, cleanup := newTestHandler(t)
	defer cleanup()

	householdID, storeID, receiptID, productID := seedTestData(t, h)
	d := h.DB

	// Add an alias.
	if _, err := d.Exec(
		"INSERT INTO product_aliases (product_id, alias) VALUES (?, 'widg')", productID,
	); err != nil {
		t.Fatalf("insert alias: %v", err)
	}

	// Add 2 matching_rules.
	for _, val := range []string{"widget", "wid"} {
		if _, err := d.Exec(
			"INSERT INTO matching_rules (household_id, condition_op, condition_val, product_id) VALUES (?, 'contains', ?, ?)",
			householdID, val, productID,
		); err != nil {
			t.Fatalf("insert matching_rule: %v", err)
		}
	}

	// Add a shopping list item.
	var listID string
	if err := d.QueryRow(
		"INSERT INTO shopping_lists (household_id, name) VALUES (?, 'List') RETURNING id", householdID,
	).Scan(&listID); err != nil {
		t.Fatalf("insert shopping_list: %v", err)
	}
	if _, err := d.Exec(
		"INSERT INTO shopping_list_items (list_id, product_id, name) VALUES (?, ?, 'Widget')",
		listID, productID,
	); err != nil {
		t.Fatalf("insert shopping_list_item: %v", err)
	}

	// Add 3 line items.
	for i := 0; i < 3; i++ {
		if _, err := d.Exec(
			"INSERT INTO line_items (receipt_id, product_id, raw_name, total_price, matched) VALUES (?, ?, 'Widget', '5.00', 'auto')",
			receiptID, productID,
		); err != nil {
			t.Fatalf("insert line_item: %v", err)
		}
	}

	// Add an image row (file doesn't need to exist).
	if _, err := d.Exec(
		"INSERT INTO product_images (product_id, image_path) VALUES (?, 'products/test/img.jpg')", productID,
	); err != nil {
		t.Fatalf("insert product_image: %v", err)
	}

	_ = storeID // used via receipt

	e := echo.New()
	c, rec := makeContext(e, http.MethodGet, "/api/v1/products/"+productID+"/usage", "", householdID, productID)
	if err := h.GetProductUsage(c); err != nil {
		t.Fatalf("GetProductUsage error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]int
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["line_items"] != 3 {
		t.Errorf("line_items: expected 3, got %d", resp["line_items"])
	}
	if resp["shopping_list_items"] != 1 {
		t.Errorf("shopping_list_items: expected 1, got %d", resp["shopping_list_items"])
	}
	if resp["matching_rules"] != 2 {
		t.Errorf("matching_rules: expected 2, got %d", resp["matching_rules"])
	}
	if resp["aliases"] != 1 {
		t.Errorf("aliases: expected 1, got %d", resp["aliases"])
	}
	if resp["images"] != 1 {
		t.Errorf("images: expected 1, got %d", resp["images"])
	}
}

// TestUnmatchedList_HouseholdScoping verifies only the caller's household items are returned.
func TestUnmatchedList_HouseholdScoping(t *testing.T) {
	h, rh, cleanup := newTestHandler(t)
	defer cleanup()

	d := h.DB

	// Create two households.
	var hh1, hh2 string
	if err := d.QueryRow("INSERT INTO households (name) VALUES ('HH1') RETURNING id").Scan(&hh1); err != nil {
		t.Fatalf("insert hh1: %v", err)
	}
	if err := d.QueryRow("INSERT INTO households (name) VALUES ('HH2') RETURNING id").Scan(&hh2); err != nil {
		t.Fatalf("insert hh2: %v", err)
	}

	insertUnmatchedItem := func(householdID, rawName string) {
		t.Helper()
		var storeID, receiptID string
		storeName := rawName + "_store"
		d.QueryRow("INSERT INTO stores (household_id, name) VALUES (?, ?) RETURNING id", householdID, storeName).Scan(&storeID)
		// receipts.id has no DEFAULT after migration 002 — generate explicitly.
		if err := d.QueryRow("SELECT lower(hex(randomblob(16)))").Scan(&receiptID); err != nil {
			t.Fatalf("gen receiptID: %v", err)
		}
		if _, err := d.Exec(
			"INSERT INTO receipts (id, household_id, store_id, receipt_date, total, status) VALUES (?, ?, ?, '2024-01-01', '10.00', 'matched')",
			receiptID, householdID, storeID,
		); err != nil {
			t.Fatalf("insert receipt: %v", err)
		}
		d.Exec(
			"INSERT INTO line_items (receipt_id, raw_name, total_price, matched) VALUES (?, ?, '1.00', 'unmatched')",
			receiptID, rawName,
		)
	}

	// HH1 gets 2 unmatched items, HH2 gets 1.
	insertUnmatchedItem(hh1, "apple")
	insertUnmatchedItem(hh1, "banana")
	insertUnmatchedItem(hh2, "cherry")

	e := echo.New()

	// Query as HH1.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/line-items/unmatched", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set(auth.ContextKeyHouseholdID, hh1)
	if err := rh.ListUnmatchedLineItems(c); err != nil {
		t.Fatalf("ListUnmatchedLineItems error: %v", err)
	}
	var items []unmatchedLineItemResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("hh1: expected 2 items, got %d", len(items))
	}

	// Query as HH2.
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/line-items/unmatched", nil)
	rec2 := httptest.NewRecorder()
	c2 := e.NewContext(req2, rec2)
	c2.Set(auth.ContextKeyHouseholdID, hh2)
	if err := rh.ListUnmatchedLineItems(c2); err != nil {
		t.Fatalf("ListUnmatchedLineItems error: %v", err)
	}
	var items2 []unmatchedLineItemResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &items2); err != nil {
		t.Fatalf("unmarshal hh2: %v", err)
	}
	if len(items2) != 1 {
		t.Errorf("hh2: expected 1 item, got %d", len(items2))
	}

	// Count endpoint for HH1.
	req3 := httptest.NewRequest(http.MethodGet, "/api/v1/line-items/unmatched/count", nil)
	rec3 := httptest.NewRecorder()
	c3 := e.NewContext(req3, rec3)
	c3.Set(auth.ContextKeyHouseholdID, hh1)
	if err := rh.GetUnmatchedCount(c3); err != nil {
		t.Fatalf("GetUnmatchedCount error: %v", err)
	}
	var countResp map[string]int
	if err := json.Unmarshal(rec3.Body.Bytes(), &countResp); err != nil {
		t.Fatalf("unmarshal count: %v", err)
	}
	if countResp["count"] != 2 {
		t.Errorf("expected count=2 for hh1, got %d", countResp["count"])
	}
}
