package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
)

// ExportHandler holds dependencies for export/share endpoints.
type ExportHandler struct {
	DB  *sql.DB
	Cfg *config.Config
}

// RegisterRoutes mounts export endpoints onto the protected group.
func (h *ExportHandler) RegisterRoutes(protected *echo.Group) {
	protected.GET("/lists/:id/share", h.ShareList)
}

// ShareList returns a plain-text formatted shopping list for sharing.
// GET /api/v1/lists/:id/share
func (h *ExportHandler) ShareList(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	listID := c.Param("id")

	// Get list name.
	var listName string
	err := h.DB.QueryRow(
		"SELECT name FROM shopping_lists WHERE id = ? AND household_id = ?",
		listID, householdID,
	).Scan(&listName)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "list not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// Get items with estimated prices.
	rows, err := h.DB.Query(
		`SELECT sli.name, sli.quantity, sli.unit, sli.checked,
		        (SELECT pp.unit_price FROM product_prices pp
		         WHERE pp.product_id = sli.product_id
		         ORDER BY pp.receipt_date DESC LIMIT 1) AS estimated_price
		 FROM shopping_list_items sli
		 WHERE sli.list_id = ?
		 ORDER BY sli.sort_order, sli.created_at`,
		listID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer rows.Close()

	var lines []string
	var totalEstimate float64
	hasEstimate := false

	for rows.Next() {
		var name, quantity string
		var unit *string
		var checked bool
		var price *float64

		if err := rows.Scan(&name, &quantity, &unit, &checked, &price); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}

		checkbox := "\u2610" // ☐
		if checked {
			checkbox = "\u2611" // ☑
		}

		// Build quantity/unit suffix.
		qtyStr := ""
		if quantity != "" && quantity != "1" {
			if unit != nil && *unit != "" {
				qtyStr = fmt.Sprintf(" (%s %s)", quantity, *unit)
			} else {
				qtyStr = fmt.Sprintf(" (%s)", quantity)
			}
		} else if unit != nil && *unit != "" {
			qtyStr = fmt.Sprintf(" (1 %s)", *unit)
		}

		priceStr := ""
		if price != nil {
			priceStr = fmt.Sprintf(" \u2014 $%.2f", *price)
			totalEstimate += *price
			hasEstimate = true
		}

		lines = append(lines, fmt.Sprintf("%s %s%s%s", checkbox, name, qtyStr, priceStr))
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// Build header.
	header := listName
	if hasEstimate {
		header = fmt.Sprintf("%s \u2014 Est. $%.2f", listName, totalEstimate)
	}

	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteString("\n\n")
	for _, line := range lines {
		sb.WriteString(line)
		sb.WriteString("\n")
	}

	return c.String(http.StatusOK, sb.String())
}
