package search

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mstefanko/cartledger/internal/db"
)

// newSearchTestDB opens an in-memory SQLite file (via a tempdir, since
// the project's db.Open expects a file path) and runs every migration.
// Returns the *sql.DB + a seeded householdID.
func newSearchTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "search_test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	if err := db.RunMigrations(database); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	var householdID string
	if err := database.QueryRow(
		"INSERT INTO households (name) VALUES ('SearchTest') RETURNING id",
	).Scan(&householdID); err != nil {
		t.Fatalf("insert household: %v", err)
	}
	return database, householdID
}

// seedProduct inserts a product under householdID. brand may be empty.
func seedProduct(t *testing.T, database *sql.DB, householdID, name, brand string) string {
	t.Helper()
	var id string
	if brand == "" {
		if err := database.QueryRow(
			"INSERT INTO products (household_id, name) VALUES (?, ?) RETURNING id",
			householdID, name,
		).Scan(&id); err != nil {
			t.Fatalf("insert product %q: %v", name, err)
		}
	} else {
		if err := database.QueryRow(
			"INSERT INTO products (household_id, name, brand) VALUES (?, ?, ?) RETURNING id",
			householdID, name, brand,
		).Scan(&id); err != nil {
			t.Fatalf("insert product %q: %v", name, err)
		}
	}
	return id
}

// seedGroup inserts a product group under householdID.
func seedGroup(t *testing.T, database *sql.DB, householdID, name string) string {
	t.Helper()
	var id string
	if err := database.QueryRow(
		`INSERT INTO product_groups (id, household_id, name)
		 VALUES (lower(hex(randomblob(16))), ?, ?) RETURNING id`,
		householdID, name,
	).Scan(&id); err != nil {
		t.Fatalf("insert product_group %q: %v", name, err)
	}
	return id
}

// TestSanitizeFTSPrefixToken_FuzzCorpus verifies the sanitizer produces
// FTS5-safe output for every hostile token we can think of.
func TestSanitizeFTSPrefixToken_FuzzCorpus(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantEmpty bool // true when input should be rejected entirely
		wantHas   []string
		wantLacks []string
	}{
		{name: "empty", in: "", wantEmpty: true},
		{name: "single_char", in: "a", wantEmpty: true},
		{name: "whitespace_only", in: "   ", wantEmpty: true},
		{name: "plain_word", in: "milk", wantHas: []string{`"milk"*`}},
		{name: "uppercase_lowered", in: "MILK", wantHas: []string{`"milk"*`}},
		{name: "embedded_quote", in: `say"hi`, wantHas: []string{`"say""hi"*`}},
		{name: "double_quote_only", in: `""`, wantHas: []string{`""""*`}},
		{name: "asterisk_operator", in: "foo*", wantHas: []string{`"foo*"*`}, wantLacks: []string{` *`}},
		{name: "caret_operator", in: "^foo", wantHas: []string{`"^foo"*`}},
		{name: "near_operator", in: "NEAR(", wantHas: []string{`"near("*`}},
		{name: "apostrophe", in: "don't", wantHas: []string{`"don't"*`}},
		{name: "backslash", in: `a\b`, wantHas: []string{`"a\b"*`}},
		{name: "unicode_accents", in: "café", wantHas: []string{`"café"*`}},
		{name: "unicode_cjk", in: "豆腐", wantHas: []string{`"豆腐"*`}},
		{name: "mixed_junk", in: `"ab*^`, wantHas: []string{`"""ab*^"*`}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeFTSPrefixToken(tc.in)
			if tc.wantEmpty {
				if got != "" {
					t.Fatalf("sanitizeFTSPrefixToken(%q) = %q, want empty", tc.in, got)
				}
				return
			}
			if got == "" {
				t.Fatalf("sanitizeFTSPrefixToken(%q) = \"\", want non-empty", tc.in)
			}
			// Invariants: wrapped in double quotes with trailing *.
			if !strings.HasPrefix(got, `"`) || !strings.HasSuffix(got, `"*`) {
				t.Fatalf("sanitizeFTSPrefixToken(%q) = %q, want wrapped as \"tok\"*", tc.in, got)
			}
			for _, s := range tc.wantHas {
				if !strings.Contains(got, s) {
					t.Errorf("sanitizeFTSPrefixToken(%q) = %q, want contains %q", tc.in, got, s)
				}
			}
			for _, s := range tc.wantLacks {
				if strings.Contains(got, s) {
					t.Errorf("sanitizeFTSPrefixToken(%q) = %q, want NOT contains %q", tc.in, got, s)
				}
			}
		})
	}
}

