package spreadsheet

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/mstefanko/cartledger/internal/db"
	"github.com/mstefanko/cartledger/internal/matcher"
)

// newCommitTestDB opens a fresh file-backed SQLite DB and runs the full
// migration set. Mirrors internal/matcher/backfill_test.go's pattern
// (modernc.org/sqlite can't reliably share a :memory: DB across multiple
// connections in this project).
func newCommitTestDB(t *testing.T) *sql.DB {
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
	return database
}

// fakeMatchEngine implements MatchEngine with a caller-controlled lookup so
// tests can pin matched/unmatched outcomes without depending on the real
// fuzzy-match pipeline's ordering.
type fakeMatchEngine struct {
	// results maps lowercased rawName -> MatchResult.
	results map[string]matcher.MatchResult
	// errOnCall, when non-nil, fires on the first call whose raw name is
	// present — used by the "per-receipt tx isolation" test to force a
	// single group to fail. It panics (simulating a matcher crash) so we
	// exercise the rollback path; the test recovers via commitGroup's
	// deferred tx.Rollback.
	panicRawNames map[string]bool
}

func (f *fakeMatchEngine) MatchWithSuggestion(rawName, _, _, _ string) matcher.MatchResult {
	if f.panicRawNames[rawName] {
		panic("simulated matcher failure for " + rawName)
	}
	if r, ok := f.results[rawName]; ok {
		return r
	}
	return matcher.MatchResult{Method: "unmatched"}
}

// NewSession deliberately errors so commit falls through to the per-call
// MatchWithSuggestion path above — that's what every fakeMatchEngine-backed
// test expects. Session-path coverage lives in internal/matcher/session_test.go.
func (f *fakeMatchEngine) NewSession(_, _ string) (*matcher.Session, error) {
	return nil, errors.New("fakeMatchEngine: session unsupported")
}

// rawSheet builds a ParsedSheet with the given rows laid out in the column
// order used by the mapping returned from stdMapping().
func rawSheet(rows ...[]string) *ParsedSheet {
	rr := make([]RawRow, len(rows))
	for i, cells := range rows {
		rr[i] = RawRow{Index: i, Cells: cells}
	}
	return &ParsedSheet{Name: "Sheet1", Headers: []string{"date", "store", "item", "qty", "unit_price", "total"}, Rows: rr}
}

// stdMapping mirrors the column layout in rawSheet: [date, store, item,
// qty, unit_price, total]. Unit role is unmapped; default "ea" applies.
func stdMapping() Mapping {
	return Mapping{
		RoleDate:       0,
		RoleStore:      1,
		RoleItem:       2,
		RoleQty:        3,
		RoleUnitPrice:  4,
		RoleTotalPrice: 5,
	}
}

func stdInput(sheet *ParsedSheet) CommitInput {
	return CommitInput{
		HouseholdID:    "h1",
		Sheet:          sheet,
		Mapping:        stdMapping(),
		DateFormat:     DateFmtISO,
		CSVOptions:     DefaultCSVOptions(),
		UnitOptions:    DefaultUnitOptions(),
		Grouping:       Grouping{Strategy: GroupDateStore},
		SourceFilename: "test.csv",
		SourceType:     "csv",
		Now:            time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC),
	}
}

// -----------------------------------------------------------------------------
// Test 1: Happy path — 3 groups across 2 dates, counters + stamping right.
// -----------------------------------------------------------------------------

