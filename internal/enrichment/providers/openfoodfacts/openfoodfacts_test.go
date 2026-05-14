package openfoodfacts

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mstefanko/cartledger/internal/enrichment/providers"
	"github.com/mstefanko/cartledger/internal/httpsafe"
)

func TestProviderMapsProductResponse(t *testing.T) {
	upc := "0001111008404"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/product/"+upc+".json" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		requireProductFields(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status": 1,
			"product": {
				"product_name": "Mission Carb Balance Tortillas",
				"brands": "Mission",
				"code": "0001111008404",
				"quantity": "12 oz",
				"ingredients_text": "Water, wheat.",
				"allergens": "en:wheat",
				"serving_size": "1 tortilla",
				"nutriscore_grade": "b",
				"nutrient_levels": {
					"fat": "low",
					"salt": "moderate"
				},
				"nutriments": {
					"energy-kcal_serving": 70,
					"energy-kj_modifier": "~",
					"proteins_serving": 5,
					"proteins_unit": "g"
				},
				"image_front_url": "https://images.example/front.jpg",
				"image_nutrition_url": "https://images.example/nutrition.jpg"
			}
		}`))
	}))
	defer server.Close()

	provider := &Provider{
		Client:      httpsafe.NewSafeHTTPClient(5*time.Second, 512*1024, true),
		APIBase:     server.URL + "/api/v2/product",
		ProductBase: server.URL + "/product",
	}
	metadata, err := provider.Lookup(context.Background(), providers.LookupInput{
		UPC:       &upc,
		LookupKey: "upc:" + upc,
	})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(metadata) != 1 {
		t.Fatalf("metadata len = %d, want 1", len(metadata))
	}
	got := metadata[0]
	if got.Source != "openfoodfacts" || got.SourceRecordID == nil || *got.SourceRecordID != upc {
		t.Fatalf("unexpected source metadata: %+v", got)
	}
	if got.Payload.Name == nil || *got.Payload.Name != "Mission Carb Balance Tortillas" {
		t.Fatalf("payload name = %+v", got.Payload.Name)
	}
	if got.Payload.Package == nil || got.Payload.Package.Quantity == nil || *got.Payload.Package.Quantity != 12 {
		t.Fatalf("package payload = %+v", got.Payload.Package)
	}
	if got.Payload.ImageURLs["nutrition"] != "https://images.example/nutrition.jpg" {
		t.Fatalf("image urls = %+v", got.Payload.ImageURLs)
	}
	if got.Payload.ProviderMeta["nutriscore_grade"] != "B" || got.Payload.ProviderMeta["nutrient_level_fat"] != "low" {
		t.Fatalf("provider meta = %+v", got.Payload.ProviderMeta)
	}
	byField := map[string]string{}
	for _, suggestion := range got.Suggestions {
		byField[suggestion.Field] = suggestion.Value
	}
	if byField["pack_quantity"] != "12" || byField["calories"] != "70" || byField["protein_g"] != "5" {
		t.Fatalf("suggestions = %+v", byField)
	}
	if byField["image_front_url"] != "https://images.example/front.jpg" || byField["image_nutrition_url"] != "https://images.example/nutrition.jpg" {
		t.Fatalf("image suggestions = %+v", byField)
	}
}

func TestProviderRetriesUPCAsEAN13(t *testing.T) {
	upc := "096619000081"
	ean13 := "0" + upc
	seenUPC := false
	seenEAN13 := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		requireProductFields(t, r)
		switch r.URL.Path {
		case "/api/v2/product/" + upc + ".json":
			seenUPC = true
			_, _ = w.Write([]byte(`{
				"status": 1,
				"product": {
					"code": "096619000081"
				}
			}`))
		case "/api/v2/product/" + ean13 + ".json":
			seenEAN13 = true
			_, _ = w.Write([]byte(`{
				"status": 1,
				"product": {
					"product_name": "2% Reduced Fat Milk",
					"brands": "Kirkland Signature",
					"code": "0096619000081",
					"quantity": "3,78 l",
					"serving_size": "1 cup",
					"nutriscore_grade": "b",
					"nutriments": {
						"energy-kcal_serving": 130,
						"fat_serving": 5,
						"fat_unit": "g"
					},
					"image_front_url": "https://images.example/milk.jpg"
				}
			}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	provider := &Provider{
		Client:      httpsafe.NewSafeHTTPClient(5*time.Second, 512*1024, true),
		APIBase:     server.URL + "/api/v2/product",
		ProductBase: server.URL + "/product",
	}
	metadata, err := provider.Lookup(context.Background(), providers.LookupInput{
		UPC:       &upc,
		LookupKey: "upc:" + upc,
	})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !seenUPC || !seenEAN13 {
		t.Fatalf("lookup paths seen UPC=%t EAN13=%t, want both", seenUPC, seenEAN13)
	}
	if len(metadata) != 1 {
		t.Fatalf("metadata len = %d, want 1", len(metadata))
	}
	got := metadata[0]
	if got.SourceRecordID == nil || *got.SourceRecordID != ean13 {
		t.Fatalf("source record id = %+v, want %q", got.SourceRecordID, ean13)
	}
	if got.SourceURL != server.URL+"/product/"+upc {
		t.Fatalf("source url = %q", got.SourceURL)
	}
	if got.Payload.SourceURL == nil || *got.Payload.SourceURL != server.URL+"/product/"+ean13 {
		t.Fatalf("payload source url = %+v, want EAN-13 source", got.Payload.SourceURL)
	}
	if got.Payload.ProviderMeta["nutriscore_grade"] != "B" {
		t.Fatalf("provider meta = %+v", got.Payload.ProviderMeta)
	}
	byField := map[string]string{}
	for _, suggestion := range got.Suggestions {
		byField[suggestion.Field] = suggestion.Value
	}
	if byField["calories"] != "130" || byField["total_fat_g"] != "5" {
		t.Fatalf("nutrition suggestions = %+v", byField)
	}
	if byField["pack_quantity"] != "3.78" || byField["pack_unit"] != "l" {
		t.Fatalf("package suggestions = %+v", byField)
	}
}

func requireProductFields(t *testing.T, r *http.Request) {
	t.Helper()
	fields := r.URL.Query().Get("fields")
	if fields == "" {
		t.Fatal("missing fields query parameter")
	}
	for _, want := range []string{"product_name", "nutriments", "image_nutrition_url"} {
		if !strings.Contains(","+fields+",", ","+want+",") {
			t.Fatalf("fields %q missing %q", fields, want)
		}
	}
}
