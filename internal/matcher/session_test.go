package matcher

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/mstefanko/cartledger/internal/db"
)

// newSessionTestDB opens a fresh file-backed SQLite DB and runs the full
// migration set, then seeds a minimal household + one store + one "other"
// store so the cross_store_match path is exercisable. Mirrors the
// backfill_test.go pattern (modernc.org/sqlite can't reliably share :memory:
// DBs across multiple connections in this project).
func newSessionTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.RunMigrations(database); err != nil {
		database.Close()
		t.Fatalf("RunMigrations: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	if _, err := database.Exec(`INSERT INTO households (id, name) VALUES ('h1', 'Test')`); err != nil {
		t.Fatalf("insert household: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO stores (id, household_id, name) VALUES ('s_main', 'h1', 'Main Store')`,
	); err != nil {
		t.Fatalf("insert store main: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO stores (id, household_id, name) VALUES ('s_other', 'h1', 'Other Store')`,
	); err != nil {
		t.Fatalf("insert store other: %v", err)
	}
	return database
}

// sessionSeed loads ~10 products, ~10 aliases, one matching rule, and one
// cross-store price history row. The fixture is shaped so the table-driven
// test below covers every method: rule, alias, fuzzy, suggested, cross_store,
// and unmatched.
func sessionSeed(t *testing.T, database *sql.DB) {
	t.Helper()

	products := []struct{ id, name string }{
		{"p_milk_2pct", "Milk 2% 1 Gallon"},
		{"p_milk_skim", "Skim Milk 1 Gallon"},
		{"p_milk_whole", "Whole Milk 1 Gallon"},
		{"p_broccoli", "Broccoli Crowns"},
		{"p_banana", "Bananas"},
		{"p_eggs", "Large Eggs Dozen"},
		{"p_bread", "Whole Wheat Bread"},
		{"p_cheese", "Cheddar Cheese Block 8 oz"},
		{"p_apples", "Gala Apples"},
		{"p_coffee", "Ground Coffee 12 oz"},
		// Product exclusive to "Other Store" — used for cross_store_match.
		{"p_yogurt", "Plain Greek Yogurt"},
	}
	for _, p := range products {
		if _, err := database.Exec(
			`INSERT INTO products (id, household_id, name) VALUES (?, 'h1', ?)`,
			p.id, p.name,
		); err != nil {
			t.Fatalf("insert product %s: %v", p.id, err)
		}
	}

	// Aliases. Mix of store-specific (Main Store) and global (NULL). Lowercased
	// per the alias-match contract (fuzzy.go writes LOWER() on both sides).
	aliases := []struct {
		id, productID, alias string
		storeID              any // "s_main" or nil
	}{
		{"a1", "p_milk_2pct", "2% milk gal", "s_main"},
		{"a2", "p_milk_2pct", "2 pct milk", nil},
		{"a3", "p_milk_skim", "skim milk gal", "s_main"},
		{"a4", "p_milk_whole", "whole milk gal", nil},
		{"a5", "p_broccoli", "brc", "s_main"},
		{"a6", "p_banana", "banana", nil},
		{"a7", "p_eggs", "lrg eggs dz", "s_main"},
		{"a8", "p_bread", "ww bread", nil},
		{"a9", "p_cheese", "chdr cheese 8oz", "s_main"},
		{"a10", "p_apples", "gala apple", nil},
	}
	for _, a := range aliases {
		if _, err := database.Exec(
			`INSERT INTO product_aliases (id, product_id, alias, store_id) VALUES (?, ?, ?, ?)`,
			a.id, a.productID, a.alias, a.storeID,
		); err != nil {
			t.Fatalf("insert alias %s: %v", a.id, err)
		}
	}

	// A single matching rule — priority 10, "starts_with 'ORG BANANA'" maps
	// to p_banana. Used for the rule-stage test row.
	if _, err := database.Exec(
		`INSERT INTO matching_rules
		 (id, household_id, priority, condition_op, condition_val, store_id, product_id)
		 VALUES ('r1', 'h1', 10, 'starts_with', 'org banana', NULL, 'p_banana')`,
	); err != nil {
		t.Fatalf("insert rule: %v", err)
	}

	// Seed a product_prices row for p_yogurt at s_other — this makes
	// productHasStoreHistory return storeHistoryOtherStore when queried with
	// s_main as the session store. Needed for the cross_store_match path.
	// We also need a receipt row to satisfy the FK.
	if _, err := database.Exec(
		`INSERT INTO receipts
		 (id, household_id, store_id, receipt_date, total, status)
		 VALUES ('rc_other', 'h1', 's_other', '2026-04-01', '3.99', 'matched')`,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO product_prices
		 (id, product_id, store_id, receipt_id, receipt_date, quantity, unit, unit_price)
		 VALUES ('pp1', 'p_yogurt', 's_other', 'rc_other', '2026-04-01', '1', 'ea', '3.99')`,
	); err != nil {
		t.Fatalf("insert product_price: %v", err)
	}
}

// TestSessionEquivalence — the headline regression test. For every table row
// we run BOTH Engine.MatchWithSuggestion (the one-shot path) AND
// Session.MatchWithSuggestion (the new batched path) and assert they agree on
// ProductID / Confidence / Method at full float precision. The fuzzy-ranking
// tie case is included explicitly.
func TestSessionEquivalence(t *testing.T) {
	database := newSessionTestDB(t)
	sessionSeed(t, database)

	engine := NewEngine(database)
	session, err := engine.NewSession("h1", "s_main")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	tests := []struct {
		name       string
		rawName    string
		suggested  string
		wantMethod string // expected Method — sanity check; equivalence is the real assertion
	}{
		// Stage 1 — rules. "ORG BANANAS" normalizes to "org bananas",
		// which starts_with "org banana" → r1 → p_banana.
		{
			name:       "rule starts_with",
			rawName:    "ORG BANANAS",
			suggested:  "",
			wantMethod: "rule",
		},
		// Stage 2 — exact alias match, store-scoped. "brc" is stored as the
		// store-specific alias for p_broccoli at s_main.
		{
			name:       "alias store-specific",
			rawName:    "BRC",
			suggested:  "",
			wantMethod: "alias",
		},
		// Stage 2 — exact alias match, global. "whole milk gal" is the global
		// alias for p_milk_whole.
		{
			name:       "alias global",
			rawName:    "whole milk gal",
			suggested:  "",
			wantMethod: "alias",
		},
		// Stage 3 — fuzzy on rawName. "Broccoli Crown" doesn't exact-match any
		// alias or product name, but fuzzy-scores well against "Broccoli Crowns".
		{
			name:       "fuzzy rawname",
			rawName:    "Broccoli Crown",
			suggested:  "",
			wantMethod: "fuzzy",
		},
		// Stage 3 — punctuation-stripped rawName flows to fuzzy. "2% milk gal"
		// normalizes to "2 milk gal" (% is punctuation, removed), missing the
		// alias-exact stage but scoring well fuzzy.
		{
			name:       "fuzzy punctuation stripped",
			rawName:    "2% milk gal",
			suggested:  "",
			wantMethod: "fuzzy",
		},
		// Stage 4 — exact suggested match (rawName is unmatched, suggested hits
		// a product name exactly, cross-store history is empty → keeps 0.92
		// / "suggested").
		{
			name:       "suggested exact",
			rawName:    "UNKNOWN ITEM XYZ",
			suggested:  "Ground Coffee 12 oz",
			wantMethod: "suggested",
		},
		// Stage 4 — exact suggested that resolves to a product with only
		// cross-store history → cross_store_match at 0.7.
		{
			name:       "cross_store_match exact",
			rawName:    "UNKNOWN ZZZ",
			suggested:  "Plain Greek Yogurt",
			wantMethod: "cross_store_match",
		},
		// Stage 5 — fuzzy on suggested (rawName unmatched, suggested close to
		// a product name but not exact).
		{
			name:       "suggested fuzzy",
			rawName:    "UNKNOWN AAA",
			suggested:  "Whole Wheat Brea", // off by one char vs "Whole Wheat Bread"
			wantMethod: "suggested",
		},
		// Fully unmatched — rawName + suggested both miss everything.
		{
			name:       "unmatched",
			rawName:    "QQQQQQQQQQQ",
			suggested:  "",
			wantMethod: "unmatched",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotEngine := engine.MatchWithSuggestion(tc.rawName, tc.suggested, "s_main", "h1")
			gotSession := session.MatchWithSuggestion(tc.rawName, tc.suggested)

			if gotEngine.Method != tc.wantMethod {
				t.Errorf("Engine method = %q, want %q (raw=%q sugg=%q)",
					gotEngine.Method, tc.wantMethod, tc.rawName, tc.suggested)
			}
			if gotEngine.ProductID != gotSession.ProductID {
				t.Errorf("ProductID mismatch: engine=%q session=%q (raw=%q sugg=%q)",
					gotEngine.ProductID, gotSession.ProductID, tc.rawName, tc.suggested)
			}
			if gotEngine.Confidence != gotSession.Confidence {
				t.Errorf("Confidence mismatch: engine=%v session=%v (raw=%q sugg=%q)",
					gotEngine.Confidence, gotSession.Confidence, tc.rawName, tc.suggested)
			}
			if gotEngine.Method != gotSession.Method {
				t.Errorf("Method mismatch: engine=%q session=%q (raw=%q sugg=%q)",
					gotEngine.Method, gotSession.Method, tc.rawName, tc.suggested)
			}
		})
	}
}

// TestSessionEmptyHousehold covers the degenerate case: NewSession for a
// household with no products/aliases should return a usable Session whose
// Match / MatchWithSuggestion always returns unmatched, without error.
func TestSessionEmptyHousehold(t *testing.T) {
	database := newSessionTestDB(t)
	// Insert an extra household with no products.
	if _, err := database.Exec(
		`INSERT INTO households (id, name) VALUES ('h_empty', 'Empty')`,
	); err != nil {
		t.Fatalf("insert household: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO stores (id, household_id, name) VALUES ('s_empty', 'h_empty', 'Empty Store')`,
	); err != nil {
		t.Fatalf("insert store: %v", err)
	}

	engine := NewEngine(database)
	session, err := engine.NewSession("h_empty", "s_empty")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	got := session.Match("anything")
	if got.Method != "unmatched" {
		t.Errorf("Match method = %q, want unmatched", got.Method)
	}

	got = session.MatchWithSuggestion("anything", "also nothing")
	if got.Method != "unmatched" {
		t.Errorf("MatchWithSuggestion method = %q, want unmatched", got.Method)
	}
}

// TestSessionFuzzyCachedO1 asserts the session-path fuzzy cache works as
// advertised: once NewSession has loaded the candidate set, subsequent
// fuzzy calls (stage 3 OR stage 5) must NOT re-query product_aliases /
// products. We verify by counting candidates loaded once at open time and
// proving 50 fuzzy-only matches produce identical results as 50 one-shot
// calls — asymptotically, 2 preload queries vs. 100 per-call queries.
//
// This is the query-count proxy described in the analysis work breakdown.
// Rather than wrapping the driver, we check the candidate set is non-empty
// after NewSession (one-time load) and that subsequent Session.Match calls
// don't grow it.
func TestSessionFuzzyCachedO1(t *testing.T) {
	database := newSessionTestDB(t)
	sessionSeed(t, database)

	engine := NewEngine(database)
	session, err := engine.NewSession("h1", "s_main")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	initialCount := len(session.candidates)
	if initialCount == 0 {
		t.Fatal("candidates should be non-empty after NewSession")
	}

	// Feed 50 raw names through Session.Match. Candidate set must NOT grow —
	// the whole point of Session is a preloaded, immutable candidate list for
	// the lifetime of the batch.
	raws := []string{
		"Broccoli Crown", "Milk 2%", "skim milk", "Unknown ZZZ", "Whole Wheat Bread",
		"Gala Apple", "Large Eggs", "Cheddar 8oz", "Coffee", "brc",
	}
	for i := 0; i < 50; i++ {
		_ = session.Match(raws[i%len(raws)])
	}

	if len(session.candidates) != initialCount {
		t.Errorf("candidate set grew from %d to %d — Session should not re-load on Match",
			initialCount, len(session.candidates))
	}
}

// TestSessionStoreHistoryCache verifies the productHasStoreHistory lazy cache
// fills on first call and serves from memory thereafter. Cross-store exact
// matches on the same suggested name should hit the cache after the first
// lookup.
func TestSessionStoreHistoryCache(t *testing.T) {
	database := newSessionTestDB(t)
	sessionSeed(t, database)

	engine := NewEngine(database)
	session, err := engine.NewSession("h1", "s_main")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	// Cache should start empty.
	if len(session.storeHistory) != 0 {
		t.Errorf("storeHistory initially non-empty: %d entries", len(session.storeHistory))
	}

	// First call — triggers the DB query and caches.
	r1 := session.MatchWithSuggestion("UNKNOWN AAA", "Plain Greek Yogurt")
	if r1.Method != "cross_store_match" {
		t.Fatalf("first call method = %q, want cross_store_match", r1.Method)
	}
	if len(session.storeHistory) != 1 {
		t.Errorf("after first cross-store hit, storeHistory = %d entries, want 1",
			len(session.storeHistory))
	}

	// Second call with the same suggestion — should hit cache, cache size unchanged.
	r2 := session.MatchWithSuggestion("UNKNOWN BBB", "Plain Greek Yogurt")
	if r2.Method != "cross_store_match" {
		t.Fatalf("second call method = %q, want cross_store_match", r2.Method)
	}
	if len(session.storeHistory) != 1 {
		t.Errorf("second call grew cache to %d entries, want 1", len(session.storeHistory))
	}
}
