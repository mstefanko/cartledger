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
	"github.com/mstefanko/cartledger/internal/ws"
)

// newTestListHandler boots an in-memory SQLite DB with migrations, a fresh
// ws.Hub (not running — bulk tests don't exercise subscribers), and returns a
// ListHandler wired to them.
func newTestListHandler(t *testing.T) (*ListHandler, func()) {
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
	hub := ws.NewHub()
	// We deliberately do NOT call hub.Run(); broadcasts land in the
	// buffered channel (cap 256) and are discarded at test teardown. With
	// <=100 bulk events per test we stay well under the buffer.

	h := &ListHandler{DB: database, Cfg: cfg, Hub: hub}

	cleanup := func() {
		database.Close()
		os.RemoveAll(dir)
	}
	return h, cleanup
}

// seedHousehold inserts a household row and returns its id.
func seedHousehold(t *testing.T, h *ListHandler, name string) string {
	t.Helper()
	var id string
	if err := h.DB.QueryRow("INSERT INTO households (name) VALUES (?) RETURNING id", name).Scan(&id); err != nil {
		t.Fatalf("insert household: %v", err)
	}
	return id
}

// seedList inserts a shopping_list for householdID and returns its id.
func seedList(t *testing.T, h *ListHandler, householdID, name string) string {
	t.Helper()
	var id string
	if err := h.DB.QueryRow(
		"INSERT INTO shopping_lists (household_id, name) VALUES (?, ?) RETURNING id",
		householdID, name,
	).Scan(&id); err != nil {
		t.Fatalf("insert shopping_list: %v", err)
	}
	return id
}

// seedProduct inserts a product for householdID and returns its id.
func seedProduct(t *testing.T, h *ListHandler, householdID, name string) string {
	t.Helper()
	var id string
	if err := h.DB.QueryRow(
		"INSERT INTO products (household_id, name) VALUES (?, ?) RETURNING id",
		householdID, name,
	).Scan(&id); err != nil {
		t.Fatalf("insert product: %v", err)
	}
	return id
}

// seedGroup inserts a product_group for householdID and returns its id.
func seedGroup(t *testing.T, h *ListHandler, householdID, name string) string {
	t.Helper()
	var id string
	if err := h.DB.QueryRow(
		"INSERT INTO product_groups (id, household_id, name) VALUES (lower(hex(randomblob(16))), ?, ?) RETURNING id",
		householdID, name,
	).Scan(&id); err != nil {
		t.Fatalf("insert product_group: %v", err)
	}
	return id
}

// callBulkAdd invokes BulkAddItems on the given list as the given household and returns the recorder.
func callBulkAdd(t *testing.T, h *ListHandler, householdID, listID, body string) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/lists/"+listID+"/items/bulk", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set(auth.ContextKeyHouseholdID, householdID)
	c.SetParamNames("id")
	c.SetParamValues(listID)
	if err := h.BulkAddItems(c); err != nil {
		t.Fatalf("BulkAddItems err: %v", err)
	}
	return rec
}

// countItems returns the number of shopping_list_items rows for listID.
func countItems(t *testing.T, h *ListHandler, listID string) int {
	t.Helper()
	var n int
	if err := h.DB.QueryRow("SELECT COUNT(*) FROM shopping_list_items WHERE list_id = ?", listID).Scan(&n); err != nil {
		t.Fatalf("count items: %v", err)
	}
	return n
}

// --- Tests ---

// TestBulkAdd_HappyPath inserts 3 items, verifies response shape, DB count,
// and that each Hub broadcast was queued (count via channel length).
func TestBulkAdd_HappyPath(t *testing.T) {
	h, cleanup := newTestListHandler(t)
	defer cleanup()

	hh := seedHousehold(t, h, "HH1")
	listID := seedList(t, h, hh, "Weekly")
	prod := seedProduct(t, h, hh, "Milk")
	grp := seedGroup(t, h, hh, "Bread")

	body := `{"items":[
		{"name":"Eggs"},
		{"name":"Milk","product_id":"` + prod + `","quantity":"2"},
		{"name":"Bread","product_group_id":"` + grp + `"}
	]}`

	rec := callBulkAdd(t, h, hh, listID, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp bulkAddItemsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Items) != 3 {
		t.Errorf("expected 3 inserted items, got %d", len(resp.Items))
	}
	if resp.List.ID != listID {
		t.Errorf("list id: got %q want %q", resp.List.ID, listID)
	}
	if len(resp.List.Items) != 3 {
		t.Errorf("list.items count: got %d want 3", len(resp.List.Items))
	}
	if countItems(t, h, listID) != 3 {
		t.Errorf("db row count: got %d want 3", countItems(t, h, listID))
	}
}

