package adapters

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mstefanko/cartledger/internal/enrichment"
)

func TestKrogerParseExtractsIdentityPackAndNutrition(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "kroger_tortillas.html"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	suggestions := Parse("https://www.kroger.com/p/example/0001111008404", enrichment.VisibleText(raw))
	fields := map[string]string{}
	for _, s := range suggestions {
		fields[s.Field] = s.Value
	}

	assertField := func(field, want string) {
		t.Helper()
		if fields[field] != want {
			t.Fatalf("field %s = %q, want %q; all fields=%v", field, fields[field], want, fields)
		}
	}
	assertField("name", "Mission Carb Balance Soft Taco Flour Tortillas")
	assertField("brand", "Mission")
	assertField("upc", "0001111008404")
	assertField("pack_quantity", "12")
	assertField("pack_unit", "oz")
	assertField("calories", "70")
	assertField("protein_g", "5")
	assertField("ingredients", "Water, modified wheat starch, wheat gluten, vegetable shortening.")
	assertField("allergens", "Wheat")
}

func TestKrogerParseSkipsNavigationBeforeProductName(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "kroger_layout_alt.html"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	suggestions := Parse("https://www.kroger.com/p/example/0002222200000", enrichment.VisibleText(raw))
	fields := map[string]string{}
	for _, s := range suggestions {
		fields[s.Field] = s.Value
	}
	if fields["name"] != "Simple Truth Organic Black Beans 15 oz" {
		t.Fatalf("name = %q, want product title; fields=%v", fields["name"], fields)
	}
	if fields["pack_quantity"] != "15" || fields["pack_unit"] != "oz" {
		t.Fatalf("pack = %q %q, want 15 oz", fields["pack_quantity"], fields["pack_unit"])
	}
}
