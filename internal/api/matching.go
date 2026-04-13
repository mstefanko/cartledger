package api

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/shopspring/decimal"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/matcher"
)

// MatchingHandler holds dependencies for matching and rule endpoints.
type MatchingHandler struct {
	DB  *sql.DB
	Cfg *config.Config
}

// --- Request / Response types ---

type manualMatchRequest struct {
	ProductID  string `json:"product_id"`
	CreateRule bool   `json:"create_rule"`
}

type createRuleRequest struct {
	Priority     int     `json:"priority"`
	ConditionOp  string  `json:"condition_op"`
	ConditionVal string  `json:"condition_val"`
	StoreID      *string `json:"store_id,omitempty"`
	ProductID    string  `json:"product_id"`
	Category     *string `json:"category,omitempty"`
}

type updateRuleRequest struct {
	Priority     *int    `json:"priority,omitempty"`
	ConditionOp  *string `json:"condition_op,omitempty"`
	ConditionVal *string `json:"condition_val,omitempty"`
	StoreID      *string `json:"store_id,omitempty"`
	ProductID    *string `json:"product_id,omitempty"`
	Category     *string `json:"category,omitempty"`
}

type ruleResponse struct {
	ID           string  `json:"id"`
	HouseholdID  string  `json:"household_id"`
	Priority     int     `json:"priority"`
	ConditionOp  string  `json:"condition_op"`
	ConditionVal string  `json:"condition_val"`
	StoreID      *string `json:"store_id,omitempty"`
	ProductID    string  `json:"product_id"`
	Category     *string `json:"category,omitempty"`
	CreatedAt    string  `json:"created_at"`
}

// RegisterRoutes mounts matching and rule endpoints onto the protected group.
func (h *MatchingHandler) RegisterRoutes(protected *echo.Group) {
	protected.PUT("/line-items/:id/match", h.ManualMatch)

	rules := protected.Group("/rules")
	rules.GET("", h.ListRules)
	rules.POST("", h.CreateRule)
	rules.PUT("/:id", h.UpdateRule)
	rules.DELETE("/:id", h.DeleteRule)
}

