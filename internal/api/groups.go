package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
)

// GroupHandler holds dependencies for product-group endpoints.
type GroupHandler struct {
	DB  *sql.DB
	Cfg *config.Config
}

// --- Request types ---

type createGroupRequest struct {
	Name           string  `json:"name"`
	ComparisonUnit *string `json:"comparison_unit,omitempty"`
}

type updateGroupRequest struct {
	Name           string  `json:"name"`
	ComparisonUnit *string `json:"comparison_unit,omitempty"`
}

// --- Response types ---

type groupListItem struct {
	ID             string  `json:"id"`
	HouseholdID    string  `json:"household_id"`
	Name           string  `json:"name"`
	ComparisonUnit *string `json:"comparison_unit,omitempty"`
	MemberCount    int     `json:"member_count"`
	BestPrice      *string `json:"best_price,omitempty"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
}

type groupMember struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Brand        *string  `json:"brand,omitempty"`
	PackQuantity *float64 `json:"pack_quantity,omitempty"`
	PackUnit     *string  `json:"pack_unit,omitempty"`
	StoreName    *string  `json:"store_name,omitempty"`
	LatestPrice  *string  `json:"latest_price,omitempty"`
	ReceiptDate  *string  `json:"receipt_date,omitempty"`
	PricePerUnit *string  `json:"price_per_unit,omitempty"`
}

type groupDetailResponse struct {
	ID             string        `json:"id"`
	HouseholdID    string        `json:"household_id"`
	Name           string        `json:"name"`
	ComparisonUnit *string       `json:"comparison_unit,omitempty"`
	UnitsMixed     bool          `json:"units_mixed"`
	Members        []groupMember `json:"members"`
	CreatedAt      string        `json:"created_at"`
	UpdatedAt      string        `json:"updated_at"`
}

type groupSuggestion struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Brand        *string  `json:"brand,omitempty"`
	PackQuantity *float64 `json:"pack_quantity,omitempty"`
	PackUnit     *string  `json:"pack_unit,omitempty"`
}

// RegisterRoutes mounts product-group endpoints onto the protected group.
func (h *GroupHandler) RegisterRoutes(protected *echo.Group) {
	groups := protected.Group("/product-groups")
	groups.GET("/suggestions", h.Suggestions) // Must be before /:id
	groups.GET("", h.List)
	groups.POST("", h.Create)
	groups.GET("/:id", h.Get)
	groups.PUT("/:id", h.Update)
	groups.DELETE("/:id", h.Delete)
}

// Create adds a new product group.
// POST /api/v1/product-groups
func (h *GroupHandler) Create(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)

	var req createGroupRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "name is required"})
	}

	id := uuid.New().String()
	now := time.Now().UTC()

	_, err := h.DB.Exec(
		`INSERT INTO product_groups (id, household_id, name, comparison_unit, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, householdID, req.Name, req.ComparisonUnit, now, now,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return c.JSON(http.StatusConflict, map[string]string{"error": "group name already exists"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusCreated, groupListItem{
		ID:             id,
		HouseholdID:    householdID,
		Name:           req.Name,
		ComparisonUnit: req.ComparisonUnit,
		MemberCount:    0,
		CreatedAt:      now.Format(time.RFC3339),
		UpdatedAt:      now.Format(time.RFC3339),
	})
}

// List returns all product groups for the household with member count and best price.
// GET /api/v1/product-groups
func (h *GroupHandler) List(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)

	rows, err := h.DB.Query(
		`SELECT g.id, g.household_id, g.name, g.comparison_unit,
		        (SELECT COUNT(*) FROM products p WHERE p.product_group_id = g.id) as member_count,
		        (SELECT PRINTF('%.2f', MIN(CAST(pp.unit_price AS REAL)))
		         FROM products p2
		         JOIN (
		             SELECT product_id, unit_price,
		                    ROW_NUMBER() OVER (PARTITION BY product_id ORDER BY receipt_date DESC) rn
		             FROM product_prices
		         ) pp ON pp.product_id = p2.id AND pp.rn = 1
		         WHERE p2.product_group_id = g.id
		        ) as best_price,
		        g.created_at, g.updated_at
		 FROM product_groups g
		 WHERE g.household_id = ?
		 ORDER BY g.name`,
		householdID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer rows.Close()

	groups := make([]groupListItem, 0)
	for rows.Next() {
		var g groupListItem
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&g.ID, &g.HouseholdID, &g.Name, &g.ComparisonUnit,
			&g.MemberCount, &g.BestPrice, &createdAt, &updatedAt); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		g.CreatedAt = createdAt.Format(time.RFC3339)
		g.UpdatedAt = updatedAt.Format(time.RFC3339)
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusOK, groups)
}

// Get returns a product group with full member details and price comparison.
// GET /api/v1/product-groups/:id
func (h *GroupHandler) Get(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	groupID := c.Param("id")

	var resp groupDetailResponse
	var createdAt, updatedAt time.Time
	err := h.DB.QueryRow(
		`SELECT id, household_id, name, comparison_unit, created_at, updated_at
		 FROM product_groups WHERE id = ? AND household_id = ?`,
		groupID, householdID,
	).Scan(&resp.ID, &resp.HouseholdID, &resp.Name, &resp.ComparisonUnit, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "group not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	resp.CreatedAt = createdAt.Format(time.RFC3339)
	resp.UpdatedAt = updatedAt.Format(time.RFC3339)

	// Fetch members with latest price.
	rows, err := h.DB.Query(
		`SELECT p.id, p.name, p.brand, p.pack_quantity, p.pack_unit, s.name,
		        pp.unit_price, pp.receipt_date
		 FROM products p
		 LEFT JOIN (
		     SELECT product_id, store_id, unit_price, receipt_date,
		            ROW_NUMBER() OVER (PARTITION BY product_id ORDER BY receipt_date DESC) rn
		     FROM product_prices
		 ) pp ON pp.product_id = p.id AND pp.rn = 1
		 LEFT JOIN stores s ON pp.store_id = s.id
		 WHERE p.product_group_id = ? AND p.household_id = ?
		 ORDER BY CAST(pp.unit_price AS REAL) ASC`,
		groupID, householdID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer rows.Close()

	resp.Members = make([]groupMember, 0)
	unitSet := make(map[string]bool)

	for rows.Next() {
		var m groupMember
		var unitPrice *float64
		var receiptDate *time.Time

		if err := rows.Scan(&m.ID, &m.Name, &m.Brand, &m.PackQuantity, &m.PackUnit,
			&m.StoreName, &unitPrice, &receiptDate); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}

		if unitPrice != nil {
			s := fmt.Sprintf("%.2f", *unitPrice)
			m.LatestPrice = &s
		}
		if receiptDate != nil {
			s := receiptDate.Format("2006-01-02")
			m.ReceiptDate = &s
		}

		// Compute price_per_unit = latest_price / pack_quantity.
		if unitPrice != nil && m.PackQuantity != nil && *m.PackQuantity > 0 {
			ppu := *unitPrice / *m.PackQuantity
			s := fmt.Sprintf("%.2f", ppu)
			m.PricePerUnit = &s
		}

		if m.PackUnit != nil && *m.PackUnit != "" {
			unitSet[strings.ToLower(*m.PackUnit)] = true
		}

		resp.Members = append(resp.Members, m)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	resp.UnitsMixed = len(unitSet) > 1

	return c.JSON(http.StatusOK, resp)
}

// Update modifies a product group.
// PUT /api/v1/product-groups/:id
func (h *GroupHandler) Update(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	groupID := c.Param("id")

	var req updateGroupRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "name is required"})
	}

	now := time.Now().UTC()
	result, err := h.DB.Exec(
		`UPDATE product_groups SET name = ?, comparison_unit = ?, updated_at = ?
		 WHERE id = ? AND household_id = ?`,
		req.Name, req.ComparisonUnit, now, groupID, householdID,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return c.JSON(http.StatusConflict, map[string]string{"error": "group name already exists"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "group not found"})
	}

	// Re-fetch to return updated state.
	var g groupListItem
	var createdAt, updatedAt time.Time
	err = h.DB.QueryRow(
		`SELECT g.id, g.household_id, g.name, g.comparison_unit,
		        (SELECT COUNT(*) FROM products p WHERE p.product_group_id = g.id) as member_count,
		        g.created_at, g.updated_at
		 FROM product_groups g WHERE g.id = ? AND g.household_id = ?`,
		groupID, householdID,
	).Scan(&g.ID, &g.HouseholdID, &g.Name, &g.ComparisonUnit, &g.MemberCount, &createdAt, &updatedAt)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	g.CreatedAt = createdAt.Format(time.RFC3339)
	g.UpdatedAt = updatedAt.Format(time.RFC3339)

	return c.JSON(http.StatusOK, g)
}

