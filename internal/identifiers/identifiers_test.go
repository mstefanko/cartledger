package identifiers

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mstefanko/cartledger/internal/db"
)

func TestNormalizeGTIN(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{"ean8", "9638-5074", "96385074", false},
		{"upca", "036000291452", "036000291452", false},
		{"ean13", "4006381333931", "4006381333931", false},
		{"gtin14", "10012345678902", "10012345678902", false},
		{"blank", " ", "", false},
		{"bad check digit", "036000291453", "", true},
		{"bad length", "123456", "", true},
		{"bad character", "03600029145X", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeGTIN(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("NormalizeGTIN error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeGTIN error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeGTIN = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizePLU(t *testing.T) {
	tests := []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{"4011", "4011", false},
		{"94011", "94011", false},
		{"84011", "", true},
		{"401", "", true},
	}
	for _, tt := range tests {
		got, authority, err := NormalizePLU(tt.raw)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("NormalizePLU(%q) error = nil, want error", tt.raw)
			}
			continue
		}
		if err != nil {
			t.Fatalf("NormalizePLU(%q) error = %v", tt.raw, err)
		}
		if got != tt.want || authority != "ifps" {
			t.Fatalf("NormalizePLU(%q) = %q/%q, want %q/ifps", tt.raw, got, authority, tt.want)
		}
	}
}

func TestNormalizeExternalID(t *testing.T) {
	got, err := NormalizeExternalID("Open Food Facts", " ABC  123 ")
	if err != nil {
		t.Fatalf("NormalizeExternalID: %v", err)
	}
	if got != "abc 123" {
		t.Fatalf("NormalizeExternalID = %q, want abc 123", got)
	}
}

func TestSetProductPrimaryGTINKeepsProductsUPCSynchronized(t *testing.T) {
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()
	if err := db.RunMigrations(database); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO households (id, name) VALUES ('h1', 'Test')`); err != nil {
		t.Fatalf("insert household: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO products (id, household_id, name, upc) VALUES ('p1', 'h1', 'Widget', '036000291452')`,
	); err != nil {
		t.Fatalf("insert product: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO product_identifiers
		    (household_id, product_id, kind, authority, value, normalized_value, source)
		 VALUES ('h1', 'p1', 'gtin', '', '036000291452', '036000291452', 'manual')`,
	); err != nil {
		t.Fatalf("insert old identifier: %v", err)
	}

	got, err := SetProductPrimaryGTIN(context.Background(), database, "h1", "p1", "4006381333931", "manual", nil)
	if err != nil {
		t.Fatalf("SetProductPrimaryGTIN: %v", err)
	}
	if got == nil || *got != "4006381333931" {
		t.Fatalf("normalized = %v, want 4006381333931", got)
	}

	var productUPC string
	if err := database.QueryRow(`SELECT upc FROM products WHERE id = 'p1'`).Scan(&productUPC); err != nil {
		t.Fatalf("query product upc: %v", err)
	}
	if productUPC != "4006381333931" {
		t.Fatalf("products.upc = %q, want 4006381333931", productUPC)
	}
	var oldCount int
	if err := database.QueryRow(
		`SELECT COUNT(*) FROM product_identifiers
		  WHERE product_id = 'p1' AND normalized_value = '036000291452'`,
	).Scan(&oldCount); err != nil {
		t.Fatalf("query old identifier: %v", err)
	}
	if oldCount != 0 {
		t.Fatalf("old primary identifiers = %d, want 0", oldCount)
	}

	if _, err := database.Exec(
		`INSERT INTO products (id, household_id, name, upc) VALUES ('p2', 'h1', 'Other Widget', '036000291452')`,
	); err != nil {
		t.Fatalf("insert second product: %v", err)
	}
	if err := UpsertProductIdentifier(context.Background(), database, ProductIdentifier{
		HouseholdID:       "h1",
		ProductID:         "p2",
		Kind:              KindGTIN,
		Value:             "10012345678902",
		NormalizedValue:   "10012345678902",
		Source:            "line_item",
		SetPrimaryProduct: true,
	}); err != nil {
		t.Fatalf("UpsertProductIdentifier: %v", err)
	}
	if err := database.QueryRow(`SELECT upc FROM products WHERE id = 'p2'`).Scan(&productUPC); err != nil {
		t.Fatalf("query second product upc: %v", err)
	}
	if productUPC != "10012345678902" {
		t.Fatalf("products.upc after UpsertProductIdentifier = %q, want 10012345678902", productUPC)
	}
}