// TestProductIDs_PrefixMatch covers the primary use case: typing a
// prefix like "tortil" should match "Tortilla Wraps".
func TestProductIDs_PrefixMatch(t *testing.T) {
	database, hh := newSearchTestDB(t)
	wantID := seedProduct(t, database, hh, "Tortilla Wraps", "")
	_ = seedProduct(t, database, hh, "Milk", "")
	_ = seedProduct(t, database, hh, "Bananas", "")

	ids, err := ProductIDs(context.Background(), database, hh, "tortil", 10)
	if err != nil {
		t.Fatalf("ProductIDs: %v", err)
	}
	if !containsID(ids, wantID) {
		t.Fatalf("expected %q (Tortilla Wraps) in results; got %v", wantID, ids)
	}
}

// TestProductIDs_MidWordMatch covers sahilm/fuzzy's mid-word matching,
// which FTS5 prefix alone cannot do.
func TestProductIDs_MidWordMatch(t *testing.T) {
	database, hh := newSearchTestDB(t)
	wantID := seedProduct(t, database, hh, "Organic Whole Milk", "")
	_ = seedProduct(t, database, hh, "Apples", "")
	_ = seedProduct(t, database, hh, "Carrots", "")

	// "whol" is a prefix of "Whole", which is in the middle of the name.
	// FTS5 tokenizes on whitespace, so "whol"* still matches the Whole
	// token. This verifies the prefix path works for mid-name tokens.
	ids, err := ProductIDs(context.Background(), database, hh, "whol", 10)
	if err != nil {
		t.Fatalf("ProductIDs: %v", err)
	}
	if !containsID(ids, wantID) {
		t.Fatalf("expected %q in results; got %v", wantID, ids)
	}
}

// TestProductIDs_TypoTolerance verifies the stage-2 in-memory fuzzy
// fallback rescues us when the user drops a character.
//
// Note: sahilm/fuzzy is a subsequence matcher — query characters must
// appear in the target in order. That handles omissions ("tortila"
// → "Tortilla") but not transpositions ("trotilla"). The plan calls out
// transposition as aspirational; we cover the omission case here since
// that's what the library actually supports.
func TestProductIDs_TypoTolerance(t *testing.T) {
	database, hh := newSearchTestDB(t)
	wantID := seedProduct(t, database, hh, "Tortilla Wraps", "")
	_ = seedProduct(t, database, hh, "Bananas", "")
	_ = seedProduct(t, database, hh, "Apples", "")

	// "tortila" (single dropped 'l') — FTS prefix "tortila"* misses
	// because no token starts with "tortila". Stage-2 sahilm/fuzzy
	// rescues it via subsequence match against "tortilla wraps".
	ids, err := ProductIDs(context.Background(), database, hh, "tortila", 10)
	if err != nil {
		t.Fatalf("ProductIDs: %v", err)
	}
	if !containsID(ids, wantID) {
		t.Fatalf("expected %q for typo query; got %v", wantID, ids)
	}
}

// TestProductIDs_MultiToken verifies multi-token prefix queries work
// (e.g. "milk org" matches "Organic Milk").
func TestProductIDs_MultiToken(t *testing.T) {
	database, hh := newSearchTestDB(t)
	wantID := seedProduct(t, database, hh, "Organic Milk", "")
	_ = seedProduct(t, database, hh, "Organic Apples", "")
	_ = seedProduct(t, database, hh, "Whole Milk Regular", "")

	ids, err := ProductIDs(context.Background(), database, hh, "org milk", 10)
	if err != nil {
		t.Fatalf("ProductIDs: %v", err)
	}
	if !containsID(ids, wantID) {
		t.Fatalf("expected %q (Organic Milk) for multi-token; got %v", wantID, ids)
	}
}

