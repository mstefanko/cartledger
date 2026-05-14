package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/enrichment"
	"github.com/mstefanko/cartledger/internal/enrichment/adapters"
	"github.com/mstefanko/cartledger/internal/enrichment/runner"
	estore "github.com/mstefanko/cartledger/internal/enrichment/store"
	"github.com/mstefanko/cartledger/internal/httpsafe"
	"github.com/mstefanko/cartledger/internal/identifiers"
	"github.com/mstefanko/cartledger/internal/imaging"
	"github.com/mstefanko/cartledger/internal/matcher"
	"github.com/mstefanko/cartledger/internal/storage"
	"github.com/mstefanko/cartledger/internal/upc"
)

type addProductLinkRequest struct {
	URL string `json:"url"`
}

type addProductLinkResponse struct {
	Link        productLinkResponse                   `json:"link"`
	Suggestions []productEnrichmentSuggestionResponse `json:"suggestions"`
}

var (
	openFoodFactsAPIBase     = "https://world.openfoodfacts.org/api/v2/product/"
	openFoodFactsProductBase = "https://world.openfoodfacts.org/product/"
	usdaSearchAPIBase        = "https://api.nal.usda.gov/fdc/v1/foods/search"
	usdaFoodDetailsBase      = "https://fdc.nal.usda.gov/fdc-app.html#/food-details/"
)

type enrichUPCRequest struct {
	UPC string `json:"upc"`
}

type createProductEnrichmentJobRequest struct {
	Trigger string   `json:"trigger"`
	Sources []string `json:"sources"`
	UPC     string   `json:"upc"`
	URL     string   `json:"url"`
}

type productEnrichmentJobResponse struct {
	Job runner.Job `json:"job"`
}

type acceptProductEnrichmentSuggestionRequest struct {
	Fields []string `json:"fields,omitempty"`
}

type bulkProductEnrichmentSuggestionsRequest struct {
	SuggestionIDs   []string `json:"suggestion_ids"`
	RecomputePrices bool     `json:"recompute_prices"`
}

type bulkSuggestionAcceptedResponse struct {
	SuggestionID string `json:"suggestion_id"`
	Field        string `json:"field"`
	Value        string `json:"value"`
}

type bulkSuggestionSkippedResponse struct {
	SuggestionID string `json:"suggestion_id"`
	Field        string `json:"field,omitempty"`
	Reason       string `json:"reason"`
}

type productEnrichmentConflictResponse struct {
	SuggestionID        string `json:"suggestion_id"`
	Field               string `json:"field"`
	Code                string `json:"code"`
	Message             string `json:"message"`
	ExistingProductID   string `json:"existing_product_id,omitempty"`
	ExistingProductName string `json:"existing_product_name,omitempty"`
	SuggestedMerge      bool   `json:"suggested_merge"`
}

type bulkProductEnrichmentSuggestionsResponse struct {
	Accepted  []bulkSuggestionAcceptedResponse    `json:"accepted"`
	Skipped   []bulkSuggestionSkippedResponse     `json:"skipped"`
	Conflicts []productEnrichmentConflictResponse `json:"conflicts"`
}

type productEnrichmentSuggestionResponse struct {
	ID           string    `json:"id"`
	ProductID    string    `json:"product_id"`
	LinkID       *string   `json:"product_link_id,omitempty"`
	MetadataID   *string   `json:"external_metadata_id,omitempty"`
	Source       string    `json:"source"`
	SourceURL    string    `json:"source_url"`
	Field        string    `json:"field"`
	Value        string    `json:"value"`
	CurrentValue *string   `json:"current_value,omitempty"`
	Evidence     *string   `json:"evidence,omitempty"`
	Confidence   *float64  `json:"confidence,omitempty"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type productNutritionResponse struct {
	ID                   string    `json:"id"`
	ProductID            string    `json:"product_id"`
	ProductLinkID        *string   `json:"product_link_id,omitempty"`
	ServingQuantity      *float64  `json:"serving_quantity,omitempty"`
	ServingUnit          *string   `json:"serving_unit,omitempty"`
	ServingLabel         *string   `json:"serving_label,omitempty"`
	ServingsPerContainer *float64  `json:"servings_per_container,omitempty"`
	Calories             *float64  `json:"calories,omitempty"`
	TotalFatG            *float64  `json:"total_fat_g,omitempty"`
	SaturatedFatG        *float64  `json:"saturated_fat_g,omitempty"`
	TransFatG            *float64  `json:"trans_fat_g,omitempty"`
	CholesterolMG        *float64  `json:"cholesterol_mg,omitempty"`
	SodiumMG             *float64  `json:"sodium_mg,omitempty"`
	TotalCarbohydrateG   *float64  `json:"total_carbohydrate_g,omitempty"`
	DietaryFiberG        *float64  `json:"dietary_fiber_g,omitempty"`
	TotalSugarsG         *float64  `json:"total_sugars_g,omitempty"`
	AddedSugarsG         *float64  `json:"added_sugars_g,omitempty"`
	ProteinG             *float64  `json:"protein_g,omitempty"`
	Ingredients          *string   `json:"ingredients,omitempty"`
	AllergensJSON        *string   `json:"allergens_json,omitempty"`
	SourceConfidence     *float64  `json:"source_confidence,omitempty"`
	AcceptedByUser       bool      `json:"accepted_by_user"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type rowScanner interface {
	Scan(dest ...interface{}) error
}

func normalizeUPCPointer(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	upc, err := normalizeUPCValue(*value)
	if err != nil {
		return nil, err
	}
	if upc == "" {
		return nil, nil
	}
	return &upc, nil
}

func normalizeUPCValue(value string) (string, error) {
	return upc.Normalize(value)
}

func scanProductLink(scanner rowScanner) (productLinkResponse, error) {
	var l productLinkResponse
	var fetchedAt sql.NullTime
	var httpStatus sql.NullInt64
	var contentHash, lastError sql.NullString
	var sourceConfidence sql.NullFloat64
	err := scanner.Scan(
		&l.ID, &l.ProductID, &l.Source, &l.ExternalID, &l.URL, &l.Label, &l.CreatedAt,
		&fetchedAt, &httpStatus, &contentHash, &lastError, &sourceConfidence,
	)
	if err != nil {
		return l, err
	}
	if fetchedAt.Valid {
		l.FetchedAt = &fetchedAt.Time
	}
	if httpStatus.Valid {
		status := int(httpStatus.Int64)
		l.HTTPStatus = &status
	}
	if contentHash.Valid {
		l.ContentHash = &contentHash.String
	}
	if lastError.Valid {
		l.LastError = &lastError.String
	}
	if sourceConfidence.Valid {
		l.SourceConfidence = &sourceConfidence.Float64
	}
	return l, nil
}

func (h *ProductHandler) productEnrichmentEnabled() bool {
	return h.Cfg == nil || h.Cfg.ProductEnrichmentEnabled
}

func (h *ProductHandler) enrichmentService() *runner.Service {
	return h.Enrichment
}

// AddLink fetches a user-provided product URL and returns field-level
// suggestions without overwriting product data.
func (h *ProductHandler) AddLink(c echo.Context) error {
	ctx := c.Request().Context()
	householdID := auth.HouseholdIDFrom(c)
	productID := c.Param("id")
	if !h.productEnrichmentEnabled() {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "product enrichment is disabled"})
	}
	if err := h.verifyProduct(ctx, productID, householdID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "product not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	var req addProductLinkRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	req.URL = strings.TrimSpace(req.URL)
	if req.URL == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "url is required"})
	}

	client := httpsafe.NewSafeHTTPClient(8*time.Second, 512*1024, h.Cfg != nil && h.Cfg.AllowPrivateIntegrations)
	result, err := client.Fetch(ctx, req.URL)
	if err != nil {
		return c.JSON(statusForFetchError(err), map[string]string{"error": productFetchError(err)})
	}

	source, externalID, label := classifyProductURL(result.URL)
	visibleText := enrichment.VisibleText(result.Body)
	contentHash := hashContent(visibleText)
	suggestions := suggestionsForURL(result.URL, visibleText)
	sourceConfidence := sourceConfidenceForSuggestions(suggestions)

	link, err := h.upsertProductLink(ctx, productID, source, externalID, result.URL, label, result.FetchedAt, result.StatusCode, contentHash, nil, sourceConfidence)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	sourceURL := result.URL
	payload := enrichment.MetadataPayload{
		Version:        1,
		Source:         source,
		SourceRecordID: externalID,
		SourceURL:      &sourceURL,
		Evidence: []enrichment.EvidencePayload{{
			Field: "visible_text",
			Text:  truncateEvidence(visibleText, 2000),
			URL:   &sourceURL,
		}},
	}
	metadataID, err := estore.Repository{DB: h.DB}.UpsertMetadata(ctx, estore.MetadataInput{
		HouseholdID:    householdID,
		ProductID:      productID,
		ProductLinkID:  &link.ID,
		Source:         source,
		SourceRecordID: externalID,
		SourceURL:      &sourceURL,
		LookupKey:      stringPtr("url:" + result.URL),
		Payload:        payload,
		ContentHash:    &contentHash,
		FetchedAt:      result.FetchedAt,
		HTTPStatus:     result.StatusCode,
		Confidence:     sourceConfidence,
	})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	stored, err := h.storeSuggestionsForMetadata(ctx, productID, &link.ID, &metadataID, suggestions, false)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	return c.JSON(http.StatusOK, addProductLinkResponse{Link: link, Suggestions: stored})
}

