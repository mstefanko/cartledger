package usda

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mstefanko/cartledger/internal/enrichment/providers"
	"github.com/mstefanko/cartledger/internal/httpsafe"
)

func TestProviderUsesOnlyExactUPCMatches(t *testing.T) {
	upc := "0001111008404"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") != "test-key" || r.URL.Query().Get("query") != upc {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"foods": [
				{
					"fdcId": 111,
					"description": "WRONG PRODUCT",
					"brandOwner": "NOPE",
					"gtinUpc": "9999999999999",
					"foodNutrients": [{"nutrientName": "Energy", "unitName": "KCAL", "value": 1}]
				},
				{
					"fdcId": 222,
					"description": "MISSION TORTILLAS",
					"brandOwner": "MISSION",
					"gtinUpc": "0001111008404",
					"ingredients": "Water, wheat.",
					"foodNutrients": [
						{"nutrientName": "Energy", "unitName": "KCAL", "value": 72},
						{"nutrientName": "Sodium, Na", "unitName": "MG", "value": 280}
					]
				}
			]
		}`))
	}))
	defer server.Close()

	provider := &Provider{
		Client:          httpsafe.NewSafeHTTPClient(5*time.Second, 512*1024, true),
		SearchAPIBase:   server.URL + "/fdc/v1/foods/search",
		FoodDetailsBase: server.URL + "/fdc-app.html#/food-details/",
	}
	metadata, err := provider.Lookup(context.Background(), providers.LookupInput{
		UPC:        &upc,
		LookupKey:  "upc:" + upc,
		USDAAPIKey: "test-key",
	})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(metadata) != 1 {
		t.Fatalf("metadata len = %d, want 1", len(metadata))
	}
	got := metadata[0]
	if got.SourceRecordID == nil || *got.SourceRecordID != "222" {
		t.Fatalf("source record = %+v, want exact UPC FDC id 222", got.SourceRecordID)
	}
	if got.Payload.Name == nil || *got.Payload.Name != "Mission Tortillas" {
		t.Fatalf("payload name = %+v", got.Payload.Name)
	}
	byField := map[string]string{}
	for _, suggestion := range got.Suggestions {
		byField[suggestion.Field] = suggestion.Value
	}
	if byField["calories"] != "72" || byField["sodium_mg"] != "280" {
		t.Fatalf("suggestions = %+v", byField)
	}
	if byField["brand"] != "MISSION" {
		t.Fatalf("brand suggestion = %q", byField["brand"])
	}
}