func TestCommit_HappyPath(t *testing.T) {
	database := newCommitTestDB(t)
	sheet := rawSheet(
		[]string{"2026-03-10", "Whole Foods", "Milk", "1", "3.99", "3.99"},
		[]string{"2026-03-10", "Whole Foods", "Eggs", "1", "4.50", "4.50"},
		[]string{"2026-03-10", "Costco", "Paper Towels", "1", "19.99", "19.99"},
		[]string{"2026-03-11", "Whole Foods", "Bread", "1", "5.00", "5.00"},
	)
	in := stdInput(sheet)

	// Pre-create a matched product so one line resolves; the rest go
	// unmatched. This exercises both the product_prices / alias write path
	// AND the status='pending' (has unmatched) branch.
	if _, err := database.Exec(
		`INSERT INTO products (id, household_id, name) VALUES ('prod-milk', 'h1', 'Milk')`,
	); err != nil {
		t.Fatalf("insert product: %v", err)
	}
	me := &fakeMatchEngine{results: map[string]matcher.MatchResult{
		"Milk": {ProductID: "prod-milk", Confidence: 0.9, Method: "alias"},
	}}

	res, err := Commit(context.Background(), database, me, in)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if res.BatchID == "" {
		t.Fatal("BatchID empty")
	}
	if res.ReceiptsCreated != 3 {
		t.Errorf("ReceiptsCreated = %d, want 3", res.ReceiptsCreated)
	}
	if res.LineItemsCreated != 4 {
		t.Errorf("LineItemsCreated = %d, want 4", res.LineItemsCreated)
	}
	if res.UnmatchedLineItems != 3 {
		t.Errorf("UnmatchedLineItems = %d, want 3", res.UnmatchedLineItems)
	}

	// Every receipt stamped correctly.
	rows, err := database.Query(
		`SELECT source, import_batch_id, image_paths, raw_llm_json, llm_provider, status FROM receipts`)
	if err != nil {
		t.Fatalf("query receipts: %v", err)
	}
	defer rows.Close()
	count := 0
	statusCounts := map[string]int{}
	for rows.Next() {
		var src, bid string
		var img, raw, prov sql.NullString
		var status string
		if err := rows.Scan(&src, &bid, &img, &raw, &prov, &status); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if src != "import" {
			t.Errorf("source = %q, want import", src)
		}
		if bid != res.BatchID {
			t.Errorf("import_batch_id = %q, want %q", bid, res.BatchID)
		}
		if img.Valid || raw.Valid || prov.Valid {
			t.Errorf("expected image_paths/raw_llm_json/llm_provider all NULL, got %v/%v/%v", img, raw, prov)
		}
		statusCounts[status]++
		count++
	}
	if count != 3 {
		t.Errorf("receipts row count = %d, want 3", count)
	}
	// Two unmatched-only receipts stay 'pending'; the mixed Whole-Foods one
	// has one unmatched row so it also stays 'pending' (allMatched=false).
	if statusCounts["pending"] != 3 {
		t.Errorf("pending statuses = %d, want 3 (all groups had ≥1 unmatched item)", statusCounts["pending"])
	}

	// Every line_item stamped with import_batch_id.
	var liBatched int
	_ = database.QueryRow(
		`SELECT COUNT(*) FROM line_items WHERE import_batch_id = ?`, res.BatchID,
	).Scan(&liBatched)
	if liBatched != 4 {
		t.Errorf("line_items stamped = %d, want 4", liBatched)
	}

	// import_batches finalized.
	var rc, ic, uc int
	var completed sql.NullTime
	_ = database.QueryRow(
		`SELECT receipts_count, items_count, unmatched_count, completed_at
		 FROM import_batches WHERE id = ?`, res.BatchID,
	).Scan(&rc, &ic, &uc, &completed)
	if rc != 3 || ic != 4 || uc != 3 {
		t.Errorf("batch counters = %d/%d/%d, want 3/4/3", rc, ic, uc)
	}
	if !completed.Valid {
		t.Error("completed_at should be set")
	}

	// Matched product → alias + product_prices + purchase_count bump.
	var aliasCount int
	_ = database.QueryRow(
		`SELECT COUNT(*) FROM product_aliases WHERE product_id = 'prod-milk'`,
	).Scan(&aliasCount)
	if aliasCount != 1 {
		t.Errorf("alias count = %d, want 1", aliasCount)
	}
	var priceCount int
	_ = database.QueryRow(
		`SELECT COUNT(*) FROM product_prices WHERE product_id = 'prod-milk'`,
	).Scan(&priceCount)
	if priceCount != 1 {
		t.Errorf("product_prices count = %d, want 1", priceCount)
	}
	var pc int
	_ = database.QueryRow(`SELECT purchase_count FROM products WHERE id = 'prod-milk'`).Scan(&pc)
	if pc != 1 {
		t.Errorf("purchase_count = %d, want 1", pc)
	}
}

