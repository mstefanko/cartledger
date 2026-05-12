package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/identifiers"
	"github.com/mstefanko/cartledger/internal/imaging"
	"github.com/mstefanko/cartledger/internal/matcher"
	"github.com/mstefanko/cartledger/internal/prices"
	"github.com/mstefanko/cartledger/internal/search"
	"github.com/mstefanko/cartledger/internal/storage"
	"github.com/mstefanko/cartledger/internal/ws"
)

// ProductHandler holds dependencies for product-related endpoints.
type ProductHandler struct {
	DB  *sql.DB
	Cfg *config.Config
	Hub *ws.Hub
}

// --- Request types ---

type createProductRequest struct {
	Name         string   `json:"name"`
	Category     *string  `json:"category,omitempty"`
	DefaultUnit  *string  `json:"default_unit,omitempty"`
	Notes        *string  `json:"notes,omitempty"`
	Brand        *string  `json:"brand,omitempty"`
	UPC          *string  `json:"upc,omitempty"`
	PackQuantity *float64 `json:"pack_quantity,omitempty"`
	PackUnit     *string  `json:"pack_unit,omitempty"`
}

type updateProductRequest struct {
	Name           string   `json:"name"`
	Category       *string  `json:"category,omitempty"`
	DefaultUnit    *string  `json:"default_unit,omitempty"`
	Notes          *string  `json:"notes,omitempty"`
	Brand          *string  `json:"brand,omitempty"`
	UPC            *string  `json:"upc,omitempty"`
	PackQuantity   *float64 `json:"pack_quantity,omitempty"`
	PackUnit       *string  `json:"pack_unit,omitempty"`
	ProductGroupID *string  `json:"product_group_id,omitempty"`
}

// --- Response types ---

type productResponse struct {
	ID              string     `json:"id"`
	HouseholdID     string     `json:"household_id"`
	Name            string     `json:"name"`
	Category        *string    `json:"category,omitempty"`
	DefaultUnit     *string    `json:"default_unit,omitempty"`
	Notes           *string    `json:"notes,omitempty"`
	Brand           *string    `json:"brand,omitempty"`
	UPC             *string    `json:"upc,omitempty"`
	PackQuantity    *float64   `json:"pack_quantity,omitempty"`
	PackUnit        *string    `json:"pack_unit,omitempty"`
	ProductGroupID  *string    `json:"product_group_id,omitempty"`
	LastPurchasedAt *time.Time `json:"last_purchased_at,omitempty"`
	PurchaseCount   int        `json:"purchase_count"`
	AliasCount      int        `json:"alias_count"`
	LastPrice       *string    `json:"last_price,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type productImageResponse struct {
	ID        string    `json:"id"`
	ProductID string    `json:"product_id"`
	ImagePath string    `json:"image_path"`
	Type      string    `json:"type"`
	Caption   *string   `json:"caption,omitempty"`
	IsPrimary bool      `json:"is_primary"`
	CreatedAt time.Time `json:"created_at"`
}

type productLinkResponse struct {
	ID               string     `json:"id"`
	ProductID        string     `json:"product_id"`
	Source           string     `json:"source"`
	ExternalID       *string    `json:"external_id,omitempty"`
	URL              string     `json:"url"`
	Label            *string    `json:"label,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	FetchedAt        *time.Time `json:"fetched_at,omitempty"`
	HTTPStatus       *int       `json:"http_status,omitempty"`
	ContentHash      *string    `json:"content_hash,omitempty"`
	LastError        *string    `json:"last_error,omitempty"`
	SourceConfidence *float64   `json:"source_confidence,omitempty"`
}