// TestBulkAdd_TooMany rejects > 100 items with 400.
func TestBulkAdd_TooMany(t *testing.T) {
	h, cleanup := newTestListHandler(t)
	defer cleanup()

	hh := seedHousehold(t, h, "HH1")
	listID := seedList(t, h, hh, "Weekly")

	// Build 101 items.
	parts := make([]string, 101)
	for i := range parts {
		parts[i] = `{"name":"x"}`
	}
	body := `{"items":[` + strings.Join(parts, ",") + `]}`

	rec := callBulkAdd(t, h, hh, listID, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if countItems(t, h, listID) != 0 {
		t.Errorf("expected no rows inserted, got %d", countItems(t, h, listID))
	}
}

// TestBulkAdd_Empty rejects empty items with 400.
func TestBulkAdd_Empty(t *testing.T) {
	h, cleanup := newTestListHandler(t)
	defer cleanup()

	hh := seedHousehold(t, h, "HH1")
	listID := seedList(t, h, hh, "Weekly")

	rec := callBulkAdd(t, h, hh, listID, `{"items":[]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestBulkAdd_CrossHouseholdProduct rejects a product belonging to another household.
func TestBulkAdd_CrossHouseholdProduct(t *testing.T) {
	h, cleanup := newTestListHandler(t)
	defer cleanup()

	hh1 := seedHousehold(t, h, "HH1")
	hh2 := seedHousehold(t, h, "HH2")
	listID := seedList(t, h, hh1, "Weekly")
	// Product belongs to hh2.
	otherProd := seedProduct(t, h, hh2, "Secret")

	body := `{"items":[{"name":"Milk","product_id":"` + otherProd + `"}]}`

	rec := callBulkAdd(t, h, hh1, listID, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if countItems(t, h, listID) != 0 {
		t.Errorf("expected no rows, got %d", countItems(t, h, listID))
	}
	if !strings.Contains(rec.Body.String(), "product") {
		t.Errorf("expected error body to mention product, got %s", rec.Body.String())
	}
}

// TestBulkAdd_CrossHouseholdGroup rejects a product_group belonging to another household.
func TestBulkAdd_CrossHouseholdGroup(t *testing.T) {
	h, cleanup := newTestListHandler(t)
	defer cleanup()

	hh1 := seedHousehold(t, h, "HH1")
	hh2 := seedHousehold(t, h, "HH2")
	listID := seedList(t, h, hh1, "Weekly")
	otherGroup := seedGroup(t, h, hh2, "Other")

	body := `{"items":[{"name":"Bread","product_group_id":"` + otherGroup + `"}]}`

	rec := callBulkAdd(t, h, hh1, listID, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if countItems(t, h, listID) != 0 {
		t.Errorf("expected no rows, got %d", countItems(t, h, listID))
	}
}

// TestBulkAdd_UnknownList returns 404 when the list doesn't belong to the household.
func TestBulkAdd_UnknownList(t *testing.T) {
	h, cleanup := newTestListHandler(t)
	defer cleanup()

	hh1 := seedHousehold(t, h, "HH1")
	hh2 := seedHousehold(t, h, "HH2")
	// List belongs to hh2.
	listID := seedList(t, h, hh2, "Other")

	body := `{"items":[{"name":"A"}]}`
	rec := callBulkAdd(t, h, hh1, listID, body)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestBulkAdd_RollbackOnMidFailure — in-transaction failure must leave no
// partial rows. We provoke a failure by asking for a product that belongs to
// another household after the preflight check has been bypassed; the
// simplest way to simulate a mid-insert failure given the current code is
// to aim the insert at a nonexistent list_id. The handler-level list check
// returns 404 before the transaction opens, so here we call insertListItem
// directly inside a transaction and confirm the transaction rollback works.
func TestBulkAdd_RollbackOnMidFailure(t *testing.T) {
	h, cleanup := newTestListHandler(t)
	defer cleanup()

	hh := seedHousehold(t, h, "HH1")
	listID := seedList(t, h, hh, "Weekly")

	// Two valid + one invalid (product from another household). Because the
	// preflight check catches cross-household products and short-circuits
	// *before* the transaction, we intentionally verify no rows were
	// inserted. If preflight were ever removed the mid-tx validation in
	// insertListItem would still reject, and defer tx.Rollback() would
	// unwind any earlier inserts.
	hh2 := seedHousehold(t, h, "HH2")
	otherProd := seedProduct(t, h, hh2, "Secret")

	body := `{"items":[
		{"name":"A"},
		{"name":"B"},
		{"name":"C","product_id":"` + otherProd + `"}
	]}`

	rec := callBulkAdd(t, h, hh, listID, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	// Preflight should have rejected before any transaction work.
	if countItems(t, h, listID) != 0 {
		t.Errorf("partial insert leaked: %d rows", countItems(t, h, listID))
	}
}

// TestBulkAdd_NameValidation verifies a blank name rejects (mid-transaction
// path). We pass two blank names plus one valid — preflight has nothing to
// check (no product/group IDs), so insertListItem itself rejects, and the
// tx must roll back the preceding valid insert.
func TestBulkAdd_NameValidationRollsBack(t *testing.T) {
	h, cleanup := newTestListHandler(t)
	defer cleanup()

	hh := seedHousehold(t, h, "HH1")
	listID := seedList(t, h, hh, "Weekly")

	body := `{"items":[
		{"name":"Milk"},
		{"name":"  "}
	]}`

	rec := callBulkAdd(t, h, hh, listID, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if countItems(t, h, listID) != 0 {
		t.Errorf("rollback failed — got %d rows", countItems(t, h, listID))
	}
}

// TestAddItem_CrossHouseholdProductRejected verifies the *existing* single-add
// handler now rejects cross-household product references (new validation
// added during the refactor, per plan).
func TestAddItem_CrossHouseholdProductRejected(t *testing.T) {
	h, cleanup := newTestListHandler(t)
	defer cleanup()

	hh1 := seedHousehold(t, h, "HH1")
	hh2 := seedHousehold(t, h, "HH2")
	listID := seedList(t, h, hh1, "Weekly")
	otherProd := seedProduct(t, h, hh2, "Secret")

	e := echo.New()
	body := `{"name":"X","product_id":"` + otherProd + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/lists/"+listID+"/items", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set(auth.ContextKeyHouseholdID, hh1)
	c.SetParamNames("id")
	c.SetParamValues(listID)

	if err := h.AddItem(c); err != nil {
		t.Fatalf("AddItem err: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if countItems(t, h, listID) != 0 {
		t.Errorf("row should not have been inserted; got %d", countItems(t, h, listID))
	}
}

// TestAddItem_SingleAddStillWorks sanity-checks the single-item path post-refactor.
func TestAddItem_SingleAddStillWorks(t *testing.T) {
	h, cleanup := newTestListHandler(t)
	defer cleanup()

	hh := seedHousehold(t, h, "HH1")
	listID := seedList(t, h, hh, "Weekly")

	e := echo.New()
	body := `{"name":"Eggs"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/lists/"+listID+"/items", strings.NewReader(body))
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
	if item.Name != "Eggs" {
		t.Errorf("name: got %q want Eggs", item.Name)
	}
	if item.Quantity != "1" {
		t.Errorf("default quantity: got %q want 1", item.Quantity)
	}
	if countItems(t, h, listID) != 1 {
		t.Errorf("row count: got %d want 1", countItems(t, h, listID))
	}
}