// TestCommit_AllMatchedBumpsStatus covers the companion branch: when every
// item in a receipt matches, status goes to 'matched'.
func TestCommit_AllMatchedBumpsStatus(t *testing.T) {
	database := newCommitTestDB(t)
	sheet := rawSheet(
		[]string{"2026-03-10", "Whole Foods", "Milk", "1", "3.99", "3.99"},
		[]string{"2026-03-10", "Whole Foods", "Eggs", "1", "4.50", "4.50"},
	)
	in := stdInput(sheet)

	if _, err := database.Exec(
		`INSERT INTO products (id, household_id, name) VALUES
		 ('p-milk', 'h1', 'Milk'),
		 ('p-eggs', 'h1', 'Eggs')`,
	); err != nil {
		t.Fatalf("insert products: %v", err)
	}
	me := &fakeMatchEngine{results: map[string]matcher.MatchResult{
		"Milk": {ProductID: "p-milk", Confidence: 0.9, Method: "alias"},
		"Eggs": {ProductID: "p-eggs", Confidence: 0.9, Method: "alias"},
	}}
	res, err := Commit(context.Background(), database, me, in)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	var status string
	_ = database.QueryRow(`SELECT status FROM receipts WHERE import_batch_id = ?`, res.BatchID).Scan(&status)
	if status != "matched" {
		t.Errorf("status = %q, want matched", status)
	}
}

// -----------------------------------------------------------------------------
// Test 2: Auto-create new store.
// -----------------------------------------------------------------------------

func TestCommit_AutoCreateStore(t *testing.T) {
	database := newCommitTestDB(t)
	// Pre-existing store NOT in the household — commit must not reuse it.
	if _, err := database.Exec(
		`INSERT INTO households (id, name) VALUES ('other', 'Other');
		 INSERT INTO stores (id, household_id, name) VALUES ('s-other', 'other', 'Whole Foods')`,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	sheet := rawSheet(
		[]string{"2026-03-10", "Whole Foods", "Milk", "1", "3.99", "3.99"},
	)
	res, err := Commit(context.Background(), database, &fakeMatchEngine{}, stdInput(sheet))
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}

	var storeCount int
	_ = database.QueryRow(
		`SELECT COUNT(*) FROM stores WHERE household_id = 'h1' AND name = 'Whole Foods'`,
	).Scan(&storeCount)
	if storeCount != 1 {
		t.Errorf("new store count = %d, want 1", storeCount)
	}

	var receiptStore sql.NullString
	_ = database.QueryRow(
		`SELECT store_id FROM receipts WHERE import_batch_id = ?`, res.BatchID,
	).Scan(&receiptStore)
	if !receiptStore.Valid {
		t.Error("receipt.store_id should not be NULL for auto-created store")
	}
}

// -----------------------------------------------------------------------------
// Test 3: Duplicate skipped by default.
// -----------------------------------------------------------------------------

func TestCommit_DuplicateSkippedByDefault(t *testing.T) {
	database := newCommitTestDB(t)
	sheet := rawSheet(
		[]string{"2026-03-10", "Whole Foods", "Milk", "1", "3.99", "3.99"},
	)

	// First commit — establishes the receipt.
	first, err := Commit(context.Background(), database, &fakeMatchEngine{}, stdInput(sheet))
	if err != nil {
		t.Fatalf("first Commit: %v", err)
	}
	if first.ReceiptsCreated != 1 {
		t.Fatalf("first batch expected 1 receipt, got %d", first.ReceiptsCreated)
	}

	// Second attempt — pre-populate Duplicates to signal the group is a dup.
	// With ConfirmedDuplicates empty, commit should skip it.
	in := stdInput(sheet)
	// Reproduce grouping to get the group ID
	parsed := make([]ParsedValue, 0, len(sheet.Rows))
	for _, raw := range sheet.Rows {
		parsed = append(parsed, NormalizeRow(raw, in.Mapping, in.UnitOptions, in.Grouping, in.DateFormat))
	}
	groups := GroupRows(parsed, in.Grouping)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	in.Duplicates = DuplicateMap{groups[0].ID: "some-existing-receipt-id"}

	second, err := Commit(context.Background(), database, &fakeMatchEngine{}, in)
	if err != nil {
		t.Fatalf("second Commit: %v", err)
	}
	if second.ReceiptsCreated != 0 {
		t.Errorf("ReceiptsCreated = %d, want 0", second.ReceiptsCreated)
	}
	if second.DuplicatesSkipped != 1 {
		t.Errorf("DuplicatesSkipped = %d, want 1", second.DuplicatesSkipped)
	}
}

