package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
)

// TestGet_StorageZone_FallbackChain verifies that GET /api/v1/lists/:id
// populates listItemResponse.StorageZone correctly across all four branches
// of the resolver chain:
//
//	(a) products.category non-null/non-empty   -> mapped zone
//	(b) products.category NULL, line_items.suggested_category present
//	    (most-recent by created_at)            -> mapped zone
//	(c) products.category NULL, no line_items  -> "other"
//	(d) shopping_list_items.product_id is NULL -> "other"
//
// Item naming is used only for lookup and does not influence the resolver.
func TestGet_StorageZone_FallbackChain(t *testing.T) {
	h, cleanup := newTestListHandler(t)
	defer cleanup()

	hh := seedHousehold(t, h, "HH1")
	listID := seedList(t, h, hh, "L")
	store := seedStore(t, h, hh, "Costco")

	// (a) Product with category='Produce' -> zone "produce".
	prodA := seedProduct(t, h, hh, "Apples")
	if _, err := h.DB.Exec(
		"UPDATE products SET category = 'Produce' WHERE id = ?", prodA,
	); err != nil {
		t.Fatalf("set category for prodA: %v", err)
	}
	itemA := seedListItem(t, h, listID, "Apples")
	setProductID(t, h, itemA, prodA)

	// (b) Product with NULL category, but a line_item exists referencing it with
	// suggested_category='Frozen'. Also seed an older line_item with a
	// *different* suggested_category to ensure ORDER BY created_at DESC picks
	// the newest one.
	prodB := seedProduct(t, h, hh, "IceCream")
	// products.category defaults to NULL — no UPDATE needed.
	receiptOld := seedReceipt(t, h, hh, store, "2024-01-01")
	receiptNew := seedReceipt(t, h, hh, store, "2024-02-01")
	// Older row with a stale category; explicit created_at to force ordering.
	if _, err := h.DB.Exec(
		`INSERT INTO line_items (receipt_id, product_id, raw_name, total_price, suggested_category, created_at)
		 VALUES (?, ?, 'ICE CREAM', '5.99', 'Pantry', '2024-01-01 00:00:00')`,
		receiptOld, prodB,
	); err != nil {
		t.Fatalf("insert old line_item: %v", err)
	}
	// Newer row — this is the one the resolver should pick up.
	if _, err := h.DB.Exec(
		`INSERT INTO line_items (receipt_id, product_id, raw_name, total_price, suggested_category, created_at)
		 VALUES (?, ?, 'ICE CREAM', '5.99', 'Frozen', '2024-02-01 00:00:00')`,
		receiptNew, prodB,
	); err != nil {
		t.Fatalf("insert new line_item: %v", err)
	}
	itemB := seedListItem(t, h, listID, "IceCream")
	setProductID(t, h, itemB, prodB)

	// (c) Product with NULL category and no line_items at all -> "other".
	prodC := seedProduct(t, h, hh, "MysteryItem")
	itemC := seedListItem(t, h, listID, "MysteryItem")
	setProductID(t, h, itemC, prodC)

	// (d) List item with product_id=NULL (user-typed) -> "other".
	// seedListItem already inserts with product_id NULL; no further setup needed.
	_ = seedListItem(t, h, listID, "HandTypedOnly")

	// Fetch and assert.
	resp := callGetList(t, h, hh, listID)

	cases := []struct {
		name string
		want string
	}{
		{"Apples", "produce"},
		{"IceCream", "frozen"},
		{"MysteryItem", "other"},
		{"HandTypedOnly", "other"},
	}
	for _, tc := range cases {
		item := findItemByName(resp.Items, tc.name)
		if item == nil {
			t.Errorf("%s: item not found in response", tc.name)
			continue
		}
		if item.StorageZone != tc.want {
			t.Errorf("%s: StorageZone=%q, want %q", tc.name, item.StorageZone, tc.want)
		}
	}
}

