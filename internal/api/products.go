package api

import (
	"database/sql"
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
	"github.com/mstefanko/cartledger/internal/matcher"
)

// ProductHandler holds dependencies for product-related endpoints.
type ProductHandler struct {
	DB  *sql.DB
	Cfg *config.Config
}

// --- Request types ---

type createProductRequest struct {
	Name         string   `json:"name"`
	Category     *string  `json:"category,omitempty"`
	DefaultUnit  *string  `json:"default_unit,omitempty"`
	Notes        *string  `json:"notes,omitempty"`
	Brand        *string  `json:"brand,omitempty"`
	PackQuantity *float64 `json:"pack_quantity,omitempty"`
	PackUnit     *string  `json:"pack_unit,omitempty"`
}

type updateProductRequest struct {
	Name           string   `json:"name"`
	Category       *string  `json:"category,omitempty"`
	DefaultUnit    *string  `json:"default_unit,omitempty"`
	Notes          *string  `json:"notes,omitempty"`
	Brand          *string  `json:"brand,omitempty"`
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
	PackQuantity    *float64   `json:"pack_quantity,omitempty"`
	PackUnit        *string    `json:"pack_unit,omitempty"`
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
	ID         string    `json:"id"`
	ProductID  string    `json:"product_id"`
	Source     string    `json:"source"`
	ExternalID *string   `json:"external_id,omitempty"`
	URL        string    `json:"url"`
	Label      *string   `json:"label,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// fetchProduct loads a single product with computed alias_count and last_price.
func (h *ProductHandler) fetchProduct(id string) (productResponse, error) {
	var p productResponse
	err := h.DB.QueryRow(
		`SELECT p.id, p.household_id, p.name, p.category, p.default_unit, p.notes,
		        p.brand, p.pack_quantity, p.pack_unit,
		        p.last_purchased_at, p.purchase_count,
		        (SELECT COUNT(*) FROM product_aliases WHERE product_id = p.id) as alias_count,
		        (SELECT PRINTF('%.2f', pp.unit_price) FROM product_prices pp WHERE pp.product_id = p.id ORDER BY pp.receipt_date DESC, pp.created_at DESC, pp.id DESC LIMIT 1) as last_price,
		        p.created_at, p.updated_at
		 FROM products p WHERE p.id = ?`, id,
	).Scan(&p.ID, &p.HouseholdID, &p.Name, &p.Category, &p.DefaultUnit, &p.Notes,
		&p.Brand, &p.PackQuantity, &p.PackUnit,
		&p.LastPurchasedAt, &p.PurchaseCount, &p.AliasCount, &p.LastPrice, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

// RegisterRoutes mounts product endpoints onto the protected group.
func (h *ProductHandler) RegisterRoutes(protected *echo.Group) {
	products := protected.Group("/products")
	products.POST("/merge", h.Merge) // Must be before /:id to avoid "merge" matching as an ID.
	products.GET("", h.List)
	products.POST("", h.Create)
	products.PUT("/:id", h.Update)
	products.DELETE("/:id", h.Delete)
	products.POST("/:id/images", h.UploadImage)
	products.DELETE("/:id/images/:imageId", h.DeleteImage)
	products.GET("/:id/links", h.ListLinks)
	products.GET("/:id/detail", h.Detail)
}

// List returns products for the household. If query param `q` is provided, uses FTS5 search.
// GET /api/v1/products
func (h *ProductHandler) List(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	q := strings.TrimSpace(c.QueryParam("q"))
	brandFilter := strings.TrimSpace(c.QueryParam("brand"))

	var rows *sql.Rows
	var err error

	if q != "" {
		// Sanitize search input: wrap in double quotes to treat as literal phrase,
		// escaping any embedded double quotes. This prevents FTS5 operator injection.
		sanitizedQ := `"` + strings.ReplaceAll(q, `"`, `""`) + `"`

		if brandFilter != "" {
			rows, err = h.DB.Query(
				`SELECT p.id, p.household_id, p.name, p.category, p.default_unit, p.notes,
				        p.brand, p.pack_quantity, p.pack_unit,
				        p.last_purchased_at, p.purchase_count,
				        (SELECT COUNT(*) FROM product_aliases WHERE product_id = p.id) as alias_count,
				        (SELECT PRINTF('%.2f', pp.unit_price) FROM product_prices pp WHERE pp.product_id = p.id ORDER BY pp.receipt_date DESC, pp.created_at DESC, pp.id DESC LIMIT 1) as last_price,
				        p.created_at, p.updated_at
				 FROM products p
				 JOIN products_fts f ON p.rowid = f.rowid
				 WHERE products_fts MATCH ? AND p.household_id = ? AND LOWER(p.brand) = LOWER(?)
				 ORDER BY rank`,
				sanitizedQ, householdID, brandFilter,
			)
		} else {
			rows, err = h.DB.Query(
				`SELECT p.id, p.household_id, p.name, p.category, p.default_unit, p.notes,
				        p.brand, p.pack_quantity, p.pack_unit,
				        p.last_purchased_at, p.purchase_count,
				        (SELECT COUNT(*) FROM product_aliases WHERE product_id = p.id) as alias_count,
				        (SELECT PRINTF('%.2f', pp.unit_price) FROM product_prices pp WHERE pp.product_id = p.id ORDER BY pp.receipt_date DESC, pp.created_at DESC, pp.id DESC LIMIT 1) as last_price,
				        p.created_at, p.updated_at
				 FROM products p
				 JOIN products_fts f ON p.rowid = f.rowid
				 WHERE products_fts MATCH ? AND p.household_id = ?
				 ORDER BY rank`,
				sanitizedQ, householdID,
			)
		}
	} else if brandFilter != "" {
		rows, err = h.DB.Query(
			`SELECT p.id, p.household_id, p.name, p.category, p.default_unit, p.notes,
			        p.brand, p.pack_quantity, p.pack_unit,
			        p.last_purchased_at, p.purchase_count,
			        (SELECT COUNT(*) FROM product_aliases WHERE product_id = p.id) as alias_count,
			        (SELECT PRINTF('%.2f', pp.unit_price) FROM product_prices pp WHERE pp.product_id = p.id ORDER BY pp.receipt_date DESC, pp.created_at DESC, pp.id DESC LIMIT 1) as last_price,
			        p.created_at, p.updated_at
			 FROM products p WHERE p.household_id = ? AND LOWER(p.brand) = LOWER(?) ORDER BY p.name`,
			householdID, brandFilter,
		)
	} else {
		rows, err = h.DB.Query(
			`SELECT p.id, p.household_id, p.name, p.category, p.default_unit, p.notes,
			        p.brand, p.pack_quantity, p.pack_unit,
			        p.last_purchased_at, p.purchase_count,
			        (SELECT COUNT(*) FROM product_aliases WHERE product_id = p.id) as alias_count,
			        (SELECT PRINTF('%.2f', pp.unit_price) FROM product_prices pp WHERE pp.product_id = p.id ORDER BY pp.receipt_date DESC, pp.created_at DESC, pp.id DESC LIMIT 1) as last_price,
			        p.created_at, p.updated_at
			 FROM products p WHERE p.household_id = ? ORDER BY p.name`,
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
		if err := rows.Scan(&p.ID, &p.HouseholdID, &p.Name, &p.Category, &p.DefaultUnit, &p.Notes,
			&p.Brand, &p.PackQuantity, &p.PackUnit,
			&p.LastPurchasedAt, &p.PurchaseCount, &p.AliasCount, &p.LastPrice, &p.CreatedAt, &p.UpdatedAt); err != nil {
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

	now := time.Now().UTC()
	var id string
	err := h.DB.QueryRow(
		`INSERT INTO products (id, household_id, name, category, default_unit, notes, brand, pack_quantity, pack_unit, created_at, updated_at)
		 VALUES (lower(hex(randomblob(16))), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 RETURNING id`,
		householdID, req.Name, req.Category, req.DefaultUnit, req.Notes, req.Brand, req.PackQuantity, req.PackUnit, now, now,
	).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return c.JSON(http.StatusConflict, map[string]string{"error": "product name already exists"})
		}
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
	result, err := h.DB.Exec(
		`UPDATE products SET name = ?, category = ?, default_unit = ?, notes = ?, brand = ?, pack_quantity = ?, pack_unit = ?, product_group_id = ?, updated_at = ?
		 WHERE id = ? AND household_id = ?`,
		req.Name, req.Category, req.DefaultUnit, req.Notes, req.Brand, req.PackQuantity, req.PackUnit, req.ProductGroupID, now, productID, householdID,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return c.JSON(http.StatusConflict, map[string]string{"error": "product name already exists"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "product not found"})
	}

	p, err := h.fetchProduct(productID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusOK, p)
}

// Delete removes a product. CASCADE handles aliases, images, and links.
// DELETE /api/v1/products/:id
func (h *ProductHandler) Delete(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	productID := c.Param("id")

	// Verify the product exists and belongs to this household BEFORE touching files.
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

	// Clean up image files before deleting the DB row.
	rows, err := h.DB.Query("SELECT image_path FROM product_images WHERE product_id = ?", productID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var imagePath string
			if rows.Scan(&imagePath) == nil {
				fullPath := filepath.Join(h.Cfg.DataDir, imagePath)
				os.Remove(fullPath)
			}
		}
	}
	// Also remove the product image directory if it exists.
	productDir := filepath.Join(h.Cfg.DataDir, "products", productID)
	os.RemoveAll(productDir)

	_, err = h.DB.Exec(
		"DELETE FROM products WHERE id = ? AND household_id = ?",
		productID, householdID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.NoContent(http.StatusNoContent)
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

	// Generate image ID and create directory.
	var imageID string
	err = h.DB.QueryRow("SELECT lower(hex(randomblob(16)))").Scan(&imageID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	productDir := filepath.Join(h.Cfg.DataDir, "products", productID)
	if err := os.MkdirAll(productDir, 0o755); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create image directory"})
	}

	filename := fmt.Sprintf("%s.%s", imageID, ext)
	filePath := filepath.Join(productDir, filename)
	relativePath := filepath.Join("products", productID, filename)

	dst, err := os.Create(filePath)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to save image"})
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		os.Remove(filePath)
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
		imageID, productID, relativePath, imageType, captionPtr, isPrimary, now,
	)
	if err != nil {
		os.Remove(filePath)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusCreated, productImageResponse{
		ID:        imageID,
		ProductID: productID,
		ImagePath: relativePath,
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

	// Delete the file.
	fullPath := filepath.Join(h.Cfg.DataDir, imagePath)
	os.Remove(fullPath)

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
		`SELECT id, product_id, source, external_id, url, label, created_at
		 FROM product_links WHERE product_id = ? ORDER BY created_at`,
		productID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer rows.Close()

	links := make([]productLinkResponse, 0)
	for rows.Next() {
		var l productLinkResponse
		if err := rows.Scan(&l.ID, &l.ProductID, &l.Source, &l.ExternalID, &l.URL, &l.Label, &l.CreatedAt); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		links = append(links, l)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusOK, links)
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
	ID      string  `json:"id"`
	Alias   string  `json:"alias"`
	StoreID *string `json:"store_id,omitempty"`
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
	Product      productResponse        `json:"product"`
	PricePerUnit *string                `json:"price_per_unit,omitempty"`
	Group        *productGroupInfo      `json:"group,omitempty"`
	Siblings     []productSibling       `json:"siblings,omitempty"`
	Aliases      []productAliasResponse `json:"aliases"`
	Images       []productImageResponse `json:"images"`
	Links        []productLinkResponse  `json:"links"`
	PriceHistory []priceHistoryEntry    `json:"price_history"`
	StoreCompare []storeComparison      `json:"store_comparison"`
	Stats        purchaseStats          `json:"stats"`
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
		Product:      p,
		Aliases:      make([]productAliasResponse, 0),
		Images:       make([]productImageResponse, 0),
		Links:        make([]productLinkResponse, 0),
		PriceHistory: make([]priceHistoryEntry, 0),
		StoreCompare: make([]storeComparison, 0),
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
		"SELECT id, alias, store_id FROM product_aliases WHERE product_id = ? ORDER BY alias",
		productID,
	)
	if err == nil {
		defer aliasRows.Close()
		for aliasRows.Next() {
			var a productAliasResponse
			if aliasRows.Scan(&a.ID, &a.Alias, &a.StoreID) == nil {
				resp.Aliases = append(resp.Aliases, a)
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
		"SELECT id, product_id, source, external_id, url, label, created_at FROM product_links WHERE product_id = ? ORDER BY created_at",
		productID,
	)
	if err == nil {
		defer linkRows.Close()
		for linkRows.Next() {
			var l productLinkResponse
			if linkRows.Scan(&l.ID, &l.ProductID, &l.Source, &l.ExternalID, &l.URL, &l.Label, &l.CreatedAt) == nil {
				resp.Links = append(resp.Links, l)
			}
		}
	}

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
