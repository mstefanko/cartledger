package matcher

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/mstefanko/cartledger/internal/db"
)

// newBackfillTestDB opens a fresh file-backed SQLite DB and runs the full
// migration set. modernc.org/sqlite doesn't handle ":memory:" reliably across
// multiple connections in this project — we follow the same file-in-tempdir
// pattern used by internal/db/backups_test.go.
func newBackfillTestDB(t *testing.T) *sql.DB {
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

	// Minimal household fixture — products requires household_id FK.
	if _, err := database.Exec(
		`INSERT INTO households (id, name) VALUES ('h1', 'Test')`,
	); err != nil {
		t.Fatalf("insert household: %v", err)
	}
	return database
}

// insertProduct creates a product row with the given brand/category (may be
// nil to leave them NULL) and returns the product_id.
func insertProduct(t *testing.T, database *sql.DB, id, name string, brand, category *string) {
	t.Helper()
	_, err := database.Exec(
		`INSERT INTO products (id, household_id, name, brand, category) VALUES (?, ?, ?, ?, ?)`,
		id, "h1", name, brand, category,
	)
	if err != nil {
		t.Fatalf("insert product: %v", err)
	}
}

// readProduct fetches the current brand/category of a product. NULL columns
// come back as empty strings; the tests use explicit (valid, value) semantics
// via sql.NullString.
func readProduct(t *testing.T, database *sql.DB, id string) (brand, category sql.NullString) {
	t.Helper()
	err := database.QueryRow(
		`SELECT brand, category FROM products WHERE id = ?`, id,
	).Scan(&brand, &category)
	if err != nil {
		t.Fatalf("read product: %v", err)
	}
	return brand, category
}

func strPtr(s string) *string { return &s }

func TestBackfillProductMetadata_BothNull(t *testing.T) {
	// Product starts with brand=NULL AND category=NULL; incoming has both.
	// Expect both fields filled after call. Brand goes through NormalizeBrand,
	// so lowercase "heb" becomes "HEB" via the override table.
	database := newBackfillTestDB(t)
	insertProduct(t, database, "p1", "Coffee", nil, nil)

	if err := BackfillProductMetadata(database, "p1", "heb", "beverages"); err != nil {
		t.Fatalf("BackfillProductMetadata: %v", err)
	}

	brand, category := readProduct(t, database, "p1")
	if !brand.Valid || brand.String != "HEB" {
		t.Errorf("brand = %v, want HEB", brand)
	}
	if !category.Valid || category.String != "beverages" {
		t.Errorf("category = %v, want beverages", category)
	}
}

func TestBackfillProductMetadata_OnePresent(t *testing.T) {
	// Product has brand set (user-provided) but category=NULL. Incoming
	// suggestion has both. Expect brand UNCHANGED (user value protected by
	// the "AND brand IS NULL" guard), category filled.
	database := newBackfillTestDB(t)
	insertProduct(t, database, "p1", "Coke", strPtr("Coca-Cola"), nil)

	if err := BackfillProductMetadata(database, "p1", "Pepsi", "beverages"); err != nil {
		t.Fatalf("BackfillProductMetadata: %v", err)
	}

	brand, category := readProduct(t, database, "p1")
	if !brand.Valid || brand.String != "Coca-Cola" {
		t.Errorf("brand = %v, want unchanged Coca-Cola", brand)
	}
	if !category.Valid || category.String != "beverages" {
		t.Errorf("category = %v, want beverages", category)
	}
}

