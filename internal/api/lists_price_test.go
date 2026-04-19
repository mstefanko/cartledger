package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
)

// seedReceipt inserts a minimal receipt and returns its id. Tests that seed
// product_prices need a receipt_id for the FK.
func seedReceipt(t *testing.T, h *ListHandler, householdID, storeID, date string) string {
	t.Helper()
	var id string
	if err := h.DB.QueryRow("SELECT lower(hex(randomblob(16)))").Scan(&id); err != nil {
		t.Fatalf("gen receipt id: %v", err)
	}
	if _, err := h.DB.Exec(
		"INSERT INTO receipts (id, household_id, store_id, receipt_date, status) VALUES (?, ?, ?, ?, 'matched')",
		id, householdID, storeID, date,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	return id
}

// seedProductPrice inserts a product_prices row with a matching receipt.
func seedProductPrice(t *testing.T, h *ListHandler, householdID, productID, storeID, date, unitPrice string) {
	t.Helper()
	receiptID := seedReceipt(t, h, householdID, storeID, date)
	if _, err := h.DB.Exec(
		"INSERT INTO product_prices (product_id, store_id, receipt_id, receipt_date, quantity, unit, unit_price) VALUES (?, ?, ?, ?, '1', 'each', ?)",
		productID, storeID, receiptID, date, unitPrice,
	); err != nil {
		t.Fatalf("insert product_price: %v", err)
	}
}

// setAssignedStore updates the assigned_store_id for an existing list item.
// Used rather than an update handler so tests remain focused on the Get path.
func setAssignedStore(t *testing.T, h *ListHandler, itemID, storeID string) {
	t.Helper()
	if _, err := h.DB.Exec(
		"UPDATE shopping_list_items SET assigned_store_id = ? WHERE id = ?", storeID, itemID,
	); err != nil {
		t.Fatalf("update assigned_store_id: %v", err)
	}
}

// setProductID updates the product_id for an existing list item.
func setProductID(t *testing.T, h *ListHandler, itemID, productID string) {
	t.Helper()
	if _, err := h.DB.Exec(
		"UPDATE shopping_list_items SET product_id = ? WHERE id = ?", productID, itemID,
	); err != nil {
		t.Fatalf("update product_id: %v", err)
	}
}

// setProductGroupID updates the product_group_id for an existing list item.
func setProductGroupID(t *testing.T, h *ListHandler, itemID, groupID string) {
	t.Helper()
	if _, err := h.DB.Exec(
		"UPDATE shopping_list_items SET product_group_id = ? WHERE id = ?", groupID, itemID,
	); err != nil {
		t.Fatalf("update product_group_id: %v", err)
	}
}

// setProductGroup sets a product's product_group_id (join products→groups).
func setProductGroup(t *testing.T, h *ListHandler, productID, groupID string) {
	t.Helper()
	if _, err := h.DB.Exec(
		"UPDATE products SET product_group_id = ? WHERE id = ?", groupID, productID,
	); err != nil {
		t.Fatalf("update product.product_group_id: %v", err)
	}
}

// callGetList invokes ListHandler.Get and returns the decoded detail.
func callGetList(t *testing.T, h *ListHandler, householdID, listID string) listDetailResponse {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/lists/"+listID, nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set(auth.ContextKeyHouseholdID, householdID)
	c.SetParamNames("id")
	c.SetParamValues(listID)
	if err := h.Get(c); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("Get status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp listDetailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp
}

// findItemByName returns a pointer to the item with the given name (nil if absent).
func findItemByName(items []listItemResponse, name string) *listItemResponse {
	for i := range items {
		if items[i].Name == name {
			return &items[i]
		}
	}
	return nil
}

// strp dereferences a *string (returns "" if nil) for concise assertions.
func strp(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// Case 1: Single-store history, that store is the assigned store.
// Expected: assigned_store_price set, store_history_count=1, cheapest_store=that store.
func TestGet_AssignedStoreHasHistory_SingleStore(t *testing.T) {
	h, cleanup := newTestListHandler(t)
	defer cleanup()

	hh := seedHousehold(t, h, "HH1")
	listID := seedList(t, h, hh, "L")
	prod := seedProduct(t, h, hh, "Blueberries")
	costco := seedStore(t, h, hh, "Costco")

	seedProductPrice(t, h, hh, prod, costco, "2024-01-01", "3.99")

	itemID := seedListItem(t, h, listID, "Blueberries")
	setProductID(t, h, itemID, prod)
	setAssignedStore(t, h, itemID, costco)

	resp := callGetList(t, h, hh, listID)
	item := findItemByName(resp.Items, "Blueberries")
	if item == nil {
		t.Fatalf("item not found")
	}
	if strp(item.AssignedStorePrice) != "3.99" {
		t.Errorf("AssignedStorePrice: got %q, want 3.99", strp(item.AssignedStorePrice))
	}
	if item.StoreHistoryCount != 1 {
		t.Errorf("StoreHistoryCount: got %d, want 1", item.StoreHistoryCount)
	}
	if strp(item.CheapestStore) != "Costco" {
		t.Errorf("CheapestStore: got %q, want Costco", strp(item.CheapestStore))
	}
	if strp(item.CheapestPrice) != "3.99" {
		t.Errorf("CheapestPrice: got %q, want 3.99", strp(item.CheapestPrice))
	}
}

// Case 2: Single-store history, DIFFERENT store assigned (Blueberries bug).
// Expected: assigned_store_price nil, store_history_count=1, cheapest set to the
// only store with data. This is the neutral-state signal that makes the UI
// render gray "Only seen at Costco" instead of red "worse than Costco".
func TestGet_AssignedStoreHasNoHistory_SingleStoreElsewhere(t *testing.T) {
	h, cleanup := newTestListHandler(t)
	defer cleanup()

	hh := seedHousehold(t, h, "HH1")
	listID := seedList(t, h, hh, "L")
	prod := seedProduct(t, h, hh, "Blueberries")
	costco := seedStore(t, h, hh, "Costco")
	shoprite := seedStore(t, h, hh, "ShopRite")

	seedProductPrice(t, h, hh, prod, costco, "2024-01-01", "3.99")

	itemID := seedListItem(t, h, listID, "Blueberries")
	setProductID(t, h, itemID, prod)
	setAssignedStore(t, h, itemID, shoprite)

	resp := callGetList(t, h, hh, listID)
	item := findItemByName(resp.Items, "Blueberries")
	if item == nil {
		t.Fatalf("item not found")
	}
	if item.AssignedStorePrice != nil {
		t.Errorf("AssignedStorePrice: got %q, want nil", strp(item.AssignedStorePrice))
	}
	if item.StoreHistoryCount != 1 {
		t.Errorf("StoreHistoryCount: got %d, want 1", item.StoreHistoryCount)
	}
	if strp(item.CheapestStore) != "Costco" {
		t.Errorf("CheapestStore: got %q, want Costco", strp(item.CheapestStore))
	}
	if strp(item.CheapestPrice) != "3.99" {
		t.Errorf("CheapestPrice: got %q, want 3.99", strp(item.CheapestPrice))
	}
}

// Case 3: Multi-store history, assigned store is the cheapest.
// Expected: assigned_store_price matches cheapest_price.
func TestGet_AssignedStoreIsCheapest_MultiStore(t *testing.T) {
	h, cleanup := newTestListHandler(t)
	defer cleanup()

	hh := seedHousehold(t, h, "HH1")
	listID := seedList(t, h, hh, "L")
	prod := seedProduct(t, h, hh, "Milk")
	costco := seedStore(t, h, hh, "Costco")
	shoprite := seedStore(t, h, hh, "ShopRite")

	seedProductPrice(t, h, hh, prod, costco, "2024-01-01", "2.99")
	seedProductPrice(t, h, hh, prod, shoprite, "2024-01-01", "4.49")

	itemID := seedListItem(t, h, listID, "Milk")
	setProductID(t, h, itemID, prod)
	setAssignedStore(t, h, itemID, costco)

	resp := callGetList(t, h, hh, listID)
	item := findItemByName(resp.Items, "Milk")
	if item == nil {
		t.Fatalf("item not found")
	}
	if strp(item.AssignedStorePrice) != "2.99" {
		t.Errorf("AssignedStorePrice: got %q, want 2.99", strp(item.AssignedStorePrice))
	}
	if item.StoreHistoryCount != 2 {
		t.Errorf("StoreHistoryCount: got %d, want 2", item.StoreHistoryCount)
	}
	if strp(item.CheapestPrice) != "2.99" {
		t.Errorf("CheapestPrice: got %q, want 2.99", strp(item.CheapestPrice))
	}
	if strp(item.CheapestStore) != "Costco" {
		t.Errorf("CheapestStore: got %q, want Costco", strp(item.CheapestStore))
	}
}

// Case 4: Multi-store history, assigned NOT cheapest but has own history.
// Expected: assigned_store_price set AND strictly greater than cheapest_price —
// the "confirmed worse" red-X state.
func TestGet_AssignedStoreHasHistoryButNotCheapest(t *testing.T) {
	h, cleanup := newTestListHandler(t)
	defer cleanup()

	hh := seedHousehold(t, h, "HH1")
	listID := seedList(t, h, hh, "L")
	prod := seedProduct(t, h, hh, "Milk")
	costco := seedStore(t, h, hh, "Costco")
	shoprite := seedStore(t, h, hh, "ShopRite")

	seedProductPrice(t, h, hh, prod, costco, "2024-01-01", "2.99")
	seedProductPrice(t, h, hh, prod, shoprite, "2024-01-01", "4.49")

	itemID := seedListItem(t, h, listID, "Milk")
	setProductID(t, h, itemID, prod)
	setAssignedStore(t, h, itemID, shoprite)

	resp := callGetList(t, h, hh, listID)
	item := findItemByName(resp.Items, "Milk")
	if item == nil {
		t.Fatalf("item not found")
	}
	if strp(item.AssignedStorePrice) != "4.49" {
		t.Errorf("AssignedStorePrice: got %q, want 4.49", strp(item.AssignedStorePrice))
	}
	if item.StoreHistoryCount != 2 {
		t.Errorf("StoreHistoryCount: got %d, want 2", item.StoreHistoryCount)
	}
	if strp(item.CheapestPrice) != "2.99" {
		t.Errorf("CheapestPrice: got %q, want 2.99", strp(item.CheapestPrice))
	}
	if strp(item.CheapestStore) != "Costco" {
		t.Errorf("CheapestStore: got %q, want Costco", strp(item.CheapestStore))
	}
}

// Case 5: No price history anywhere.
// Expected: all price/history fields unset; StoreHistoryCount=0.
func TestGet_NoHistoryAnywhere(t *testing.T) {
	h, cleanup := newTestListHandler(t)
	defer cleanup()

	hh := seedHousehold(t, h, "HH1")
	listID := seedList(t, h, hh, "L")
	prod := seedProduct(t, h, hh, "Kumquats")
	shoprite := seedStore(t, h, hh, "ShopRite")

	itemID := seedListItem(t, h, listID, "Kumquats")
	setProductID(t, h, itemID, prod)
	setAssignedStore(t, h, itemID, shoprite)

	resp := callGetList(t, h, hh, listID)
	item := findItemByName(resp.Items, "Kumquats")
	if item == nil {
		t.Fatalf("item not found")
	}
	if item.AssignedStorePrice != nil {
		t.Errorf("AssignedStorePrice: got %q, want nil", strp(item.AssignedStorePrice))
	}
	if item.StoreHistoryCount != 0 {
		t.Errorf("StoreHistoryCount: got %d, want 0", item.StoreHistoryCount)
	}
	if item.CheapestStore != nil {
		t.Errorf("CheapestStore: got %q, want nil", strp(item.CheapestStore))
	}
	if item.CheapestPrice != nil {
		t.Errorf("CheapestPrice: got %q, want nil", strp(item.CheapestPrice))
	}
	if item.EstimatedPrice != nil {
		t.Errorf("EstimatedPrice: got %q, want nil", strp(item.EstimatedPrice))
	}
}

// Case 6: Group with items across different stores — symmetric fields populated.
// Two products in the same group: Wonder Bread priced at ShopRite 3.49, Pepperidge
// Farm priced at Costco 4.99. List item is backed by the group only (no
// product_id) and assigned to ShopRite. Expect:
//   - AssignedStorePrice = 3.49 (MIN across group at assigned store)
//   - StoreHistoryCount = 2 (two distinct stores saw group members)
//   - CheapestStore/CheapestPrice/CheapestProductID = ShopRite/3.49/Wonder Bread id
func TestGet_GroupPath_MixedStoreSymmetry(t *testing.T) {
	h, cleanup := newTestListHandler(t)
	defer cleanup()

	hh := seedHousehold(t, h, "HH1")
	listID := seedList(t, h, hh, "L")
	grp := seedGroup(t, h, hh, "Bread")
	wonder := seedProduct(t, h, hh, "Wonder Bread")
	pepperidge := seedProduct(t, h, hh, "Pepperidge Farm")
	setProductGroup(t, h, wonder, grp)
	setProductGroup(t, h, pepperidge, grp)

	costco := seedStore(t, h, hh, "Costco")
	shoprite := seedStore(t, h, hh, "ShopRite")

	seedProductPrice(t, h, hh, wonder, shoprite, "2024-01-01", "3.49")
	seedProductPrice(t, h, hh, pepperidge, costco, "2024-01-01", "4.99")

	itemID := seedListItem(t, h, listID, "Bread")
	setProductGroupID(t, h, itemID, grp)
	setAssignedStore(t, h, itemID, shoprite)

	resp := callGetList(t, h, hh, listID)
	item := findItemByName(resp.Items, "Bread")
	if item == nil {
		t.Fatalf("item not found")
	}

	if strp(item.AssignedStorePrice) != "3.49" {
		t.Errorf("AssignedStorePrice: got %q, want 3.49", strp(item.AssignedStorePrice))
	}
	if item.StoreHistoryCount != 2 {
		t.Errorf("StoreHistoryCount: got %d, want 2", item.StoreHistoryCount)
	}
	if strp(item.CheapestStore) != "ShopRite" {
		t.Errorf("CheapestStore: got %q, want ShopRite", strp(item.CheapestStore))
	}
	if strp(item.CheapestPrice) != "3.49" {
		t.Errorf("CheapestPrice: got %q, want 3.49", strp(item.CheapestPrice))
	}
	if strp(item.CheapestProductID) != wonder {
		t.Errorf("CheapestProductID: got %q, want %q", strp(item.CheapestProductID), wonder)
	}

	// Spot-check the "assigned has no history at this group" case on the same
	// seed data: assign to a third store (with zero group history).
	third := seedStore(t, h, hh, "Wegmans")
	item2ID := seedListItem(t, h, listID, "Bread-Wegmans")
	setProductGroupID(t, h, item2ID, grp)
	setAssignedStore(t, h, item2ID, third)

	resp2 := callGetList(t, h, hh, listID)
	item2 := findItemByName(resp2.Items, "Bread-Wegmans")
	if item2 == nil {
		t.Fatalf("item2 not found")
	}
	if item2.AssignedStorePrice != nil {
		t.Errorf("Bread-Wegmans AssignedStorePrice: got %q, want nil", strp(item2.AssignedStorePrice))
	}
	if item2.StoreHistoryCount != 2 {
		t.Errorf("Bread-Wegmans StoreHistoryCount: got %d, want 2", item2.StoreHistoryCount)
	}
}
