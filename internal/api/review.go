package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/ws"
)

// ReviewHandler holds dependencies for review-related endpoints.
type ReviewHandler struct {
	DB  *sql.DB
	Cfg *config.Config
	Hub *ws.Hub
}

// --- Response types ---

type unmatchedLineItemResponse struct {
	ID               string                  `json:"id"`
	ReceiptID        string                  `json:"receipt_id"`
	ReceiptDate      string                  `json:"receipt_date"`
	StoreID          *string                 `json:"store_id,omitempty"`
	StoreName        *string                 `json:"store_name,omitempty"`
	RawText          string                  `json:"raw_text"`
	Quantity         string                  `json:"quantity"`
	Unit             *string                 `json:"unit,omitempty"`
	UnitPrice        *string                 `json:"unit_price,omitempty"`
	TotalPrice       string                  `json:"total_price"`
	PossibleListItems []possibleListItemMatch `json:"possible_list_items,omitempty"`
}

type possibleListItemMatch struct {
	ListID   string `json:"list_id"`
	ListName string `json:"list_name"`
	ItemID   string `json:"item_id"`
	ItemName string `json:"item_name"`
}

type linkListItemRequest struct {
	ListItemID          string  `json:"list_item_id"`
	AlsoAssignProductID *string `json:"also_assign_product_id,omitempty"`
}

// RegisterRoutes mounts review endpoints onto the protected group.
func (h *ReviewHandler) RegisterRoutes(protected *echo.Group) {
	lineItems := protected.Group("/line-items")
	lineItems.GET("/unmatched", h.ListUnmatchedLineItems)
	lineItems.GET("/unmatched/count", h.GetUnmatchedCount)
	lineItems.POST("/:id/link-list-item", h.LinkListItem)

	// Batch review header — lives under /import/batches/:id so the frontend
	// can fetch a filename + counts for the persistent header strip when a
	// user opens /review?batch=<id>. Kept on ReviewHandler (not the
	// spreadsheet handler) because the data served is review-scoped and may
	// later back non-spreadsheet batch sources.
	protected.GET("/import/batches/:id", h.GetImportBatch)
}

// maxBatchIDLen bounds the accepted batch_id query param. Plan calls for a
// "non-empty and < 64 chars" check (we don't parse as a strict UUID because
// migration 021 stores ids as 32-char lowercase hex — `hex(randomblob(16))`
// — and spreadsheet.Commit writes uuid.New().String() shaped like a normal
// UUID with dashes). 64 is generous enough for either form while rejecting
// pathological inputs.
const maxBatchIDLen = 64

// validateBatchID normalizes and bounds-checks a batch_id query param.
// Returns (id, "") when acceptable, ("", reason) otherwise. The caller
// turns a non-empty reason into a 400.
func validateBatchID(raw string) (string, string) {
	id := strings.TrimSpace(raw)
	if id == "" {
		return "", "batch_id must not be empty"
	}
	if len(id) > maxBatchIDLen {
		return "", "batch_id too long"
	}
	return id, ""
}

// verifyBatchOwnership returns the (filename, source_type, created_at,
// receipts_count, items_count) tuple for a batch, but only when the batch
// belongs to the caller's household. sql.ErrNoRows is surfaced verbatim so
// the caller can 404 on either missing-or-cross-household (no existence
// leak per plan §Auth).
func (h *ReviewHandler) verifyBatchOwnership(c echo.Context, batchID, householdID string) (filename sql.NullString, sourceType string, createdAt time.Time, receipts int, items int, err error) {
	err = h.DB.QueryRowContext(c.Request().Context(),
		`SELECT filename, source_type, created_at, receipts_count, items_count
		 FROM import_batches
		 WHERE id = ? AND household_id = ?`,
		batchID, householdID,
	).Scan(&filename, &sourceType, &createdAt, &receipts, &items)
	return
}

