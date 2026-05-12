package openfoodfacts

import (
	"context"
	"net/http"
	"net/http/httptest"
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
				"nutriments": {
					"energy-kcal_serving": 70,
					"proteins_serving": 5
				},
				"image_front_url": "https://images.example/front.jpg"
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
	byField := map[string]string{}
	for _, suggestion := range got.Suggestions {
		byField[suggestion.Field] = suggestion.Value
	}
	if byField["pack_quantity"] != "12" || byField["calories"] != "70" || byField["protein_g"] != "5" {
		t.Fatalf("suggestions = %+v", byField)
	}
}
