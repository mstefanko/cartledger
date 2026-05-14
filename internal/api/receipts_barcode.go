package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
	enrichmentrunner "github.com/mstefanko/cartledger/internal/enrichment/runner"
	"github.com/mstefanko/cartledger/internal/identifiers"
	"github.com/mstefanko/cartledger/internal/matcher"
	"github.com/mstefanko/cartledger/internal/prices"
)

type lineItemBarcodeRequest struct {
	UPC           string `json:"upc"`
	CreateProduct bool   `json:"create_product"`
}

type barcodeProductResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type lineItemBarcodePreviewResponse struct {
	UPC                  string                             `json:"upc"`
	MatchedProduct       *barcodeProductResponse            `json:"matched_product,omitempty"`
	CreateProductAllowed bool                               `json:"create_product_allowed"`
	LookupAvailable      bool                               `json:"lookup_available"`
	LookupSkippedReason  *string                            `json:"lookup_skipped_reason,omitempty"`
	Conflict             *productEnrichmentConflictResponse `json:"conflict,omitempty"`
}

type lineItemBarcodeApplyResponse struct {
	LineItemID          string                `json:"line_item_id"`
	UPC                 string                `json:"upc"`
	ProductID           string                `json:"product_id"`
	ProductName         string                `json:"product_name"`
	CreatedProduct      bool                  `json:"created_product"`
	Job                 *enrichmentrunner.Job `json:"job,omitempty"`
	LookupSkippedReason *string               `json:"lookup_skipped_reason,omitempty"`
}

type barcodeLineItem struct {
	ReceiptStatus      string
	StoreID            *string
	LineID             string
	RawName            string
	ReceiptDescription *string
	ProductID          *string
	ProductName        *string
	UPC                *string
	Matched            string
	ReviewStatus       string
	SuggestedName      *string
	SuggestedCategory  *string
	SuggestedProductID *string
}

