package api

import (
	"testing"
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
