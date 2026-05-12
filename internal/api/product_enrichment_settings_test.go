package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestProductEnrichmentSettingsUpdateCreatesDefaultRow(t *testing.T) {
	h, _, cleanup := newTestHandler(t)
	defer cleanup()
	householdID, _, _, _ := seedTestData(t, h)

	var before int
	if err := h.DB.QueryRow("SELECT COUNT(*) FROM product_enrichment_settings WHERE household_id = ?", householdID).Scan(&before); err != nil {
		t.Fatalf("count settings before update: %v", err)
	}
	if before != 0 {
		t.Fatalf("settings rows before update = %d, want 0", before)
	}

	handler := &ProductEnrichmentSettingsHandler{DB: h.DB, Cfg: h.Cfg}
	e := echo.New()
	c, rec := makeContext(
		e,
		http.MethodPut,
		"/api/v1/product-enrichment/settings",
		`{"provider_openfoodfacts_enabled":false,"first_run_backfill_limit":25}`,
		householdID,
		"",
	)
	if err := handler.Update(c); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp productEnrichmentSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.ProviderOpenFoodFactsEnabled {
		t.Fatalf("ProviderOpenFoodFactsEnabled = true, want false")
	}
	if resp.FirstRunBackfillLimit != 25 {
		t.Fatalf("FirstRunBackfillLimit = %d, want 25", resp.FirstRunBackfillLimit)
	}

	var after int
	if err := h.DB.QueryRow("SELECT COUNT(*) FROM product_enrichment_settings WHERE household_id = ?", householdID).Scan(&after); err != nil {
		t.Fatalf("count settings after update: %v", err)
	}
	if after != 1 {
		t.Fatalf("settings rows after update = %d, want 1", after)
	}
}