// ListUnmatchedLineItems returns all unmatched line items for the household.
// GET /api/v1/line-items/unmatched
// Optional query param ?batch_id=<id> narrows the list to a specific import
// batch. The batch must belong to the caller's household; unknown or
// cross-household batch ids return 404.
func (h *ReviewHandler) ListUnmatchedLineItems(c echo.Context) error {
	ctx := c.Request().Context()
	householdID := auth.HouseholdIDFrom(c)

	// Optional batch filter. When present, validate shape, then verify
	// ownership before letting it into the WHERE clause. Both checks MUST
	// run before we execute the list query so a malformed or cross-household
	// id can't leak row existence timing.
	var batchID string
	if raw := c.QueryParam("batch_id"); raw != "" {
		id, reason := validateBatchID(raw)
		if reason != "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": reason})
		}
		if _, _, _, _, _, err := h.verifyBatchOwnership(c, id, householdID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return c.JSON(http.StatusNotFound, map[string]string{"error": "batch not found"})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		batchID = id
	}

	// Build query — the only change when a batch is supplied is an extra
	// AND clause + bound arg. Row shape is unchanged so the frontend parser
	// keeps working (plan §Constraints).
	query := `
		SELECT
			li.id,
			li.receipt_id,
			r.receipt_date,
			r.store_id,
			s.name,
			li.raw_name,
			li.quantity,
			li.unit,
			li.unit_price,
			li.total_price
		FROM line_items li
		JOIN receipts r ON r.id = li.receipt_id
		LEFT JOIN stores s ON s.id = r.store_id
		WHERE r.household_id = ?
		  AND (li.product_id IS NULL OR li.matched = 'unmatched')`
	args := []interface{}{householdID}
	if batchID != "" {
		query += ` AND li.import_batch_id = ?`
		args = append(args, batchID)
	}
	query += ` ORDER BY r.receipt_date DESC, li.id ASC`

	rows, err := h.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer rows.Close()

	items := make([]unmatchedLineItemResponse, 0)
	for rows.Next() {
		var item unmatchedLineItemResponse
		if err := rows.Scan(
			&item.ID,
			&item.ReceiptID,
			&item.ReceiptDate,
			&item.StoreID,
			&item.StoreName,
			&item.RawText,
			&item.Quantity,
			&item.Unit,
			&item.UnitPrice,
			&item.TotalPrice,
		); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// Enrich items with fuzzy list-item matches.
	if len(items) > 0 {
		ids := make([]string, len(items))
		for i, item := range items {
			ids[i] = item.ID
		}
		placeholders := strings.Repeat("?,", len(ids))
		placeholders = placeholders[:len(placeholders)-1]
		matchSQL := fmt.Sprintf(`
			SELECT li.id, sl.id, sl.name, sli.id, sli.name
			FROM line_items li
			JOIN receipts r ON li.receipt_id = r.id
			JOIN shopping_list_items sli ON (
				LOWER(li.raw_name) LIKE '%%' || LOWER(sli.name) || '%%'
				OR LOWER(sli.name) LIKE '%%' || LOWER(li.raw_name) || '%%'
			)
			JOIN shopping_lists sl ON sli.list_id = sl.id
			WHERE li.id IN (%s)
			  AND sli.product_id IS NULL
			  AND sl.status = 'active'
			  AND sl.household_id = ?`, placeholders)
		matchArgs := make([]interface{}, len(ids)+1)
		for i, id := range ids {
			matchArgs[i] = id
		}
		matchArgs[len(ids)] = householdID
		matchRows, err := h.DB.QueryContext(ctx, matchSQL, matchArgs...)
		if err == nil {
			defer matchRows.Close()
			matchMap := map[string][]possibleListItemMatch{}
			for matchRows.Next() {
				var liID, listID, listName, itemID, itemName string
				if err := matchRows.Scan(&liID, &listID, &listName, &itemID, &itemName); err == nil {
					matchMap[liID] = append(matchMap[liID], possibleListItemMatch{
						ListID: listID, ListName: listName, ItemID: itemID, ItemName: itemName,
					})
				}
			}
			for i := range items {
				if matches, ok := matchMap[items[i].ID]; ok {
					items[i].PossibleListItems = matches
				}
			}
		}
	}

	return c.JSON(http.StatusOK, items)
}

// GetUnmatchedCount returns the count of unmatched line items for the household.
// GET /api/v1/line-items/unmatched/count
// Optional query param ?batch_id=<id> narrows the count to a specific batch,
// same validation rules as ListUnmatchedLineItems.
func (h *ReviewHandler) GetUnmatchedCount(c echo.Context) error {
	ctx := c.Request().Context()
	householdID := auth.HouseholdIDFrom(c)

	var batchID string
	if raw := c.QueryParam("batch_id"); raw != "" {
		id, reason := validateBatchID(raw)
		if reason != "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": reason})
		}
		if _, _, _, _, _, err := h.verifyBatchOwnership(c, id, householdID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return c.JSON(http.StatusNotFound, map[string]string{"error": "batch not found"})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		batchID = id
	}

	query := `
		SELECT COUNT(*)
		FROM line_items li
		JOIN receipts r ON r.id = li.receipt_id
		WHERE r.household_id = ?
		  AND (li.product_id IS NULL OR li.matched = 'unmatched')`
	args := []interface{}{householdID}
	if batchID != "" {
		query += ` AND li.import_batch_id = ?`
		args = append(args, batchID)
	}

	var count int
	if err := h.DB.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusOK, map[string]int{"count": count})
}

