package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
	"github.com/mstefanko/cartledger/internal/models"
)

type ProductEnrichmentSettingsHandler struct {
	DB  *sql.DB
	Cfg *config.Config
}

type productEnrichmentSettingsResponse struct {
	HouseholdID                  string                                  `json:"household_id"`
	GlobalEnabled                bool                                    `json:"global_enabled"`
	ManualLookupEnabled          bool                                    `json:"manual_lookup_enabled"`
	AutoOnScanEnabled            bool                                    `json:"auto_on_scan_enabled"`
	ScheduledSweepEnabled        bool                                    `json:"scheduled_sweep_enabled"`
	ProviderOpenFoodFactsEnabled bool                                    `json:"provider_openfoodfacts_enabled"`
	ProviderUSDAFDCEnabled       bool                                    `json:"provider_usda_fdc_enabled"`
	ProviderKrogerEnabled        bool                                    `json:"provider_kroger_enabled"`
	FirstRunBackfillLimit        int                                     `json:"first_run_backfill_limit"`
	ProviderAvailability         map[string]providerAvailabilityResponse `json:"provider_availability"`
	CreatedAt                    time.Time                               `json:"created_at"`
	UpdatedAt                    time.Time                               `json:"updated_at"`
}

type providerAvailabilityResponse struct {
	Configured            bool   `json:"configured"`
	Enabled               bool   `json:"enabled"`
	EnvFallbackConfigured bool   `json:"env_fallback_configured,omitempty"`
	CredentialSource      string `json:"credential_source,omitempty"`
	Reason                string `json:"reason,omitempty"`
}

type updateProductEnrichmentSettingsRequest struct {
	ManualLookupEnabled          *bool `json:"manual_lookup_enabled"`
	AutoOnScanEnabled            *bool `json:"auto_on_scan_enabled"`
	ScheduledSweepEnabled        *bool `json:"scheduled_sweep_enabled"`
	ProviderOpenFoodFactsEnabled *bool `json:"provider_openfoodfacts_enabled"`
	ProviderUSDAFDCEnabled       *bool `json:"provider_usda_fdc_enabled"`
	ProviderKrogerEnabled        *bool `json:"provider_kroger_enabled"`
	FirstRunBackfillLimit        *int  `json:"first_run_backfill_limit"`
}

func (h *ProductEnrichmentSettingsHandler) RegisterRoutes(protected *echo.Group) {
	g := protected.Group("/product-enrichment")
	g.GET("/settings", h.Get)
	g.PUT("/settings", h.Update)
}

func (h *ProductEnrichmentSettingsHandler) Get(c echo.Context) error {
	resp, err := h.load(c.Request().Context(), auth.HouseholdIDFrom(c))
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	return c.JSON(http.StatusOK, resp)
}

func (h *ProductEnrichmentSettingsHandler) Update(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	if err := h.ensure(c.Request().Context(), householdID); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	var req updateProductEnrichmentSettingsRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	current, err := h.load(c.Request().Context(), householdID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	manual := current.ManualLookupEnabled
	autoScan := current.AutoOnScanEnabled
	sweep := current.ScheduledSweepEnabled
	off := current.ProviderOpenFoodFactsEnabled
	usda := current.ProviderUSDAFDCEnabled
	kroger := current.ProviderKrogerEnabled
	limit := current.FirstRunBackfillLimit
	if req.ManualLookupEnabled != nil {
		manual = *req.ManualLookupEnabled
	}
	if req.AutoOnScanEnabled != nil {
		autoScan = *req.AutoOnScanEnabled
	}
	if req.ScheduledSweepEnabled != nil {
		sweep = *req.ScheduledSweepEnabled
	}
	if req.ProviderOpenFoodFactsEnabled != nil {
		off = *req.ProviderOpenFoodFactsEnabled
	}
	if req.ProviderUSDAFDCEnabled != nil {
		usda = *req.ProviderUSDAFDCEnabled
	}
	if req.ProviderKrogerEnabled != nil {
		kroger = *req.ProviderKrogerEnabled
	}
	if req.FirstRunBackfillLimit != nil {
		limit = *req.FirstRunBackfillLimit
		if limit < 1 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "first_run_backfill_limit must be positive"})
		}
		if limit > 1000 {
			limit = 1000
		}
	}
	if _, err := h.DB.ExecContext(c.Request().Context(),
		`UPDATE product_enrichment_settings
		    SET manual_lookup_enabled = ?,
		        auto_on_scan_enabled = ?,
		        scheduled_sweep_enabled = ?,
		        provider_openfoodfacts_enabled = ?,
		        provider_usda_fdc_enabled = ?,
		        provider_kroger_enabled = ?,
		        first_run_backfill_limit = ?,
		        updated_at = CURRENT_TIMESTAMP
		  WHERE household_id = ?`,
		boolInt(manual), boolInt(autoScan), boolInt(sweep), boolInt(off), boolInt(usda), boolInt(kroger), limit, householdID,
	); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	resp, err := h.load(c.Request().Context(), householdID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	return c.JSON(http.StatusOK, resp)
}

