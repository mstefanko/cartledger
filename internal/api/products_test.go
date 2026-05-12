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

// TestList_SortLastPurchasedAt verifies that `?sort=last_purchased_at` orders
// products by `last_purchased_at DESC NULLS LAST, name`, distinct from the
// default alphabetical ordering.
//
// Setup: three products in one household —
//   - "Apples"  last_purchased_at = 2024-03-01
//   - "Bananas" last_purchased_at = NULL     (never purchased)
//   - "Carrots" last_purchased_at = 2024-05-01
//
// Default order (?no sort?):      Apples, Bananas, Carrots (alphabetical).
// sort=last_purchased_at order:   Carrots, Apples, Bananas (most-recent first, NULL last).
func TestList_SortLastPurchasedAt(t *testing.T) {
	h, _, cleanup := newTestHandler(t)
	defer cleanup()

	householdID, _, _, _ := seedTestData(t, h)
	d := h.DB

	// Remove the pre-seeded "Widget" so we control the full result set.
	if _, err := d.Exec("DELETE FROM products WHERE household_id = ?", householdID); err != nil {
		t.Fatalf("clear products: %v", err)
	}

	mar := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	may := time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)

	// Insert in a non-sorted order to catch any accidental insertion-order bias.
	if _, err := d.Exec(
		`INSERT INTO products (household_id, name, last_purchased_at) VALUES (?, 'Carrots', ?)`,
		householdID, may,
	); err != nil {
		t.Fatalf("insert carrots: %v", err)
	}
	if _, err := d.Exec(
		`INSERT INTO products (household_id, name, last_purchased_at) VALUES (?, 'Apples', ?)`,
		householdID, mar,
	); err != nil {
		t.Fatalf("insert apples: %v", err)
	}
	if _, err := d.Exec(
		`INSERT INTO products (household_id, name) VALUES (?, 'Bananas')`,
		householdID,
	); err != nil {
		t.Fatalf("insert bananas: %v", err)
	}

	list := func(t *testing.T, sort string) []productResponse {
		t.Helper()
		e := echo.New()
		target := "/api/v1/products"
		if sort != "" {
			target += "?sort=" + sort
		}
		req := httptest.NewRequest(http.MethodGet, target, nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.Set(auth.ContextKeyHouseholdID, householdID)
		if err := h.List(c); err != nil {
			t.Fatalf("List error: %v", err)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var got []productResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return got
	}

	// Default sort — alphabetical.
	got := list(t, "")
	wantDefault := []string{"Apples", "Bananas", "Carrots"}
	if len(got) != len(wantDefault) {
		t.Fatalf("default sort: expected %d rows, got %d", len(wantDefault), len(got))
	}
	for i, name := range wantDefault {
		if got[i].Name != name {
			t.Errorf("default sort [%d]: expected %q, got %q", i, name, got[i].Name)
		}
	}

	// sort=last_purchased_at — most-recent first, NULL last.
	got = list(t, "last_purchased_at")
	wantRecent := []string{"Carrots", "Apples", "Bananas"}
	if len(got) != len(wantRecent) {
		t.Fatalf("last_purchased_at sort: expected %d rows, got %d", len(wantRecent), len(got))
	}
	for i, name := range wantRecent {
		if got[i].Name != name {
			t.Errorf("last_purchased_at sort [%d]: expected %q, got %q", i, name, got[i].Name)
		}
	}
}

func TestUpdateRecordsManualProductFieldEdits(t *testing.T) {
	h, _, cleanup := newTestHandler(t)
	defer cleanup()

	householdID, _, _, productID := seedTestData(t, h)
	if _, err := h.DB.Exec(
		"INSERT INTO users (id, household_id, email, name, password_hash) VALUES ('user-manual-editor', ?, 'editor@example.com', 'Editor', 'hash')",
		householdID,
	); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	e := echo.New()
	c, rec := makeContext(e, http.MethodPut, "/api/v1/products/"+productID, `{"brand":"Acme","pack_quantity":12,"pack_unit":"oz"}`, householdID, productID)
	c.Set(auth.ContextKeyUserID, "user-manual-editor")
	if err := h.Update(c); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	rows, err := h.DB.Query(
		`SELECT field, edited_by_user_id, edit_source
		   FROM product_field_edits
		  WHERE product_id = ?
		  ORDER BY field`,
		productID,
	)
	if err != nil {
		t.Fatalf("query field edits: %v", err)
	}
	defer rows.Close()

	got := map[string]string{}
	for rows.Next() {
		var field, userID, source string
		if err := rows.Scan(&field, &userID, &source); err != nil {
			t.Fatalf("scan field edit: %v", err)
		}
		got[field] = userID + "/" + source
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("field edit rows: %v", err)
	}
	for _, field := range []string{"brand", "pack_quantity", "pack_unit"} {
		if got[field] != "user-manual-editor/manual" {
			t.Fatalf("field edit %s = %q, want user-manual-editor/manual; all=%+v", field, got[field], got)
		}
	}
	if _, ok := got["name"]; ok {
		t.Fatalf("name edit was recorded even though name was not in request: %+v", got)
	}
}