// fetchProduct loads a single product with computed alias_count and last_price.
func (h *ProductHandler) fetchProduct(id string) (productResponse, error) {
	var p productResponse
	err := h.DB.QueryRow(
		`SELECT p.id, p.household_id, p.name, p.category, p.default_unit, p.notes,
		        p.brand, p.upc, p.pack_quantity, p.pack_unit, p.product_group_id,
		        p.last_purchased_at, p.purchase_count,
		        (SELECT COUNT(*) FROM product_aliases WHERE product_id = p.id) as alias_count,
		        (SELECT PRINTF('%.2f', pp.unit_price) FROM product_prices pp WHERE pp.product_id = p.id ORDER BY pp.receipt_date DESC, pp.created_at DESC, pp.id DESC LIMIT 1) as last_price,
		        p.created_at, p.updated_at
		 FROM products p WHERE p.id = ?`, id,
	).Scan(&p.ID, &p.HouseholdID, &p.Name, &p.Category, &p.DefaultUnit, &p.Notes,
		&p.Brand, &p.UPC, &p.PackQuantity, &p.PackUnit, &p.ProductGroupID,
		&p.LastPurchasedAt, &p.PurchaseCount, &p.AliasCount, &p.LastPrice, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

// RegisterRoutes mounts product endpoints onto the protected group.
func (h *ProductHandler) RegisterRoutes(protected *echo.Group) {
	products := protected.Group("/products")
	products.POST("/merge", h.Merge)                              // Must be before /:id to avoid "merge" matching as an ID.
	products.POST("/bulk-group", h.BulkGroup)                     // Must be before /:id.
	products.GET("/duplicate-candidates", h.DuplicateCandidates)  // Must be before /:id.
	products.POST("/not-duplicate-pairs", h.MarkNotDuplicate)     // Must be before /:id.
	products.DELETE("/not-duplicate-pairs", h.UnmarkNotDuplicate) // Must be before /:id.
	products.GET("", h.List)
	products.POST("", h.Create)
	products.PUT("/:id", h.Update)
	products.DELETE("/:id", h.Delete)
	products.GET("/:id/recompute-prices/preview", h.RecomputePricesPreview)
	products.POST("/:id/recompute-prices", h.RecomputePrices)
	products.POST("/:id/images", h.UploadImage)
	products.DELETE("/:id/images/:imageId", h.DeleteImage)
	products.GET("/:id/links", h.ListLinks)
	products.POST("/:id/links", h.AddLink)
	products.POST("/:id/enrich/upc", h.EnrichByUPC)
	products.POST("/:id/enrichment-suggestions/bulk-accept", h.BulkAcceptEnrichmentSuggestions)
	products.POST("/:id/enrichment-suggestions/bulk-reject", h.BulkRejectEnrichmentSuggestions)
	products.POST("/:id/enrichment-suggestions/:suggestionId/accept", h.AcceptEnrichmentSuggestion)
	products.POST("/:id/enrichment-suggestions/:suggestionId/reject", h.RejectEnrichmentSuggestion)
	products.GET("/:id/detail", h.Detail)
	products.GET("/:id/usage", h.GetProductUsage)
}

// productListColumns is the projection shared by every List branch.
// Kept as a const so query variants stay byte-identical and reviewers can
// diff the WHERE/ORDER BY clauses without re-reading a 14-column tuple.
const productListColumns = `p.id, p.household_id, p.name, p.category, p.default_unit, p.notes,
	        p.brand, p.upc, p.pack_quantity, p.pack_unit, p.product_group_id,
	        p.last_purchased_at, p.purchase_count,
	        (SELECT COUNT(*) FROM product_aliases WHERE product_id = p.id) as alias_count,
	        (SELECT PRINTF('%.2f', pp.unit_price) FROM product_prices pp WHERE pp.product_id = p.id ORDER BY pp.receipt_date DESC, pp.created_at DESC, pp.id DESC LIMIT 1) as last_price,
	        p.created_at, p.updated_at`

// scanProductRow reads one row into a productResponse using the
// productListColumns projection. Extracted so every List branch uses the
// same Scan signature — a divergence here would be silent and awful to
// debug.
func scanProductRow(rows *sql.Rows, p *productResponse) error {
	return rows.Scan(&p.ID, &p.HouseholdID, &p.Name, &p.Category, &p.DefaultUnit, &p.Notes,
		&p.Brand, &p.UPC, &p.PackQuantity, &p.PackUnit, &p.ProductGroupID,
		&p.LastPurchasedAt, &p.PurchaseCount, &p.AliasCount, &p.LastPrice, &p.CreatedAt, &p.UpdatedAt)
}

func floatPtr(v float64) *float64 {
	return &v
}

func productEditFieldsFromRequest(c echo.Context) (map[string]struct{}, error) {
	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return nil, err
	}
	c.Request().Body = io.NopCloser(bytes.NewReader(body))
	fields := make(map[string]struct{})
	if len(strings.TrimSpace(string(body))) == 0 {
		return fields, nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	for jsonField, productField := range map[string]string{
		"name":          "name",
		"brand":         "brand",
		"upc":           "upc",
		"pack_quantity": "pack_quantity",
		"pack_unit":     "pack_unit",
	} {
		if _, ok := raw[jsonField]; ok {
			fields[productField] = struct{}{}
		}
	}
	return fields, nil
}

func fieldsFromSet(fields map[string]struct{}) []string {
	if len(fields) == 0 {
		return nil
	}
	order := []string{"name", "brand", "upc", "pack_quantity", "pack_unit", "price_history"}
	out := make([]string, 0, len(fields))
	for _, field := range order {
		if _, ok := fields[field]; ok {
			out = append(out, field)
		}
	}
	for field := range fields {
		found := false
		for _, known := range order {
			if field == known {
				found = true
				break
			}
		}
		if !found {
			out = append(out, field)
		}
	}
	return out
}

func hasField(fields map[string]struct{}, field string) bool {
	_, ok := fields[field]
	return ok
}

// List returns products for the household.
//
// Query params:
//   - q: fuzzy search term (FTS5 prefix + in-memory rerank via internal/search).
//   - brand: exact brand filter (case-insensitive).
//   - sort=last_purchased_at: when q is empty, order by most-recently-purchased
//     first (NULLs last), falling back to name.
//
// GET /api/v1/products
func (h *ProductHandler) List(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	q := strings.TrimSpace(c.QueryParam("q"))
	brandFilter := strings.TrimSpace(c.QueryParam("brand"))
	sortParam := strings.TrimSpace(c.QueryParam("sort"))
	ctx := c.Request().Context()

	// --- Search branch: delegate ranking to internal/search, then hydrate.
	if q != "" {
		// limit=0 → no cap. The List endpoint historically returned every
		// match and the frontend scrolls; preserve that.
		ids, err := search.ProductIDs(ctx, h.DB, householdID, q, 0)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		if len(ids) == 0 {
			return c.JSON(http.StatusOK, make([]productResponse, 0))
		}

		// Hydrate in one IN(...) query, then re-order by rank from `ids`.
		placeholders := make([]string, len(ids))
		args := make([]interface{}, 0, len(ids)+2)
		args = append(args, householdID)
		for i, id := range ids {
			placeholders[i] = "?"
			args = append(args, id)
		}
		query := `SELECT ` + productListColumns + `
		 FROM products p
		 WHERE p.household_id = ? AND p.id IN (` + strings.Join(placeholders, ",") + `)`
		if brandFilter != "" {
			query += ` AND LOWER(p.brand) = LOWER(?)`
			args = append(args, brandFilter)
		}

		rows, err := h.DB.QueryContext(ctx, query, args...)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		defer rows.Close()

		// Bucket by id so we can emit in the rank order returned by search.ProductIDs.
		byID := make(map[string]productResponse, len(ids))
		for rows.Next() {
			var p productResponse
			if err := scanProductRow(rows, &p); err != nil {
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
			}
			byID[p.ID] = p
		}
		if err := rows.Err(); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}

		products := make([]productResponse, 0, len(ids))
		for _, id := range ids {
			if p, ok := byID[id]; ok {
				products = append(products, p)
			}
		}
		return c.JSON(http.StatusOK, products)
	}

	// --- Non-search branches.
	var rows *sql.Rows
	var err error

	// Pick ORDER BY based on sort param. Only `last_purchased_at` is recognised;
	// anything else (including unset) falls back to name.
	orderBy := "p.name"
	if sortParam == "last_purchased_at" {
		// SQLite treats NULL as smaller than any value; `DESC NULLS LAST` puts
		// never-purchased products at the bottom.
		orderBy = "p.last_purchased_at DESC NULLS LAST, p.name"
	}

	if brandFilter != "" {
		rows, err = h.DB.QueryContext(ctx,
			`SELECT `+productListColumns+`
			 FROM products p WHERE p.household_id = ? AND LOWER(p.brand) = LOWER(?) ORDER BY `+orderBy,
			householdID, brandFilter,
		)
	} else {
		rows, err = h.DB.QueryContext(ctx,
			`SELECT `+productListColumns+`
			 FROM products p WHERE p.household_id = ? ORDER BY `+orderBy,
			householdID,
		)
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer rows.Close()

	products := make([]productResponse, 0)
	for rows.Next() {
		var p productResponse
		if err := scanProductRow(rows, &p); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		products = append(products, p)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusOK, products)
}

// Create adds a new product for the household.
// POST /api/v1/products
func (h *ProductHandler) Create(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)

	var req createProductRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "name is required"})
	}

	// Normalize brand if provided.
	if req.Brand != nil {
		normalized := matcher.NormalizeBrand(*req.Brand)
		req.Brand = &normalized
	}
	upc, err := normalizeUPCPointer(req.UPC)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	now := time.Now().UTC()
	var id string
	tx, err := h.DB.BeginTx(c.Request().Context(), nil)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer tx.Rollback()

	err = tx.QueryRowContext(c.Request().Context(),
		`INSERT INTO products (id, household_id, name, name_normalized, category, default_unit, notes, brand, upc, pack_quantity, pack_unit, created_at, updated_at)
		 VALUES (lower(hex(randomblob(16))), ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?)
		 RETURNING id`,
		householdID, req.Name, matcher.NormalizeProductName(req.Name), req.Category, req.DefaultUnit, req.Notes, req.Brand, req.PackQuantity, req.PackUnit, now, now,
	).Scan(&id)
	if err != nil {
		if isUniqueConstraintError(err) {
			return c.JSON(http.StatusConflict, map[string]string{"error": "product name already exists"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if upc != nil {
		if _, err := identifiers.SetProductPrimaryGTIN(c.Request().Context(), tx, householdID, id, *upc, "manual", floatPtr(1)); err != nil {
			if errors.Is(err, identifiers.ErrIdentifierConflict) || isUniqueConstraintError(err) {
				return c.JSON(http.StatusConflict, map[string]string{"error": "upc already belongs to another product"})
			}
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
	}
	if err := tx.Commit(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	p, err := h.fetchProduct(id)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusCreated, p)
}

// Update modifies an existing product.
// PUT /api/v1/products/:id
func (h *ProductHandler) Update(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	productID := c.Param("id")

	var req updateProductRequest
	touchedFields, err := productEditFieldsFromRequest(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	req.Name = strings.TrimSpace(req.Name)

	// Support partial updates: if name is empty, merge with existing product data.
	if req.Name == "" {
		existing, err := h.fetchProduct(productID)
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "product not found"})
		}
		req.Name = existing.Name
		if req.Category == nil {
			req.Category = existing.Category
		}
		if req.DefaultUnit == nil {
			req.DefaultUnit = existing.DefaultUnit
		}
		if req.Notes == nil {
			req.Notes = existing.Notes
		}
		if req.Brand == nil {
			req.Brand = existing.Brand
		}
		if req.UPC == nil {
			req.UPC = existing.UPC
		}
		if req.PackQuantity == nil {
			req.PackQuantity = existing.PackQuantity
		}
		if req.PackUnit == nil {
			req.PackUnit = existing.PackUnit
		}
		if req.ProductGroupID == nil {
			req.ProductGroupID = existing.ProductGroupID
		}
	}

	// Normalize brand if provided.
	if req.Brand != nil {
		normalized := matcher.NormalizeBrand(*req.Brand)
		req.Brand = &normalized
	}
	upc, err := normalizeUPCPointer(req.UPC)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	// Validate product_group_id belongs to this household if set.
	if req.ProductGroupID != nil && *req.ProductGroupID != "" {
		var groupHouseholdID string
		err := h.DB.QueryRow("SELECT household_id FROM product_groups WHERE id = ?", *req.ProductGroupID).Scan(&groupHouseholdID)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "product group not found"})
		}
		if groupHouseholdID != householdID {
			return c.JSON(http.StatusForbidden, map[string]string{"error": "product group belongs to another household"})
		}
	}

	now := time.Now().UTC()
	tx, err := h.DB.BeginTx(c.Request().Context(), nil)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(c.Request().Context(),
		`UPDATE products SET name = ?, name_normalized = ?, category = ?, default_unit = ?, notes = ?, brand = ?, pack_quantity = ?, pack_unit = ?, product_group_id = ?, updated_at = ?
		 WHERE id = ? AND household_id = ?`,
		req.Name, matcher.NormalizeProductName(req.Name), req.Category, req.DefaultUnit, req.Notes, req.Brand, req.PackQuantity, req.PackUnit, req.ProductGroupID, now, productID, householdID,
	)
	if err != nil {
		if isUniqueConstraintError(err) {
			return c.JSON(http.StatusConflict, map[string]string{"error": "product name already exists"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "product not found"})
	}
	if upc == nil {
		if _, err := identifiers.SetProductPrimaryGTIN(c.Request().Context(), tx, householdID, productID, "", "manual", nil); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
	} else {
		if _, err := identifiers.SetProductPrimaryGTIN(c.Request().Context(), tx, householdID, productID, *upc, "manual", floatPtr(1)); err != nil {
			if errors.Is(err, identifiers.ErrIdentifierConflict) || isUniqueConstraintError(err) {
				return c.JSON(http.StatusConflict, map[string]string{"error": "upc already belongs to another product"})
			}
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
	}
	if err := recordProductFieldEdits(c.Request().Context(), tx, productID, auth.UserIDFrom(c), touchedFields, "manual"); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if err := tx.Commit(); err != nil {
		if isUniqueConstraintError(err) {
			return c.JSON(http.StatusConflict, map[string]string{"error": "upc already belongs to another product"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	p, err := h.fetchProduct(productID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	h.broadcastProductUpdated(householdID, productID, fieldsFromSet(touchedFields))
	return c.JSON(http.StatusOK, p)
}

type recomputePricesPreviewResponse struct {
	AffectedCount int `json:"affected_count"`
}

type recomputePricesResponse struct {
	UpdatedCount int `json:"updated_count"`
}

// RecomputePricesPreview returns the count of linked historical purchases that
// can be recomputed from their source line items.
// GET /api/v1/products/:id/recompute-prices/preview
func (h *ProductHandler) RecomputePricesPreview(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	productID := c.Param("id")

	if err := h.verifyProduct(c.Request().Context(), productID, householdID); err != nil {
		if err == sql.ErrNoRows {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "product not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	lineItemIDs, err := h.linkedLineItemsForProduct(c.Request().Context(), productID, householdID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	return c.JSON(http.StatusOK, recomputePricesPreviewResponse{AffectedCount: len(lineItemIDs)})
}

// RecomputePrices recomputes linked product_prices rows from their line items.
// POST /api/v1/products/:id/recompute-prices
func (h *ProductHandler) RecomputePrices(c echo.Context) error {
	ctx := c.Request().Context()
	householdID := auth.HouseholdIDFrom(c)
	productID := c.Param("id")

	if err := h.verifyProduct(ctx, productID, householdID); err != nil {
		if err == sql.ErrNoRows {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "product not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	updated, err := h.recomputeProductPrices(ctx, productID, householdID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to recompute price history"})
	}
	h.broadcastProductUpdated(householdID, productID, []string{"price_history"})
	return c.JSON(http.StatusOK, recomputePricesResponse{UpdatedCount: updated})
}

func (h *ProductHandler) recomputeProductPrices(ctx context.Context, productID, householdID string) (int, error) {
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	updated, err := h.recomputeProductPricesTx(ctx, tx, productID, householdID)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return updated, nil
}

func (h *ProductHandler) recomputeProductPricesTx(ctx context.Context, tx *sql.Tx, productID, householdID string) (int, error) {
	lineItemIDs, err := h.linkedLineItemsForProduct(ctx, productID, householdID)
	if err != nil {
		return 0, err
	}

	for _, lineItemID := range lineItemIDs {
		if err := prices.RecordProductPriceFromLineItem(ctx, tx, lineItemID); err != nil {
			return 0, err
		}
	}
	return len(lineItemIDs), nil
}

func (h *ProductHandler) verifyProduct(ctx context.Context, productID, householdID string) error {
	var exists int
	return h.DB.QueryRowContext(ctx,
		"SELECT 1 FROM products WHERE id = ? AND household_id = ?",
		productID, householdID,
	).Scan(&exists)
}

func recordProductFieldEdits(ctx context.Context, tx *sql.Tx, productID, userID string, fields map[string]struct{}, source string) error {
	for field := range fields {
		if strings.TrimSpace(field) == "" {
			continue
		}
		var editedBy interface{}
		if strings.TrimSpace(userID) != "" {
			editedBy = userID
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO product_field_edits
			    (product_id, field, edited_by_user_id, edit_source, edited_at)
			 VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
			 ON CONFLICT(product_id, field) DO UPDATE SET
			    edited_by_user_id = excluded.edited_by_user_id,
			    edit_source = excluded.edit_source,
			    edited_at = CURRENT_TIMESTAMP`,
			productID, field, editedBy, source,
		); err != nil {
			return err
		}
	}
	return nil
}

func (h *ProductHandler) broadcastProductUpdated(householdID, productID string, changedFields []string) {
	if h.Hub == nil {
		return
	}
	h.Hub.Broadcast(ws.Message{
		Type:      ws.EventProductUpdated,
		Household: householdID,
		Payload: map[string]interface{}{
			"product_id":     productID,
			"changed_fields": changedFields,
		},
	})
}

func (h *ProductHandler) linkedLineItemsForProduct(ctx context.Context, productID, householdID string) ([]string, error) {
	rows, err := h.DB.QueryContext(ctx,
		`SELECT li.id
		   FROM line_items li
		   JOIN receipts r ON r.id = li.receipt_id
		   JOIN product_prices pp ON pp.line_item_id = li.id
		  WHERE li.product_id = ?
		    AND r.household_id = ?
		    AND r.store_id IS NOT NULL
		  ORDER BY r.receipt_date DESC, li.line_number, li.id`,
		productID, householdID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Delete removes a product and all dependent records in a single transaction.
// Line items that referenced this product are unmatched (product_id set to NULL).
// DELETE /api/v1/products/:id
func (h *ProductHandler) Delete(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	productID := c.Param("id")

	// 1. Verify the product exists and stash its product_group_id.
	var groupID *string
	err := h.DB.QueryRow(
		"SELECT product_group_id FROM products WHERE id = ? AND household_id = ?",
		productID, householdID,
	).Scan(&groupID)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "product not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	tx, err := h.DB.Begin()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer tx.Rollback()

	// 2. Count line_items referencing this product (for response).
	var unmatchedCount int
	if err := tx.QueryRow(
		"SELECT COUNT(*) FROM line_items WHERE product_id = ?", productID,
	).Scan(&unmatchedCount); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// 3. Unmatch line_items that reference this product.
	if _, err := tx.Exec(
		"UPDATE line_items SET product_id = NULL, matched = 'unmatched', confidence = NULL, review_status = 'pending' WHERE product_id = ?",
		productID,
	); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// 4. Clear suggested_product_id references.
	if _, err := tx.Exec(
		"UPDATE line_items SET suggested_product_id = NULL, suggested_name = NULL, suggested_category = NULL WHERE suggested_product_id = ?",
		productID,
	); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// 5. Delete product_prices.
	if _, err := tx.Exec("DELETE FROM product_prices WHERE product_id = ?", productID); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// 6. Delete matching_rules.
	if _, err := tx.Exec("DELETE FROM matching_rules WHERE product_id = ?", productID); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// 7. Delete shopping_list_items.
	if _, err := tx.Exec("DELETE FROM shopping_list_items WHERE product_id = ?", productID); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// 8. Delete unit_conversions.
	if _, err := tx.Exec("DELETE FROM unit_conversions WHERE product_id = ?", productID); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// 9. Delete the product itself (CASCADE handles aliases, images, links).
	if _, err := tx.Exec(
		"DELETE FROM products WHERE id = ? AND household_id = ?",
		productID, householdID,
	); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// 10. If the product belonged to a group and is now the last member, delete the group.
	if groupID != nil {
		if _, err := tx.Exec(
			"DELETE FROM product_groups WHERE id = ? AND NOT EXISTS (SELECT 1 FROM products WHERE product_group_id = ?)",
			*groupID, *groupID,
		); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
	}

	if err := tx.Commit(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// After commit: clean up on-disk image files by typed product id.
	if localStore, err := storage.NewLocal(h.Cfg.DataDir); err == nil {
		_ = localStore.DeleteProduct(productID)
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"deleted":              true,
		"unmatched_line_items": unmatchedCount,
	})
}

// GetProductUsage returns usage counts for a product.
// GET /api/v1/products/:id/usage
func (h *ProductHandler) GetProductUsage(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	productID := c.Param("id")

	// Verify the product exists and belongs to this household.
	var exists int
	err := h.DB.QueryRow(
		"SELECT 1 FROM products WHERE id = ? AND household_id = ?",
		productID, householdID,
	).Scan(&exists)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "product not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	var lineItems, shoppingListItems, matchingRules, aliases, images int

	if err := h.DB.QueryRow("SELECT COUNT(*) FROM line_items WHERE product_id = ?", productID).Scan(&lineItems); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if err := h.DB.QueryRow("SELECT COUNT(*) FROM shopping_list_items WHERE product_id = ?", productID).Scan(&shoppingListItems); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if err := h.DB.QueryRow("SELECT COUNT(*) FROM matching_rules WHERE product_id = ?", productID).Scan(&matchingRules); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if err := h.DB.QueryRow("SELECT COUNT(*) FROM product_aliases WHERE product_id = ?", productID).Scan(&aliases); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if err := h.DB.QueryRow("SELECT COUNT(*) FROM product_images WHERE product_id = ?", productID).Scan(&images); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusOK, map[string]int{
		"line_items":          lineItems,
		"shopping_list_items": shoppingListItems,
		"matching_rules":      matchingRules,
		"aliases":             aliases,
		"images":              images,
	})
}

// UploadImage handles multipart image upload for a product.
// POST /api/v1/products/:id/images
func (h *ProductHandler) UploadImage(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	productID := c.Param("id")

	// Verify the product belongs to this household.
	var exists int
	err := h.DB.QueryRow(
		"SELECT 1 FROM products WHERE id = ? AND household_id = ?",
		productID, householdID,
	).Scan(&exists)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "product not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// Parse multipart form with 10MB limit.
	if err := c.Request().ParseMultipartForm(10 << 20); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "file too large or invalid multipart form (max 10MB)"})
	}

	file, header, err := c.Request().FormFile("image")
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "image file is required"})
	}
	defer file.Close()

	// Validate content type.
	contentType := header.Header.Get("Content-Type")
	var ext string
	switch contentType {
	case "image/jpeg":
		ext = "jpg"
	case "image/png":
		ext = "png"
	default:
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "only JPEG and PNG images are allowed"})
	}

	// Validate file size.
	if header.Size > 10<<20 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "file too large (max 10MB)"})
	}

	raw, err := io.ReadAll(file)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to read image"})
	}
	scrubbed, err := imaging.StripMetadata(raw, 95)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "image could not be decoded"})
	}

	// Generate image ID and create storage key.
	var imageID string
	err = h.DB.QueryRow("SELECT lower(hex(randomblob(16)))").Scan(&imageID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	key, err := storage.ProductImageKey(productID, imageID, ext)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to prepare image storage"})
	}
	localStore, err := storage.NewLocal(h.Cfg.DataDir)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "storage unavailable"})
	}
	if err := localStore.WriteFileAtomic(key, scrubbed, 0o644); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to save image"})
	}

	// Optional form fields.
	imageType := c.FormValue("type")
	if imageType == "" {
		imageType = "photo"
	}
	caption := c.FormValue("caption")
	var captionPtr *string
	if caption != "" {
		captionPtr = &caption
	}
	isPrimary := c.FormValue("is_primary") == "true"

	now := time.Now().UTC()
	_, err = h.DB.Exec(
		`INSERT INTO product_images (id, product_id, image_path, type, caption, is_primary, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		imageID, productID, key, imageType, captionPtr, isPrimary, now,
	)
	if err != nil {
		if p, perr := localStore.Path(key); perr == nil {
			_ = os.Remove(p)
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusCreated, productImageResponse{
		ID:        imageID,
		ProductID: productID,
		ImagePath: key,
		Type:      imageType,
		Caption:   captionPtr,
		IsPrimary: isPrimary,
		CreatedAt: now,
	})
}

// DeleteImage removes a product image (DB row + file on disk).
// DELETE /api/v1/products/:id/images/:imageId
func (h *ProductHandler) DeleteImage(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	productID := c.Param("id")
	imageID := c.Param("imageId")

	// Verify the product belongs to this household.
	var exists int
	err := h.DB.QueryRow(
		"SELECT 1 FROM products WHERE id = ? AND household_id = ?",
		productID, householdID,
	).Scan(&exists)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "product not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// Get the image path before deleting.
	var imagePath string
	err = h.DB.QueryRow(
		"SELECT image_path FROM product_images WHERE id = ? AND product_id = ?",
		imageID, productID,
	).Scan(&imagePath)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "image not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// Delete the DB row.
	_, err = h.DB.Exec("DELETE FROM product_images WHERE id = ? AND product_id = ?", imageID, productID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// Delete the file through the storage boundary; invalid historical paths
	// are ignored after the DB row is gone rather than letting a path string
	// influence filesystem deletion outside DATA_DIR.
	if localStore, err := storage.NewLocal(h.Cfg.DataDir); err == nil {
		if fullPath, err := localStore.Path(filepath.ToSlash(imagePath)); err == nil {
			_ = os.Remove(fullPath)
		}
	}

	return c.NoContent(http.StatusNoContent)
}

// ListLinks returns all product links for a product.
// GET /api/v1/products/:id/links
func (h *ProductHandler) ListLinks(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	productID := c.Param("id")

	// Verify the product belongs to this household.
	var exists int
	err := h.DB.QueryRow(
		"SELECT 1 FROM products WHERE id = ? AND household_id = ?",
		productID, householdID,
	).Scan(&exists)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "product not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	rows, err := h.DB.Query(
		`SELECT id, product_id, source, external_id, url, label, created_at,
		        fetched_at, http_status, content_hash, last_error, source_confidence
		 FROM product_links WHERE product_id = ? ORDER BY created_at`,
		productID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer rows.Close()

	links := make([]productLinkResponse, 0)
	for rows.Next() {
		l, err := scanProductLink(rows)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		links = append(links, l)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusOK, links)
}

// --- Bulk group types ---

type bulkGroupRequest struct {
	ProductIDs     []string `json:"product_ids"`
	ProductGroupID *string  `json:"product_group_id"`
}

// BulkGroup assigns (or clears) product_group_id for many products in one transaction.
// POST /api/v1/products/bulk-group
// Body: { product_ids: [...], product_group_id: "uuid" | null }
// Passing null (or omitting) clears the group assignment for the listed products.
func (h *ProductHandler) BulkGroup(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)

	var req bulkGroupRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	if len(req.ProductIDs) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "product_ids is required"})
	}

	// Normalize empty string → nil (clear).
	if req.ProductGroupID != nil && strings.TrimSpace(*req.ProductGroupID) == "" {
		req.ProductGroupID = nil
	}

	// If a group is specified, validate it belongs to the household.
	if req.ProductGroupID != nil {
		var groupHouseholdID string
		err := h.DB.QueryRow("SELECT household_id FROM product_groups WHERE id = ?", *req.ProductGroupID).Scan(&groupHouseholdID)
		if err == sql.ErrNoRows {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "product group not found"})
		}
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		if groupHouseholdID != householdID {
			return c.JSON(http.StatusForbidden, map[string]string{"error": "product group belongs to another household"})
		}
	}

	tx, err := h.DB.Begin()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	stmt, err := tx.Prepare(
		`UPDATE products SET product_group_id = ?, updated_at = ? WHERE id = ? AND household_id = ?`,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer stmt.Close()

	updated := 0
	for _, id := range req.ProductIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		res, err := stmt.Exec(req.ProductGroupID, now, id, householdID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		n, _ := res.RowsAffected()
		updated += int(n)
	}

	if err := tx.Commit(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusOK, map[string]int{"updated": updated})
}

// --- Merge types ---

type mergeProductRequest struct {
	KeepID  string `json:"keep_id"`
	MergeID string `json:"merge_id"`
}

// Merge combines two products into one. All related records (aliases, line items,
// prices, shopping list items, matching rules, images, links) are moved from the
// merge product to the keep product, purchase stats are aggregated, and the merge
// product is deleted. Everything runs in a single transaction.
// POST /api/v1/products/merge
func (h *ProductHandler) Merge(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)

	var req mergeProductRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	req.KeepID = strings.TrimSpace(req.KeepID)
	req.MergeID = strings.TrimSpace(req.MergeID)
	if req.KeepID == "" || req.MergeID == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "keep_id and merge_id are required"})
	}
	if req.KeepID == req.MergeID {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "keep_id and merge_id must be different"})
	}

	tx, err := h.DB.Begin()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer tx.Rollback()

	// 1. Verify both products exist and belong to the same household.
	var keepHouseholdID, mergeHouseholdID string
	var keepPurchaseCount, mergePurchaseCount int
	var keepLastPurchased, mergeLastPurchased *time.Time

	err = tx.QueryRow(
		"SELECT household_id, purchase_count, last_purchased_at FROM products WHERE id = ?",
		req.KeepID,
	).Scan(&keepHouseholdID, &keepPurchaseCount, &keepLastPurchased)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "keep product not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	err = tx.QueryRow(
		"SELECT household_id, purchase_count, last_purchased_at FROM products WHERE id = ?",
		req.MergeID,
	).Scan(&mergeHouseholdID, &mergePurchaseCount, &mergeLastPurchased)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "merge product not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	if keepHouseholdID != householdID || mergeHouseholdID != householdID {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "products do not belong to your household"})
	}

	// 2. Move aliases from merge → keep.
	if _, err := tx.Exec(
		"UPDATE product_aliases SET product_id = ? WHERE product_id = ?",
		req.KeepID, req.MergeID,
	); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// 3. Move line_items from merge → keep.
	if _, err := tx.Exec(
		"UPDATE line_items SET product_id = ? WHERE product_id = ?",
		req.KeepID, req.MergeID,
	); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// 4. Move product_prices from merge → keep.
	if _, err := tx.Exec(
		"UPDATE product_prices SET product_id = ? WHERE product_id = ?",
		req.KeepID, req.MergeID,
	); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// 5. Update shopping_list_items from merge → keep.
	if _, err := tx.Exec(
		"UPDATE shopping_list_items SET product_id = ? WHERE product_id = ?",
		req.KeepID, req.MergeID,
	); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// 6. Update matching_rules from merge → keep.
	if _, err := tx.Exec(
		"UPDATE matching_rules SET product_id = ? WHERE product_id = ?",
		req.KeepID, req.MergeID,
	); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// 7. Move product_images from merge → keep.
	if _, err := tx.Exec(
		"UPDATE product_images SET product_id = ? WHERE product_id = ?",
		req.KeepID, req.MergeID,
	); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// 8. Move product_links from merge → keep.
	if _, err := tx.Exec(
		"UPDATE product_links SET product_id = ? WHERE product_id = ?",
		req.KeepID, req.MergeID,
	); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// 9. Aggregate purchase stats: add counts, use later last_purchased_at.
	newPurchaseCount := keepPurchaseCount + mergePurchaseCount
	newLastPurchased := keepLastPurchased
	if mergeLastPurchased != nil {
		if newLastPurchased == nil || mergeLastPurchased.After(*newLastPurchased) {
			newLastPurchased = mergeLastPurchased
		}
	}

	now := time.Now().UTC()
	if _, err := tx.Exec(
		"UPDATE products SET purchase_count = ?, last_purchased_at = ?, updated_at = ? WHERE id = ?",
		newPurchaseCount, newLastPurchased, now, req.KeepID,
	); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// 10. Delete the merge product (CASCADE handles any remaining FKs).
	if _, err := tx.Exec("DELETE FROM products WHERE id = ?", req.MergeID); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	if err := tx.Commit(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// Return the kept product.
	p, err := h.fetchProduct(req.KeepID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	h.broadcastProductUpdated(householdID, req.KeepID, []string{"merge"})
	return c.JSON(http.StatusOK, p)
}

// --- Product Detail types ---

type priceHistoryEntry struct {
	ID              string  `json:"id"`
	StoreID         string  `json:"store_id"`
	StoreName       string  `json:"store_name"`
	ReceiptID       string  `json:"receipt_id"`
	ReceiptDate     string  `json:"receipt_date"`
	Quantity        string  `json:"quantity"`
	Unit            string  `json:"unit"`
	UnitPrice       string  `json:"unit_price"`
	NormalizedPrice *string `json:"normalized_price,omitempty"`
	NormalizedUnit  *string `json:"normalized_unit,omitempty"`
	RegularPrice    *string `json:"regular_price,omitempty"`
	DiscountAmount  *string `json:"discount_amount,omitempty"`
	IsSale          bool    `json:"is_sale"`
}

type storeComparison struct {
	StoreID         string  `json:"store_id"`
	StoreName       string  `json:"store_name"`
	LatestPrice     string  `json:"latest_price"`
	ReceiptDate     string  `json:"receipt_date"`
	NormalizedPrice *string `json:"normalized_price,omitempty"`
}

type purchaseStats struct {
	TotalPurchases int     `json:"total_purchases"`
	AvgPrice       *string `json:"avg_price,omitempty"`
	MinPrice       *string `json:"min_price,omitempty"`
	MaxPrice       *string `json:"max_price,omitempty"`
	TotalSaved     *string `json:"total_saved,omitempty"`
}

type productAliasResponse struct {
	ID              string     `json:"id"`
	ProductID       string     `json:"product_id,omitempty"`
	Alias           string     `json:"alias"`
	AliasNormalized *string    `json:"alias_normalized,omitempty"`
	StoreID         *string    `json:"store_id,omitempty"`
	Source          string     `json:"source"`
	Confidence      *float64   `json:"confidence,omitempty"`
	AcceptedAt      *time.Time `json:"accepted_at,omitempty"`
	UpdatedAt       *time.Time `json:"updated_at,omitempty"`
}

type productStoreCodeResponse struct {
	ID            string   `json:"id"`
	StoreID       string   `json:"store_id"`
	StoreName     string   `json:"store_name"`
	StoreItemCode string   `json:"store_item_code"`
	Label         *string  `json:"label"`
	Source        string   `json:"source"`
	Confidence    *float64 `json:"confidence"`
	FirstSeenAt   string   `json:"first_seen_at"`
	LastSeenAt    string   `json:"last_seen_at"`
}

type productGroupInfo struct {
	GroupID   string `json:"group_id"`
	GroupName string `json:"group_name"`
}

type productSibling struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Brand        *string  `json:"brand,omitempty"`
	PackQuantity *float64 `json:"pack_quantity,omitempty"`
	PackUnit     *string  `json:"pack_unit,omitempty"`
}

