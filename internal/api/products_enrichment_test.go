package api

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/httpsafe"
)

func TestAddProductLinkRejectsUnsafeURL(t *testing.T) {
	h, _, cleanup := newTestHandler(t)
	defer cleanup()
	householdID, _, _, productID := seedTestData(t, h)

	e := echo.New()
	c, rec := makeContext(e, http.MethodPost, "/products/"+productID+"/links", `{"url":"http://localhost/internal"}`, householdID, productID)
	if err := h.AddLink(c); err != nil {
		t.Fatalf("AddLink: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}

	var count int
	if err := h.DB.QueryRow("SELECT COUNT(*) FROM product_links WHERE product_id = ?", productID).Scan(&count); err != nil {
		t.Fatalf("count links: %v", err)
	}
	if count != 0 {
		t.Fatalf("product_links count = %d, want 0", count)
	}
}

func TestAddProductLinkReturnsKrogerSuggestionsAndAcceptsPackOnly(t *testing.T) {
	h, _, cleanup := newTestHandler(t)
	defer cleanup()
	h.Cfg.AllowPrivateIntegrations = true
	householdID, _, _, productID := seedTestData(t, h)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html>
<title>Mission Carb Balance Soft Taco Flour Tortillas - Kroger</title>
<h1>Mission Carb Balance Soft Taco Flour Tortillas</h1>
<p>Brand: Mission</p>
<p>UPC: 0001111008404</p>
<p>Net Wt: 12 oz</p>
<p>Calories 70</p>
<p>Protein 5 g</p>
<h2>Ingredients</h2><p>Water, wheat.</p>
<h2>Allergens</h2><p>Wheat</p>`))
	}))
	defer server.Close()

	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	restore := httpsafe.SetLookupIPsForTest(func(host string) ([]net.IP, error) {
		if host == "www.kroger.com" {
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		}
		return net.LookupIP(host)
	})
	defer restore()

	krogerURL := "http://www.kroger.com:" + serverURL.Port() + "/p/tortillas/0001111008404"
	e := echo.New()
	c, rec := makeContext(e, http.MethodPost, "/products/"+productID+"/links", `{"url":"`+krogerURL+`"}`, householdID, productID)
	if err := h.AddLink(c); err != nil {
		t.Fatalf("AddLink: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var body addProductLinkResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body.Link.Source != "kroger" || body.Link.FetchedAt == nil || body.Link.HTTPStatus == nil || *body.Link.HTTPStatus != http.StatusOK {
		t.Fatalf("unexpected link metadata: %+v", body.Link)
	}

	var packSuggestion *productEnrichmentSuggestionResponse
	var nutritionSuggestion *productEnrichmentSuggestionResponse
	for i := range body.Suggestions {
		if body.Suggestions[i].Field == "pack_quantity" && body.Suggestions[i].Value == "12" {
			packSuggestion = &body.Suggestions[i]
		}
		if body.Suggestions[i].Field == "calories" {
			nutritionSuggestion = &body.Suggestions[i]
		}
	}
	if packSuggestion == nil || nutritionSuggestion == nil {
		t.Fatalf("expected pack and nutrition suggestions, got %+v", body.Suggestions)
	}

	acceptBody := `{"fields":["pack_quantity"]}`
	c, rec = makeContext(e, http.MethodPost, "/products/"+productID+"/enrichment-suggestions/"+packSuggestion.ID+"/accept", acceptBody, householdID, productID)
	c.SetParamNames("id", "suggestionId")
	c.SetParamValues(productID, packSuggestion.ID)
	if err := h.AcceptEnrichmentSuggestion(c); err != nil {
		t.Fatalf("AcceptEnrichmentSuggestion: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("accept status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var packQuantity sqlNullFloat
	if err := h.DB.QueryRow("SELECT pack_quantity FROM products WHERE id = ?", productID).Scan(&packQuantity); err != nil {
		t.Fatalf("query product pack: %v", err)
	}
	if !packQuantity.Valid || packQuantity.Float64 != 12 {
		t.Fatalf("pack_quantity = %+v, want 12", packQuantity)
	}

	var nutritionRows int
	if err := h.DB.QueryRow("SELECT COUNT(*) FROM product_nutrition WHERE product_id = ?", productID).Scan(&nutritionRows); err != nil {
		t.Fatalf("query nutrition count: %v", err)
	}
	if nutritionRows != 0 {
		t.Fatalf("nutrition rows = %d, want 0 after accepting only pack", nutritionRows)
	}

	if !strings.Contains(rec.Body.String(), `"status":"accepted"`) {
		t.Fatalf("accepted response did not mark accepted: %s", rec.Body.String())
	}
}

func TestEnrichByUPCReturnsOpenFoodFactsAndUSDASuggestions(t *testing.T) {
	h, _, cleanup := newTestHandler(t)
	defer cleanup()
	h.Cfg.AllowPrivateIntegrations = true
	h.Cfg.USDAFDCAPIKey = "test-key"
	householdID, _, _, productID := seedTestData(t, h)

	upc := "0001111008404"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/off/api/v2/product/"):
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
					}
				}
			}`))
		case r.URL.Path == "/fdc/v1/foods/search":
			if r.URL.Query().Get("api_key") != "test-key" || r.URL.Query().Get("query") != upc {
				t.Fatalf("unexpected USDA query: %s", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"foods": [{
					"fdcId": 123,
					"description": "MISSION TORTILLAS",
					"brandOwner": "MISSION",
					"gtinUpc": "0001111008404",
					"ingredients": "Water, wheat.",
					"foodNutrients": [
						{"nutrientName": "Energy", "unitName": "KCAL", "value": 72},
						{"nutrientName": "Sodium, Na", "unitName": "MG", "value": 280}
					]
				}]
			}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.String())
		}
	}))
	defer server.Close()

	oldOFFAPI := openFoodFactsAPIBase
	oldOFFProduct := openFoodFactsProductBase
	oldUSDASearch := usdaSearchAPIBase
	oldUSDADetails := usdaFoodDetailsBase
	openFoodFactsAPIBase = server.URL + "/off/api/v2/product"
	openFoodFactsProductBase = server.URL + "/off/product"
	usdaSearchAPIBase = server.URL + "/fdc/v1/foods/search"
	usdaFoodDetailsBase = server.URL + "/fdc-app.html#/food-details/"
	defer func() {
		openFoodFactsAPIBase = oldOFFAPI
		openFoodFactsProductBase = oldOFFProduct
		usdaSearchAPIBase = oldUSDASearch
		usdaFoodDetailsBase = oldUSDADetails
	}()

	e := echo.New()
	c, rec := makeContext(e, http.MethodPost, "/products/"+productID+"/enrich/upc", `{"upc":"`+upc+`"}`, householdID, productID)
	if err := h.EnrichByUPC(c); err != nil {
		t.Fatalf("EnrichByUPC: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var body addProductLinkResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	bySourceField := map[string]string{}
	for _, suggestion := range body.Suggestions {
		bySourceField[suggestion.Source+"."+suggestion.Field] = suggestion.Value
	}
	if bySourceField["openfoodfacts.name"] != "Mission Carb Balance Tortillas" {
		t.Fatalf("OFF name missing; suggestions=%+v", body.Suggestions)
	}
	if bySourceField["openfoodfacts.pack_quantity"] != "12" || bySourceField["openfoodfacts.calories"] != "70" {
		t.Fatalf("OFF pack/nutrition missing; suggestions=%+v", body.Suggestions)
	}
	if bySourceField["usda_fdc.sodium_mg"] != "280" || bySourceField["usda_fdc.calories"] != "72" {
		t.Fatalf("USDA nutrition missing; suggestions=%+v", body.Suggestions)
	}

	var linkCount int
	if err := h.DB.QueryRow("SELECT COUNT(*) FROM product_links WHERE product_id = ?", productID).Scan(&linkCount); err != nil {
		t.Fatalf("link count: %v", err)
	}
	if linkCount != 2 {
		t.Fatalf("product_links = %d, want OFF + USDA links", linkCount)
	}
}

func TestAcceptNutritionSuggestionWithoutLinkUpdatesExistingRow(t *testing.T) {
	h, _, cleanup := newTestHandler(t)
	defer cleanup()
	householdID, _, _, productID := seedTestData(t, h)

	insertSuggestion := func(id, value string) {
		t.Helper()
		if _, err := h.DB.Exec(
			`INSERT INTO product_enrichment_suggestions
			    (id, product_id, source, source_url, field, value, status)
			 VALUES (?, ?, 'manual', 'manual://nutrition', 'calories', ?, 'pending')`,
			id, productID, value,
		); err != nil {
			t.Fatalf("insert suggestion %s: %v", id, err)
		}
	}
	insertSuggestion("sug-calories-70", "70")
	insertSuggestion("sug-calories-80", "80")

	e := echo.New()
	for _, id := range []string{"sug-calories-70", "sug-calories-80"} {
		c, rec := makeContext(e, http.MethodPost, "/products/"+productID+"/enrichment-suggestions/"+id+"/accept", `{}`, householdID, productID)
		c.SetParamNames("id", "suggestionId")
		c.SetParamValues(productID, id)
		if err := h.AcceptEnrichmentSuggestion(c); err != nil {
			t.Fatalf("AcceptEnrichmentSuggestion %s: %v", id, err)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("accept %s status = %d, want 200; body=%s", id, rec.Code, rec.Body.String())
		}
	}

	var rowCount int
	var calories sqlNullFloat
	if err := h.DB.QueryRow("SELECT COUNT(*), calories FROM product_nutrition WHERE product_id = ?", productID).Scan(&rowCount, &calories); err != nil {
		t.Fatalf("query nutrition: %v", err)
	}
	if rowCount != 1 || !calories.Valid || calories.Float64 != 80 {
		t.Fatalf("nutrition rowCount=%d calories=%+v, want one row updated to 80", rowCount, calories)
	}
}

type sqlNullFloat struct {
	Float64 float64
	Valid   bool
}

func (n *sqlNullFloat) Scan(value interface{}) error {
	if value == nil {
		n.Valid = false
		return nil
	}
	switch v := value.(type) {
	case float64:
		n.Float64 = v
	case int64:
		n.Float64 = float64(v)
	}
	n.Valid = true
	return nil
}