// -----------------------------------------------------------------------------
// Test 4: Duplicate accepted when in ConfirmedDuplicates.
// -----------------------------------------------------------------------------

func TestCommit_DuplicateAccepted(t *testing.T) {
	database := newCommitTestDB(t)
	sheet := rawSheet(
		[]string{"2026-03-10", "Whole Foods", "Milk", "1", "3.99", "3.99"},
	)
	in := stdInput(sheet)

	parsed := make([]ParsedValue, 0, len(sheet.Rows))
	for _, raw := range sheet.Rows {
		parsed = append(parsed, NormalizeRow(raw, in.Mapping, in.UnitOptions, in.Grouping, in.DateFormat))
	}
	groups := GroupRows(parsed, in.Grouping)
	in.Duplicates = DuplicateMap{groups[0].ID: "fake-existing"}
	in.ConfirmedDuplicates = map[string]bool{groups[0].ID: true}

	res, err := Commit(context.Background(), database, &fakeMatchEngine{}, in)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if res.ReceiptsCreated != 1 {
		t.Errorf("ReceiptsCreated = %d, want 1", res.ReceiptsCreated)
	}
	if res.DuplicatesSkipped != 0 {
		t.Errorf("DuplicatesSkipped = %d, want 0", res.DuplicatesSkipped)
	}
}

// -----------------------------------------------------------------------------
// Test 5: IncludedGroupIDs filter.
// -----------------------------------------------------------------------------

func TestCommit_IncludedGroupIDsFilter(t *testing.T) {
	database := newCommitTestDB(t)
	sheet := rawSheet(
		[]string{"2026-03-10", "Whole Foods", "Milk", "1", "3.99", "3.99"},
		[]string{"2026-03-10", "Costco", "Paper Towels", "1", "19.99", "19.99"},
		[]string{"2026-03-11", "Target", "Soap", "1", "4.99", "4.99"},
	)
	in := stdInput(sheet)

	// Force grouping so we know IDs.
	parsed := make([]ParsedValue, 0, len(sheet.Rows))
	for _, raw := range sheet.Rows {
		parsed = append(parsed, NormalizeRow(raw, in.Mapping, in.UnitOptions, in.Grouping, in.DateFormat))
	}
	groups := GroupRows(parsed, in.Grouping)
	if len(groups) != 3 {
		t.Fatalf("grouping produced %d, want 3", len(groups))
	}
	in.IncludedGroupIDs = map[string]bool{groups[0].ID: true}

	res, err := Commit(context.Background(), database, &fakeMatchEngine{}, in)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if res.ReceiptsCreated != 1 {
		t.Errorf("ReceiptsCreated = %d, want 1 (only g0 included)", res.ReceiptsCreated)
	}
}

// -----------------------------------------------------------------------------
// Test 6: Matcher backfill integration — spreadsheet path leaves NULLs alone.
// -----------------------------------------------------------------------------

