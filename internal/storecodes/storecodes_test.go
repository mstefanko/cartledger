package storecodes

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mstefanko/cartledger/internal/db"
)

func TestNormalize(t *testing.T) {
	longCode := strings.Repeat("9", 70)
	if got := Normalize("  1407506  "); got != "1407506" {
		t.Fatalf("Normalize trim = %q, want 1407506", got)
	}
	if got := Normalize(longCode); len(got) != 64 {
		t.Fatalf("Normalize cap length = %d, want 64", len(got))
	}
}

func TestUpsertReceiptDoesNotOverwriteManualMapping(t *testing.T) {
	database := newStoreCodesTestDB(t)
	ctx := context.Background()
	t1 := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	label := "2% MILK 1GAL"

	if err := UpsertManual(ctx, database, "h1", "s1", "p_manual", "8", &label, t1); err != nil {
		t.Fatalf("UpsertManual: %v", err)
	}
	if err := UpsertReceipt(ctx, database, "h1", "s1", "p_receipt", "8", &label, t2); err != nil {
		t.Fatalf("UpsertReceipt: %v", err)
	}

	var productID, source string
	var confidence sql.NullFloat64
	var lastSeen time.Time
	if err := database.QueryRow(
		`SELECT product_id, source, confidence, last_seen_at
		   FROM store_product_codes WHERE store_id = 's1' AND store_item_code = '8'`,
	).Scan(&productID, &source, &confidence, &lastSeen); err != nil {
		t.Fatalf("query mapping: %v", err)
	}
	if productID != "p_manual" || source != "manual" || confidence.Valid {
		t.Fatalf("mapping = product %q source %q confidence %v, want manual product/source and NULL confidence", productID, source, confidence)
	}
	if !lastSeen.Equal(t2) {
		t.Fatalf("last_seen_at = %s, want %s", lastSeen, t2)
	}
}

func newStoreCodesTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := db.RunMigrations(database); err != nil {
		database.Close()
		t.Fatalf("RunMigrations: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	if _, err := database.Exec(`INSERT INTO households (id, name) VALUES ('h1', 'Test')`); err != nil {
		t.Fatalf("insert household: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO stores (id, household_id, name) VALUES ('s1', 'h1', 'Costco')`); err != nil {
		t.Fatalf("insert store: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO products (id, household_id, name) VALUES
		('p_manual', 'h1', 'Manual Milk'),
		('p_receipt', 'h1', 'Receipt Milk')`); err != nil {
		t.Fatalf("insert products: %v", err)
	}
	return database
}
