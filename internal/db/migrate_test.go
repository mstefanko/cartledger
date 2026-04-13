package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrationsUpAndDown(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()
	defer os.Remove(dbPath)

	// Run up migrations.
	if err := RunMigrations(database); err != nil {
		t.Fatalf("run migrations up: %v", err)
	}

	// Verify tables exist by querying sqlite_master.
	tables := []string{
		"households", "users", "stores", "products", "product_aliases",
		"matching_rules", "receipts", "line_items", "product_prices",
		"shopping_lists", "shopping_list_items", "unit_conversions",
		"product_images", "product_links",
		"products_fts", "product_aliases_fts",
	}
	for _, table := range tables {
		var name string
		err := database.QueryRow(
			"SELECT name FROM sqlite_master WHERE type IN ('table', 'view') AND name = ?", table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q not found: %v", table, err)
		}
	}

	// Verify triggers exist.
	triggers := []string{
		"products_fts_insert", "products_fts_update", "products_fts_delete",
		"aliases_fts_insert", "aliases_fts_update", "aliases_fts_delete",
	}
	for _, trigger := range triggers {
		var name string
		err := database.QueryRow(
			"SELECT name FROM sqlite_master WHERE type = 'trigger' AND name = ?", trigger,
		).Scan(&name)
		if err != nil {
			t.Errorf("trigger %q not found: %v", trigger, err)
		}
	}

	// Verify indexes exist.
	indexes := []string{
		"idx_alias_global", "idx_alias_store",
		"idx_line_items_receipt", "idx_line_items_product",
		"idx_product_prices_product", "idx_product_prices_store",
		"idx_product_aliases_alias",
		"idx_receipts_store", "idx_receipts_date",
		"idx_matching_rules_priority",
		"idx_product_images_product",
		"idx_product_links_product", "idx_product_links_source",
	}
	for _, idx := range indexes {
		var name string
		err := database.QueryRow(
			"SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?", idx,
		).Scan(&name)
		if err != nil {
			t.Errorf("index %q not found: %v", idx, err)
		}
	}
}