type productDetailResponse struct {
	Product               productResponse                       `json:"product"`
	PricePerUnit          *string                               `json:"price_per_unit,omitempty"`
	Group                 *productGroupInfo                     `json:"group,omitempty"`
	Siblings              []productSibling                      `json:"siblings,omitempty"`
	Aliases               []productAliasResponse                `json:"aliases"`
	StoreCodes            []productStoreCodeResponse            `json:"store_codes"`
	Images                []productImageResponse                `json:"images"`
	Links                 []productLinkResponse                 `json:"links"`
	Nutrition             []productNutritionResponse            `json:"nutrition"`
	EnrichmentSuggestions []productEnrichmentSuggestionResponse `json:"enrichment_suggestions"`
	PriceHistory          []priceHistoryEntry                   `json:"price_history"`
	StoreCompare          []storeComparison                     `json:"store_comparison"`
	Stats                 purchaseStats                         `json:"stats"`
}

// Detail returns comprehensive product information including aliases, images, links,
// price history, per-store comparison, and purchase stats.
// GET /api/v1/products/:id/detail
func (h *ProductHandler) Detail(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	productID := c.Param("id")

	// Verify product belongs to household.
	var exists int
	err := h.DB.QueryRow(
		"SELECT 1 FROM products WHERE id = ? AND household_id = ?",
		productID, householdID,
	).Scan(&exists)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "product not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// Fetch product with computed fields.
	p, err := h.fetchProduct(productID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	resp := productDetailResponse{
		Product:               p,
		Aliases:               make([]productAliasResponse, 0),
		StoreCodes:            make([]productStoreCodeResponse, 0),
		Images:                make([]productImageResponse, 0),
		Links:                 make([]productLinkResponse, 0),
		Nutrition:             make([]productNutritionResponse, 0),
		EnrichmentSuggestions: make([]productEnrichmentSuggestionResponse, 0),
		PriceHistory:          make([]priceHistoryEntry, 0),
		StoreCompare:          make([]storeComparison, 0),
	}

	// Compute price_per_unit when pack_quantity is available.
	if p.PackQuantity != nil && *p.PackQuantity > 0 && p.LastPrice != nil {
		var lastPriceFloat float64
		if _, err := fmt.Sscanf(*p.LastPrice, "%f", &lastPriceFloat); err == nil {
			ppu := lastPriceFloat / *p.PackQuantity
			ppuStr := fmt.Sprintf("%.2f", ppu)
			resp.PricePerUnit = &ppuStr
		}
	}

	// Fetch group info and siblings if product is in a group.
	var groupID *string
	h.DB.QueryRow("SELECT product_group_id FROM products WHERE id = ?", productID).Scan(&groupID)
	if groupID != nil && *groupID != "" {
		var gi productGroupInfo
		err := h.DB.QueryRow(
			"SELECT id, name FROM product_groups WHERE id = ?", *groupID,
		).Scan(&gi.GroupID, &gi.GroupName)
		if err == nil {
			resp.Group = &gi

			// Fetch sibling products (same group, different product).
			sibRows, err := h.DB.Query(
				`SELECT id, name, brand, pack_quantity, pack_unit
				 FROM products WHERE product_group_id = ? AND id != ? AND household_id = ? ORDER BY name`,
				*groupID, productID, householdID,
			)
			if err == nil {
				defer sibRows.Close()
				siblings := make([]productSibling, 0)
				for sibRows.Next() {
					var s productSibling
					if sibRows.Scan(&s.ID, &s.Name, &s.Brand, &s.PackQuantity, &s.PackUnit) == nil {
						siblings = append(siblings, s)
					}
				}
				if len(siblings) > 0 {
					resp.Siblings = siblings
				}
			}
		}
	}

	// Fetch aliases.
	aliasRows, err := h.DB.Query(
		`SELECT id, product_id, alias, alias_normalized, store_id, source, confidence, accepted_at, updated_at
		   FROM product_aliases
		  WHERE product_id = ?
		  ORDER BY alias`,
		productID,
	)
	if err == nil {
		defer aliasRows.Close()
		for aliasRows.Next() {
			var a productAliasResponse
			if aliasRows.Scan(&a.ID, &a.ProductID, &a.Alias, &a.AliasNormalized, &a.StoreID, &a.Source, &a.Confidence, &a.AcceptedAt, &a.UpdatedAt) == nil {
				resp.Aliases = append(resp.Aliases, a)
			}
		}
	}

	codeRows, err := h.DB.Query(
		`SELECT spc.id, spc.store_id, s.name, spc.store_item_code, spc.label,
		        spc.source, spc.confidence, spc.first_seen_at, spc.last_seen_at
		   FROM store_product_codes spc
		   JOIN stores s ON s.id = spc.store_id
		  WHERE spc.product_id = ? AND spc.household_id = ?
		  ORDER BY s.name COLLATE NOCASE, spc.store_item_code`,
		productID, householdID,
	)
	if err == nil {
		defer codeRows.Close()
		for codeRows.Next() {
			var code productStoreCodeResponse
			var firstSeen, lastSeen time.Time
			if codeRows.Scan(
				&code.ID, &code.StoreID, &code.StoreName, &code.StoreItemCode,
				&code.Label, &code.Source, &code.Confidence, &firstSeen, &lastSeen,
			) == nil {
				code.FirstSeenAt = firstSeen.Format(time.RFC3339)
				code.LastSeenAt = lastSeen.Format(time.RFC3339)
				resp.StoreCodes = append(resp.StoreCodes, code)
			}
		}
	}

	// Fetch images.
	imgRows, err := h.DB.Query(
		"SELECT id, product_id, image_path, type, caption, is_primary, created_at FROM product_images WHERE product_id = ? ORDER BY is_primary DESC, created_at",
		productID,
	)
	if err == nil {
		defer imgRows.Close()
		for imgRows.Next() {
			var img productImageResponse
			if imgRows.Scan(&img.ID, &img.ProductID, &img.ImagePath, &img.Type, &img.Caption, &img.IsPrimary, &img.CreatedAt) == nil {
				resp.Images = append(resp.Images, img)
			}
		}
	}

	// Fetch links.
	linkRows, err := h.DB.Query(
		`SELECT id, product_id, source, external_id, url, label, created_at,
		        fetched_at, http_status, content_hash, last_error, source_confidence
		   FROM product_links WHERE product_id = ? ORDER BY created_at`,
		productID,
	)
	if err == nil {
		defer linkRows.Close()
		for linkRows.Next() {
			l, scanErr := scanProductLink(linkRows)
			if scanErr == nil {
				resp.Links = append(resp.Links, l)
			}
		}
	}

	resp.Nutrition = h.fetchProductNutrition(productID)
	resp.EnrichmentSuggestions = h.fetchProductEnrichmentSuggestions(productID, true)

	// Fetch price history with store name.
	priceRows, err := h.DB.Query(
		`SELECT pp.id, pp.store_id, s.name, pp.receipt_id, pp.receipt_date,
		        pp.quantity, pp.unit, pp.unit_price, pp.normalized_price, pp.normalized_unit,
		        pp.regular_price, pp.discount_amount, pp.is_sale
		 FROM product_prices pp
		 JOIN stores s ON pp.store_id = s.id
		 WHERE pp.product_id = ?
		 ORDER BY pp.receipt_date DESC`,
		productID,
	)
	if err == nil {
		defer priceRows.Close()
		for priceRows.Next() {
			var e priceHistoryEntry
			var receiptDate time.Time
			var quantity float64
			var unitPrice float64
			var normalizedPrice *float64
			if priceRows.Scan(&e.ID, &e.StoreID, &e.StoreName, &e.ReceiptID, &receiptDate,
				&quantity, &e.Unit, &unitPrice, &normalizedPrice, &e.NormalizedUnit,
				&e.RegularPrice, &e.DiscountAmount, &e.IsSale) == nil {
				e.ReceiptDate = receiptDate.Format("2006-01-02")
				e.Quantity = fmt.Sprintf("%g", quantity)
				e.UnitPrice = fmt.Sprintf("%.2f", unitPrice)
				if normalizedPrice != nil {
					s := fmt.Sprintf("%.2f", *normalizedPrice)
					e.NormalizedPrice = &s
				}
				resp.PriceHistory = append(resp.PriceHistory, e)
			}
		}
	}

	// Per-store comparison: most recent price per store.
	storeRows, err := h.DB.Query(
		`SELECT pp.store_id, s.name, pp.unit_price, pp.receipt_date, pp.normalized_price
		 FROM product_prices pp
		 JOIN stores s ON pp.store_id = s.id
		 WHERE pp.product_id = ?
		   AND pp.receipt_date = (
		       SELECT MAX(pp2.receipt_date) FROM product_prices pp2
		       WHERE pp2.product_id = pp.product_id AND pp2.store_id = pp.store_id
		   )
		 ORDER BY pp.unit_price ASC`,
		productID,
	)
	if err == nil {
		defer storeRows.Close()
		for storeRows.Next() {
			var sc storeComparison
			var receiptDate time.Time
			var unitPrice float64
			var normalizedPrice *float64
			if storeRows.Scan(&sc.StoreID, &sc.StoreName, &unitPrice, &receiptDate, &normalizedPrice) == nil {
				sc.LatestPrice = fmt.Sprintf("%.2f", unitPrice)
				sc.ReceiptDate = receiptDate.Format("2006-01-02")
				if normalizedPrice != nil {
					s := fmt.Sprintf("%.2f", *normalizedPrice)
					sc.NormalizedPrice = &s
				}
				resp.StoreCompare = append(resp.StoreCompare, sc)
			}
		}
	}

	// Purchase stats.
	var totalPurchases int
	var avgPrice, minPrice, maxPrice *float64
	err = h.DB.QueryRow(
		`SELECT COUNT(*), AVG(unit_price), MIN(unit_price), MAX(unit_price)
		 FROM product_prices WHERE product_id = ?`,
		productID,
	).Scan(&totalPurchases, &avgPrice, &minPrice, &maxPrice)
	if err == nil {
		resp.Stats.TotalPurchases = totalPurchases
		if avgPrice != nil {
			s := fmt.Sprintf("%.2f", *avgPrice)
			resp.Stats.AvgPrice = &s
		}
		if minPrice != nil {
			s := fmt.Sprintf("%.2f", *minPrice)
			resp.Stats.MinPrice = &s
		}
		if maxPrice != nil {
			s := fmt.Sprintf("%.2f", *maxPrice)
			resp.Stats.MaxPrice = &s
		}
	}

	// Total saved from sale items.
	var totalSaved string
	err = h.DB.QueryRow(
		`SELECT COALESCE(SUM(CAST(discount_amount AS REAL)), 0) FROM product_prices WHERE product_id = ? AND is_sale = TRUE`,
		productID,
	).Scan(&totalSaved)
	if err == nil {
		resp.Stats.TotalSaved = &totalSaved
	}

	return c.JSON(http.StatusOK, resp)
}
