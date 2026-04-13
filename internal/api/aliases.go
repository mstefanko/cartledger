package api

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
)

// AliasHandler holds dependencies for product alias endpoints.
type AliasHandler struct {
	DB  *sql.DB
	Cfg *config.Config
}

// --- Request types ---

type createAliasRequest struct {
	Alias   string  `json:"alias"`
	StoreID *string `json:"store_id,omitempty"`
}

// --- Response types ---

type aliasResponse struct {
	ID        string    `json:"id"`
	ProductID string    `json:"product_id"`
	Alias     string    `json:"alias"`
	StoreID   *string   `json:"store_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// RegisterRoutes mounts alias endpoints onto the protected group.
func (h *AliasHandler) RegisterRoutes(protected *echo.Group) {
	aliases := protected.Group("/products/:id/aliases")
	aliases.GET("", h.List)
	aliases.POST("", h.Create)
	aliases.DELETE("/:aliasId", h.Delete)
}

// List returns all aliases for a product.
// GET /api/v1/products/:id/aliases
func (h *AliasHandler) List(c echo.Context) error {
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
		"SELECT id, product_id, alias, store_id, created_at FROM product_aliases WHERE product_id = ? ORDER BY alias",
		productID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer rows.Close()

	aliases := make([]aliasResponse, 0)
	for rows.Next() {
		var a aliasResponse
		if err := rows.Scan(&a.ID, &a.ProductID, &a.Alias, &a.StoreID, &a.CreatedAt); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		aliases = append(aliases, a)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusOK, aliases)
}

// Create adds a new alias for a product.
// POST /api/v1/products/:id/aliases
func (h *AliasHandler) Create(c echo.Context) error {
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

	var req createAliasRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	req.Alias = strings.TrimSpace(req.Alias)
	if req.Alias == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "alias is required"})
	}

	now := time.Now().UTC()
	var id string
	err = h.DB.QueryRow(
		`INSERT INTO product_aliases (id, product_id, alias, store_id, created_at)
		 VALUES (lower(hex(randomblob(16))), ?, ?, ?, ?)
		 RETURNING id`,
		productID, req.Alias, req.StoreID, now,
	).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return c.JSON(http.StatusConflict, map[string]string{"error": "alias already exists"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusCreated, aliasResponse{
		ID:        id,
		ProductID: productID,
		Alias:     req.Alias,
		StoreID:   req.StoreID,
		CreatedAt: now,
	})
}

// Delete removes a product alias.
// DELETE /api/v1/products/:id/aliases/:aliasId
func (h *AliasHandler) Delete(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	productID := c.Param("id")
	aliasID := c.Param("aliasId")

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

	result, err := h.DB.Exec(
		"DELETE FROM product_aliases WHERE id = ? AND product_id = ?",
		aliasID, productID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "alias not found"})
	}

	return c.NoContent(http.StatusNoContent)
}
