package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
)

// dupTestDB wraps the duplicate-candidates test fixtures. The happy-path
// fixture picks product names whose pairwise Levenshtein similarity lands
// inside the default 0.6–0.85 band exactly once — so tests assert "exactly
// one pair surfaces" deterministically. Scores were computed from the
// exported matcher.Similarity helper: see comment in each test.
type dupTestDB struct {
	ph          *ProductHandler
	householdID string
	productIDs  map[string]string // name → id
}

// seedDupFixture inserts a household and a handful of named products into
// it. Returns the household id and a name→id map so tests don't need to
// re-query for ids.
func seedDupFixture(t *testing.T, ph *ProductHandler, householdName string, names []string) (string, map[string]string) {
	t.Helper()
	d := ph.DB
	var householdID string
	if err := d.QueryRow(
		"INSERT INTO households (name) VALUES (?) RETURNING id", householdName,
	).Scan(&householdID); err != nil {
		t.Fatalf("insert household %q: %v", householdName, err)
	}
	ids := make(map[string]string, len(names))
	for _, n := range names {
		var id string
		if err := d.QueryRow(
			"INSERT INTO products (household_id, name) VALUES (?, ?) RETURNING id",
			householdID, n,
		).Scan(&id); err != nil {
			t.Fatalf("insert product %q: %v", n, err)
		}
		ids[n] = id
	}
	return householdID, ids
}

