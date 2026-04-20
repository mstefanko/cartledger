package spreadsheet

import (
	"context"
	"database/sql"
	"testing"
)

// seedCommittedReceipt inserts a realistic receipts row for the duplicate
// tests. store may be "" to leave the receipt's store_id NULL (such rows
// must never match a group, per CheckDuplicates's contract).
func seedCommittedReceipt(t *testing.T, database *sql.DB, householdID, storeName, date, totalStr string) string {
	t.Helper()
	var storeID sql.NullString
	if storeName != "" {
		id := "store-" + storeName
		_, err := database.Exec(
			`INSERT INTO stores (id, household_id, name) VALUES (?, ?, ?)`,
			id, householdID, storeName,
		)
		if err != nil {
			t.Fatalf("insert store: %v", err)
		}
		storeID = sql.NullString{String: id, Valid: true}
	}
	receiptID := "r-" + date + "-" + storeName
	_, err := database.Exec(
		`INSERT INTO receipts (id, household_id, store_id, receipt_date, total, source)
		 VALUES (?, ?, ?, ?, ?, 'import')`,
		receiptID, householdID, storeID, date, totalStr,
	)
	if err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	return receiptID
}

// Test 1: empty household → no duplicates.
func TestCheckDuplicates_EmptyHousehold(t *testing.T) {
	database := newCommitTestDB(t)
	groups := []Group{
		{ID: "g0", Date: "2026-03-10", Store: "Whole Foods", TotalCents: 499},
	}
	out, err := CheckDuplicates(context.Background(), database, "h1", groups)
	if err != nil {
		t.Fatalf("CheckDuplicates: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("got %d dup entries, want 0", len(out))
	}
}

// Test 2: identical match.
func TestCheckDuplicates_ExactMatch(t *testing.T) {
	database := newCommitTestDB(t)
	existing := seedCommittedReceipt(t, database, "h1", "Whole Foods", "2026-03-10", "4.99")
	groups := []Group{
		{ID: "g0", Date: "2026-03-10", Store: "Whole Foods", TotalCents: 499},
	}
	out, err := CheckDuplicates(context.Background(), database, "h1", groups)
	if err != nil {
		t.Fatalf("CheckDuplicates: %v", err)
	}
	if out["g0"] != existing {
		t.Errorf("g0 -> %q, want %q", out["g0"], existing)
	}
}

// Test 3: tolerance at the boundary.
func TestCheckDuplicates_Tolerance(t *testing.T) {
	database := newCommitTestDB(t)
	// Committed total = $4.99 (499 cents).
	existing := seedCommittedReceipt(t, database, "h1", "Whole Foods", "2026-03-10", "4.99")

	cases := []struct {
		label      string
		groupCents int64
		wantMatch  bool
	}{
		{"on the dot", 499, true},
		{"49 cents over", 548, true},    // 499 + 49 = under 50 tolerance
		{"exactly 50 over", 549, true},  // <= 50 qualifies
		{"51 cents over", 550, false},
		{"49 cents under", 450, true},
		{"exactly 50 under", 449, true},
		{"51 cents under", 448, false},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			groups := []Group{
				{ID: "g0", Date: "2026-03-10", Store: "Whole Foods", TotalCents: tc.groupCents},
			}
			out, err := CheckDuplicates(context.Background(), database, "h1", groups)
			if err != nil {
				t.Fatalf("CheckDuplicates: %v", err)
			}
			got := out["g0"] != ""
			if got != tc.wantMatch {
				t.Errorf("%s: match=%v, want %v (receipt=%s cents=%d)", tc.label, got, tc.wantMatch, existing, tc.groupCents)
			}
		})
	}
}

// Test 4: normalized store match.
func TestCheckDuplicates_NormalizedStore(t *testing.T) {
	database := newCommitTestDB(t)
	existing := seedCommittedReceipt(t, database, "h1", "Whole Foods", "2026-03-10", "4.99")

	// Incoming group spelled with trailing space + lower case.
	groups := []Group{
		{ID: "g0", Date: "2026-03-10", Store: "whole foods ", TotalCents: 499},
	}
	out, err := CheckDuplicates(context.Background(), database, "h1", groups)
	if err != nil {
		t.Fatalf("CheckDuplicates: %v", err)
	}
	if out["g0"] != existing {
		t.Errorf("normalized store mismatch: g0 -> %q, want %q", out["g0"], existing)
	}
}

// Test 5: different households isolated.
func TestCheckDuplicates_HouseholdIsolation(t *testing.T) {
	database := newCommitTestDB(t)
	if _, err := database.Exec(
		`INSERT INTO households (id, name) VALUES ('h2', 'Other')`,
	); err != nil {
		t.Fatalf("seed household: %v", err)
	}
	// Committed in h2, not h1.
	seedCommittedReceipt(t, database, "h2", "Whole Foods", "2026-03-10", "4.99")

	groups := []Group{
		{ID: "g0", Date: "2026-03-10", Store: "Whole Foods", TotalCents: 499},
	}
	out, err := CheckDuplicates(context.Background(), database, "h1", groups)
	if err != nil {
		t.Fatalf("CheckDuplicates: %v", err)
	}
	if _, found := out["g0"]; found {
		t.Error("cross-household leak: group matched an other-household receipt")
	}
}

// Test 6: groups with empty date/store are skipped.
func TestCheckDuplicates_EmptyDateOrStore(t *testing.T) {
	database := newCommitTestDB(t)
	seedCommittedReceipt(t, database, "h1", "Whole Foods", "2026-03-10", "4.99")

	groups := []Group{
		{ID: "empty-date", Date: "", Store: "Whole Foods", TotalCents: 499},
		{ID: "empty-store", Date: "2026-03-10", Store: "", TotalCents: 499},
	}
	out, err := CheckDuplicates(context.Background(), database, "h1", groups)
	if err != nil {
		t.Fatalf("CheckDuplicates: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("got %d dup entries, want 0 (both inputs ineligible)", len(out))
	}
}

// Test 7: different date → no match even if store + total line up.
func TestCheckDuplicates_DateMismatch(t *testing.T) {
	database := newCommitTestDB(t)
	seedCommittedReceipt(t, database, "h1", "Whole Foods", "2026-03-10", "4.99")

	groups := []Group{
		{ID: "g0", Date: "2026-03-11", Store: "Whole Foods", TotalCents: 499},
	}
	out, err := CheckDuplicates(context.Background(), database, "h1", groups)
	if err != nil {
		t.Fatalf("CheckDuplicates: %v", err)
	}
	if _, found := out["g0"]; found {
		t.Error("matched across dates")
	}
}

// decimalStringToCents unit tests — round-trip money formats the duplicate
// check may see on the receipts table.
func TestDecimalStringToCents(t *testing.T) {
	cases := []struct {
		in      string
		want    int64
		wantOK  bool
	}{
		{"4.99", 499, true},
		{"4.9", 490, true},
		{"5", 500, true},
		{"0.01", 1, true},
		{"4.995", 500, true}, // round-half-up on the third digit
		{"4.994", 499, true},
		{"-1.50", -150, true},
		{"", 0, false},
		{"abc", 0, false},
		{"4.99.00", 0, false},
	}
	for _, tc := range cases {
		got, ok := decimalStringToCents(tc.in)
		if ok != tc.wantOK {
			t.Errorf("decimalStringToCents(%q) ok=%v, want %v", tc.in, ok, tc.wantOK)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("decimalStringToCents(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
