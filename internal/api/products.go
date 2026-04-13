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
)

// ProductHandler holds dependencies for product-related endpoints.
type ProductHandler struct {
	DB  *sql.DB
	Cfg *config.Config
}

// --- Request types ---

type createProductRequest struct {
	Name        string  `json:"name"`
	Category    *string `json:"category,omitempty"`
	DefaultUnit *string `json:"default_unit,omitempty"`
	Notes       *string `json:"notes,omitempty"`
}

type updateProductRequest struct {
	Name        string  `json:"name"`
	Category    *string `json:"category,omitempty"`
	DefaultUnit *string `json:"default_unit,omitempty"`
	Notes       *string `json:"notes,omitempty"`
}

// --- Response types ---

type productResponse struct {
	ID              string     `json:"id"`
	HouseholdID     string     `json:"household_id"`
	Name            string     `json:"name"`
	Category        *string    `json:"category,omitempty"`
	DefaultUnit     *string    `json:"default_unit,omitempty"`
	Notes           *string    `json:"notes,omitempty"`
	LastPurchasedAt *time.Time `json:"last_purchased_at,omitempty"`
	PurchaseCount   int        `json:"purchase_count"`
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

// RegisterRoutes mounts product endpoints onto the protected group.
func (h *ProductHandler) RegisterRoutes(protected *echo.Group) {
	products := protected.Group("/products")
	products.GET("", h.List)
	products.POST("", h.Create)
	products.PUT("/:id", h.Update)
	products.DELETE("/:id", h.Delete)
	products.POST("/:id/images", h.UploadImage)
	products.DELETE("/:id/images/:imageId", h.DeleteImage)
	products.GET("/:id/links", h.ListLinks)
}

// List returns products for the household. If query param `q` is provided, uses FTS5 search.
// GET /api/v1/products
func (h *ProductHandler) List(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	q := strings.TrimSpace(c.QueryParam("q"))

	var rows *sql.Rows
	var err error

	if q != "" {
		// FTS5 search — scope to household via JOIN.
		rows, err = h.DB.Query(
			`SELECT p.id, p.household_id, p.name, p.category, p.default_unit, p.notes,
			        p.last_purchased_at, p.purchase_count, p.created_at, p.updated_at
			 FROM products p
			 JOIN products_fts f ON p.rowid = f.rowid
			 WHERE products_fts MATCH ? AND p.household_id = ?
			 ORDER BY rank`,
			q, householdID,
		)
	} else {
		rows, err = h.DB.Query(
			`SELECT id, household_id, name, category, default_unit, notes,
			        last_purchased_at, purchase_count, created_at, updated_at
			 FROM products WHERE household_id = ? ORDER BY name`,
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
			&p.LastPurchasedAt, &p.PurchaseCount, &p.CreatedAt, &p.UpdatedAt); err != nil {
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

	now := time.Now().UTC()
	var id string
	err := h.DB.QueryRow(
		`INSERT INTO products (id, household_id, name, category, default_unit, notes, created_at, updated_at)
		 VALUES (lower(hex(randomblob(16))), ?, ?, ?, ?, ?, ?, ?)
		 RETURNING id`,
		householdID, req.Name, req.Category, req.DefaultUnit, req.Notes, now, now,
	).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return c.JSON(http.StatusConflict, map[string]string{"error": "product name already exists"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	var p productResponse
	err = h.DB.QueryRow(
		`SELECT id, household_id, name, category, default_unit, notes,
		        last_purchased_at, purchase_count, created_at, updated_at
		 FROM products WHERE id = ?`, id,
	).Scan(&p.ID, &p.HouseholdID, &p.Name, &p.Category, &p.DefaultUnit, &p.Notes,
		&p.LastPurchasedAt, &p.PurchaseCount, &p.CreatedAt, &p.UpdatedAt)
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

	now := time.Now().UTC()
	result, err := h.DB.Exec(
		`UPDATE products SET name = ?, category = ?, default_unit = ?, notes = ?, updated_at = ?
		 WHERE id = ? AND household_id = ?`,
		req.Name, req.Category, req.DefaultUnit, req.Notes, now, productID, householdID,
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

	var p productResponse
	err = h.DB.QueryRow(
		`SELECT id, household_id, name, category, default_unit, notes,
		        last_purchased_at, purchase_count, created_at, updated_at
		 FROM products WHERE id = ?`, productID,
	).Scan(&p.ID, &p.HouseholdID, &p.Name, &p.Category, &p.DefaultUnit, &p.Notes,
		&p.LastPurchasedAt, &p.PurchaseCount, &p.CreatedAt, &p.UpdatedAt)
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

	result, err := h.DB.Exec(
		"DELETE FROM products WHERE id = ? AND household_id = ?",
		productID, householdID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "product not found"})
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