// callDupCandidates executes the duplicate-candidates endpoint and decodes
// the response. Query string is passed through raw so tests can exercise
// limit/min_similarity/max_similarity as a group.
func callDupCandidates(t *testing.T, ph *ProductHandler, householdID, rawQuery string) duplicateCandidatesResponse {
	t.Helper()
	e := echo.New()
	target := "/api/v1/products/duplicate-candidates"
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set(auth.ContextKeyHouseholdID, householdID)
	if err := ph.DuplicateCandidates(c); err != nil {
		t.Fatalf("DuplicateCandidates: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp duplicateCandidatesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp
}

// callNotDup exercises the POST/DELETE not-duplicate-pairs endpoints.
// Returns the response recorder so callers can assert status codes.
func callNotDup(t *testing.T, ph *ProductHandler, method, householdID, aID, bID string) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	body := fmt.Sprintf(`{"product_a_id":%q,"product_b_id":%q}`, aID, bID)
	req := httptest.NewRequest(method, "/api/v1/products/not-duplicate-pairs", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set(auth.ContextKeyHouseholdID, householdID)
	var err error
	if method == http.MethodPost {
		err = ph.MarkNotDuplicate(c)
	} else {
		err = ph.UnmarkNotDuplicate(c)
	}
	if err != nil {
		t.Fatalf("%s MarkNotDuplicate: %v", method, err)
	}
	return rec
}

// TestDuplicateCandidates_HappyPath: exactly one pair surfaces in the
// default band. Scores for the fixture (computed via matcher.Similarity):
//   Apples Gala  ↔ Apples Gala Org = 0.7333 (in band)
//   Apples Gala  ↔ Diet Coke       = 0.1818
//   Apples Gala  ↔ Milk            = 0.0909
//   Apples Gala Org ↔ Diet Coke    = 0.2000
//   Apples Gala Org ↔ Milk         = 0.0667
//   Diet Coke    ↔ Milk            = 0.2222
func TestDuplicateCandidates_HappyPath(t *testing.T) {
	h, _, cleanup := newTestHandler(t)
	defer cleanup()

	hh, ids := seedDupFixture(t, h, "HH", []string{
		"Apples Gala", "Apples Gala Org", "Diet Coke", "Milk",
	})

	resp := callDupCandidates(t, h, hh, "")
	if resp.Count != 1 {
		t.Fatalf("expected count=1, got %d", resp.Count)
	}
	if len(resp.Pairs) != 1 {
		t.Fatalf("expected 1 pair, got %d", len(resp.Pairs))
	}
	pair := resp.Pairs[0]
	// Canonical order: A.id < B.id.
	aID := ids["Apples Gala"]
	bID := ids["Apples Gala Org"]
	wantA, wantB := aID, bID
	if wantA > wantB {
		wantA, wantB = wantB, wantA
	}
	if pair.A.ID != wantA || pair.B.ID != wantB {
		t.Errorf("canonical ordering broken: got (%s,%s), want (%s,%s)", pair.A.ID, pair.B.ID, wantA, wantB)
	}
	if pair.Similarity < 0.6 || pair.Similarity > 0.85 {
		t.Errorf("similarity outside band: %.4f", pair.Similarity)
	}
	if pair.A.SampleAliases == nil || pair.B.SampleAliases == nil {
		t.Errorf("sample_aliases should be non-nil (possibly empty) slices")
	}
}

// TestDuplicateCandidates_DismissedExcluded pre-inserts a row into
// not_duplicate_pairs for the only in-band pair; the endpoint must skip it.
func TestDuplicateCandidates_DismissedExcluded(t *testing.T) {
	h, _, cleanup := newTestHandler(t)
	defer cleanup()

	hh, ids := seedDupFixture(t, h, "HH", []string{
		"Apples Gala", "Apples Gala Org", "Diet Coke", "Milk",
	})

	aID := ids["Apples Gala"]
	bID := ids["Apples Gala Org"]
	lo, hi := aID, bID
	if lo > hi {
		lo, hi = hi, lo
	}
	if _, err := h.DB.Exec(
		"INSERT INTO not_duplicate_pairs (household_id, product_a_id, product_b_id) VALUES (?, ?, ?)",
		hh, lo, hi,
	); err != nil {
		t.Fatalf("pre-insert dismissal: %v", err)
	}

	resp := callDupCandidates(t, h, hh, "")
	if resp.Count != 0 || len(resp.Pairs) != 0 {
		t.Fatalf("dismissed pair should be filtered: count=%d pairs=%d", resp.Count, len(resp.Pairs))
	}
}

// TestDuplicateCandidates_HouseholdIsolation: HH1 cannot see HH2's pairs.
func TestDuplicateCandidates_HouseholdIsolation(t *testing.T) {
	h, _, cleanup := newTestHandler(t)
	defer cleanup()

	hh1, _ := seedDupFixture(t, h, "HH1", []string{"Apples Gala", "Apples Gala Org"})
	hh2, _ := seedDupFixture(t, h, "HH2", []string{"Bread Whole Wheat", "Bread Wheat"})

	resp1 := callDupCandidates(t, h, hh1, "")
	if resp1.Count != 1 || len(resp1.Pairs) != 1 {
		t.Fatalf("hh1 expected exactly its own pair, got count=%d", resp1.Count)
	}
	if !strings.Contains(resp1.Pairs[0].A.Name+resp1.Pairs[0].B.Name, "Apples") {
		t.Errorf("hh1 saw non-own product: %+v", resp1.Pairs[0])
	}
	resp2 := callDupCandidates(t, h, hh2, "")
	if resp2.Count != 1 || len(resp2.Pairs) != 1 {
		t.Fatalf("hh2 expected exactly its own pair, got count=%d", resp2.Count)
	}
	if !strings.Contains(resp2.Pairs[0].A.Name+resp2.Pairs[0].B.Name, "Bread") {
		t.Errorf("hh2 saw non-own product: %+v", resp2.Pairs[0])
	}
}

// TestMarkNotDuplicate_Canonicalizes: callers can pass the pair in either
// order — the row is inserted as (lower_id, higher_id).
func TestMarkNotDuplicate_Canonicalizes(t *testing.T) {
	h, _, cleanup := newTestHandler(t)
	defer cleanup()

	hh, ids := seedDupFixture(t, h, "HH", []string{"Apples Gala", "Apples Gala Org"})
	a := ids["Apples Gala"]
	b := ids["Apples Gala Org"]
	// Ensure we call with (bigger, smaller) to exercise canonicalization.
	bigger, smaller := a, b
	if bigger < smaller {
		bigger, smaller = smaller, bigger
	}

	rec := callNotDup(t, h, http.MethodPost, hh, bigger, smaller)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	var storedA, storedB string
	if err := h.DB.QueryRow(
		"SELECT product_a_id, product_b_id FROM not_duplicate_pairs WHERE household_id = ?", hh,
	).Scan(&storedA, &storedB); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if storedA >= storedB {
		t.Errorf("stored rows not canonical: got (%s,%s)", storedA, storedB)
	}

	// Repeat POST — INSERT OR IGNORE keeps it idempotent; still 204.
	rec2 := callNotDup(t, h, http.MethodPost, hh, bigger, smaller)
	if rec2.Code != http.StatusNoContent {
		t.Fatalf("second POST expected 204, got %d: %s", rec2.Code, rec2.Body.String())
	}
	var count int
	h.DB.QueryRow("SELECT COUNT(*) FROM not_duplicate_pairs WHERE household_id = ?", hh).Scan(&count)
	if count != 1 {
		t.Errorf("idempotency broken: count=%d", count)
	}
}

// TestMarkNotDuplicate_SelfPair: same id on both sides → 400.
func TestMarkNotDuplicate_SelfPair(t *testing.T) {
	h, _, cleanup := newTestHandler(t)
	defer cleanup()

	hh, ids := seedDupFixture(t, h, "HH", []string{"Apples Gala"})
	a := ids["Apples Gala"]

	rec := callNotDup(t, h, http.MethodPost, hh, a, a)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for self-pair, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestMarkNotDuplicate_CrossHousehold: one id belongs to a different
// household → 404 (we don't leak existence across households).
func TestMarkNotDuplicate_CrossHousehold(t *testing.T) {
	h, _, cleanup := newTestHandler(t)
	defer cleanup()

	hh1, ids1 := seedDupFixture(t, h, "HH1", []string{"Apples Gala"})
	_, ids2 := seedDupFixture(t, h, "HH2", []string{"Apples Gala Org"})

	rec := callNotDup(t, h, http.MethodPost, hh1, ids1["Apples Gala"], ids2["Apples Gala Org"])
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestUnmarkNotDuplicate_Removes: after DELETE the pair resurfaces in GET.
func TestUnmarkNotDuplicate_Removes(t *testing.T) {
	h, _, cleanup := newTestHandler(t)
	defer cleanup()

	hh, ids := seedDupFixture(t, h, "HH", []string{"Apples Gala", "Apples Gala Org"})
	a := ids["Apples Gala"]
	b := ids["Apples Gala Org"]

	// Mark it, verify GET is empty.
	if rec := callNotDup(t, h, http.MethodPost, hh, a, b); rec.Code != http.StatusNoContent {
		t.Fatalf("POST: %d %s", rec.Code, rec.Body.String())
	}
	if resp := callDupCandidates(t, h, hh, ""); resp.Count != 0 {
		t.Fatalf("expected 0 pairs after mark, got %d", resp.Count)
	}

	// Unmark, verify GET surfaces the pair again.
	if rec := callNotDup(t, h, http.MethodDelete, hh, a, b); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE: %d %s", rec.Code, rec.Body.String())
	}
	resp := callDupCandidates(t, h, hh, "")
	if resp.Count != 1 {
		t.Fatalf("expected 1 pair after unmark, got %d", resp.Count)
	}
}

// TestDuplicateCandidates_LimitAndMinSimilarity: seed 4 in-band pairs,
// exercise limit and min_similarity. Families are chosen so each family's
// two products pair only with each other (verified via matcher.Similarity —
// see the go run validation in the phase notes).
func TestDuplicateCandidates_LimitAndMinSimilarity(t *testing.T) {
	h, _, cleanup := newTestHandler(t)
	defer cleanup()

	// 4 distinct in-band families. Pair scores:
	//   Apples Gala ↔ Apples Gala Org      = 0.7333
	//   Bread Whole Wheat ↔ Bread Wheat    = 0.6471
	//   Coke 12pk Cans ↔ Coke 12 Pack Cans = 0.8235
	//   Milk Whole Gallon ↔ Milk Whole Gal = 0.8235
	hh, _ := seedDupFixture(t, h, "HH", []string{
		"Apples Gala", "Apples Gala Org",
		"Bread Whole Wheat", "Bread Wheat",
		"Coke 12pk Cans", "Coke 12 Pack Cans",
		"Milk Whole Gallon", "Milk Whole Gal",
	})

	// No limit → all 4 pairs.
	all := callDupCandidates(t, h, hh, "")
	if all.Count != 4 || len(all.Pairs) != 4 {
		t.Fatalf("expected 4 pairs, got count=%d len=%d", all.Count, len(all.Pairs))
	}
	// Sorted by similarity desc; the two 0.8235 pairs should be first.
	if all.Pairs[0].Similarity < all.Pairs[len(all.Pairs)-1].Similarity {
		t.Errorf("not sorted desc by similarity: %v", all.Pairs)
	}

	// Limit caps the slice but Count is the full total.
	limited := callDupCandidates(t, h, hh, "limit=2")
	if limited.Count != 4 {
		t.Errorf("Count should be total, got %d", limited.Count)
	}
	if len(limited.Pairs) != 2 {
		t.Errorf("Pairs length should match limit, got %d", len(limited.Pairs))
	}

	// Raise min_similarity past the Bread pair (0.6471 — lowest of the 4) → 3 survive.
	raised := callDupCandidates(t, h, hh, "min_similarity=0.7")
	if raised.Count != 3 {
		t.Errorf("expected 3 pairs with min=0.7, got %d", raised.Count)
	}
}

// TestDuplicateCandidates_LargeCatalog: catalog over duplicateMaxProducts
// (2000) short-circuits to an empty response. Tests the guard-rail, not the
// warn log; asserting log text across Go versions is brittle.
func TestDuplicateCandidates_LargeCatalog(t *testing.T) {
	h, _, cleanup := newTestHandler(t)
	defer cleanup()

	hh, _ := seedDupFixture(t, h, "HH", nil)

	// Bulk insert duplicateMaxProducts+1 products. Use a single transaction
	// so this stays under a second on a cold in-memory DB.
	tx, err := h.DB.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	stmt, err := tx.Prepare("INSERT INTO products (household_id, name) VALUES (?, ?)")
	if err != nil {
		tx.Rollback()
		t.Fatalf("prepare: %v", err)
	}
	for i := 0; i <= duplicateMaxProducts; i++ {
		if _, err := stmt.Exec(hh, fmt.Sprintf("bulk-prod-%05d", i)); err != nil {
			stmt.Close()
			tx.Rollback()
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	resp := callDupCandidates(t, h, hh, "")
	if resp.Count != 0 || len(resp.Pairs) != 0 {
		t.Fatalf("large catalog should short-circuit: count=%d len=%d", resp.Count, len(resp.Pairs))
	}
}