func TestBackfillProductMetadata_BothSet(t *testing.T) {
	// Both columns are non-NULL (user data). Backfill must no-op regardless
	// of incoming values — the "AND ... IS NULL" clause in each UPDATE makes
	// this safe even if some future caller forgets to check upfront.
	database := newBackfillTestDB(t)
	insertProduct(t, database, "p1", "Coke", strPtr("Coca-Cola"), strPtr("Beverages"))

	if err := BackfillProductMetadata(database, "p1", "Pepsi", "Snacks"); err != nil {
		t.Fatalf("BackfillProductMetadata: %v", err)
	}

	brand, category := readProduct(t, database, "p1")
	if !brand.Valid || brand.String != "Coca-Cola" {
		t.Errorf("brand = %v, want unchanged Coca-Cola", brand)
	}
	if !category.Valid || category.String != "Beverages" {
		t.Errorf("category = %v, want unchanged Beverages", category)
	}
}

func TestBackfillProductMetadata_IncomingEmpty(t *testing.T) {
	// Product has NULL everywhere. Incoming suggestion is all empty strings
	// (which can happen when the LLM returned no brand/category). Expect
	// no change — we must NOT write empty string into the canonical column.
	database := newBackfillTestDB(t)
	insertProduct(t, database, "p1", "Mystery item", nil, nil)

	if err := BackfillProductMetadata(database, "p1", "", "   "); err != nil {
		t.Fatalf("BackfillProductMetadata: %v", err)
	}

	brand, category := readProduct(t, database, "p1")
	if brand.Valid {
		t.Errorf("brand = %q, want NULL", brand.String)
	}
	if category.Valid {
		t.Errorf("category = %q, want NULL", category.String)
	}
}

func TestBackfillProductMetadata_Idempotent(t *testing.T) {
	// Calling the helper twice must succeed and leave the same state as one
	// call. After the first call both fields are set, so the second call hits
	// the "AND ... IS NULL" guard and no-ops — this verifies the idempotence
	// contract the PLAN calls out.
	database := newBackfillTestDB(t)
	insertProduct(t, database, "p1", "Coffee", nil, nil)

	if err := BackfillProductMetadata(database, "p1", "Starbucks", "beverages"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := BackfillProductMetadata(database, "p1", "DifferentBrand", "different"); err != nil {
		t.Fatalf("second call: %v", err)
	}

	brand, category := readProduct(t, database, "p1")
	if !brand.Valid || brand.String != "Starbucks" {
		t.Errorf("brand = %v, want Starbucks (first-writer-wins)", brand)
	}
	if !category.Valid || category.String != "beverages" {
		t.Errorf("category = %v, want beverages (first-writer-wins)", category)
	}
}

func TestBackfillProductMetadata_EmptyProductID(t *testing.T) {
	// Defensive: an empty productID must early-return without error and
	// without touching the DB. This guards against accidental widening of
	// an UPDATE ... WHERE id = '' if a caller ever passes the zero value.
	database := newBackfillTestDB(t)
	insertProduct(t, database, "p1", "Coffee", nil, nil)

	if err := BackfillProductMetadata(database, "", "Starbucks", "beverages"); err != nil {
		t.Fatalf("BackfillProductMetadata: %v", err)
	}

	brand, category := readProduct(t, database, "p1")
	if brand.Valid {
		t.Errorf("brand = %q, want NULL (nothing should have been written)", brand.String)
	}
	if category.Valid {
		t.Errorf("category = %q, want NULL (nothing should have been written)", category.String)
	}
}

// TestBackfillProductMetadata_OnlyBrand verifies the two fields are
// independent: supplying only a brand must fill brand without touching
// category (even when category is NULL — we just didn't get a suggestion).
func TestBackfillProductMetadata_OnlyBrand(t *testing.T) {
	database := newBackfillTestDB(t)
	insertProduct(t, database, "p1", "Coffee", nil, nil)

	if err := BackfillProductMetadata(database, "p1", "Starbucks", ""); err != nil {
		t.Fatalf("BackfillProductMetadata: %v", err)
	}

	brand, category := readProduct(t, database, "p1")
	if !brand.Valid || brand.String != "Starbucks" {
		t.Errorf("brand = %v, want Starbucks", brand)
	}
	if category.Valid {
		t.Errorf("category = %q, want still NULL", category.String)
	}
}
