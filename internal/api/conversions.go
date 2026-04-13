package api

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/shopspring/decimal"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
)

// ConversionHandler holds dependencies for unit conversion management endpoints.
type ConversionHandler struct {
	DB  *sql.DB
	Cfg *config.Config
}

// RegisterRoutes mounts conversion endpoints onto the protected group.
func (h *ConversionHandler) RegisterRoutes(protected *echo.Group) {
	conversions := protected.Group("/conversions")
	conversions.GET("", h.List)
	conversions.POST("", h.Create)
	conversions.DELETE("/:id", h.Delete)
}

// --- Request/response types ---

type conversionResponse struct {
	ID        string  `json:"id"`
	ProductID *string `json:"product_id,omitempty"`
	FromUnit  string  `json:"from_unit"`
	ToUnit    string  `json:"to_unit"`
	Factor    string  `json:"factor"`
}

type createConversionRequest struct {
	ProductID *string `json:"product_id,omitempty"`
	FromUnit  string  `json:"from_unit"`
	ToUnit    string  `json:"to_unit"`
	Factor    string  `json:"factor"`
}

// List returns unit conversions. If product_id query param is provided, filters
// to that product plus generic conversions. Otherwise returns all.
// GET /api/v1/conversions
func (h *ConversionHandler) List(c echo.Context) error {
	_ = auth.HouseholdIDFrom(c)
	productID := c.QueryParam("product_id")

	var rows *sql.Rows
	var err error

	if productID != "" {
		rows, err = h.DB.Query(
			`SELECT id, product_id, from_unit, to_unit, factor
			 FROM unit_conversions
			 WHERE product_id = ? OR product_id IS NULL
			 ORDER BY product_id IS NULL, from_unit`,
			productID,
		)
	} else {
		rows, err = h.DB.Query(
			`SELECT id, product_id, from_unit, to_unit, factor
			 FROM unit_conversions
			 ORDER BY product_id IS NULL, from_unit`,
		)
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer rows.Close()

	conversions := make([]conversionResponse, 0)
	for rows.Next() {
		var conv conversionResponse
		var factorStr string
		if err := rows.Scan(&conv.ID, &conv.ProductID, &conv.FromUnit, &conv.ToUnit, &factorStr); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		conv.Factor = factorStr
		conversions = append(conversions, conv)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusOK, conversions)
}

// Create adds a new unit conversion.
// POST /api/v1/conversions
func (h *ConversionHandler) Create(c echo.Context) error {
	_ = auth.HouseholdIDFrom(c)

	var req createConversionRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	req.FromUnit = strings.TrimSpace(req.FromUnit)
	req.ToUnit = strings.TrimSpace(req.ToUnit)
	req.Factor = strings.TrimSpace(req.Factor)

	if req.FromUnit == "" || req.ToUnit == "" || req.Factor == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "from_unit, to_unit, and factor are required"})
	}

	factor, err := decimal.NewFromString(req.Factor)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "factor must be a valid number"})
	}
	if factor.IsZero() || factor.IsNegative() {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "factor must be positive"})
	}

	id := uuid.New().String()
	_, err = h.DB.Exec(
		`INSERT INTO unit_conversions (id, product_id, from_unit, to_unit, factor)
		 VALUES (?, ?, ?, ?, ?)`,
		id, req.ProductID, req.FromUnit, req.ToUnit, factor.String(),
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return c.JSON(http.StatusConflict, map[string]string{"error": "conversion already exists"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusCreated, conversionResponse{
		ID:        id,
		ProductID: req.ProductID,
		FromUnit:  req.FromUnit,
		ToUnit:    req.ToUnit,
		Factor:    factor.String(),
	})
}

// Delete removes a unit conversion.
// DELETE /api/v1/conversions/:id
func (h *ConversionHandler) Delete(c echo.Context) error {
	_ = auth.HouseholdIDFrom(c)
	convID := c.Param("id")

	result, err := h.DB.Exec("DELETE FROM unit_conversions WHERE id = ?", convID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "conversion not found"})
	}

	return c.NoContent(http.StatusNoContent)
}