func TestCommit_BackfillLeavesNullsAlone(t *testing.T) {
	database := newCommitTestDB(t)
	// Product with NULL brand + NULL category.
	if _, err := database.Exec(
		`INSERT INTO products (id, household_id, name) VALUES ('p-milk', 'h1', 'Milk')`,
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	sheet := rawSheet(
		[]string{"2026-03-10", "Whole Foods", "Milk", "1", "3.99", "3.99"},
	)
	me := &fakeMatchEngine{results: map[string]matcher.MatchResult{
		"Milk": {ProductID: "p-milk", Confidence: 0.9, Method: "alias"},
	}}

	if _, err := Commit(context.Background(), database, me, stdInput(sheet)); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	var brand, category sql.NullString
	if err := database.QueryRow(
		`SELECT brand, category FROM products WHERE id = 'p-milk'`,
	).Scan(&brand, &category); err != nil {
		t.Fatalf("read product: %v", err)
	}
	if brand.Valid {
		t.Errorf("brand = %q, want NULL (spreadsheet carries no brand suggestion)", brand.String)
	}
	if category.Valid {
		t.Errorf("category = %q, want NULL (spreadsheet carries no category suggestion)", category.String)
	}
}

// -----------------------------------------------------------------------------
// Test 7: Per-receipt tx isolation.
// -----------------------------------------------------------------------------

// errEngine returns a plain unmatched result but records an error in a field
// and errors on INSERT. Simpler than panicking: we wrap the DB at call time.
// Instead of fighting the MatchEngine interface for this, we use a sentinel
// raw name and let the match result be a bogus productID that won't satisfy
// the products FK — the resulting tx INSERT error causes commitGroup to
// roll back and return an error, which Commit records.
type flakyEngine struct {
	badRawName string
	realID     string
}

func (f *flakyEngine) MatchWithSuggestion(rawName, _, _, _ string) matcher.MatchResult {
	if rawName == f.badRawName {
		// Pointing at a non-existent product_id triggers a FK violation on
		// line_items INSERT — commitGroup's tx rolls back, and Commit
		// captures the error in result.Errors.
		return matcher.MatchResult{ProductID: "nonexistent-product", Confidence: 0.9, Method: "alias"}
	}
	if rawName == "Milk" {
		return matcher.MatchResult{ProductID: f.realID, Confidence: 0.9, Method: "alias"}
	}
	return matcher.MatchResult{Method: "unmatched"}
}

// NewSession errors so commit falls through to the per-call
// MatchWithSuggestion path — the flakyEngine test fixture is built around
// return values from that method.
func (f *flakyEngine) NewSession(_, _ string) (*matcher.Session, error) {
	return nil, errors.New("flakyEngine: session unsupported")
}

func TestCommit_PerReceiptTxIsolation(t *testing.T) {
	database := newCommitTestDB(t)
	// Enable foreign_keys so the bad productID triggers an error.
	if _, err := database.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatalf("pragma: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO products (id, household_id, name) VALUES ('p-milk', 'h1', 'Milk')`,
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	sheet := rawSheet(
		// g0: succeeds (unmatched)
		[]string{"2026-03-10", "Whole Foods", "Milk", "1", "3.99", "3.99"},
		// g1: fails (bad product FK)
		[]string{"2026-03-11", "Costco", "POISON", "1", "1.00", "1.00"},
		// g2: succeeds
		[]string{"2026-03-12", "Target", "Soap", "1", "4.99", "4.99"},
	)
	me := &flakyEngine{badRawName: "POISON", realID: "p-milk"}

	res, err := Commit(context.Background(), database, me, stdInput(sheet))
	if err != nil {
		t.Fatalf("Commit returned setup error: %v", err)
	}
	if res.BatchID == "" {
		t.Fatal("BatchID empty even though 2/3 groups succeeded")
	}
	if res.ReceiptsCreated != 2 {
		t.Errorf("ReceiptsCreated = %d, want 2", res.ReceiptsCreated)
	}
	if len(res.Errors) != 1 {
		t.Errorf("Errors = %d, want 1", len(res.Errors))
	}

	// Batch counters reflect partial success.
	var rc, ic int
	_ = database.QueryRow(
		`SELECT receipts_count, items_count FROM import_batches WHERE id = ?`,
		res.BatchID,
	).Scan(&rc, &ic)
	if rc != 2 {
		t.Errorf("receipts_count = %d, want 2", rc)
	}
	if ic != 2 {
		t.Errorf("items_count = %d, want 2", ic)
	}

	// No dangling row for the failed group.
	var rowCount int
	_ = database.QueryRow(
		`SELECT COUNT(*) FROM receipts WHERE import_batch_id = ?`, res.BatchID,
	).Scan(&rowCount)
	if rowCount != 2 {
		t.Errorf("receipts in DB = %d, want 2 (failed tx should have rolled back)", rowCount)
	}
}

// -----------------------------------------------------------------------------
// Test 8: Money precision — $4.99 stays exactly "4.99".
// -----------------------------------------------------------------------------

func TestCommit_MoneyPrecision(t *testing.T) {
	database := newCommitTestDB(t)
	sheet := rawSheet(
		[]string{"2026-03-10", "Whole Foods", "Milk", "1", "4.99", "4.99"},
	)
	res, err := Commit(context.Background(), database, &fakeMatchEngine{}, stdInput(sheet))
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}

	var liTotal, liUnit sql.NullString
	if err := database.QueryRow(
		`SELECT total_price, unit_price FROM line_items WHERE import_batch_id = ?`,
		res.BatchID,
	).Scan(&liTotal, &liUnit); err != nil {
		t.Fatalf("read line_item: %v", err)
	}
	if !liTotal.Valid || liTotal.String != "4.99" {
		t.Errorf("total_price = %q, want 4.99", liTotal.String)
	}
	if !liUnit.Valid || liUnit.String != "4.99" {
		t.Errorf("unit_price = %q, want 4.99", liUnit.String)
	}

	var rTotal sql.NullString
	if err := database.QueryRow(
		`SELECT total FROM receipts WHERE import_batch_id = ?`, res.BatchID,
	).Scan(&rTotal); err != nil {
		t.Fatalf("read receipt: %v", err)
	}
	if !rTotal.Valid || rTotal.String != "4.99" {
		t.Errorf("receipt.total = %q, want 4.99", rTotal.String)
	}
}

// -----------------------------------------------------------------------------
// Test 9: MapRecorder fires per committed group.
// -----------------------------------------------------------------------------

func TestCommit_MapRecorderInvoked(t *testing.T) {
	database := newCommitTestDB(t)
	sheet := rawSheet(
		[]string{"2026-03-10", "Whole Foods", "Milk", "1", "3.99", "3.99"},
		[]string{"2026-03-11", "Costco", "TP", "1", "4.99", "4.99"},
	)
	in := stdInput(sheet)

	type recorded struct {
		groupID, receiptID string
		lines              int
	}
	var records []recorded
	in.MapRecorder = func(groupID, receiptID string, lineItemIDs []string) {
		records = append(records, recorded{groupID, receiptID, len(lineItemIDs)})
	}
	if _, err := Commit(context.Background(), database, &fakeMatchEngine{}, in); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("MapRecorder called %d times, want 2", len(records))
	}
	for _, r := range records {
		if r.receiptID == "" || r.groupID == "" || r.lines != 1 {
			t.Errorf("unexpected record: %+v", r)
		}
	}
}