// TestBulkAddItems_PopulatesStorageZone verifies that POST
// /api/v1/lists/:id/items/bulk returns a bulkAddItemsResponse whose embedded
// list.items each carry a populated StorageZone (not empty) — the frontend's
// ListItemWithPrice TS contract requires one of
// 'produce' | 'cold' | 'frozen' | 'other'. Covers three branches:
//
//	(a) product_id linked to a product with category='Produce' -> "produce"
//	(b) product_id linked to a product with NULL category     -> "other"
//	(c) user-typed item with product_id = nil                 -> "other"
func TestBulkAddItems_PopulatesStorageZone(t *testing.T) {
	h, cleanup := newTestListHandler(t)
	defer cleanup()

	hh := seedHousehold(t, h, "HH1")
	listID := seedList(t, h, hh, "Weekly")

	prodA := seedProduct(t, h, hh, "Apples")
	if _, err := h.DB.Exec("UPDATE products SET category = 'Produce' WHERE id = ?", prodA); err != nil {
		t.Fatalf("set category: %v", err)
	}
	prodB := seedProduct(t, h, hh, "Mystery")
	// prodB keeps NULL category.

	body := `{"items":[
		{"name":"Apples","product_id":"` + prodA + `"},
		{"name":"Mystery","product_id":"` + prodB + `"},
		{"name":"HandTyped"}
	]}`

	rec := callBulkAdd(t, h, hh, listID, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp bulkAddItemsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.List.Items) != 3 {
		t.Fatalf("list.items count: got %d want 3", len(resp.List.Items))
	}

	wants := map[string]string{
		"Apples":    "produce",
		"Mystery":   "other",
		"HandTyped": "other",
	}
	for _, item := range resp.List.Items {
		if item.StorageZone == "" {
			t.Errorf("%s: StorageZone empty — violates ListItemWithPrice contract", item.Name)
			continue
		}
		if want, ok := wants[item.Name]; ok && item.StorageZone != want {
			t.Errorf("%s: StorageZone=%q, want %q", item.Name, item.StorageZone, want)
		}
	}
}

// TestAddItem_PopulatesStorageZone verifies that POST /api/v1/lists/:id/items
// returns a listItemResponse with StorageZone populated (not empty) — the
// frontend's ListItemWithPrice TS contract requires one of
// 'produce' | 'cold' | 'frozen' | 'other'. Covers three branches:
//
//	(a) product_id linked to a product with category='Produce' -> "produce"
//	(b) product_id linked to a product with NULL category     -> "other"
//	(c) user-typed item with product_id = nil                 -> "other"
//
// Also covers the whitespace-only category bypass: when products.category
// is '   ' (spaces only), the resolver must fall through to line_items and
// ultimately land on "other" rather than returning empty string.
func TestAddItem_PopulatesStorageZone(t *testing.T) {
	h, cleanup := newTestListHandler(t)
	defer cleanup()

	hh := seedHousehold(t, h, "HH1")
	listID := seedList(t, h, hh, "L")

	// (a) Product with category='Produce'.
	prodA := seedProduct(t, h, hh, "Apples")
	if _, err := h.DB.Exec("UPDATE products SET category = 'Produce' WHERE id = ?", prodA); err != nil {
		t.Fatalf("set category: %v", err)
	}
	// (b) Product with whitespace-only category — must not force empty zone.
	prodB := seedProduct(t, h, hh, "Mystery")
	if _, err := h.DB.Exec("UPDATE products SET category = '   ' WHERE id = ?", prodB); err != nil {
		t.Fatalf("set whitespace category: %v", err)
	}

	cases := []struct {
		name      string
		body      string
		wantZone  string
	}{
		{"linked produce", `{"name":"Apples","product_id":"` + prodA + `"}`, "produce"},
		{"whitespace category", `{"name":"Mystery","product_id":"` + prodB + `"}`, "other"},
		{"user-typed", `{"name":"HandTyped"}`, "other"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/lists/"+listID+"/items", strings.NewReader(tc.body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.Set(auth.ContextKeyHouseholdID, hh)
			c.SetParamNames("id")
			c.SetParamValues(listID)

			if err := h.AddItem(c); err != nil {
				t.Fatalf("AddItem err: %v", err)
			}
			if rec.Code != http.StatusCreated {
				t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
			}

			var item listItemResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if item.StorageZone == "" {
				t.Fatalf("StorageZone is empty — violates ListItemWithPrice contract")
			}
			if item.StorageZone != tc.wantZone {
				t.Errorf("StorageZone=%q, want %q", item.StorageZone, tc.wantZone)
			}
		})
	}
}