// DeleteLink removes a source link and the source-bound enrichment artifacts
// derived from it. Accepted source-specific nutrition is removed too so a bad
// barcode lookup does not leave orphaned facts behind.
func (h *ProductHandler) DeleteLink(c echo.Context) error {
	ctx := c.Request().Context()
	householdID := auth.HouseholdIDFrom(c)
	productID := c.Param("id")
	linkID := strings.TrimSpace(c.Param("linkId"))
	if linkID == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "link id is required"})
	}
	if err := h.verifyProduct(ctx, productID, householdID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "product not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRowContext(ctx,
		"SELECT 1 FROM product_links WHERE id = ? AND product_id = ?",
		linkID, productID,
	).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "source link not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	for _, stmt := range []string{
		"DELETE FROM product_nutrition WHERE product_id = ? AND product_link_id = ?",
		"DELETE FROM product_enrichment_suggestions WHERE product_id = ? AND product_link_id = ?",
		"DELETE FROM product_external_metadata WHERE product_id = ? AND product_link_id = ?",
		"DELETE FROM product_links WHERE product_id = ? AND id = ?",
	} {
		if _, err := tx.ExecContext(ctx, stmt, productID, linkID); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
	}

	if err := tx.Commit(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	h.broadcastProductUpdated(householdID, productID, []string{"links", "external_metadata", "enrichment_suggestions", "nutrition"})
	return c.NoContent(http.StatusNoContent)
}

func (h *ProductHandler) CreateEnrichmentJob(c echo.Context) error {
	ctx := c.Request().Context()
	householdID := auth.HouseholdIDFrom(c)
	productID := c.Param("id")
	if !h.productEnrichmentEnabled() {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "product enrichment is disabled"})
	}
	if err := h.verifyProduct(ctx, productID, householdID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "product not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	var req createProductEnrichmentJobRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	req.Trigger = strings.TrimSpace(req.Trigger)
	if req.Trigger == "" {
		req.Trigger = runner.TriggerManualLookup
	}
	if req.Trigger != runner.TriggerManualLookup && req.Trigger != runner.TriggerManualRefresh {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "unsupported enrichment trigger"})
	}

	lookupKey := ""
	req.URL = strings.TrimSpace(req.URL)
	if req.URL != "" {
		validatedURL, err := httpsafe.ValidateURL(req.URL, h.Cfg != nil && h.Cfg.AllowPrivateIntegrations)
		if err != nil {
			return c.JSON(statusForFetchError(err), map[string]string{"error": productFetchError(err)})
		}
		lookupKey = "url:" + validatedURL.String()
	} else {
		upcValue := strings.TrimSpace(req.UPC)
		if upcValue == "" {
			p, err := h.fetchProduct(productID)
			if err == nil && p.UPC != nil {
				upcValue = *p.UPC
			}
		}
		upc, err := normalizeUPCValue(upcValue)
		if err != nil || upc == "" {
			if err == nil {
				err = fmt.Errorf("upc or url is required")
			}
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		lookupKey = "upc:" + upc
	}

	service := h.enrichmentService()
	if service == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "product enrichment worker is unavailable"})
	}
	job, _, err := service.QueueJob(ctx, runner.QueueJobRequest{
		HouseholdID:       householdID,
		ProductID:         productID,
		RequestedByUserID: auth.UserIDFrom(c),
		Trigger:           req.Trigger,
		LookupKey:         lookupKey,
		RequestedSources:  req.Sources,
	})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	return c.JSON(http.StatusAccepted, productEnrichmentJobResponse{Job: job})
}

func (h *ProductHandler) ListEnrichmentJobs(c echo.Context) error {
	ctx := c.Request().Context()
	householdID := auth.HouseholdIDFrom(c)
	productID := c.Param("id")
	if err := h.verifyProduct(ctx, productID, householdID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "product not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	service := h.enrichmentService()
	if service == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "product enrichment worker is unavailable"})
	}
	jobs, err := service.ListJobs(ctx, householdID, productID, 20)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	return c.JSON(http.StatusOK, map[string][]runner.Job{"jobs": jobs})
}

func (h *ProductHandler) EnrichByUPC(c echo.Context) error {
	ctx := c.Request().Context()
	householdID := auth.HouseholdIDFrom(c)
	productID := c.Param("id")
	if !h.productEnrichmentEnabled() {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "product enrichment is disabled"})
	}
	if err := h.verifyProduct(ctx, productID, householdID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "product not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	var req enrichUPCRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	upc, err := normalizeUPCValue(req.UPC)
	if err != nil || upc == "" {
		if err == nil {
			err = fmt.Errorf("upc is required")
		}
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	service := h.enrichmentService()
	if service == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "product enrichment worker is unavailable"})
	}
	job, _, err := service.QueueJob(ctx, runner.QueueJobRequest{
		HouseholdID:       householdID,
		ProductID:         productID,
		RequestedByUserID: auth.UserIDFrom(c),
		Trigger:           runner.TriggerManualLookup,
		LookupKey:         "upc:" + upc,
	})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	return c.JSON(http.StatusAccepted, productEnrichmentJobResponse{Job: job})
}

