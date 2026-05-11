package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mstefanko/cartledger/internal/db"
)

func TestBackfillStoreItemCodesReportsConflictsWithoutMapping(t *testing.T) {
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer database.Close()
	if err := db.RunMigrations(database); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	if _, err := database.Exec(`INSERT INTO households (id, name) VALUES ('h1', 'Test')`); err != nil {
		t.Fatalf("insert household: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO stores (id, household_id, name) VALUES ('s1', 'h1', 'Costco Wholesale')`); err != nil {
		t.Fatalf("insert store: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO products (id, household_id, name) VALUES
		('p_milk', 'h1', 'Milk'),
		('p_tortilla', 'h1', 'Tortillas'),
		('p_eggs', 'h1', 'Eggs')`); err != nil {
		t.Fatalf("insert products: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO receipts (id, household_id, store_id, receipt_date, status) VALUES
		('r1', 'h1', 's1', '2026-05-01', 'matched'),
		('r2', 'h1', 's1', '2026-05-02', 'matched'),
		('r3', 'h1', 's1', '2026-05-03', 'matched')`); err != nil {
		t.Fatalf("insert receipts: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO line_items
		(id, receipt_id, product_id, raw_name, total_price, matched)
		VALUES
		('li1', 'r1', 'p_milk', '8 2% MILK 1GAL', '2.92', 'manual'),
		('li2', 'r2', 'p_tortilla', '8 LC SOFT TACO', '6.49', 'manual'),
		('li3', 'r3', 'p_eggs', '9 EGGS 24CT', '5.99', 'manual')`); err != nil {
		t.Fatalf("insert line items: %v", err)
	}

	summary, err := backfillStoreItemCodes(context.Background(), database, true, 5)
	if err != nil {
		t.Fatalf("backfillStoreItemCodes: %v", err)
	}
	if summary.ConflictCount != 1 || len(summary.ConflictSamples) != 1 {
		t.Fatalf("conflicts = %d samples=%v, want one conflict sample", summary.ConflictCount, summary.ConflictSamples)
	}
	if len(summary.Conflicts) != 1 || summary.Conflicts[0].StoreItemCode != "8" || len(summary.Conflicts[0].ProductIDs) != 2 {
		t.Fatalf("conflict details = %+v, want code 8 with two products", summary.Conflicts)
	}
	if summary.LineItemsUpdated != 3 {
		t.Fatalf("line_items_updated = %d, want 3", summary.LineItemsUpdated)
	}

	reportPath, err := writeStoreCodeConflictReport(dir, summary.Conflicts, time.Date(2026, 5, 11, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("writeStoreCodeConflictReport: %v", err)
	}
	if !strings.HasSuffix(reportPath, filepath.Join("backups", "code-conflicts-20260511.json")) {
		t.Fatalf("report path = %q, want code-conflicts-20260511.json", reportPath)
	}
	report, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if !strings.Contains(string(report), `"store_item_code": "8"`) || !strings.Contains(string(report), `"raw_name": "8 2% MILK 1GAL"`) {
		t.Fatalf("report contents = %s", string(report))
	}

	var conflictingMappings int
	if err := database.QueryRow(
		`SELECT COUNT(*) FROM store_product_codes WHERE store_id = 's1' AND store_item_code = '8'`,
	).Scan(&conflictingMappings); err != nil {
		t.Fatalf("query conflicting mapping: %v", err)
	}
	if conflictingMappings != 0 {
		t.Fatalf("conflicting mappings = %d, want 0", conflictingMappings)
	}

	var productID, source string
	if err := database.QueryRow(
		`SELECT product_id, source FROM store_product_codes WHERE store_id = 's1' AND store_item_code = '9'`,
	).Scan(&productID, &source); err != nil {
		t.Fatalf("query non-conflicting mapping: %v", err)
	}
	if productID != "p_eggs" || source != "backfill" {
		t.Fatalf("mapping = (%q, %q), want (p_eggs, backfill)", productID, source)
	}
}