// Delete removes a product group. Products in the group get product_group_id set to NULL (ON DELETE SET NULL).
// DELETE /api/v1/product-groups/:id
func (h *GroupHandler) Delete(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	groupID := c.Param("id")

	result, err := h.DB.Exec(
		"DELETE FROM product_groups WHERE id = ? AND household_id = ?",
		groupID, householdID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "group not found"})
	}

	return c.NoContent(http.StatusNoContent)
}

// Suggestions returns candidate products for grouping with a given product.
// GET /api/v1/product-groups/suggestions?product_id=xxx
func (h *GroupHandler) Suggestions(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	productID := strings.TrimSpace(c.QueryParam("product_id"))
	if productID == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "product_id is required"})
	}

	// Look up the source product's brand.
	var brand *string
	err := h.DB.QueryRow(
		"SELECT brand FROM products WHERE id = ? AND household_id = ?",
		productID, householdID,
	).Scan(&brand)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "product not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	if brand == nil || *brand == "" {
		// No brand set — cannot suggest.
		return c.JSON(http.StatusOK, make([]groupSuggestion, 0))
	}

	rows, err := h.DB.Query(
		`SELECT id, name, brand, pack_quantity, pack_unit
		 FROM products
		 WHERE LOWER(brand) = LOWER(?) AND household_id = ? AND product_group_id IS NULL AND id != ?
		 ORDER BY name`,
		*brand, householdID, productID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer rows.Close()

	suggestions := make([]groupSuggestion, 0)
	for rows.Next() {
		var s groupSuggestion
		if err := rows.Scan(&s.ID, &s.Name, &s.Brand, &s.PackQuantity, &s.PackUnit); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		suggestions = append(suggestions, s)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusOK, suggestions)
}