// ManualMatch manually matches a line item to a product.
// PUT /api/v1/line-items/:id/match
func (h *MatchingHandler) ManualMatch(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	lineItemID := c.Param("id")

	var req manualMatchRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	if req.ProductID == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "product_id is required"})
	}

	// Verify product belongs to household.
	var productExists int
	if err := h.DB.QueryRow(
		"SELECT COUNT(*) FROM products WHERE id = ? AND household_id = ?",
		req.ProductID, householdID,
	).Scan(&productExists); err != nil || productExists == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "product not found"})
	}

	// Fetch the line item and verify it belongs to the household.
	var rawName, receiptID string
	var storeID *string
	var receiptDate time.Time
	var quantity decimal.Decimal
	var unit *string
	var totalPrice decimal.Decimal
	err := h.DB.QueryRow(
		`SELECT li.raw_name, li.receipt_id, r.store_id, r.receipt_date, li.quantity, li.unit, li.total_price
		 FROM line_items li
		 JOIN receipts r ON li.receipt_id = r.id
		 WHERE li.id = ? AND r.household_id = ?`,
		lineItemID, householdID,
	).Scan(&rawName, &receiptID, &storeID, &receiptDate, &quantity, &unit, &totalPrice)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "line item not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	now := time.Now().UTC()

	tx, err := h.DB.Begin()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer tx.Rollback()

	// Update line_item.
	_, err = tx.Exec(
		"UPDATE line_items SET product_id = ?, matched = 'manual' WHERE id = ?",
		req.ProductID, lineItemID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to update line item"})
	}

	// Create product_alias from raw_name -> product.
	normalized := matcher.Normalize(rawName)
	var aliasExists int
	if err := tx.QueryRow(
		"SELECT COUNT(*) FROM product_aliases WHERE product_id = ? AND alias = ?",
		req.ProductID, normalized,
	).Scan(&aliasExists); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if aliasExists == 0 {
		_, err = tx.Exec(
			"INSERT INTO product_aliases (id, product_id, alias, store_id, created_at) VALUES (?, ?, ?, ?, ?)",
			uuid.New().String(), req.ProductID, normalized, storeID, now,
		)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create alias"})
		}
	}

	// Insert product_prices row.
	if storeID != nil {
		unitStr := "each"
		if unit != nil {
			unitStr = *unit
		}
		if quantity.IsZero() {
			quantity = decimal.NewFromInt(1)
		}
		unitPrice := totalPrice.Div(quantity)

		_, err = tx.Exec(
			`INSERT INTO product_prices (id, product_id, store_id, receipt_id, receipt_date, quantity, unit, unit_price, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			uuid.New().String(), req.ProductID, *storeID, receiptID,
			receiptDate, quantity.String(), unitStr, unitPrice.String(), now,
		)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create price record"})
		}
	}

	// Optionally create matching rule.
	if req.CreateRule {
		_, err = tx.Exec(
			`INSERT INTO matching_rules (id, household_id, priority, condition_op, condition_val, store_id, product_id, created_at)
			 VALUES (?, ?, 0, 'exact', ?, ?, ?, ?)`,
			uuid.New().String(), householdID, normalized, storeID, req.ProductID, now,
		)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create rule"})
		}
	}

	// Update product purchase stats.
	_, err = tx.Exec(
		"UPDATE products SET last_purchased_at = ?, purchase_count = purchase_count + 1, updated_at = ? WHERE id = ?",
		receiptDate, now, req.ProductID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to update product"})
	}

	if err := tx.Commit(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to commit"})
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "matched"})
}

// ListRules returns all matching rules for the household.
// GET /api/v1/rules
func (h *MatchingHandler) ListRules(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)

	rows, err := h.DB.Query(
		`SELECT id, household_id, priority, condition_op, condition_val, store_id, product_id, category, created_at
		 FROM matching_rules WHERE household_id = ? ORDER BY priority DESC, created_at`,
		householdID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer rows.Close()

	rules := make([]ruleResponse, 0)
	for rows.Next() {
		var r ruleResponse
		var createdAt time.Time
		if err := rows.Scan(&r.ID, &r.HouseholdID, &r.Priority, &r.ConditionOp, &r.ConditionVal, &r.StoreID, &r.ProductID, &r.Category, &createdAt); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		r.CreatedAt = createdAt.Format(time.RFC3339)
		rules = append(rules, r)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusOK, rules)
}

// CreateRule creates a new matching rule.
// POST /api/v1/rules
func (h *MatchingHandler) CreateRule(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)

	var req createRuleRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	req.ConditionOp = strings.TrimSpace(req.ConditionOp)
	req.ConditionVal = strings.TrimSpace(req.ConditionVal)
	if req.ConditionOp == "" || req.ConditionVal == "" || req.ProductID == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "condition_op, condition_val, and product_id are required"})
	}

	validOps := map[string]bool{"exact": true, "contains": true, "starts_with": true, "matches": true}
	if !validOps[req.ConditionOp] {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "condition_op must be one of: exact, contains, starts_with, matches"})
	}

	now := time.Now().UTC()
	id := uuid.New().String()

	_, err := h.DB.Exec(
		`INSERT INTO matching_rules (id, household_id, priority, condition_op, condition_val, store_id, product_id, category, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, householdID, req.Priority, req.ConditionOp, req.ConditionVal,
		req.StoreID, req.ProductID, req.Category, now,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create rule"})
	}

	return c.JSON(http.StatusCreated, ruleResponse{
		ID:           id,
		HouseholdID:  householdID,
		Priority:     req.Priority,
		ConditionOp:  req.ConditionOp,
		ConditionVal: req.ConditionVal,
		StoreID:      req.StoreID,
		ProductID:    req.ProductID,
		Category:     req.Category,
		CreatedAt:    now.Format(time.RFC3339),
	})
}

// UpdateRule updates an existing matching rule.
// PUT /api/v1/rules/:id
func (h *MatchingHandler) UpdateRule(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	ruleID := c.Param("id")

	var req updateRuleRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	setClauses := make([]string, 0)
	args := make([]interface{}, 0)

	if req.Priority != nil {
		setClauses = append(setClauses, "priority = ?")
		args = append(args, *req.Priority)
	}
	if req.ConditionOp != nil {
		validOps := map[string]bool{"exact": true, "contains": true, "starts_with": true, "matches": true}
		if !validOps[*req.ConditionOp] {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "condition_op must be one of: exact, contains, starts_with, matches"})
		}
		setClauses = append(setClauses, "condition_op = ?")
		args = append(args, *req.ConditionOp)
	}
	if req.ConditionVal != nil {
		setClauses = append(setClauses, "condition_val = ?")
		args = append(args, *req.ConditionVal)
	}
	if req.StoreID != nil {
		setClauses = append(setClauses, "store_id = ?")
		args = append(args, *req.StoreID)
	}
	if req.ProductID != nil {
		setClauses = append(setClauses, "product_id = ?")
		args = append(args, *req.ProductID)
	}
	if req.Category != nil {
		setClauses = append(setClauses, "category = ?")
		args = append(args, *req.Category)
	}

	if len(setClauses) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "no fields to update"})
	}

	args = append(args, ruleID, householdID)
	query := "UPDATE matching_rules SET " + strings.Join(setClauses, ", ") + " WHERE id = ? AND household_id = ?"

	result, err := h.DB.Exec(query, args...)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "rule not found"})
	}

	// Fetch and return updated rule.
	var r ruleResponse
	var createdAt time.Time
	err = h.DB.QueryRow(
		`SELECT id, household_id, priority, condition_op, condition_val, store_id, product_id, category, created_at
		 FROM matching_rules WHERE id = ?`,
		ruleID,
	).Scan(&r.ID, &r.HouseholdID, &r.Priority, &r.ConditionOp, &r.ConditionVal, &r.StoreID, &r.ProductID, &r.Category, &createdAt)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	r.CreatedAt = createdAt.Format(time.RFC3339)

	return c.JSON(http.StatusOK, r)
}

// DeleteRule removes a matching rule.
// DELETE /api/v1/rules/:id
func (h *MatchingHandler) DeleteRule(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	ruleID := c.Param("id")

	result, err := h.DB.Exec(
		"DELETE FROM matching_rules WHERE id = ? AND household_id = ?",
		ruleID, householdID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "rule not found"})
	}

	return c.NoContent(http.StatusNoContent)
}
