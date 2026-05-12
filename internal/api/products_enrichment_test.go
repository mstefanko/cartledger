package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/enrichment"
	"github.com/mstefanko/cartledger/internal/enrichment/providers"
	"github.com/mstefanko/cartledger/internal/enrichment/providers/openfoodfacts"
	"github.com/mstefanko/cartledger/internal/enrichment/providers/usda"
	"github.com/mstefanko/cartledger/internal/enrichment/runner"
	"github.com/mstefanko/cartledger/internal/httpsafe"
	"github.com/mstefanko/cartledger/internal/identifiers"
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

func TestCreateEnrichmentJobRejectsUnsafeURLScheme(t *testing.T) {
	h, _, cleanup := newTestHandler(t)
	defer cleanup()
	householdID, _, _, productID := seedTestData(t, h)

	e := echo.New()
	c, rec := makeContext(e, http.MethodPost, "/products/"+productID+"/enrichment-jobs", `{"url":"javascript:alert(1)"}`, householdID, productID)
	if err := h.CreateEnrichmentJob(c); err != nil {
		t.Fatalf("CreateEnrichmentJob: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "url scheme must be http or https") {
		t.Fatalf("response = %s, want invalid scheme message", rec.Body.String())
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

	var editSource string
	if err := h.DB.QueryRow(
		"SELECT edit_source FROM product_field_edits WHERE product_id = ? AND field = 'pack_quantity'",
		productID,
	).Scan(&editSource); err != nil {
		t.Fatalf("query field edit: %v", err)
	}
	if editSource != "suggestion_accept" {
		t.Fatalf("edit_source = %q, want suggestion_accept", editSource)
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

	client := httpsafe.NewSafeHTTPClient(8*time.Second, 512*1024, true)
	h.Enrichment = runner.NewServiceWithProviders(h.DB, h.Cfg, nil, []providers.Provider{
		&openfoodfacts.Provider{
			Client:      client,
			APIBase:     server.URL + "/off/api/v2/product",
			ProductBase: server.URL + "/off/product",
		},
		&usda.Provider{
			Client:          client,
			SearchAPIBase:   server.URL + "/fdc/v1/foods/search",
			FoodDetailsBase: server.URL + "/fdc-app.html#/food-details/",
		},
	})
	if _, err := h.DB.Exec(
		`INSERT INTO product_enrichment_settings
		    (household_id, provider_usda_fdc_enabled)
		 VALUES (?, 1)
		 ON CONFLICT(household_id) DO UPDATE SET provider_usda_fdc_enabled = 1`,
		householdID,
	); err != nil {
		t.Fatalf("enable USDA provider: %v", err)
	}

	e := echo.New()
	c, rec := makeContext(e, http.MethodPost, "/products/"+productID+"/enrich/upc", `{"upc":"`+upc+`"}`, householdID, productID)
	if err := h.EnrichByUPC(c); err != nil {
		t.Fatalf("EnrichByUPC: %v", err)
	}
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}

	var body productEnrichmentJobResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body.Job.Status != runner.StatusQueued {
		t.Fatalf("job status = %q, want queued", body.Job.Status)
	}
	if err := h.Enrichment.ProcessJob(context.Background(), body.Job.ID); err != nil {
		t.Fatalf("process enrichment job: %v", err)
	}

	bySourceField := map[string]string{}
	rows, err := h.DB.Query(
		`SELECT source, field, value
		   FROM product_enrichment_suggestions
		  WHERE product_id = ?`,
		productID,
	)
	if err != nil {
		t.Fatalf("query suggestions: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var source, field, value string
		if err := rows.Scan(&source, &field, &value); err != nil {
			t.Fatalf("scan suggestion: %v", err)
		}
		bySourceField[source+"."+field] = value
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("suggestions rows: %v", err)
	}
	if bySourceField["openfoodfacts.name"] != "Mission Carb Balance Tortillas" {
		t.Fatalf("OFF name missing; suggestions=%+v", bySourceField)
	}
	if bySourceField["openfoodfacts.pack_quantity"] != "12" || bySourceField["openfoodfacts.calories"] != "70" {
		t.Fatalf("OFF pack/nutrition missing; suggestions=%+v", bySourceField)
	}
	if bySourceField["usda_fdc.sodium_mg"] != "280" || bySourceField["usda_fdc.calories"] != "72" {
		t.Fatalf("USDA nutrition missing; suggestions=%+v", bySourceField)
	}

	var linkCount int
	if err := h.DB.QueryRow("SELECT COUNT(*) FROM product_links WHERE product_id = ?", productID).Scan(&linkCount); err != nil {
		t.Fatalf("link count: %v", err)
	}
	if linkCount != 2 {
		t.Fatalf("product_links = %d, want OFF + USDA links", linkCount)
	}
	var metadataCount int
	if err := h.DB.QueryRow("SELECT COUNT(*) FROM product_external_metadata WHERE product_id = ?", productID).Scan(&metadataCount); err != nil {
		t.Fatalf("metadata count: %v", err)
	}
	if metadataCount != 2 {
		t.Fatalf("product_external_metadata = %d, want OFF + USDA snapshots", metadataCount)
	}
	var jobStatus string
	if err := h.DB.QueryRow("SELECT status FROM product_enrichment_jobs WHERE id = ?", body.Job.ID).Scan(&jobStatus); err != nil {
		t.Fatalf("job status query: %v", err)
	}
	if jobStatus != runner.StatusSucceeded {
		t.Fatalf("job status = %q, want succeeded", jobStatus)
	}
}

func TestStoreSuggestionsSkipsEditedFieldsNewerThanLinkEvidence(t *testing.T) {
	h, _, cleanup := newTestHandler(t)
	defer cleanup()
	_, _, _, productID := seedTestData(t, h)

	var linkID string
	if err := h.DB.QueryRow(
		`INSERT INTO product_links
		    (product_id, source, url, label, created_at, fetched_at)
		 VALUES (?, 'openfoodfacts', 'https://world.openfoodfacts.org/product/0001111008404', 'Old snapshot', '2026-05-08 10:00:00', '2026-05-08 10:00:00')
		 RETURNING id`,
		productID,
	).Scan(&linkID); err != nil {
		t.Fatalf("insert link: %v", err)
	}
	if _, err := h.DB.Exec(
		`INSERT INTO product_field_edits (product_id, field, edit_source, edited_at)
		 VALUES (?, 'name', 'manual', '2026-05-09 10:00:00')`,
		productID,
	); err != nil {
		t.Fatalf("insert field edit: %v", err)
	}

	stored, err := h.storeSuggestions(context.Background(), productID, &linkID, []enrichment.Suggestion{
		enrichment.NewSuggestion("openfoodfacts", "https://world.openfoodfacts.org/product/0001111008404", "name", "Stale provider name", "Older snapshot", 0.9),
		enrichment.NewSuggestion("openfoodfacts", "https://world.openfoodfacts.org/product/0001111008404", "calories", "70", "Older snapshot", 0.9),
	})
	if err != nil {
		t.Fatalf("storeSuggestions: %v", err)
	}
	if len(stored) != 1 || stored[0].Field != "calories" {
		t.Fatalf("stored suggestions = %+v, want only calories after name edit suppression", stored)
	}

	var nameSuggestions int
	if err := h.DB.QueryRow("SELECT COUNT(*) FROM product_enrichment_suggestions WHERE product_id = ? AND field = 'name'", productID).Scan(&nameSuggestions); err != nil {
		t.Fatalf("count name suggestions: %v", err)
	}
	if nameSuggestions != 0 {
		t.Fatalf("name suggestions = %d, want 0", nameSuggestions)
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

func TestBulkAcceptReportsUPCConflict(t *testing.T) {
	h, _, cleanup := newTestHandler(t)
	defer cleanup()
	householdID, _, _, productID := seedTestData(t, h)

	upc := "0001111008404"
	var existingID string
	if err := h.DB.QueryRow(
		"INSERT INTO products (household_id, name) VALUES (?, 'Existing Tortillas') RETURNING id",
		householdID,
	).Scan(&existingID); err != nil {
		t.Fatalf("insert existing product: %v", err)
	}
	confidence := 1.0
	if _, err := identifiers.SetProductPrimaryGTIN(context.Background(), h.DB, householdID, existingID, upc, "manual", &confidence); err != nil {
		t.Fatalf("set existing gtin: %v", err)
	}
	if _, err := h.DB.Exec(
		`INSERT INTO product_enrichment_suggestions
		    (id, product_id, source, source_url, field, value, status)
		 VALUES ('sug-conflicting-upc', ?, 'openfoodfacts', 'https://world.openfoodfacts.org/product/0001111008404', 'upc', ?, 'pending')`,
		productID, upc,
	); err != nil {
		t.Fatalf("insert suggestion: %v", err)
	}

	e := echo.New()
	c, rec := makeContext(e, http.MethodPost, "/products/"+productID+"/enrichment-suggestions/bulk-accept", `{"suggestion_ids":["","sug-conflicting-upc"]}`, householdID, productID)
	if err := h.BulkAcceptEnrichmentSuggestions(c); err != nil {
		t.Fatalf("BulkAcceptEnrichmentSuggestions: %v", err)
	}
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}

	var body bulkProductEnrichmentSuggestionsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(body.Accepted) != 0 || len(body.Conflicts) != 1 {
		t.Fatalf("bulk response = %+v, want one conflict and no accepted", body)
	}
	conflict := body.Conflicts[0]
	if conflict.Code != "identifier_conflict" || conflict.ExistingProductID != existingID || !conflict.SuggestedMerge {
		t.Fatalf("conflict = %+v, want identifier conflict for %s", conflict, existingID)
	}

	var status string
	if err := h.DB.QueryRow("SELECT status FROM product_enrichment_suggestions WHERE id = 'sug-conflicting-upc'").Scan(&status); err != nil {
		t.Fatalf("query suggestion status: %v", err)
	}
	if status != "pending" {
		t.Fatalf("suggestion status = %q, want pending", status)
	}
}

func TestBulkAcceptRollsBackOnHardError(t *testing.T) {
	h, _, cleanup := newTestHandler(t)
	defer cleanup()
	householdID, _, _, productID := seedTestData(t, h)

	if _, err := h.DB.Exec(
		`CREATE TRIGGER fail_brand_update
		 BEFORE UPDATE OF brand ON products
		 WHEN NEW.brand = 'Fail'
		 BEGIN
		   SELECT RAISE(ABORT, 'forced brand failure');
		 END`,
	); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	if _, err := h.DB.Exec(
		`INSERT INTO product_enrichment_suggestions
		    (id, product_id, source, source_url, field, value, status)
		 VALUES
		    ('sug-name-ok', ?, 'manual', 'manual://bulk', 'name', 'Better Widget', 'pending'),
		    ('sug-brand-fails', ?, 'manual', 'manual://bulk', 'brand', 'fail', 'pending')`,
		productID, productID,
	); err != nil {
		t.Fatalf("insert suggestions: %v", err)
	}

	e := echo.New()
	c, rec := makeContext(e, http.MethodPost, "/products/"+productID+"/enrichment-suggestions/bulk-accept", `{"suggestion_ids":["sug-name-ok","sug-brand-fails"]}`, householdID, productID)
	if err := h.BulkAcceptEnrichmentSuggestions(c); err != nil {
		t.Fatalf("BulkAcceptEnrichmentSuggestions: %v", err)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", rec.Code, rec.Body.String())
	}

	var name string
	if err := h.DB.QueryRow("SELECT name FROM products WHERE id = ?", productID).Scan(&name); err != nil {
		t.Fatalf("query product name: %v", err)
	}
	if name != "Widget" {
		t.Fatalf("name = %q, want original Widget after rollback", name)
	}

	var accepted int
	if err := h.DB.QueryRow("SELECT COUNT(*) FROM product_enrichment_suggestions WHERE product_id = ? AND status = 'accepted'", productID).Scan(&accepted); err != nil {
		t.Fatalf("count accepted suggestions: %v", err)
	}
	if accepted != 0 {
		t.Fatalf("accepted suggestions = %d, want 0 after rollback", accepted)
	}

	var edits int
	if err := h.DB.QueryRow("SELECT COUNT(*) FROM product_field_edits WHERE product_id = ?", productID).Scan(&edits); err != nil {
		t.Fatalf("count field edits: %v", err)
	}
	if edits != 0 {
		t.Fatalf("field edits = %d, want 0 after rollback", edits)
	}
}

func TestEnrichByUPCRespectsGlobalDisable(t *testing.T) {
	h, _, cleanup := newTestHandler(t)
	defer cleanup()
	h.Cfg.ProductEnrichmentEnabled = false
	householdID, _, _, productID := seedTestData(t, h)

	e := echo.New()
	c, rec := makeContext(e, http.MethodPost, "/products/"+productID+"/enrich/upc", `{"upc":"0001111008404"}`, householdID, productID)
	if err := h.EnrichByUPC(c); err != nil {
		t.Fatalf("EnrichByUPC: %v", err)
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
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