func (h *ReceiptHandler) PreviewLineItemBarcode(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	receiptID := c.Param("id")
	itemID := c.Param("itemId")

	var req lineItemBarcodeRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	normalizedUPC, err := normalizeUPCValue(req.UPC)
	if err != nil || normalizedUPC == "" {
		if err == nil {
			err = errors.New("upc is required")
		}
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	line, err := h.loadBarcodeLineItem(c.Request().Context(), householdID, receiptID, itemID)
	if errors.Is(err, sql.ErrNoRows) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "line item not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	product, err := h.findProductByGTIN(c.Request().Context(), householdID, normalizedUPC)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	skipReason := h.barcodeLookupSkippedReason(c.Request().Context(), householdID)
	resp := lineItemBarcodePreviewResponse{
		UPC:                  normalizedUPC,
		CreateProductAllowed: barcodeLineItemEligible(line),
		LookupAvailable:      skipReason == nil,
		LookupSkippedReason:  skipReason,
	}
	if err == nil {
		resp.MatchedProduct = &product
		resp.CreateProductAllowed = false
		if req.CreateProduct {
			resp.Conflict = h.barcodeConflict(product)
		}
	}
	return c.JSON(http.StatusOK, resp)
}

func (h *ReceiptHandler) ApplyLineItemBarcode(c echo.Context) error {
	ctx := c.Request().Context()
	householdID := auth.HouseholdIDFrom(c)
	receiptID := c.Param("id")
	itemID := c.Param("itemId")

	var req lineItemBarcodeRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	obs, itemUPC, err := gtinObservation(req.UPC, string(enrichmentrunner.TriggerReceiptReviewScan), floatPtr(1.0))
	if err != nil || itemUPC == nil || strings.TrimSpace(*itemUPC) == "" {
		if err == nil {
			err = errors.New("upc is required")
		}
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	normalizedUPC := *itemUPC

	line, err := h.loadBarcodeLineItem(ctx, householdID, receiptID, itemID)
	if errors.Is(err, sql.ErrNoRows) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "line item not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	localProduct, findErr := h.findProductByGTIN(ctx, householdID, normalizedUPC)
	if findErr != nil && !errors.Is(findErr, sql.ErrNoRows) {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if line.ProductID != nil && findErr == nil && *line.ProductID == localProduct.ID {
		resp, err := h.applyBarcodeMatch(ctx, householdID, receiptID, line, obs, normalizedUPC, localProduct)
		if err != nil {
			return h.barcodeApplyError(c, householdID, normalizedUPC, err)
		}
		return c.JSON(http.StatusOK, resp)
	}
	if line.ProductID != nil && strings.TrimSpace(*line.ProductID) != "" {
		return c.JSON(http.StatusConflict, map[string]string{"error": "line item already matched", "code": "line_item_already_matched"})
	}
	if !barcodeLineItemEligible(line) {
		return c.JSON(http.StatusConflict, map[string]string{"error": "line item is not eligible for barcode apply", "code": "line_item_not_eligible"})
	}

	if findErr == nil {
		resp, err := h.applyBarcodeMatch(ctx, householdID, receiptID, line, obs, normalizedUPC, localProduct)
		if err != nil {
			return h.barcodeApplyError(c, householdID, normalizedUPC, err)
		}
		return c.JSON(http.StatusOK, resp)
	}
	if !req.CreateProduct {
		return c.JSON(http.StatusConflict, map[string]string{"error": "no local product found for UPC", "code": "product_not_found"})
	}

	resp, err := h.applyBarcodeCreate(ctx, householdID, receiptID, line, obs, normalizedUPC)
	if err != nil {
		return h.barcodeApplyError(c, householdID, normalizedUPC, err)
	}
	return c.JSON(http.StatusCreated, resp)
}

func (h *ReceiptHandler) applyBarcodeMatch(ctx context.Context, householdID, receiptID string, line barcodeLineItem, obs *identifiers.Observation, normalizedUPC string, product barcodeProductResponse) (lineItemBarcodeApplyResponse, error) {
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return lineItemBarcodeApplyResponse{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx,
		`UPDATE line_items
		    SET product_id = ?,
		        upc = ?,
		        matched = 'identifier',
		        confidence = 1.0,
		        review_status = CASE WHEN product_id = ? THEN review_status ELSE 'pending' END
		  WHERE id = ? AND receipt_id = ?`,
		product.ID, normalizedUPC, product.ID, line.LineID, receiptID,
	); err != nil {
		return lineItemBarcodeApplyResponse{}, err
	}
	if err := insertLineIdentifierObservation(ctx, tx, line.LineID, obs); err != nil {
		return lineItemBarcodeApplyResponse{}, err
	}
	if err := identifiers.UpsertProductIdentifier(ctx, tx, identifiers.ProductIdentifier{
		HouseholdID:       householdID,
		ProductID:         product.ID,
		Kind:              identifiers.KindGTIN,
		Value:             normalizedUPC,
		NormalizedValue:   normalizedUPC,
		Source:            string(enrichmentrunner.TriggerReceiptReviewScan),
		Confidence:        floatPtr(1.0),
		SetPrimaryProduct: true,
	}); err != nil {
		return lineItemBarcodeApplyResponse{}, err
	}
	if err := matcher.UpsertAlias(ctx, tx, matcher.AliasUpsert{
		HouseholdID: householdID,
		ProductID:   product.ID,
		Alias:       line.RawName,
		StoreID:     line.StoreID,
		Source:      matcher.AliasSourceReceiptReviewScan,
		Confidence:  floatPtr(1.0),
		AcceptedAt:  &now,
		CreatedAt:   now,
	}); err != nil && !errors.Is(err, matcher.ErrAliasConflict) {
		return lineItemBarcodeApplyResponse{}, err
	}
	if err := prices.RecordProductPriceFromLineItem(ctx, tx, line.LineID); err != nil {
		return lineItemBarcodeApplyResponse{}, err
	}
	if err := tx.Commit(); err != nil {
		return lineItemBarcodeApplyResponse{}, err
	}
	return lineItemBarcodeApplyResponse{
		LineItemID:  line.LineID,
		UPC:         normalizedUPC,
		ProductID:   product.ID,
		ProductName: product.Name,
	}, nil
}

func (h *ReceiptHandler) applyBarcodeCreate(ctx context.Context, householdID, receiptID string, line barcodeLineItem, obs *identifiers.Observation, normalizedUPC string) (lineItemBarcodeApplyResponse, error) {
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return lineItemBarcodeApplyResponse{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	productName := strings.TrimSpace(ptrStringValue(line.SuggestedName))
	if productName == "" {
		productName = strings.TrimSpace(line.RawName)
	}
	productID := uuid.New().String()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO products (id, household_id, name, category, upc, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		productID, householdID, productName, line.SuggestedCategory, normalizedUPC, now, now,
	)
	if err != nil {
		return lineItemBarcodeApplyResponse{}, err
	}
	if err := identifiers.UpsertProductIdentifier(ctx, tx, identifiers.ProductIdentifier{
		HouseholdID:       householdID,
		ProductID:         productID,
		Kind:              identifiers.KindGTIN,
		Value:             normalizedUPC,
		NormalizedValue:   normalizedUPC,
		Source:            string(enrichmentrunner.TriggerReceiptReviewScan),
		Confidence:        floatPtr(1.0),
		SetPrimaryProduct: true,
	}); err != nil {
		return lineItemBarcodeApplyResponse{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE line_items
		    SET product_id = ?, upc = ?, matched = 'identifier', confidence = 1.0, review_status = 'pending'
		  WHERE id = ? AND receipt_id = ?`,
		productID, normalizedUPC, line.LineID, receiptID,
	); err != nil {
		return lineItemBarcodeApplyResponse{}, err
	}
	if err := insertLineIdentifierObservation(ctx, tx, line.LineID, obs); err != nil {
		return lineItemBarcodeApplyResponse{}, err
	}
	if err := matcher.UpsertAlias(ctx, tx, matcher.AliasUpsert{
		HouseholdID: householdID,
		ProductID:   productID,
		Alias:       line.RawName,
		StoreID:     line.StoreID,
		Source:      matcher.AliasSourceReceiptReviewScan,
		Confidence:  floatPtr(1.0),
		AcceptedAt:  &now,
		CreatedAt:   now,
	}); err != nil && !errors.Is(err, matcher.ErrAliasConflict) {
		return lineItemBarcodeApplyResponse{}, err
	}
	if err := prices.RecordProductPriceFromLineItem(ctx, tx, line.LineID); err != nil {
		return lineItemBarcodeApplyResponse{}, err
	}
	if err := tx.Commit(); err != nil {
		return lineItemBarcodeApplyResponse{}, err
	}

	resp := lineItemBarcodeApplyResponse{
		LineItemID:     line.LineID,
		UPC:            normalizedUPC,
		ProductID:      productID,
		ProductName:    productName,
		CreatedProduct: true,
	}
	skipReason := h.barcodeLookupSkippedReason(ctx, householdID)
	if skipReason != nil {
		resp.LookupSkippedReason = skipReason
		return resp, nil
	}
	if h.Enrichment != nil {
		result, err := h.Enrichment.QueueForProduct(ctx, enrichmentrunner.QueueForProductRequest{
			HouseholdID: householdID,
			ProductID:   productID,
			ReceiptID:   receiptID,
			Trigger:     enrichmentrunner.TriggerReceiptReviewScan,
			UPC:         normalizedUPC,
		})
		if err != nil {
			reason := "queue_failed"
			resp.LookupSkippedReason = &reason
			return resp, nil
		}
		if result.SkippedReason != "" {
			resp.LookupSkippedReason = &result.SkippedReason
		} else {
			resp.Job = &result.Job
		}
	}
	return resp, nil
}

func (h *ReceiptHandler) loadBarcodeLineItem(ctx context.Context, householdID, receiptID, itemID string) (barcodeLineItem, error) {
	var line barcodeLineItem
	err := h.DB.QueryRowContext(ctx,
		`SELECT r.status, r.store_id, li.id, li.raw_name, li.receipt_description,
		        li.product_id, p.name, li.upc, li.matched, li.review_status,
		        li.suggested_name, li.suggested_category, li.suggested_product_id
		   FROM line_items li
		   JOIN receipts r ON r.id = li.receipt_id
		   LEFT JOIN products p ON p.id = li.product_id
		  WHERE r.household_id = ?
		    AND r.id = ?
		    AND li.id = ?`,
		householdID, receiptID, itemID,
	).Scan(
		&line.ReceiptStatus, &line.StoreID, &line.LineID, &line.RawName, &line.ReceiptDescription,
		&line.ProductID, &line.ProductName, &line.UPC, &line.Matched, &line.ReviewStatus,
		&line.SuggestedName, &line.SuggestedCategory, &line.SuggestedProductID,
	)
	return line, err
}

func barcodeLineItemEligible(line barcodeLineItem) bool {
	if line.ReceiptStatus != "matched" {
		return false
	}
	if line.ReviewStatus == "accepted" {
		return false
	}
	if line.ProductID == nil {
		return true
	}
	return false
}

func (h *ReceiptHandler) findProductByGTIN(ctx context.Context, householdID, normalizedUPC string) (barcodeProductResponse, error) {
	var p barcodeProductResponse
	err := h.DB.QueryRowContext(ctx,
		`WITH matches AS (
		     SELECT p.id, p.name, 0 AS priority
		       FROM product_identifiers pi
		       JOIN products p ON p.id = pi.product_id
		      WHERE pi.household_id = ?
		        AND pi.kind = 'gtin'
		        AND pi.authority = ''
		        AND pi.normalized_value = ?
		     UNION ALL
		     SELECT p.id, p.name, 1 AS priority
		       FROM products p
		      WHERE p.household_id = ?
		        AND p.upc = ?
		 )
		 SELECT id, name
		   FROM matches
		  ORDER BY priority, name
		  LIMIT 1`,
		householdID, normalizedUPC, householdID, normalizedUPC,
	).Scan(&p.ID, &p.Name)
	return p, err
}

func (h *ReceiptHandler) barcodeLookupSkippedReason(ctx context.Context, householdID string) *string {
	if h.Enrichment == nil {
		reason := "no_provider_configured"
		return &reason
	}
	settingsHandler := ProductEnrichmentSettingsHandler{DB: h.DB, Cfg: h.Cfg}
	settings, err := settingsHandler.load(ctx, householdID)
	if err != nil {
		reason := "no_provider_configured"
		return &reason
	}
	if !settings.GlobalEnabled {
		reason := "env_disabled"
		return &reason
	}
	if !settings.ManualLookupEnabled {
		reason := "household_manual_lookup_disabled"
		return &reason
	}
	for _, availability := range settings.ProviderAvailability {
		if availability.Enabled {
			return nil
		}
	}
	reason := "no_provider_configured"
	return &reason
}

func (h *ReceiptHandler) barcodeConflict(product barcodeProductResponse) *productEnrichmentConflictResponse {
	conflict := &productEnrichmentConflictResponse{
		Field:               "upc",
		Code:                "identifier_conflict",
		Message:             "UPC already belongs to another product",
		ExistingProductID:   product.ID,
		ExistingProductName: product.Name,
		SuggestedMerge:      true,
	}
	return conflict
}

func (h *ReceiptHandler) barcodeApplyError(c echo.Context, householdID, normalizedUPC string, err error) error {
	var conflict *identifiers.IdentifierConflictError
	if errors.As(err, &conflict) || errors.Is(err, identifiers.ErrIdentifierConflict) {
		product := barcodeProductResponse{}
		if conflict != nil {
			product.ID = conflict.ExistingProductID
		}
		if product.ID != "" {
			_ = h.DB.QueryRowContext(c.Request().Context(),
				"SELECT name FROM products WHERE id = ? AND household_id = ?",
				product.ID, householdID,
			).Scan(&product.Name)
		} else if p, findErr := h.findProductByGTIN(c.Request().Context(), householdID, normalizedUPC); findErr == nil {
			product = p
		}
		return c.JSON(http.StatusConflict, h.barcodeConflict(product))
	}
	return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
}