// -----------------------------------------------------------------------------
// Test 10: validation errors.
// -----------------------------------------------------------------------------

func TestCommit_ValidationErrors(t *testing.T) {
	database := newCommitTestDB(t)
	cases := []struct {
		name  string
		apply func(c *CommitInput)
	}{
		{"missing household", func(c *CommitInput) { c.HouseholdID = "" }},
		{"bad source type", func(c *CommitInput) { c.SourceType = "weird" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := stdInput(rawSheet([]string{"2026-03-10", "X", "Y", "1", "1.00", "1.00"}))
			tc.apply(&in)
			_, err := Commit(context.Background(), database, &fakeMatchEngine{}, in)
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

// Sanity: without the Now override, Commit falls back to time.Now and doesn't
// panic. Uses a small sheet to keep the run fast.
func TestCommit_DefaultNowUsed(t *testing.T) {
	database := newCommitTestDB(t)
	sheet := rawSheet([]string{"2026-03-10", "X", "Y", "1", "1.00", "1.00"})
	in := stdInput(sheet)
	in.Now = time.Time{}
	_, err := Commit(context.Background(), database, &fakeMatchEngine{}, in)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

// Confirm that fmt.Sprintf isn't quietly imported from a removed path — unused
// imports would fail compile. This test mostly exists so `go test` with the
// -count=1 flag re-runs compilation even when the file set is unchanged.
var _ = fmt.Sprintf