// importBatchHeaderResponse is the shape returned by GET /import/batches/:id.
// Field names match the plan §Backend changes spec verbatim; `unmatched_count`
// is recomputed on every call (not read from the denormalized snapshot on
// import_batches) so it tracks what the user has cleared in the review lane.
type importBatchHeaderResponse struct {
	ID             string `json:"id"`
	SourceType     string `json:"source_type"`
	Filename       string `json:"filename"`
	CreatedAt      string `json:"created_at"`
	ReceiptsCount  int    `json:"receipts_count"`
	ItemsCount     int    `json:"items_count"`
	UnmatchedCount int    `json:"unmatched_count"`
}

// GetImportBatch handles GET /api/v1/import/batches/:id — the header strip
// payload for the batch-scoped review lane. 404s on unknown-or-cross-household
// (no existence leak). The denormalized `import_batches.unmatched_count` is
// intentionally ignored in favor of a fresh COUNT(*): the snapshot is only
// accurate at commit time, and we need a live view as users match items.
func (h *ReviewHandler) GetImportBatch(c echo.Context) error {
	ctx := c.Request().Context()
	householdID := auth.HouseholdIDFrom(c)

	id, reason := validateBatchID(c.Param("id"))
	if reason != "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": reason})
	}

	filename, sourceType, createdAt, receipts, items, err := h.verifyBatchOwnership(c, id, householdID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "batch not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	var unmatched int
	if err := h.DB.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM line_items li
		JOIN receipts r ON r.id = li.receipt_id
		WHERE r.household_id = ?
		  AND li.import_batch_id = ?
		  AND (li.product_id IS NULL OR li.matched = 'unmatched')`,
		householdID, id,
	).Scan(&unmatched); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	resp := importBatchHeaderResponse{
		ID:             id,
		SourceType:     sourceType,
		CreatedAt:      createdAt.UTC().Format(time.RFC3339),
		ReceiptsCount:  receipts,
		ItemsCount:     items,
		UnmatchedCount: unmatched,
	}
	if filename.Valid {
		resp.Filename = filename.String
	}
	return c.JSON(http.StatusOK, resp)
}

// LinkListItem links a receipt line item to a shopping list item, checking it off.
// POST /api/v1/line-items/:id/link-list-item
func (h *ReviewHandler) LinkListItem(c echo.Context) error {
	ctx := c.Request().Context()
	householdID := auth.HouseholdIDFrom(c)
	userID := auth.UserIDFrom(c)
	lineItemID := c.Param("id")

	var req linkListItemRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request")
	}
	if req.ListItemID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "list_item_id is required")
	}

	// Verify line item belongs to household.
	var exists int
	if err := h.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM line_items li
		JOIN receipts r ON li.receipt_id = r.id
		WHERE li.id = ? AND r.household_id = ?`, lineItemID, householdID).Scan(&exists); err != nil || exists == 0 {
		return echo.NewHTTPError(http.StatusNotFound, "line item not found")
	}

	// Fetch list item, verify it belongs to household.
	var listID string
	if err := h.DB.QueryRowContext(ctx, `
		SELECT sl.id FROM shopping_list_items sli
		JOIN shopping_lists sl ON sli.list_id = sl.id
		WHERE sli.id = ? AND sl.household_id = ?`, req.ListItemID, householdID).Scan(&listID); err == sql.ErrNoRows {
		return echo.NewHTTPError(http.StatusNotFound, "list item not found")
	} else if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "db error")
	}

	// Optionally assign product to line item.
	if req.AlsoAssignProductID != nil {
		if _, err := h.DB.ExecContext(ctx, `
			UPDATE line_items SET product_id = ?, matched = 'manual' WHERE id = ?`,
			*req.AlsoAssignProductID, lineItemID); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to assign product")
		}
	}

	// Check off the list item.
	var checkErr error
	if req.AlsoAssignProductID != nil {
		_, checkErr = h.DB.ExecContext(ctx, `
			UPDATE shopping_list_items SET checked = 1, checked_by = ?, product_id = ? WHERE id = ?`,
			userID, *req.AlsoAssignProductID, req.ListItemID)
	} else {
		_, checkErr = h.DB.ExecContext(ctx, `
			UPDATE shopping_list_items SET checked = 1, checked_by = ? WHERE id = ?`,
			userID, req.ListItemID)
	}
	if checkErr != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to check off list item")
	}

	// Broadcast to household via WebSocket.
	if h.Hub != nil {
		h.Hub.Broadcast(ws.Message{
			Type:      ws.EventListItemChecked,
			Household: householdID,
			Payload: map[string]interface{}{
				"list_id": listID,
				"item_id": req.ListItemID,
			},
		})
	}

	return c.NoContent(http.StatusNoContent)
}
