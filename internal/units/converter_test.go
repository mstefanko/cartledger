package units

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/mstefanko/cartledger/internal/db"
)

func TestConvertScopedPriority(t *testing.T) {
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()
	if err := db.RunMigrations(database); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	seedScopedConversionDB(t, database)

	insertConversion := func(productID any, groupID any, householdID any, factor string) {
		t.Helper()
		if _, err := database.Exec(
			`INSERT INTO unit_conversions (product_id, product_group_id, household_id, from_unit, to_unit, factor)
			 VALUES (?, ?, ?, 'each', 'oz', ?)`,
			productID, groupID, householdID, factor,
		); err != nil {
			t.Fatalf("insert conversion: %v", err)
		}
	}
	insertConversion(nil, nil, "h1", "4")
	insertConversion(nil, "g1", nil, "6")
	insertConversion("p1", nil, nil, "8")

	qty := decimal.NewFromInt(2)
	got, err := ConvertScoped(context.Background(), database, qty, "each", "oz", ConversionScope{
		HouseholdID:    "h1",
		ProductGroupID: "g1",
		ProductID:      "p1",
	})
	if err != nil {
		t.Fatalf("ConvertScoped product: %v", err)
	}
	if !got.Equal(decimal.NewFromInt(16)) {
		t.Fatalf("product scoped conversion = %s, want 16", got)
	}

	got, err = ConvertScoped(context.Background(), database, qty, "each", "oz", ConversionScope{
		HouseholdID:    "h1",
		ProductGroupID: "g1",
	})
	if err != nil {
		t.Fatalf("ConvertScoped group: %v", err)
	}
	if !got.Equal(decimal.NewFromInt(12)) {
		t.Fatalf("group scoped conversion = %s, want 12", got)
	}

	got, err = ConvertScoped(context.Background(), database, qty, "each", "oz", ConversionScope{HouseholdID: "h1"})
	if err != nil {
		t.Fatalf("ConvertScoped household: %v", err)
	}
	if !got.Equal(decimal.NewFromInt(8)) {
		t.Fatalf("household scoped conversion = %s, want 8", got)
	}
}

func seedScopedConversionDB(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, err := database.Exec(`INSERT INTO households (id, name) VALUES ('h1', 'Home')`); err != nil {
		t.Fatalf("insert household: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO product_groups (id, household_id, name) VALUES ('g1', 'h1', 'Group')`); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO products (id, household_id, name, product_group_id) VALUES ('p1', 'h1', 'Product', 'g1')`); err != nil {
		t.Fatalf("insert product: %v", err)
	}
}