// TestProductIDs_EmptyResult verifies a query with no matches returns
// empty (not an error).
func TestProductIDs_EmptyResult(t *testing.T) {
	database, hh := newSearchTestDB(t)
	_ = seedProduct(t, database, hh, "Milk", "")
	_ = seedProduct(t, database, hh, "Eggs", "")

	// "zzzxyzqq" shares no meaningful characters with anything.
	ids, err := ProductIDs(context.Background(), database, hh, "zzzxyzqq", 10)
	if err != nil {
		t.Fatalf("ProductIDs: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("expected no matches, got %v", ids)
	}
}

// TestProductIDs_EmptyQuery returns nil (filter disabled) for blank
// input, and also for inputs that sanitize to empty (e.g. "a").
func TestProductIDs_EmptyQuery(t *testing.T) {
	database, hh := newSearchTestDB(t)
	_ = seedProduct(t, database, hh, "Milk", "")

	for _, q := range []string{"", "   ", "a"} {
		ids, err := ProductIDs(context.Background(), database, hh, q, 10)
		if err != nil {
			t.Fatalf("ProductIDs(%q): %v", q, err)
		}
		if ids != nil {
			t.Errorf("ProductIDs(%q) = %v, want nil (no filter)", q, ids)
		}
	}
}

// TestProductIDs_HouseholdScoping verifies a product in another household
// never leaks into the results.
func TestProductIDs_HouseholdScoping(t *testing.T) {
	database, hh := newSearchTestDB(t)

	var otherHH string
	if err := database.QueryRow(
		"INSERT INTO households (name) VALUES ('OtherHH') RETURNING id",
	).Scan(&otherHH); err != nil {
		t.Fatalf("insert other household: %v", err)
	}

	_ = seedProduct(t, database, otherHH, "Tortilla Wraps", "")
	mine := seedProduct(t, database, hh, "Tortilla Wraps", "")

	ids, err := ProductIDs(context.Background(), database, hh, "tortil", 10)
	if err != nil {
		t.Fatalf("ProductIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != mine {
		t.Fatalf("expected only %q (mine); got %v", mine, ids)
	}
}

// TestProductIDs_InjectionSafe — feeding FTS5-operator characters must
// not produce a MATCH syntax error, just an empty/benign result.
func TestProductIDs_InjectionSafe(t *testing.T) {
	database, hh := newSearchTestDB(t)
	_ = seedProduct(t, database, hh, "Milk", "")

	// Each case must not error. Result set may be empty.
	for _, q := range []string{
		`"`, `NEAR(`, `^foo`, `*`, `a\b`, `don't`, `abc OR def`,
	} {
		if _, err := ProductIDs(context.Background(), database, hh, q, 10); err != nil {
			t.Errorf("ProductIDs(%q) returned error: %v", q, err)
		}
	}
}

// TestGroupIDs_FuzzyMatch covers GroupIDs' in-memory ranking over
// product_groups.
func TestGroupIDs_FuzzyMatch(t *testing.T) {
	database, hh := newSearchTestDB(t)
	wantID := seedGroup(t, database, hh, "Milk Gallons")
	_ = seedGroup(t, database, hh, "Bananas")
	_ = seedGroup(t, database, hh, "Breads")

	ids, err := GroupIDs(context.Background(), database, hh, "milk", 10)
	if err != nil {
		t.Fatalf("GroupIDs: %v", err)
	}
	if !containsID(ids, wantID) {
		t.Fatalf("expected %q in results; got %v", wantID, ids)
	}
}

// TestGroupIDs_Typo verifies typo tolerance for groups. Uses an
// omission-style typo (dropped character), which sahilm/fuzzy supports.
func TestGroupIDs_Typo(t *testing.T) {
	database, hh := newSearchTestDB(t)
	wantID := seedGroup(t, database, hh, "Tortilla Packs")
	_ = seedGroup(t, database, hh, "Apples")

	ids, err := GroupIDs(context.Background(), database, hh, "tortila", 10)
	if err != nil {
		t.Fatalf("GroupIDs: %v", err)
	}
	if !containsID(ids, wantID) {
		t.Fatalf("expected %q for typo; got %v", wantID, ids)
	}
}

// TestGroupIDs_EmptyQuery returns nil for blank input.
func TestGroupIDs_EmptyQuery(t *testing.T) {
	database, hh := newSearchTestDB(t)
	_ = seedGroup(t, database, hh, "Anything")

	ids, err := GroupIDs(context.Background(), database, hh, "", 10)
	if err != nil {
		t.Fatalf("GroupIDs: %v", err)
	}
	if ids != nil {
		t.Errorf("GroupIDs(\"\") = %v, want nil", ids)
	}
}

// TestGroupIDs_HouseholdScoping verifies a group in another household is
// not returned.
func TestGroupIDs_HouseholdScoping(t *testing.T) {
	database, hh := newSearchTestDB(t)
	var otherHH string
	if err := database.QueryRow(
		"INSERT INTO households (name) VALUES ('OtherHH') RETURNING id",
	).Scan(&otherHH); err != nil {
		t.Fatalf("insert other household: %v", err)
	}
	_ = seedGroup(t, database, otherHH, "Milk Gallons")
	mine := seedGroup(t, database, hh, "Milk Gallons")

	ids, err := GroupIDs(context.Background(), database, hh, "milk", 10)
	if err != nil {
		t.Fatalf("GroupIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != mine {
		t.Fatalf("expected only %q; got %v", mine, ids)
	}
}

// TestMergeUniqueKeepOrder is a quick unit for the merge helper.
func TestMergeUniqueKeepOrder(t *testing.T) {
	got := mergeUniqueKeepOrder([]string{"a", "b", "c"}, []string{"b", "d", "a", "e"})
	want := []string{"a", "b", "c", "d", "e"}
	if !equalSlice(got, want) {
		t.Fatalf("mergeUniqueKeepOrder = %v, want %v", got, want)
	}

	if got := mergeUniqueKeepOrder(nil, []string{"x"}); !equalSlice(got, []string{"x"}) {
		t.Errorf("primary=nil: %v", got)
	}
	if got := mergeUniqueKeepOrder([]string{"x"}, nil); !equalSlice(got, []string{"x"}) {
		t.Errorf("extra=nil: %v", got)
	}
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