func (h *ProductEnrichmentSettingsHandler) load(ctx context.Context, householdID string) (productEnrichmentSettingsResponse, error) {
	if err := h.ensure(ctx, householdID); err != nil {
		return productEnrichmentSettingsResponse{}, err
	}
	var resp productEnrichmentSettingsResponse
	var raw struct {
		manual, auto, sweep, off, usda, kroger int
	}
	err := h.DB.QueryRowContext(ctx,
		`SELECT household_id, manual_lookup_enabled, auto_on_scan_enabled, scheduled_sweep_enabled,
		        provider_openfoodfacts_enabled, provider_usda_fdc_enabled, provider_kroger_enabled,
		        first_run_backfill_limit, created_at, updated_at
		   FROM product_enrichment_settings
		  WHERE household_id = ?`,
		householdID,
	).Scan(
		&resp.HouseholdID, &raw.manual, &raw.auto, &raw.sweep,
		&raw.off, &raw.usda, &raw.kroger, &resp.FirstRunBackfillLimit,
		&resp.CreatedAt, &resp.UpdatedAt,
	)
	if err != nil {
		return productEnrichmentSettingsResponse{}, err
	}
	resp.GlobalEnabled = h.Cfg == nil || h.Cfg.ProductEnrichmentEnabled
	resp.ManualLookupEnabled = raw.manual != 0
	resp.AutoOnScanEnabled = raw.auto != 0
	resp.ScheduledSweepEnabled = raw.sweep != 0
	resp.ProviderOpenFoodFactsEnabled = raw.off != 0
	resp.ProviderUSDAFDCEnabled = raw.usda != 0
	resp.ProviderKrogerEnabled = raw.kroger != 0
	resp.ProviderAvailability = h.providerAvailability(ctx, householdID, resp)
	return resp, nil
}

func (h *ProductEnrichmentSettingsHandler) ensure(ctx context.Context, householdID string) error {
	_, err := h.DB.ExecContext(ctx,
		`INSERT OR IGNORE INTO product_enrichment_settings (household_id)
		 VALUES (?)`,
		householdID,
	)
	return err
}

func (h *ProductEnrichmentSettingsHandler) providerAvailability(ctx context.Context, householdID string, settings productEnrichmentSettingsResponse) map[string]providerAvailabilityResponse {
	out := map[string]providerAvailabilityResponse{
		"openfoodfacts": {
			Configured: true,
			Enabled:    settings.GlobalEnabled && settings.ProviderOpenFoodFactsEnabled,
		},
	}
	if !settings.GlobalEnabled {
		out["openfoodfacts"] = providerAvailabilityResponse{Configured: true, Enabled: false, Reason: "disabled by operator"}
	}

	envUSDA := h.Cfg != nil && strings.TrimSpace(h.Cfg.USDAFDCAPIKey) != ""
	householdUSDA := false
	usdaSource := ""
	store := db.NewIntegrationStore(h.DB)
	integration, err := store.GetByType(ctx, householdID, models.IntegrationTypeUSDAFDC)
	if err == nil && integration != nil && integration.Enabled {
		var cfg models.USDAFDCConfig
		if json.Unmarshal(integration.Config, &cfg) == nil && strings.TrimSpace(cfg.APIKey) != "" {
			householdUSDA = true
			usdaSource = "household"
		}
	}
	if !householdUSDA && envUSDA {
		usdaSource = "env"
	}
	usdaConfigured := householdUSDA || envUSDA
	usdaEnabled := settings.GlobalEnabled && settings.ProviderUSDAFDCEnabled && usdaConfigured
	reason := ""
	if !settings.GlobalEnabled {
		reason = "disabled by operator"
	} else if !settings.ProviderUSDAFDCEnabled {
		reason = "provider disabled"
	} else if !usdaConfigured {
		reason = "api key not configured"
	}
	out["usda_fdc"] = providerAvailabilityResponse{
		Configured:            usdaConfigured,
		Enabled:               usdaEnabled,
		EnvFallbackConfigured: envUSDA,
		CredentialSource:      usdaSource,
		Reason:                reason,
	}
	out["kroger"] = providerAvailabilityResponse{
		Configured: false,
		Enabled:    false,
		Reason:     "not implemented in this phase",
	}
	return out
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
