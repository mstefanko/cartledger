package worker

import (
	"path/filepath"
	"testing"

	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
	enrichmentrunner "github.com/mstefanko/cartledger/internal/enrichment/runner"
)

func TestQueueReceiptScanEnrichmentQueuesUPCLookupsWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer database.Close()
	if err := db.RunMigrations(database); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	var householdID string
	if err := database.QueryRow("INSERT INTO households (name) VALUES ('Test') RETURNING id").Scan(&householdID); err != nil {
		t.Fatalf("insert household: %v", err)
	}
	var productID string
	if err := database.QueryRow(
		"INSERT INTO products (household_id, name, upc) VALUES (?, 'Mission Tortillas', '036000291452') RETURNING id",
		householdID,
	).Scan(&productID); err != nil {
		t.Fatalf("insert product: %v", err)
	}
	receiptID := "receipt-enrichment"
	if _, err := database.Exec(
		"INSERT INTO receipts (id, household_id, receipt_date, total, status) VALUES (?, ?, '2026-05-13', '4.99', 'matched')",
		receiptID, householdID,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO line_items (receipt_id, product_id, raw_name, quantity, total_price, matched, upc)
		 VALUES (?, ?, 'MISSION TORTILLAS', '1', '4.99', 'identifier', '7373100010')`,
		receiptID, productID,
	); err != nil {
		t.Fatalf("insert line item: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO product_enrichment_settings
		    (household_id, auto_on_scan_enabled, provider_openfoodfacts_enabled)
		 VALUES (?, 1, 1)`,
		householdID,
	); err != nil {
		t.Fatalf("insert enrichment settings: %v", err)
	}

	service := enrichmentrunner.NewService(database, &config.Config{ProductEnrichmentEnabled: true, ProductEnrichmentAutoOnScan: true}, nil)
	w := &ReceiptWorker{db: database, enrichment: service}
	w.queueReceiptScanEnrichment(householdID, receiptID)

	var count int
	var trigger, lookupKey, status string
	if err := database.QueryRow(
		`SELECT COUNT(*), COALESCE(MAX(trigger), ''), COALESCE(MAX(lookup_key), ''), COALESCE(MAX(status), '')
		   FROM product_enrichment_jobs
		  WHERE household_id = ? AND product_id = ?`,
		householdID, productID,
	).Scan(&count, &trigger, &lookupKey, &status); err != nil {
		t.Fatalf("query jobs: %v", err)
	}
	if count != 1 {
		t.Fatalf("job count = %d, want 1", count)
	}
	if trigger != enrichmentrunner.TriggerReceiptScan {
		t.Fatalf("trigger = %q, want %q", trigger, enrichmentrunner.TriggerReceiptScan)
	}
	if lookupKey != "upc:036000291452" {
		t.Fatalf("lookup_key = %q, want normalized UPC lookup", lookupKey)
	}
	if status != enrichmentrunner.StatusQueued {
		t.Fatalf("status = %q, want queued", status)
	}
}

func TestQueueReceiptScanEnrichmentSkipsWhenAutoDisabled(t *testing.T) {
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer database.Close()
	if err := db.RunMigrations(database); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	var householdID string
	if err := database.QueryRow("INSERT INTO households (name) VALUES ('Test') RETURNING id").Scan(&householdID); err != nil {
		t.Fatalf("insert household: %v", err)
	}
	var productID string
	if err := database.QueryRow(
		"INSERT INTO products (household_id, name, upc) VALUES (?, 'Cereal', '036000291452') RETURNING id",
		householdID,
	).Scan(&productID); err != nil {
		t.Fatalf("insert product: %v", err)
	}
	receiptID := "receipt-no-auto-enrichment"
	if _, err := database.Exec(
		"INSERT INTO receipts (id, household_id, receipt_date, total, status) VALUES (?, ?, '2026-05-13', '3.99', 'matched')",
		receiptID, householdID,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO line_items (receipt_id, product_id, raw_name, quantity, total_price, matched)
		 VALUES (?, ?, 'CEREAL', '1', '3.99', 'identifier')`,
		receiptID, productID,
	); err != nil {
		t.Fatalf("insert line item: %v", err)
	}

	service := enrichmentrunner.NewService(database, &config.Config{ProductEnrichmentEnabled: true, ProductEnrichmentAutoOnScan: true}, nil)
	w := &ReceiptWorker{db: database, enrichment: service}
	w.queueReceiptScanEnrichment(householdID, receiptID)

	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM product_enrichment_jobs").Scan(&count); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if count != 0 {
		t.Fatalf("job count = %d, want 0 when auto lookup is disabled", count)
	}
}