func (h *ProductHandler) AcceptEnrichmentSuggestion(c echo.Context) error {
	ctx := c.Request().Context()
	householdID := auth.HouseholdIDFrom(c)
	productID := c.Param("id")
	suggestionID := decodedParam(c, "suggestionId")
	if err := h.verifyProduct(ctx, productID, householdID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "product not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	var req acceptProductEnrichmentSuggestionRequest
	_ = c.Bind(&req)

	s, err := h.fetchSuggestion(ctx, productID, suggestionID)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "suggestion not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if len(req.Fields) > 0 && !fieldSelected(req.Fields, s.Field) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "suggestion field was not selected"})
	}

	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer tx.Rollback()

	if err := applySuggestion(ctx, tx, h.Cfg, householdID, productID, s); err != nil {
		if errors.Is(err, errInvalidSuggestionValue) {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		if errors.Is(err, identifiers.ErrIdentifierConflict) || isUniqueConstraintError(err) {
			return c.JSON(http.StatusConflict, h.suggestionConflict(ctx, householdID, s, err))
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if err := recordProductFieldEdits(ctx, tx, productID, auth.UserIDFrom(c), map[string]struct{}{s.Field: {}}, "suggestion_accept"); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE product_enrichment_suggestions SET status = 'accepted', updated_at = CURRENT_TIMESTAMP WHERE id = ? AND product_id = ?",
		s.ID, productID,
	); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if err := tx.Commit(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	updated, err := h.fetchSuggestion(ctx, productID, s.ID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	h.broadcastProductUpdated(householdID, productID, changedFieldsForSuggestion(s.Field))
	return c.JSON(http.StatusOK, updated)
}

func (h *ProductHandler) BulkAcceptEnrichmentSuggestions(c echo.Context) error {
	ctx := c.Request().Context()
	householdID := auth.HouseholdIDFrom(c)
	productID := c.Param("id")
	if err := h.verifyProduct(ctx, productID, householdID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "product not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	var req bulkProductEnrichmentSuggestionsRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	if len(req.SuggestionIDs) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "suggestion_ids is required"})
	}

	resp := bulkProductEnrichmentSuggestionsResponse{
		Accepted:  []bulkSuggestionAcceptedResponse{},
		Skipped:   []bulkSuggestionSkippedResponse{},
		Conflicts: []productEnrichmentConflictResponse{},
	}
	changed := map[string]struct{}{}
	toApply := make([]productEnrichmentSuggestionResponse, 0, len(req.SuggestionIDs))

	for _, suggestionID := range req.SuggestionIDs {
		suggestionID = strings.TrimSpace(suggestionID)
		if suggestionID == "" {
			continue
		}
		s, err := h.fetchSuggestion(ctx, productID, suggestionID)
		if errors.Is(err, sql.ErrNoRows) {
			resp.Skipped = append(resp.Skipped, bulkSuggestionSkippedResponse{SuggestionID: suggestionID, Reason: "not_found"})
			continue
		}
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		if suggestionAlreadyCurrent(s) {
			resp.Skipped = append(resp.Skipped, bulkSuggestionSkippedResponse{SuggestionID: s.ID, Field: s.Field, Reason: "already_current"})
			continue
		}
		toApply = append(toApply, s)
	}

	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer tx.Rollback()

	for _, s := range toApply {
		if err := applySuggestion(ctx, tx, h.Cfg, householdID, productID, s); err != nil {
			if errors.Is(err, identifiers.ErrIdentifierConflict) || isUniqueConstraintError(err) {
				resp.Conflicts = append(resp.Conflicts, h.suggestionConflict(ctx, householdID, s, err))
				continue
			}
			if errors.Is(err, errInvalidSuggestionValue) {
				resp.Skipped = append(resp.Skipped, bulkSuggestionSkippedResponse{SuggestionID: s.ID, Field: s.Field, Reason: err.Error()})
				continue
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		if err := recordProductFieldEdits(ctx, tx, productID, auth.UserIDFrom(c), map[string]struct{}{s.Field: {}}, "suggestion_accept"); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE product_enrichment_suggestions SET status = 'accepted', updated_at = CURRENT_TIMESTAMP WHERE id = ? AND product_id = ?",
			s.ID, productID,
		); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		resp.Accepted = append(resp.Accepted, bulkSuggestionAcceptedResponse{SuggestionID: s.ID, Field: s.Field, Value: s.Value})
		changed[s.Field] = struct{}{}
		if isSourceImageSuggestionField(s.Field) {
			changed["images"] = struct{}{}
		}
	}

	if req.RecomputePrices && (hasField(changed, "pack_quantity") || hasField(changed, "pack_unit")) {
		updated, err := h.recomputeProductPricesTx(ctx, tx, productID, householdID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to recompute price history"})
		}
		if updated > 0 {
			changed["price_history"] = struct{}{}
		}
	}

	if err := tx.Commit(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	if len(changed) > 0 {
		h.broadcastProductUpdated(householdID, productID, fieldsFromSet(changed))
	}

	status := http.StatusOK
	if len(resp.Accepted) == 0 && len(resp.Skipped) == 0 && len(resp.Conflicts) > 0 {
		status = http.StatusConflict
	}
	return c.JSON(status, resp)
}

func (h *ProductHandler) BulkRejectEnrichmentSuggestions(c echo.Context) error {
	ctx := c.Request().Context()
	householdID := auth.HouseholdIDFrom(c)
	productID := c.Param("id")
	if err := h.verifyProduct(ctx, productID, householdID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "product not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	var req bulkProductEnrichmentSuggestionsRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	if len(req.SuggestionIDs) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "suggestion_ids is required"})
	}

	updated := 0
	for _, suggestionID := range req.SuggestionIDs {
		suggestionID = strings.TrimSpace(suggestionID)
		if suggestionID == "" {
			continue
		}
		rejected, err := h.rejectEnrichmentSuggestion(ctx, productID, suggestionID, true)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		updated += rejected
	}
	if updated > 0 {
		h.broadcastProductUpdated(householdID, productID, []string{"enrichment_suggestions"})
	}
	return c.JSON(http.StatusOK, map[string]int{"rejected": updated})
}

func (h *ProductHandler) RejectEnrichmentSuggestion(c echo.Context) error {
	ctx := c.Request().Context()
	householdID := auth.HouseholdIDFrom(c)
	productID := c.Param("id")
	suggestionID := decodedParam(c, "suggestionId")
	if err := h.verifyProduct(ctx, productID, householdID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "product not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	updated, err := h.rejectEnrichmentSuggestion(ctx, productID, suggestionID, false)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if updated == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "suggestion not found"})
	}
	h.broadcastProductUpdated(householdID, productID, []string{"enrichment_suggestions"})
	return c.NoContent(http.StatusNoContent)
}

func decodedParam(c echo.Context, name string) string {
	value := c.Param(name)
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return value
	}
	return decoded
}

func (h *ProductHandler) rejectEnrichmentSuggestion(ctx context.Context, productID, suggestionID string, pendingOnly bool) (int, error) {
	query := "UPDATE product_enrichment_suggestions SET status = 'rejected', updated_at = CURRENT_TIMESTAMP WHERE id = ? AND product_id = ?"
	if pendingOnly {
		query += " AND status = 'pending'"
	}
	result, err := h.DB.ExecContext(ctx,
		query,
		suggestionID, productID,
	)
	if err != nil {
		return 0, err
	}
	rows, _ := result.RowsAffected()
	if rows > 0 || !isVirtualSourceImageSuggestionID(suggestionID) {
		return int(rows), nil
	}
	s, err := h.ensureVirtualSourceImageSuggestion(ctx, productID, suggestionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	query = "UPDATE product_enrichment_suggestions SET status = 'rejected', updated_at = CURRENT_TIMESTAMP WHERE id = ? AND product_id = ?"
	if pendingOnly {
		query += " AND status = 'pending'"
	}
	result, err = h.DB.ExecContext(ctx,
		query,
		s.ID, productID,
	)
	if err != nil {
		return 0, err
	}
	rows, _ = result.RowsAffected()
	return int(rows), nil
}

func (h *ProductHandler) upsertProductLink(ctx context.Context, productID, source string, externalID *string, rawURL string, label *string, fetchedAt time.Time, status int, contentHash string, lastError *string, confidence *float64) (productLinkResponse, error) {
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return productLinkResponse{}, err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	var id string
	err = tx.QueryRowContext(ctx,
		"SELECT id FROM product_links WHERE product_id = ? AND url = ? ORDER BY created_at LIMIT 1",
		productID, rawURL,
	).Scan(&id)
	if err == sql.ErrNoRows {
		err = tx.QueryRowContext(ctx,
			`INSERT INTO product_links
			    (id, product_id, source, external_id, url, label, created_at, fetched_at, http_status, content_hash, last_error, source_confidence)
			 VALUES (lower(hex(randomblob(16))), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 RETURNING id`,
			productID, source, externalID, rawURL, label, now, nullableTime(fetchedAt), nullableInt(status), nullableTrimmedString(contentHash), lastError, confidence,
		).Scan(&id)
	}
	if err != nil {
		return productLinkResponse{}, err
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE product_links
		    SET source = ?, external_id = ?, label = ?, fetched_at = ?, http_status = ?,
		        content_hash = ?, last_error = ?, source_confidence = ?
		  WHERE id = ? AND product_id = ?`,
		source, externalID, label, nullableTime(fetchedAt), nullableInt(status), nullableTrimmedString(contentHash), lastError, confidence, id, productID,
	)
	if err != nil {
		return productLinkResponse{}, err
	}
	if err := tx.Commit(); err != nil {
		return productLinkResponse{}, err
	}
	return h.fetchProductLink(ctx, productID, id)
}

func (h *ProductHandler) fetchProductLink(ctx context.Context, productID, linkID string) (productLinkResponse, error) {
	return scanProductLink(h.DB.QueryRowContext(ctx,
		`SELECT id, product_id, source, external_id, url, label, created_at,
		        fetched_at, http_status, content_hash, last_error, source_confidence
		   FROM product_links WHERE id = ? AND product_id = ?`,
		linkID, productID,
	))
}

func (h *ProductHandler) storeSuggestions(ctx context.Context, productID string, linkID *string, suggestions []enrichment.Suggestion) ([]productEnrichmentSuggestionResponse, error) {
	return h.storeSuggestionsForMetadata(ctx, productID, linkID, nil, suggestions, false)
}

func (h *ProductHandler) storeSuggestionsForMetadata(ctx context.Context, productID string, linkID, metadataID *string, suggestions []enrichment.Suggestion, bypassFieldEdits bool) ([]productEnrichmentSuggestionResponse, error) {
	out := make([]productEnrichmentSuggestionResponse, 0, len(suggestions))
	ids, err := estore.Repository{DB: h.DB}.StoreSuggestions(ctx, productID, linkID, metadataID, suggestions, bypassFieldEdits)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		resp, err := h.fetchSuggestion(ctx, productID, id)
		if err != nil {
			return nil, err
		}
		out = append(out, resp)
	}
	return out, nil
}

func (h *ProductHandler) suggestionEvidenceTime(ctx context.Context, productID string, linkID *string) (time.Time, bool, error) {
	if linkID == nil || strings.TrimSpace(*linkID) == "" {
		return time.Time{}, false, nil
	}
	var fetchedAt, createdAt sql.NullTime
	if err := h.DB.QueryRowContext(ctx,
		"SELECT fetched_at, created_at FROM product_links WHERE id = ? AND product_id = ?",
		*linkID, productID,
	).Scan(&fetchedAt, &createdAt); err != nil {
		return time.Time{}, false, err
	}
	if fetchedAt.Valid {
		return fetchedAt.Time, true, nil
	}
	if createdAt.Valid {
		return createdAt.Time, true, nil
	}
	return time.Time{}, false, nil
}

func (h *ProductHandler) productFieldEditBlockedFields(ctx context.Context, productID string, evidenceAt time.Time, hasEvidenceTime bool) (map[string]struct{}, error) {
	blocked := map[string]struct{}{}
	if !hasEvidenceTime {
		return blocked, nil
	}
	rows, err := h.DB.QueryContext(ctx,
		`SELECT field
		   FROM product_field_edits
		  WHERE product_id = ?
		    AND datetime(edited_at) > datetime(?)`,
		productID, evidenceAt.Format("2006-01-02 15:04:05"),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var field string
		if err := rows.Scan(&field); err != nil {
			return nil, err
		}
		blocked[field] = struct{}{}
	}
	return blocked, rows.Err()
}

func (h *ProductHandler) fetchProductEnrichmentSuggestions(productID string, pendingOnly bool) []productEnrichmentSuggestionResponse {
	currentValues := h.currentValuesForProduct(productID)
	query := `SELECT id, product_id, product_link_id, external_metadata_id, source, source_url, field, value, evidence, confidence, status, created_at, updated_at
	            FROM product_enrichment_suggestions
	           WHERE product_id = ?`
	args := []interface{}{productID}
	if pendingOnly {
		query += ` AND status = 'pending'`
	}
	query += ` ORDER BY created_at DESC, field`
	rows, err := h.DB.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []productEnrichmentSuggestionResponse
	for rows.Next() {
		s, err := h.scanSuggestion(rows, currentValues)
		if err == nil {
			out = append(out, s)
		}
	}
	if pendingOnly {
		out = h.appendVirtualSourceImageSuggestions(productID, out)
	}
	return out
}

func (h *ProductHandler) fetchSuggestion(ctx context.Context, productID, suggestionID string) (productEnrichmentSuggestionResponse, error) {
	s, err := h.scanSuggestion(h.DB.QueryRowContext(ctx,
		`SELECT id, product_id, product_link_id, external_metadata_id, source, source_url, field, value, evidence, confidence, status, created_at, updated_at
		   FROM product_enrichment_suggestions
		  WHERE id = ? AND product_id = ?`,
		suggestionID, productID,
	), h.currentValuesForProduct(productID))
	if errors.Is(err, sql.ErrNoRows) && isVirtualSourceImageSuggestionID(suggestionID) {
		return h.ensureVirtualSourceImageSuggestion(ctx, productID, suggestionID)
	}
	return s, err
}

func (h *ProductHandler) scanSuggestion(scanner rowScanner, currentValues map[string]string) (productEnrichmentSuggestionResponse, error) {
	var s productEnrichmentSuggestionResponse
	var linkID, metadataID, evidence sql.NullString
	var confidence sql.NullFloat64
	if err := scanner.Scan(&s.ID, &s.ProductID, &linkID, &metadataID, &s.Source, &s.SourceURL, &s.Field, &s.Value, &evidence, &confidence, &s.Status, &s.CreatedAt, &s.UpdatedAt); err != nil {
		return s, err
	}
	if linkID.Valid {
		s.LinkID = &linkID.String
	}
	if metadataID.Valid {
		s.MetadataID = &metadataID.String
	}
	if evidence.Valid {
		s.Evidence = &evidence.String
	}
	if confidence.Valid {
		s.Confidence = &confidence.Float64
	}
	if current := currentValues[s.Field]; current != "" {
		s.CurrentValue = &current
	}
	return s, nil
}

func (h *ProductHandler) currentValuesForProduct(productID string) map[string]string {
	values := make(map[string]string, 5)
	var p productResponse
	err := h.DB.QueryRow(
		"SELECT name, brand, upc, pack_quantity, pack_unit FROM products WHERE id = ?",
		productID,
	).Scan(&p.Name, &p.Brand, &p.UPC, &p.PackQuantity, &p.PackUnit)
	if err != nil {
		return values
	}
	values["name"] = p.Name
	values["brand"] = ptrStringValue(p.Brand)
	values["upc"] = ptrStringValue(p.UPC)
	if p.PackQuantity != nil {
		values["pack_quantity"] = strconv.FormatFloat(*p.PackQuantity, 'f', -1, 64)
	}
	values["pack_unit"] = ptrStringValue(p.PackUnit)
	return values
}

const virtualSourceImageSuggestionPrefix = "source-image:"

type sourceImageSuggestionKind struct {
	Key       string
	Field     string
	Label     string
	ImageType string
	Primary   bool
}

var sourceImageSuggestionKinds = []sourceImageSuggestionKind{
	{Key: "front", Field: "image_front_url", Label: "front", ImageType: "packaging", Primary: true},
	{Key: "nutrition", Field: "image_nutrition_url", Label: "nutrition", ImageType: "nutrition"},
	{Key: "ingredients", Field: "image_ingredients_url", Label: "ingredients", ImageType: "packaging"},
	{Key: "packaging", Field: "image_packaging_url", Label: "package", ImageType: "packaging"},
}

func sourceImageKindForKey(key string) (sourceImageSuggestionKind, bool) {
	key = strings.TrimSpace(strings.ToLower(key))
	for _, kind := range sourceImageSuggestionKinds {
		if kind.Key == key {
			return kind, true
		}
	}
	return sourceImageSuggestionKind{}, false
}

func sourceImageKindForField(field string) (sourceImageSuggestionKind, bool) {
	field = strings.TrimSpace(strings.ToLower(field))
	for _, kind := range sourceImageSuggestionKinds {
		if kind.Field == field {
			return kind, true
		}
	}
	return sourceImageSuggestionKind{}, false
}

func isSourceImageSuggestionField(field string) bool {
	_, ok := sourceImageKindForField(field)
	return ok
}

func changedFieldsForSuggestion(field string) []string {
	if isSourceImageSuggestionField(field) {
		return []string{field, "images", "enrichment_suggestions"}
	}
	return []string{field}
}

func virtualSourceImageSuggestionID(metadataID, key string) string {
	return virtualSourceImageSuggestionPrefix + metadataID + ":" + key
}

func isVirtualSourceImageSuggestionID(id string) bool {
	return strings.HasPrefix(id, virtualSourceImageSuggestionPrefix)
}

func parseVirtualSourceImageSuggestionID(id string) (metadataID, key string, ok bool) {
	rest := strings.TrimPrefix(id, virtualSourceImageSuggestionPrefix)
	metadataID, key, ok = strings.Cut(rest, ":")
	return metadataID, key, ok && strings.TrimSpace(metadataID) != "" && strings.TrimSpace(key) != ""
}

func sourceImageSuggestionKey(metadataID, field, value string) string {
	return metadataID + "\x00" + field + "\x00" + strings.TrimSpace(value)
}

func sourceImageSuggestionFields() []string {
	fields := make([]string, 0, len(sourceImageSuggestionKinds))
	for _, kind := range sourceImageSuggestionKinds {
		fields = append(fields, kind.Field)
	}
	return fields
}

func (h *ProductHandler) appendVirtualSourceImageSuggestions(productID string, out []productEnrichmentSuggestionResponse) []productEnrichmentSuggestionResponse {
	existing := h.existingSourceImageSuggestionKeys(productID)
	for _, s := range out {
		if s.MetadataID != nil && isSourceImageSuggestionField(s.Field) {
			existing[sourceImageSuggestionKey(*s.MetadataID, s.Field, s.Value)] = struct{}{}
		}
	}

	now := time.Now().UTC()
	for _, metadata := range h.fetchProductExternalMetadata(productID) {
		var payload enrichment.MetadataPayload
		if err := json.Unmarshal(metadata.Payload, &payload); err != nil {
			continue
		}
		for _, kind := range sourceImageSuggestionKinds {
			rawURL := strings.TrimSpace(payload.ImageURLs[kind.Key])
			if rawURL == "" {
				continue
			}
			if _, ok := existing[sourceImageSuggestionKey(metadata.ID, kind.Field, rawURL)]; ok {
				continue
			}
			sourceURL := sourceURLForMetadataImage(metadata, payload, rawURL)
			evidence := sourceImageEvidence(metadata.Source, kind)
			metadataID := metadata.ID
			out = append(out, productEnrichmentSuggestionResponse{
				ID:         virtualSourceImageSuggestionID(metadata.ID, kind.Key),
				ProductID:  productID,
				LinkID:     metadata.ProductLinkID,
				MetadataID: &metadataID,
				Source:     metadata.Source,
				SourceURL:  sourceURL,
				Field:      kind.Field,
				Value:      rawURL,
				Evidence:   &evidence,
				Status:     "pending",
				CreatedAt:  now,
				UpdatedAt:  now,
			})
		}
	}
	return out
}

func (h *ProductHandler) existingSourceImageSuggestionKeys(productID string) map[string]struct{} {
	out := map[string]struct{}{}
	fields := sourceImageSuggestionFields()
	if len(fields) == 0 {
		return out
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(fields)), ",")
	args := make([]interface{}, 0, len(fields)+1)
	args = append(args, productID)
	for _, field := range fields {
		args = append(args, field)
	}
	rows, err := h.DB.Query(
		`SELECT external_metadata_id, field, value
		   FROM product_enrichment_suggestions
		  WHERE product_id = ?
		    AND external_metadata_id IS NOT NULL
		    AND field IN (`+placeholders+`)`,
		args...,
	)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var metadataID, field, value string
		if rows.Scan(&metadataID, &field, &value) == nil {
			out[sourceImageSuggestionKey(metadataID, field, value)] = struct{}{}
		}
	}
	return out
}

func (h *ProductHandler) ensureVirtualSourceImageSuggestion(ctx context.Context, productID, suggestionID string) (productEnrichmentSuggestionResponse, error) {
	metadataID, key, ok := parseVirtualSourceImageSuggestionID(suggestionID)
	if !ok {
		return productEnrichmentSuggestionResponse{}, sql.ErrNoRows
	}
	kind, ok := sourceImageKindForKey(key)
	if !ok {
		return productEnrichmentSuggestionResponse{}, sql.ErrNoRows
	}

	metadata, payload, err := h.fetchMetadataPayload(ctx, productID, metadataID)
	if err != nil {
		return productEnrichmentSuggestionResponse{}, err
	}
	rawURL := strings.TrimSpace(payload.ImageURLs[kind.Key])
	if rawURL == "" {
		return productEnrichmentSuggestionResponse{}, sql.ErrNoRows
	}

	var id string
	err = h.DB.QueryRowContext(ctx,
		`SELECT id
		   FROM product_enrichment_suggestions
		  WHERE product_id = ? AND external_metadata_id = ? AND field = ? AND value = ?
		  LIMIT 1`,
		productID, metadata.ID, kind.Field, rawURL,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		sourceURL := sourceURLForMetadataImage(metadata, payload, rawURL)
		evidence := sourceImageEvidence(metadata.Source, kind)
		err = h.DB.QueryRowContext(ctx,
			`INSERT INTO product_enrichment_suggestions
			    (id, product_id, product_link_id, external_metadata_id, source, source_url, field, value, evidence, status, created_at, updated_at)
			 VALUES (lower(hex(randomblob(16))), ?, ?, ?, ?, ?, ?, ?, ?, 'pending', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
			 RETURNING id`,
			productID, metadata.ProductLinkID, metadata.ID, metadata.Source, sourceURL, kind.Field, rawURL, evidence,
		).Scan(&id)
	}
	if err != nil {
		return productEnrichmentSuggestionResponse{}, err
	}
	return h.fetchSuggestion(ctx, productID, id)
}

func (h *ProductHandler) fetchMetadataPayload(ctx context.Context, productID, metadataID string) (productExternalMetadataResponse, enrichment.MetadataPayload, error) {
	var item productExternalMetadataResponse
	var payloadRaw string
	var linkID, sourceRecordID, sourceURL, lookupKey, lastError sql.NullString
	var fetchedAt sql.NullTime
	var httpStatus sql.NullInt64
	var confidence sql.NullFloat64
	if err := h.DB.QueryRowContext(ctx,
		`SELECT id, product_id, product_link_id, source, source_record_id, source_url,
		        lookup_key, payload_json, payload_version, fetched_at, http_status,
		        last_error, confidence, created_at, updated_at
		   FROM product_external_metadata
		  WHERE id = ? AND product_id = ?`,
		metadataID, productID,
	).Scan(
		&item.ID, &item.ProductID, &linkID, &item.Source, &sourceRecordID, &sourceURL,
		&lookupKey, &payloadRaw, &item.PayloadVersion, &fetchedAt, &httpStatus,
		&lastError, &confidence, &item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return productExternalMetadataResponse{}, enrichment.MetadataPayload{}, err
	}
	item.ProductLinkID = sqlNullStringPtr(linkID)
	item.SourceRecordID = sqlNullStringPtr(sourceRecordID)
	item.SourceURL = sqlNullStringPtr(sourceURL)
	item.LookupKey = sqlNullStringPtr(lookupKey)
	item.LastError = sqlNullStringPtr(lastError)
	if fetchedAt.Valid {
		item.FetchedAt = &fetchedAt.Time
	}
	if httpStatus.Valid {
		status := int(httpStatus.Int64)
		item.HTTPStatus = &status
	}
	item.Confidence = nullFloatPtr(confidence)
	item.Payload = json.RawMessage(payloadRaw)

	var payload enrichment.MetadataPayload
	if err := json.Unmarshal([]byte(payloadRaw), &payload); err != nil {
		return productExternalMetadataResponse{}, enrichment.MetadataPayload{}, err
	}
	return item, payload, nil
}

func sourceURLForMetadataImage(metadata productExternalMetadataResponse, payload enrichment.MetadataPayload, fallback string) string {
	if metadata.SourceURL != nil && strings.TrimSpace(*metadata.SourceURL) != "" {
		return strings.TrimSpace(*metadata.SourceURL)
	}
	if payload.SourceURL != nil && strings.TrimSpace(*payload.SourceURL) != "" {
		return strings.TrimSpace(*payload.SourceURL)
	}
	return strings.TrimSpace(fallback)
}

func sourceImageEvidence(source string, kind sourceImageSuggestionKind) string {
	return sourceDisplayName(source) + " " + kind.Label + " photo"
}

func sourceDisplayName(source string) string {
	switch source {
	case "openfoodfacts":
		return "Open Food Facts"
	case "usda_fdc":
		return "USDA FoodData Central"
	case "kroger":
		return "Kroger"
	case "url":
		return "Product URL"
	default:
		if strings.TrimSpace(source) == "" {
			return "Source"
		}
		return strings.TrimSpace(source)
	}
}

func (h *ProductHandler) fetchProductNutrition(productID string) []productNutritionResponse {
	rows, err := h.DB.Query(
		`SELECT id, product_id, product_link_id, serving_quantity, serving_unit, serving_label,
		        servings_per_container, calories, total_fat_g, saturated_fat_g, trans_fat_g,
		        cholesterol_mg, sodium_mg, total_carbohydrate_g, dietary_fiber_g,
		        total_sugars_g, added_sugars_g, protein_g, ingredients, allergens_json,
		        source_confidence, accepted_by_user, created_at, updated_at
		   FROM product_nutrition
		  WHERE product_id = ? AND accepted_by_user = 1
		  ORDER BY updated_at DESC`,
		productID,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []productNutritionResponse
	for rows.Next() {
		var n productNutritionResponse
		var linkID, servingUnit, servingLabel, ingredients, allergens sql.NullString
		var servingQuantity, servingsPerContainer, calories, totalFat, saturatedFat, transFat, cholesterol, sodium, carbs, fiber, totalSugars, addedSugars, protein, confidence sql.NullFloat64
		var accepted int
		if rows.Scan(
			&n.ID, &n.ProductID, &linkID, &servingQuantity, &servingUnit, &servingLabel,
			&servingsPerContainer, &calories, &totalFat, &saturatedFat, &transFat,
			&cholesterol, &sodium, &carbs, &fiber, &totalSugars, &addedSugars, &protein,
			&ingredients, &allergens, &confidence, &accepted, &n.CreatedAt, &n.UpdatedAt,
		) != nil {
			continue
		}
		n.ProductLinkID = sqlNullStringPtr(linkID)
		n.ServingQuantity = nullFloatPtr(servingQuantity)
		n.ServingUnit = sqlNullStringPtr(servingUnit)
		n.ServingLabel = sqlNullStringPtr(servingLabel)
		n.ServingsPerContainer = nullFloatPtr(servingsPerContainer)
		n.Calories = nullFloatPtr(calories)
		n.TotalFatG = nullFloatPtr(totalFat)
		n.SaturatedFatG = nullFloatPtr(saturatedFat)
		n.TransFatG = nullFloatPtr(transFat)
		n.CholesterolMG = nullFloatPtr(cholesterol)
		n.SodiumMG = nullFloatPtr(sodium)
		n.TotalCarbohydrateG = nullFloatPtr(carbs)
		n.DietaryFiberG = nullFloatPtr(fiber)
		n.TotalSugarsG = nullFloatPtr(totalSugars)
		n.AddedSugarsG = nullFloatPtr(addedSugars)
		n.ProteinG = nullFloatPtr(protein)
		n.Ingredients = sqlNullStringPtr(ingredients)
		n.AllergensJSON = sqlNullStringPtr(allergens)
		n.SourceConfidence = nullFloatPtr(confidence)
		n.AcceptedByUser = accepted == 1
		out = append(out, n)
	}
	return out
}

func (h *ProductHandler) fetchProductExternalMetadata(productID string) []productExternalMetadataResponse {
	rows, err := h.DB.Query(
		`SELECT id, product_id, product_link_id, source, source_record_id, source_url,
		        lookup_key, payload_json, payload_version, fetched_at, http_status,
		        last_error, confidence, created_at, updated_at
		   FROM product_external_metadata
		  WHERE product_id = ?
		  ORDER BY fetched_at DESC NULLS LAST, updated_at DESC, created_at DESC`,
		productID,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()

	out := make([]productExternalMetadataResponse, 0)
	for rows.Next() {
		var item productExternalMetadataResponse
		var linkID, sourceRecordID, sourceURL, lookupKey, payload, lastError sql.NullString
		var fetchedAt sql.NullTime
		var httpStatus sql.NullInt64
		var confidence sql.NullFloat64
		if rows.Scan(
			&item.ID, &item.ProductID, &linkID, &item.Source, &sourceRecordID, &sourceURL,
			&lookupKey, &payload, &item.PayloadVersion, &fetchedAt, &httpStatus,
			&lastError, &confidence, &item.CreatedAt, &item.UpdatedAt,
		) != nil {
			continue
		}
		item.ProductLinkID = sqlNullStringPtr(linkID)
		item.SourceRecordID = sqlNullStringPtr(sourceRecordID)
		item.SourceURL = sqlNullStringPtr(sourceURL)
		item.LookupKey = sqlNullStringPtr(lookupKey)
		item.LastError = sqlNullStringPtr(lastError)
		if fetchedAt.Valid {
			item.FetchedAt = &fetchedAt.Time
		}
		if httpStatus.Valid {
			status := int(httpStatus.Int64)
			item.HTTPStatus = &status
		}
		item.Confidence = nullFloatPtr(confidence)
		raw := []byte("{}")
		if payload.Valid && json.Valid([]byte(payload.String)) {
			raw = []byte(payload.String)
		}
		item.Payload = json.RawMessage(raw)
		out = append(out, item)
	}
	return out
}

var errInvalidSuggestionValue = errors.New("invalid suggestion value")

func applySuggestion(ctx context.Context, tx *sql.Tx, cfg *config.Config, householdID, productID string, s productEnrichmentSuggestionResponse) error {
	now := time.Now().UTC()
	if isSourceImageSuggestionField(s.Field) {
		return applySourceImageSuggestion(ctx, tx, cfg, productID, s)
	}
	switch s.Field {
	case "name":
		_, err := tx.ExecContext(ctx, "UPDATE products SET name = ?, updated_at = ? WHERE id = ?", strings.TrimSpace(s.Value), now, productID)
		return err
	case "brand":
		brand := matcher.NormalizeBrand(s.Value)
		_, err := tx.ExecContext(ctx, "UPDATE products SET brand = ?, updated_at = ? WHERE id = ?", nullableString(brand), now, productID)
		return err
	case "upc":
		upc, err := normalizeUPCValue(s.Value)
		if err != nil || upc == "" {
			return fmt.Errorf("%w: upc must be a valid GTIN", errInvalidSuggestionValue)
		}
		_, err = identifiers.SetProductPrimaryGTIN(ctx, tx, householdID, productID, upc, "enrichment", s.Confidence)
		return err
	case "pack_quantity":
		qty, err := strconv.ParseFloat(strings.TrimSpace(s.Value), 64)
		if err != nil || qty <= 0 {
			return fmt.Errorf("%w: pack_quantity must be a positive number", errInvalidSuggestionValue)
		}
		_, err = tx.ExecContext(ctx, "UPDATE products SET pack_quantity = ?, updated_at = ? WHERE id = ?", qty, now, productID)
		return err
	case "pack_unit":
		_, err := tx.ExecContext(ctx, "UPDATE products SET pack_unit = ?, updated_at = ? WHERE id = ?", nullableString(s.Value), now, productID)
		return err
	}
	return applyNutritionSuggestion(ctx, tx, productID, s)
}

func applySourceImageSuggestion(ctx context.Context, tx *sql.Tx, cfg *config.Config, productID string, s productEnrichmentSuggestionResponse) error {
	kind, ok := sourceImageKindForField(s.Field)
	if !ok {
		return fmt.Errorf("%w: unsupported source image field", errInvalidSuggestionValue)
	}
	rawURL := strings.TrimSpace(s.Value)
	if rawURL == "" {
		return fmt.Errorf("%w: source image URL is required", errInvalidSuggestionValue)
	}
	if cfg == nil || strings.TrimSpace(cfg.DataDir) == "" {
		return errors.New("product image storage is not configured")
	}

	client := httpsafe.NewSafeHTTPClient(12*time.Second, 10<<20, cfg.AllowPrivateIntegrations)
	result, err := client.Fetch(ctx, rawURL)
	if err != nil {
		return fmt.Errorf("%w: source image could not be fetched", errInvalidSuggestionValue)
	}
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		return fmt.Errorf("%w: source image returned status %d", errInvalidSuggestionValue, result.StatusCode)
	}
	ext, ok := sourceImageExtension(result.Body, result.ContentType)
	if !ok {
		return fmt.Errorf("%w: source image must be JPEG, PNG, GIF, or WebP", errInvalidSuggestionValue)
	}
	scrubbed, err := imaging.StripMetadata(result.Body, 95)
	if err != nil {
		return fmt.Errorf("%w: source image could not be decoded", errInvalidSuggestionValue)
	}

	if ext != "png" {
		ext = "jpg"
	}

	var imageID string
	if err := tx.QueryRowContext(ctx, "SELECT lower(hex(randomblob(16)))").Scan(&imageID); err != nil {
		return err
	}
	key, err := storage.ProductImageKey(productID, imageID, ext)
	if err != nil {
		return err
	}
	localStore, err := storage.NewLocal(cfg.DataDir)
	if err != nil {
		return err
	}
	if err := localStore.WriteFileAtomic(key, scrubbed, 0o644); err != nil {
		return err
	}

	var existingPrimary int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM product_images WHERE product_id = ? AND is_primary = 1", productID).Scan(&existingPrimary); err != nil {
		if p, perr := localStore.Path(key); perr == nil {
			_ = os.Remove(p)
		}
		return err
	}
	isPrimary := kind.Primary && existingPrimary == 0
	caption := sourceImageEvidence(s.Source, kind)
	_, err = tx.ExecContext(ctx,
		`INSERT INTO product_images (id, product_id, image_path, type, caption, is_primary, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		imageID, productID, key, kind.ImageType, caption, isPrimary,
	)
	if err != nil {
		if p, perr := localStore.Path(key); perr == nil {
			_ = os.Remove(p)
		}
		return err
	}
	return nil
}

func sourceImageExtension(raw []byte, contentType string) (string, bool) {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if contentType == "" {
		contentType = strings.ToLower(http.DetectContentType(raw))
	}
	switch contentType {
	case "image/png":
		return "png", true
	case "image/jpeg", "image/jpg", "image/gif", "image/webp":
		return "jpg", true
	default:
		detected := strings.ToLower(http.DetectContentType(raw))
		switch detected {
		case "image/png":
			return "png", true
		case "image/jpeg", "image/jpg", "image/gif", "image/webp":
			return "jpg", true
		default:
			return "", false
		}
	}
}

func applyNutritionSuggestion(ctx context.Context, tx *sql.Tx, productID string, s productEnrichmentSuggestionResponse) error {
	column, kind, ok := nutritionFieldColumn(s.Field)
	if !ok {
		return fmt.Errorf("%w: unsupported field", errInvalidSuggestionValue)
	}

	var value interface{}
	switch kind {
	case "float":
		parsed, err := strconv.ParseFloat(strings.TrimSpace(s.Value), 64)
		if err != nil {
			return fmt.Errorf("%w: %s must be numeric", errInvalidSuggestionValue, s.Field)
		}
		value = parsed
	case "allergens":
		raw := splitAllergens(s.Value)
		data, _ := json.Marshal(raw)
		value = string(data)
	default:
		value = strings.TrimSpace(s.Value)
	}

	linkID := interface{}(nil)
	if s.LinkID != nil {
		linkID = *s.LinkID
	}
	confidence := interface{}(nil)
	if s.Confidence != nil {
		confidence = *s.Confidence
	}
	conflictTarget := "ON CONFLICT(product_id, product_link_id)"
	if linkID == nil {
		conflictTarget = "ON CONFLICT(product_id) WHERE product_link_id IS NULL"
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO product_nutrition (product_id, product_link_id, accepted_by_user, source_confidence, `+column+`, created_at, updated_at)
		 VALUES (?, ?, 1, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		 `+conflictTarget+` DO UPDATE SET
		    accepted_by_user = 1,
		    source_confidence = COALESCE(excluded.source_confidence, product_nutrition.source_confidence),
		    `+column+` = excluded.`+column+`,
		    updated_at = CURRENT_TIMESTAMP`,
		productID, linkID, confidence, value,
	)
	return err
}

func nutritionFieldColumn(field string) (column, kind string, ok bool) {
	// Trust boundary for applyNutritionSuggestion's SQL column interpolation:
	// only values from this hard-coded map may become column names.
	fields := map[string][2]string{
		"serving_quantity":       {"serving_quantity", "float"},
		"serving_unit":           {"serving_unit", "text"},
		"serving_label":          {"serving_label", "text"},
		"servings_per_container": {"servings_per_container", "float"},
		"calories":               {"calories", "float"},
		"total_fat_g":            {"total_fat_g", "float"},
		"saturated_fat_g":        {"saturated_fat_g", "float"},
		"trans_fat_g":            {"trans_fat_g", "float"},
		"cholesterol_mg":         {"cholesterol_mg", "float"},
		"sodium_mg":              {"sodium_mg", "float"},
		"total_carbohydrate_g":   {"total_carbohydrate_g", "float"},
		"dietary_fiber_g":        {"dietary_fiber_g", "float"},
		"total_sugars_g":         {"total_sugars_g", "float"},
		"added_sugars_g":         {"added_sugars_g", "float"},
		"protein_g":              {"protein_g", "float"},
		"ingredients":            {"ingredients", "text"},
		"allergens":              {"allergens_json", "allergens"},
	}
	value, ok := fields[field]
	if !ok {
		return "", "", false
	}
	return value[0], value[1], true
}

func suggestionsForURL(rawURL, visibleText string) []enrichment.Suggestion {
	if adapters.Matches(rawURL) {
		return adapters.Parse(rawURL, visibleText)
	}
	return nil
}

func suggestionsFromOpenFoodFacts(upc, sourceURL string, body []byte) []enrichment.Suggestion {
	var payload struct {
		Status  int `json:"status"`
		Product struct {
			ProductName      string             `json:"product_name"`
			Brands           string             `json:"brands"`
			Code             string             `json:"code"`
			Quantity         string             `json:"quantity"`
			IngredientsText  string             `json:"ingredients_text"`
			Allergens        string             `json:"allergens"`
			Nutriments       map[string]float64 `json:"nutriments"`
			ServingSize      string             `json:"serving_size"`
			ImageFrontURL    string             `json:"image_front_url"`
			ImageNutrition   string             `json:"image_nutrition_url"`
			ImageIngredients string             `json:"image_ingredients_url"`
		} `json:"product"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.Status != 1 {
		return nil
	}
	var out []enrichment.Suggestion
	add := func(field, value, evidence string, confidence float64) {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, enrichment.NewSuggestion("openfoodfacts", sourceURL, field, value, evidence, confidence))
		}
	}
	add("upc", upc, "Open Food Facts barcode match", 0.86)
	add("name", payload.Product.ProductName, payload.Product.ProductName, 0.72)
	if brand := strings.Split(payload.Product.Brands, ",")[0]; strings.TrimSpace(brand) != "" {
		add("brand", brand, payload.Product.Brands, 0.68)
	}
	if qty, unit := parseQuantity(payload.Product.Quantity); qty != "" && unit != "" {
		add("pack_quantity", qty, payload.Product.Quantity, 0.68)
		add("pack_unit", unit, payload.Product.Quantity, 0.68)
	}
	add("serving_label", payload.Product.ServingSize, payload.Product.ServingSize, 0.66)
	add("ingredients", payload.Product.IngredientsText, "Ingredients: "+payload.Product.IngredientsText, 0.68)
	add("allergens", strings.TrimPrefix(payload.Product.Allergens, "en:"), payload.Product.Allergens, 0.62)

	nutriments := payload.Product.Nutriments
	nutrientMap := map[string]string{
		"energy-kcal_serving":   "calories",
		"fat_serving":           "total_fat_g",
		"saturated-fat_serving": "saturated_fat_g",
		"trans-fat_serving":     "trans_fat_g",
		"cholesterol_serving":   "cholesterol_mg",
		"sodium_serving":        "sodium_mg",
		"carbohydrates_serving": "total_carbohydrate_g",
		"fiber_serving":         "dietary_fiber_g",
		"sugars_serving":        "total_sugars_g",
		"proteins_serving":      "protein_g",
	}
	for key, field := range nutrientMap {
		if value, ok := nutriments[key]; ok && value > 0 {
			add(field, strconv.FormatFloat(value, 'f', -1, 64), key, 0.65)
		}
	}
	add("image_front_url", payload.Product.ImageFrontURL, "Open Food Facts front photo", 0.68)
	add("image_nutrition_url", payload.Product.ImageNutrition, "Open Food Facts nutrition photo", 0.68)
	add("image_ingredients_url", payload.Product.ImageIngredients, "Open Food Facts ingredients photo", 0.68)
	return out
}

func (h *ProductHandler) fetchUSDASuggestions(ctx context.Context, productID, upc, apiKey string, allowPrivate bool) (productLinkResponse, []enrichment.Suggestion, error) {
	apiURL, _ := url.Parse(usdaSearchAPIBase)
	query := apiURL.Query()
	query.Set("api_key", apiKey)
	query.Set("query", upc)
	query.Set("pageSize", "3")
	apiURL.RawQuery = query.Encode()

	sourceURL := "https://fdc.nal.usda.gov/fdc-app.html#/food-search?query=" + url.QueryEscape(upc)
	client := httpsafe.NewSafeHTTPClient(8*time.Second, 512*1024, allowPrivate)
	result, err := client.Fetch(ctx, apiURL.String())
	fetchedAt := time.Now().UTC()
	status := 0
	contentHash := ""
	var lastError *string
	var suggestions []enrichment.Suggestion
	if err != nil {
		msg := productFetchError(err)
		lastError = &msg
	} else {
		fetchedAt = result.FetchedAt
		status = result.StatusCode
		contentHash = hashContent(string(result.Body))
		sourceURL, suggestions = suggestionsFromUSDA(upc, sourceURL, result.Body)
	}

	confidence := sourceConfidenceForSuggestions(suggestions)
	link, linkErr := h.upsertProductLink(ctx, productID, "usda_fdc", &upc, sourceURL, stringPtr("USDA FoodData Central"), fetchedAt, status, contentHash, lastError, confidence)
	if linkErr != nil {
		return productLinkResponse{}, nil, linkErr
	}
	if err != nil {
		return link, nil, err
	}
	return link, suggestions, nil
}

func suggestionsFromUSDA(upc, defaultSourceURL string, body []byte) (string, []enrichment.Suggestion) {
	var payload struct {
		Foods []struct {
			FdcID         int    `json:"fdcId"`
			Description   string `json:"description"`
			BrandOwner    string `json:"brandOwner"`
			GtinUPC       string `json:"gtinUpc"`
			Ingredients   string `json:"ingredients"`
			FoodNutrients []struct {
				NutrientName string  `json:"nutrientName"`
				UnitName     string  `json:"unitName"`
				Value        float64 `json:"value"`
			} `json:"foodNutrients"`
		} `json:"foods"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return defaultSourceURL, nil
	}
	for _, food := range payload.Foods {
		if food.GtinUPC != "" && normalizeUSDAUPC(food.GtinUPC) != upc {
			continue
		}
		sourceURL := defaultSourceURL
		if food.FdcID > 0 {
			sourceURL = strings.TrimRight(usdaFoodDetailsBase, "/") + "/" + strconv.Itoa(food.FdcID) + "/nutrients"
		}
		var out []enrichment.Suggestion
		add := func(field, value, evidence string, confidence float64) {
			value = strings.TrimSpace(value)
			if value != "" {
				out = append(out, enrichment.NewSuggestion("usda_fdc", sourceURL, field, value, evidence, confidence))
			}
		}
		add("upc", upc, "USDA FoodData Central barcode match", 0.78)
		add("name", titleDescription(food.Description), food.Description, 0.62)
		add("brand", food.BrandOwner, food.BrandOwner, 0.62)
		add("ingredients", food.Ingredients, "Ingredients: "+food.Ingredients, 0.62)
		for _, n := range food.FoodNutrients {
			if field := usdaNutrientField(n.NutrientName, n.UnitName); field != "" && n.Value > 0 {
				add(field, strconv.FormatFloat(n.Value, 'f', -1, 64), n.NutrientName, 0.6)
			}
		}
		return sourceURL, out
	}
	return defaultSourceURL, nil
}

func usdaNutrientField(name, unit string) string {
	name = strings.ToLower(name)
	unit = strings.ToLower(unit)
	switch {
	case strings.Contains(name, "energy") && strings.Contains(unit, "kcal"):
		return "calories"
	case strings.Contains(name, "total lipid"):
		return "total_fat_g"
	case strings.Contains(name, "saturated"):
		return "saturated_fat_g"
	case strings.Contains(name, "trans"):
		return "trans_fat_g"
	case strings.Contains(name, "cholesterol"):
		return "cholesterol_mg"
	case strings.Contains(name, "sodium"):
		return "sodium_mg"
	case strings.Contains(name, "carbohydrate"):
		return "total_carbohydrate_g"
	case strings.Contains(name, "fiber"):
		return "dietary_fiber_g"
	case strings.Contains(name, "sugars"):
		return "total_sugars_g"
	case strings.Contains(name, "protein"):
		return "protein_g"
	default:
		return ""
	}
}

func normalizeUSDAUPC(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func titleDescription(value string) string {
	parts := strings.Fields(strings.ToLower(value))
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

var quantityRE = regexp.MustCompile(`(?i)\b([0-9]+(?:[\.,][0-9]+)?)\s*(fl\s*oz|oz|lb|g|kg|ml|l|ct|count|each|ea)\b`)

func parseQuantity(value string) (string, string) {
	m := quantityRE.FindStringSubmatch(value)
	if len(m) != 3 {
		return "", ""
	}
	return strings.ReplaceAll(m[1], ",", "."), canonicalProductUnit(m[2])
}

func canonicalProductUnit(value string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(value), " "))
	switch normalized {
	case "fl oz":
		return "fl_oz"
	case "ct", "count", "ea":
		return "each"
	}
	return normalized
}

func classifyProductURL(rawURL string) (string, *string, *string) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "url", nil, nil
	}
	host := strings.ToLower(u.Hostname())
	if host == "kroger.com" || strings.HasSuffix(host, ".kroger.com") {
		var externalID *string
		for _, part := range strings.Split(strings.Trim(u.Path, "/"), "/") {
			if len(part) >= 8 && isDigits(part) {
				value := part
				externalID = &value
			}
		}
		return "kroger", externalID, stringPtr("Kroger product page")
	}
	if host != "" {
		hash := hashContent(rawURL)
		return "url", &hash, stringPtr(host)
	}
	return "url", nil, nil
}

func sourceConfidenceForSuggestions(suggestions []enrichment.Suggestion) *float64 {
	if len(suggestions) == 0 {
		return nil
	}
	var total float64
	for _, s := range suggestions {
		total += s.Confidence
	}
	avg := total / float64(len(suggestions))
	return &avg
}

func statusForFetchError(err error) int {
	if errors.Is(err, httpsafe.ErrPrivateAddressBlocked) || errors.Is(err, httpsafe.ErrInvalidScheme) || errors.Is(err, httpsafe.ErrInvalidURL) {
		return http.StatusBadRequest
	}
	return http.StatusBadGateway
}

func productFetchError(err error) string {
	switch {
	case errors.Is(err, httpsafe.ErrPrivateAddressBlocked):
		return "private/loopback address not allowed"
	case errors.Is(err, httpsafe.ErrInvalidScheme):
		return "url scheme must be http or https"
	case errors.Is(err, httpsafe.ErrInvalidURL):
		return "url is not a valid URL"
	case errors.Is(err, httpsafe.ErrResponseTooLarge):
		return "response exceeded byte limit"
	default:
		return "failed to fetch URL"
	}
}

func hashContent(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func truncateEvidence(value string, max int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= max {
		return value
	}
	return value[:max]
}

func suggestionAlreadyCurrent(s productEnrichmentSuggestionResponse) bool {
	if s.CurrentValue == nil {
		return false
	}
	current := strings.TrimSpace(*s.CurrentValue)
	value := strings.TrimSpace(s.Value)
	if current == "" && value == "" {
		return true
	}
	switch s.Field {
	case "brand":
		return matcher.NormalizeBrand(current) == matcher.NormalizeBrand(value)
	case "upc":
		currentUPC, currentErr := normalizeUPCValue(current)
		valueUPC, valueErr := normalizeUPCValue(value)
		return currentErr == nil && valueErr == nil && currentUPC == valueUPC
	case "pack_quantity":
		currentQty, currentErr := strconv.ParseFloat(current, 64)
		valueQty, valueErr := strconv.ParseFloat(value, 64)
		return currentErr == nil && valueErr == nil && currentQty == valueQty
	case "pack_unit":
		return canonicalProductUnit(current) == canonicalProductUnit(value)
	default:
		return strings.EqualFold(current, value)
	}
}

func (h *ProductHandler) suggestionConflict(ctx context.Context, householdID string, s productEnrichmentSuggestionResponse, err error) productEnrichmentConflictResponse {
	resp := productEnrichmentConflictResponse{
		SuggestionID:   s.ID,
		Field:          s.Field,
		Code:           "value_conflict",
		Message:        "accepted value conflicts with an existing product",
		SuggestedMerge: false,
	}
	if s.Field == "upc" || errors.Is(err, identifiers.ErrIdentifierConflict) {
		resp.Code = "identifier_conflict"
		resp.Message = "UPC already belongs to another product"
	}

	var conflict *identifiers.IdentifierConflictError
	if errors.As(err, &conflict) && conflict != nil {
		resp.ExistingProductID = conflict.ExistingProductID
	}
	if resp.ExistingProductID == "" && s.Field == "upc" {
		if normalized, normErr := normalizeUPCValue(s.Value); normErr == nil && normalized != "" {
			_ = h.DB.QueryRowContext(ctx,
				`SELECT id
				   FROM products
				  WHERE household_id = ?
				    AND id != ?
				    AND upc = ?
				  LIMIT 1`,
				householdID, s.ProductID, normalized,
			).Scan(&resp.ExistingProductID)
		}
	}
	if resp.ExistingProductID != "" {
		resp.SuggestedMerge = true
		_ = h.DB.QueryRowContext(ctx,
			"SELECT name FROM products WHERE id = ? AND household_id = ?",
			resp.ExistingProductID, householdID,
		).Scan(&resp.ExistingProductName)
	}
	return resp
}

func fieldSelected(fields []string, field string) bool {
	for _, item := range fields {
		if item == field {
			return true
		}
	}
	return false
}

func nullableString(value string) interface{} {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func nullableFloat(value float64) interface{} {
	if value == 0 {
		return nil
	}
	return value
}

func nullableTime(value time.Time) interface{} {
	if value.IsZero() {
		return nil
	}
	return value
}

func nullableInt(value int) interface{} {
	if value == 0 {
		return nil
	}
	return value
}

func sqlNullStringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func nullFloatPtr(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	return &value.Float64
}

func stringPtr(value string) *string {
	return &value
}

func ptrStringValueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func isDigits(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != ""
}

func splitAllergens(value string) []string {
	parts := regexp.MustCompile(`[;,]`).Split(value, -1)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(strings.TrimPrefix(part, "en:"))
		if part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 && strings.TrimSpace(value) != "" {
		out = append(out, strings.TrimSpace(value))
	}
	return out
}
