package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/locks"
	"github.com/mstefanko/cartledger/internal/ws"
)

// DBTX is the minimal interface satisfied by both *sql.DB and *sql.Tx,
// letting helper functions be called from either a direct DB path or inside
// a transaction.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
	PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
}

// ListHandler holds dependencies for shopping list endpoints.
type ListHandler struct {
	DB    *sql.DB
	Cfg   *config.Config
	Hub   *ws.Hub
	Locks *locks.Store
}

// --- Request types ---

type createListRequest struct {
	Name string `json:"name"`
}

type updateListRequest struct {
	Name            *string `json:"name,omitempty"`
	Status          *string `json:"status,omitempty"`
	PreferredStoreID *string `json:"preferred_store_id"`
}

type createListItemRequest struct {
	Name            string  `json:"name"`
	ProductID       *string `json:"product_id,omitempty"`
	ProductGroupID  *string `json:"product_group_id"`
	Quantity        *string `json:"quantity,omitempty"`
	Unit            *string `json:"unit,omitempty"`
	Notes           *string `json:"notes,omitempty"`
	AssignedStoreID *string `json:"assigned_store_id,omitempty"`
}

type updateListItemRequest struct {
	Name            *string `json:"name,omitempty"`
	ProductID       *string `json:"product_id,omitempty"`
	ProductGroupID  *string `json:"product_group_id"`
	Quantity        *string `json:"quantity,omitempty"`
	Unit            *string `json:"unit,omitempty"`
	Checked         *bool   `json:"checked,omitempty"`
	CheckedBy       *string `json:"checked_by,omitempty"`
	Notes           *string `json:"notes,omitempty"`
	SortOrder       *int    `json:"sort_order,omitempty"`
	AssignedStoreID *string `json:"assigned_store_id"`
}

type reorderListItemEntry struct {
	ID        string `json:"id"`
	SortOrder int    `json:"sort_order"`
}

type reorderListItemsRequest struct {
	Items []reorderListItemEntry `json:"items"`
}

// --- Response types ---

type listSummaryResponse struct {
	ID           string    `json:"id"`
	HouseholdID  string    `json:"household_id"`
	Name         string    `json:"name"`
	CreatedBy    *string   `json:"created_by,omitempty"`
	Status       string    `json:"status"`
	ItemCount    int       `json:"item_count"`
	CheckedCount int       `json:"checked_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type listDetailResponse struct {
	ID                 string             `json:"id"`
	HouseholdID        string             `json:"household_id"`
	Name               string             `json:"name"`
	CreatedBy          *string            `json:"created_by,omitempty"`
	Status             string             `json:"status"`
	PreferredStoreID   *string            `json:"preferred_store_id,omitempty"`
	PreferredStoreName *string            `json:"preferred_store_name,omitempty"`
	Items              []listItemResponse `json:"items"`
	CreatedAt          time.Time          `json:"created_at"`
	UpdatedAt          time.Time          `json:"updated_at"`
}

type listItemResponse struct {
	ID             string  `json:"id"`
	ListID         string  `json:"list_id"`
	ProductID      *string `json:"product_id,omitempty"`
	ProductName    *string `json:"product_name,omitempty"`
	Name           string  `json:"name"`
	Quantity       string  `json:"quantity"`
	Unit           *string `json:"unit,omitempty"`
	Checked        bool    `json:"checked"`
	CheckedBy      *string `json:"checked_by,omitempty"`
	SortOrder      int     `json:"sort_order"`
	Notes          *string `json:"notes,omitempty"`
	EstimatedPrice    *string `json:"estimated_price,omitempty"`
	CheapestStore     *string `json:"cheapest_store,omitempty"`
	CheapestPrice     *string `json:"cheapest_price,omitempty"`
	ProductGroupID    *string `json:"product_group_id,omitempty"`
	ProductGroupName  *string `json:"product_group_name,omitempty"`
	CheapestProductID *string `json:"cheapest_product_id,omitempty"`
	StorePrice        *string `json:"store_price,omitempty"`
	StorePriceStore   *string `json:"store_price_store,omitempty"`
	AssignedStoreID   *string `json:"assigned_store_id,omitempty"`
	AssignedStoreName *string `json:"assigned_store_name,omitempty"`
	// AssignedStorePrice is the latest unit_price at the per-row assigned_store_id
	// (not the list-level preferred_store_id). Nil when the assigned store has no
	// price history for this product/group — used by the UI to show a neutral
	// "unknown" badge instead of claiming "worse than cheapest".
	AssignedStorePrice *string `json:"assigned_store_price,omitempty"`
	// StoreHistoryCount is the number of distinct stores with any price history
	// for this product/group. Zero when no history anywhere.
	StoreHistoryCount int    `json:"store_history_count"`
	CreatedAt         string `json:"created_at"`
}

// RegisterRoutes mounts shopping list endpoints onto the protected group.
func (h *ListHandler) RegisterRoutes(protected *echo.Group) {
	lists := protected.Group("/lists")
	lists.GET("", h.List)
	lists.POST("", h.Create)
	lists.GET("/:id", h.Get)
	lists.PUT("/:id", h.Update)
	lists.DELETE("/:id", h.Delete)
	lists.POST("/:id/items", h.AddItem)
	lists.POST("/:id/items/bulk", h.BulkAddItems)
	lists.PATCH("/:id/items/bulk", h.BulkUpdateItems)
	lists.DELETE("/:id/items/bulk", h.BulkDeleteItems)
	lists.PUT("/:id/items/:itemId", h.UpdateItem)
	lists.DELETE("/:id/items/:itemId", h.DeleteItem)
	lists.PUT("/:id/reorder", h.ReorderItems)

	// Phase 7: per-list optimistic lock endpoints.
	lists.POST("/:id/lock", h.AcquireLock)
	lists.POST("/:id/lock/heartbeat", h.TouchLock)
	lists.POST("/:id/lock/release", h.ReleaseLock)
	lists.POST("/:id/lock/takeover", h.TakeOverLock)
}

// List returns all shopping lists for the authenticated household with item counts.
// GET /api/v1/lists
func (h *ListHandler) List(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)

	rows, err := h.DB.Query(
		`SELECT sl.id, sl.household_id, sl.name, sl.created_by, sl.status,
		        sl.created_at, sl.updated_at,
		        (SELECT COUNT(*) FROM shopping_list_items WHERE list_id = sl.id) AS item_count,
		        (SELECT COUNT(*) FROM shopping_list_items WHERE list_id = sl.id AND checked = TRUE) AS checked_count
		 FROM shopping_lists sl
		 WHERE sl.household_id = ?
		 ORDER BY sl.updated_at DESC`,
		householdID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer rows.Close()

	lists := make([]listSummaryResponse, 0)
	for rows.Next() {
		var l listSummaryResponse
		if err := rows.Scan(&l.ID, &l.HouseholdID, &l.Name, &l.CreatedBy, &l.Status,
			&l.CreatedAt, &l.UpdatedAt, &l.ItemCount, &l.CheckedCount); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		lists = append(lists, l)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusOK, lists)
}

// Create adds a new shopping list.
// POST /api/v1/lists
func (h *ListHandler) Create(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	userID := auth.UserIDFrom(c)

	var req createListRequest
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
		`INSERT INTO shopping_lists (id, household_id, name, created_by, status, created_at, updated_at)
		 VALUES (lower(hex(randomblob(16))), ?, ?, ?, 'active', ?, ?)
		 RETURNING id`,
		householdID, req.Name, userID, now, now,
	).Scan(&id)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusCreated, listSummaryResponse{
		ID:          id,
		HouseholdID: householdID,
		Name:        req.Name,
		CreatedBy:   &userID,
		Status:      "active",
		ItemCount:   0,
		CheckedCount: 0,
		CreatedAt:   now,
		UpdatedAt:   now,
	})
}

// Get returns a shopping list with all items, including price estimates.
// GET /api/v1/lists/:id
func (h *ListHandler) Get(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	listID := c.Param("id")

	var resp listDetailResponse
	err := h.DB.QueryRow(
		`SELECT sl.id, sl.household_id, sl.name, sl.created_by, sl.status,
		        sl.created_at, sl.updated_at, sl.preferred_store_id, s.name
		 FROM shopping_lists sl
		 LEFT JOIN stores s ON sl.preferred_store_id = s.id
		 WHERE sl.id = ? AND sl.household_id = ?`,
		listID, householdID,
	).Scan(&resp.ID, &resp.HouseholdID, &resp.Name, &resp.CreatedBy, &resp.Status,
		&resp.CreatedAt, &resp.UpdatedAt, &resp.PreferredStoreID, &resp.PreferredStoreName)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "list not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// Fetch items with product name, group name, and latest price.
	rows, err := h.DB.Query(
		`SELECT sli.id, sli.list_id, sli.product_id, p.name,
		        sli.name, sli.quantity, sli.unit, sli.checked, sli.checked_by,
		        sli.sort_order, sli.notes, sli.created_at,
		        (SELECT pp.unit_price FROM product_prices pp
		         WHERE pp.product_id = sli.product_id
		         ORDER BY pp.receipt_date DESC LIMIT 1) AS estimated_price,
		        sli.product_group_id, pg.name AS product_group_name,
		        sli.assigned_store_id, ast.name AS assigned_store_name
		 FROM shopping_list_items sli
		 LEFT JOIN products p ON sli.product_id = p.id
		 LEFT JOIN product_groups pg ON sli.product_group_id = pg.id
		 LEFT JOIN stores ast ON sli.assigned_store_id = ast.id
		 WHERE sli.list_id = ?
		 ORDER BY sli.sort_order, sli.created_at`,
		listID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer rows.Close()

	resp.Items = make([]listItemResponse, 0)
	for rows.Next() {
		var item listItemResponse
		var estimatedPrice *float64
		var createdAt time.Time
		if err := rows.Scan(&item.ID, &item.ListID, &item.ProductID, &item.ProductName,
			&item.Name, &item.Quantity, &item.Unit, &item.Checked, &item.CheckedBy,
			&item.SortOrder, &item.Notes, &createdAt, &estimatedPrice,
			&item.ProductGroupID, &item.ProductGroupName,
			&item.AssignedStoreID, &item.AssignedStoreName); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		item.CreatedAt = createdAt.Format(time.RFC3339)
		if estimatedPrice != nil {
			s := fmt.Sprintf("%.2f", *estimatedPrice)
			item.EstimatedPrice = &s
		}
		resp.Items = append(resp.Items, item)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// Batch query: find cheapest store for all items with a product_id.
	productIDs := make([]string, 0)
	productIDSet := make(map[string]bool)
	groupIDs := make([]string, 0)
	groupIDSet := make(map[string]bool)
	for _, item := range resp.Items {
		if item.ProductID != nil && !productIDSet[*item.ProductID] {
			productIDs = append(productIDs, *item.ProductID)
			productIDSet[*item.ProductID] = true
		}
		if item.ProductGroupID != nil && !groupIDSet[*item.ProductGroupID] {
			groupIDs = append(groupIDs, *item.ProductGroupID)
			groupIDSet[*item.ProductGroupID] = true
		}
	}

	if len(productIDs) > 0 {
		// Build placeholders for IN clause.
		placeholders := make([]string, len(productIDs))
		args := make([]interface{}, len(productIDs))
		for i, pid := range productIDs {
			placeholders[i] = "?"
			args[i] = pid
		}

		cheapestRows, err := h.DB.Query(
			fmt.Sprintf(
				`SELECT sub.product_id, s.name, sub.unit_price
				 FROM (
				     SELECT pp.product_id, pp.store_id, pp.unit_price,
				            ROW_NUMBER() OVER (PARTITION BY pp.product_id ORDER BY pp.unit_price ASC) AS rn
				     FROM product_prices pp
				     WHERE pp.product_id IN (%s)
				       AND pp.receipt_date = (
				           SELECT MAX(pp2.receipt_date) FROM product_prices pp2
				           WHERE pp2.product_id = pp.product_id AND pp2.store_id = pp.store_id
				       )
				 ) sub
				 JOIN stores s ON sub.store_id = s.id
				 WHERE sub.rn = 1`,
				strings.Join(placeholders, ",")),
			args...,
		)
		if err == nil {
			defer cheapestRows.Close()
			type cheapestInfo struct {
				StoreName string
				Price     float64
			}
			cheapestMap := make(map[string]cheapestInfo)
			for cheapestRows.Next() {
				var productID, storeName string
				var price float64
				if cheapestRows.Scan(&productID, &storeName, &price) == nil {
					cheapestMap[productID] = cheapestInfo{StoreName: storeName, Price: price}
				}
			}

			for i, item := range resp.Items {
				if item.ProductID == nil {
					continue
				}
				if info, ok := cheapestMap[*item.ProductID]; ok {
					resp.Items[i].CheapestStore = &info.StoreName
					p := fmt.Sprintf("%.2f", info.Price)
					resp.Items[i].CheapestPrice = &p
				}
			}
		}
	}

	// Q1c: store-history count per product — distinct stores with any price row.
	// SQLite does not support COUNT(DISTINCT …) OVER in all versions, so run a
	// separate parameterized IN-query. Bounded by the number of distinct product_ids
	// already computed above.
	storeHistoryCountMap := map[string]int{}
	if len(productIDs) > 0 {
		placeholders := strings.Repeat("?,", len(productIDs))
		placeholders = placeholders[:len(placeholders)-1]
		q1c := fmt.Sprintf(`
			SELECT pp.product_id, COUNT(DISTINCT pp.store_id)
			FROM product_prices pp
			WHERE pp.product_id IN (%s)
			GROUP BY pp.product_id`, placeholders)
		args1c := make([]interface{}, len(productIDs))
		for i, id := range productIDs {
			args1c[i] = id
		}
		rows1c, err := h.DB.QueryContext(c.Request().Context(), q1c, args1c...)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to fetch store history counts")
		}
		defer rows1c.Close()
		for rows1c.Next() {
			var productID string
			var count int
			if err := rows1c.Scan(&productID, &count); err != nil {
				return echo.NewHTTPError(http.StatusInternalServerError, "failed to scan store history counts")
			}
			storeHistoryCountMap[productID] = count
		}
	}

	// Q1d: price at each item's assigned_store_id (per-row, not list-preferred).
	// Keyed on "productID|storeID" because multiple rows can assign different
	// stores to the same product. Bounded by distinct (product_id, assigned_store_id)
	// pairs on this list — matches the N+1 envelope the plan calls out.
	assignedStorePriceMap := map[string]string{}
	type assignedPair struct {
		ProductID string
		StoreID   string
	}
	assignedPairs := make([]assignedPair, 0)
	assignedPairSet := make(map[string]bool)
	for _, item := range resp.Items {
		if item.ProductID == nil || item.AssignedStoreID == nil {
			continue
		}
		key := *item.ProductID + "|" + *item.AssignedStoreID
		if assignedPairSet[key] {
			continue
		}
		assignedPairSet[key] = true
		assignedPairs = append(assignedPairs, assignedPair{ProductID: *item.ProductID, StoreID: *item.AssignedStoreID})
	}
	if len(assignedPairs) > 0 {
		// Emit (?,?) pairs. Uses the same "latest receipt_date per product+store"
		// correlation as the existing storePriceMap query.
		tuplePlaceholders := make([]string, len(assignedPairs))
		args1d := make([]interface{}, 0, len(assignedPairs)*2)
		for i, p := range assignedPairs {
			tuplePlaceholders[i] = "(?, ?)"
			args1d = append(args1d, p.ProductID, p.StoreID)
		}
		q1d := fmt.Sprintf(`
			SELECT pp.product_id, pp.store_id, PRINTF('%%.2f', pp.unit_price)
			FROM product_prices pp
			WHERE (pp.product_id, pp.store_id) IN (VALUES %s)
			  AND pp.receipt_date = (
			      SELECT MAX(pp2.receipt_date) FROM product_prices pp2
			      WHERE pp2.product_id = pp.product_id AND pp2.store_id = pp.store_id
			  )`, strings.Join(tuplePlaceholders, ","))
		rows1d, err := h.DB.QueryContext(c.Request().Context(), q1d, args1d...)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to fetch assigned store prices")
		}
		defer rows1d.Close()
		for rows1d.Next() {
			var productID, storeID, price string
			if err := rows1d.Scan(&productID, &storeID, &price); err != nil {
				return echo.NewHTTPError(http.StatusInternalServerError, "failed to scan assigned store prices")
			}
			assignedStorePriceMap[productID+"|"+storeID] = price
		}
	}

	// Q1b: preferred store price for items with a product_id.
	storePriceMap := map[string]string{}
	if resp.PreferredStoreID != nil && len(productIDs) > 0 {
		placeholders := strings.Repeat("?,", len(productIDs))
		placeholders = placeholders[:len(placeholders)-1]
		q1b := fmt.Sprintf(`
			SELECT pp.product_id, PRINTF('%%.2f', pp.unit_price)
			FROM product_prices pp
			WHERE pp.product_id IN (%s)
			  AND pp.store_id = ?
			  AND pp.receipt_date = (
			      SELECT MAX(pp2.receipt_date) FROM product_prices pp2
			      WHERE pp2.product_id = pp.product_id AND pp2.store_id = pp.store_id
			  )`, placeholders)
		args1b := make([]interface{}, len(productIDs)+1)
		for i, id := range productIDs {
			args1b[i] = id
		}
		args1b[len(productIDs)] = *resp.PreferredStoreID
		rows1b, err := h.DB.QueryContext(c.Request().Context(), q1b, args1b...)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to fetch store prices")
		}
		defer rows1b.Close()
		for rows1b.Next() {
			var productID, price string
			if err := rows1b.Scan(&productID, &price); err != nil {
				return echo.NewHTTPError(http.StatusInternalServerError, "failed to scan store prices")
			}
			storePriceMap[productID] = price
		}
	}

	// Batch query: find cheapest product+store for all items with a product_group_id.
	type groupPriceInfo struct {
		CheapestStore     string
		CheapestPrice     string
		CheapestProductID string
	}
	groupPriceMap := map[string]groupPriceInfo{}
	if len(groupIDs) > 0 {
		placeholders := strings.Repeat("?,", len(groupIDs))
		placeholders = placeholders[:len(placeholders)-1]
		q2 := fmt.Sprintf(`
			SELECT sub.product_group_id, sub.product_id, s.name, PRINTF('%%.2f', sub.unit_price)
			FROM (
				SELECT p.product_group_id, pp.product_id, pp.store_id, pp.unit_price,
				       ROW_NUMBER() OVER (
				           PARTITION BY p.product_group_id
				           ORDER BY pp.unit_price ASC
				       ) AS rn
				FROM product_prices pp
				JOIN products p ON p.id = pp.product_id
				WHERE p.product_group_id IN (%s)
				  AND pp.receipt_date = (
				      SELECT MAX(pp2.receipt_date) FROM product_prices pp2
				      WHERE pp2.product_id = pp.product_id AND pp2.store_id = pp.store_id
				  )
			) sub
			JOIN stores s ON sub.store_id = s.id
			WHERE sub.rn = 1`, placeholders)
		args2 := make([]interface{}, len(groupIDs))
		for i, id := range groupIDs {
			args2[i] = id
		}
		rows2, err := h.DB.QueryContext(c.Request().Context(), q2, args2...)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to fetch group prices")
		}
		defer rows2.Close()
		for rows2.Next() {
			var groupID, productID, storeName, price string
			if err := rows2.Scan(&groupID, &productID, &storeName, &price); err != nil {
				return echo.NewHTTPError(http.StatusInternalServerError, "failed to scan group prices")
			}
			groupPriceMap[groupID] = groupPriceInfo{
				CheapestStore:     storeName,
				CheapestPrice:     price,
				CheapestProductID: productID,
			}
		}
	}

	// Q2b: preferred store price for items with a product_group_id.
	groupStorePriceMap := map[string]string{}
	if resp.PreferredStoreID != nil && len(groupIDs) > 0 {
		placeholders := strings.Repeat("?,", len(groupIDs))
		placeholders = placeholders[:len(placeholders)-1]
		q2b := fmt.Sprintf(`
			SELECT p.product_group_id, PRINTF('%%.2f', MIN(pp.unit_price))
			FROM product_prices pp
			JOIN products p ON p.id = pp.product_id
			WHERE p.product_group_id IN (%s)
			  AND pp.store_id = ?
			  AND pp.receipt_date = (
			      SELECT MAX(pp2.receipt_date) FROM product_prices pp2
			      WHERE pp2.product_id = pp.product_id AND pp2.store_id = pp.store_id
			  )
			GROUP BY p.product_group_id`, placeholders)
		args2b := make([]interface{}, len(groupIDs)+1)
		for i, id := range groupIDs {
			args2b[i] = id
		}
		args2b[len(groupIDs)] = *resp.PreferredStoreID
		rows2b, err := h.DB.QueryContext(c.Request().Context(), q2b, args2b...)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to fetch group store prices")
		}
		defer rows2b.Close()
		for rows2b.Next() {
			var groupID, price string
			if err := rows2b.Scan(&groupID, &price); err != nil {
				return echo.NewHTTPError(http.StatusInternalServerError, "failed to scan group store prices")
			}
			groupStorePriceMap[groupID] = price
		}
	}

	// Q2c: store-history count per product_group — distinct stores across any
	// product that belongs to the group.
	groupStoreHistoryCountMap := map[string]int{}
	if len(groupIDs) > 0 {
		placeholders := strings.Repeat("?,", len(groupIDs))
		placeholders = placeholders[:len(placeholders)-1]
		q2c := fmt.Sprintf(`
			SELECT p.product_group_id, COUNT(DISTINCT pp.store_id)
			FROM product_prices pp
			JOIN products p ON p.id = pp.product_id
			WHERE p.product_group_id IN (%s)
			GROUP BY p.product_group_id`, placeholders)
		args2c := make([]interface{}, len(groupIDs))
		for i, id := range groupIDs {
			args2c[i] = id
		}
		rows2c, err := h.DB.QueryContext(c.Request().Context(), q2c, args2c...)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to fetch group store history counts")
		}
		defer rows2c.Close()
		for rows2c.Next() {
			var groupID string
			var count int
			if err := rows2c.Scan(&groupID, &count); err != nil {
				return echo.NewHTTPError(http.StatusInternalServerError, "failed to scan group store history counts")
			}
			groupStoreHistoryCountMap[groupID] = count
		}
	}

	// Q2d: price at each item's assigned_store_id for group-backed rows.
	// Mirrors groupStorePriceMap (MIN(unit_price) across products in the group
	// at that store, restricted to latest receipt_date per product+store), but
	// keyed on the per-row assigned_store_id rather than the list-preferred one.
	assignedGroupStorePriceMap := map[string]string{}
	type assignedGroupPair struct {
		GroupID string
		StoreID string
	}
	assignedGroupPairs := make([]assignedGroupPair, 0)
	assignedGroupPairSet := make(map[string]bool)
	for _, item := range resp.Items {
		if item.ProductGroupID == nil || item.AssignedStoreID == nil {
			continue
		}
		key := *item.ProductGroupID + "|" + *item.AssignedStoreID
		if assignedGroupPairSet[key] {
			continue
		}
		assignedGroupPairSet[key] = true
		assignedGroupPairs = append(assignedGroupPairs, assignedGroupPair{GroupID: *item.ProductGroupID, StoreID: *item.AssignedStoreID})
	}
	if len(assignedGroupPairs) > 0 {
		tuplePlaceholders := make([]string, len(assignedGroupPairs))
		args2d := make([]interface{}, 0, len(assignedGroupPairs)*2)
		for i, pair := range assignedGroupPairs {
			tuplePlaceholders[i] = "(?, ?)"
			args2d = append(args2d, pair.GroupID, pair.StoreID)
		}
		q2d := fmt.Sprintf(`
			SELECT p.product_group_id, pp.store_id, PRINTF('%%.2f', MIN(pp.unit_price))
			FROM product_prices pp
			JOIN products p ON p.id = pp.product_id
			WHERE (p.product_group_id, pp.store_id) IN (VALUES %s)
			  AND pp.receipt_date = (
			      SELECT MAX(pp2.receipt_date) FROM product_prices pp2
			      WHERE pp2.product_id = pp.product_id AND pp2.store_id = pp.store_id
			  )
			GROUP BY p.product_group_id, pp.store_id`, strings.Join(tuplePlaceholders, ","))
		rows2d, err := h.DB.QueryContext(c.Request().Context(), q2d, args2d...)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to fetch assigned group store prices")
		}
		defer rows2d.Close()
		for rows2d.Next() {
			var groupID, storeID, price string
			if err := rows2d.Scan(&groupID, &storeID, &price); err != nil {
				return echo.NewHTTPError(http.StatusInternalServerError, "failed to scan assigned group store prices")
			}
			assignedGroupStorePriceMap[groupID+"|"+storeID] = price
		}
	}

	// Apply group price info to items, then store price info.
	for i, item := range resp.Items {
		if item.ProductGroupID != nil {
			if info, ok := groupPriceMap[*item.ProductGroupID]; ok {
				resp.Items[i].CheapestStore = &info.CheapestStore
				resp.Items[i].CheapestPrice = &info.CheapestPrice
				resp.Items[i].CheapestProductID = &info.CheapestProductID
				resp.Items[i].EstimatedPrice = &info.CheapestPrice
			}
			if price, ok := groupStorePriceMap[*item.ProductGroupID]; ok {
				p := price
				resp.Items[i].StorePrice = &p
				resp.Items[i].StorePriceStore = resp.PreferredStoreName
			}
			if count, ok := groupStoreHistoryCountMap[*item.ProductGroupID]; ok {
				resp.Items[i].StoreHistoryCount = count
			}
			if item.AssignedStoreID != nil {
				if price, ok := assignedGroupStorePriceMap[*item.ProductGroupID+"|"+*item.AssignedStoreID]; ok {
					p := price
					resp.Items[i].AssignedStorePrice = &p
				}
			}
		}
		if item.ProductID != nil {
			if price, ok := storePriceMap[*item.ProductID]; ok {
				p := price
				resp.Items[i].StorePrice = &p
				resp.Items[i].StorePriceStore = resp.PreferredStoreName
			}
			if count, ok := storeHistoryCountMap[*item.ProductID]; ok {
				resp.Items[i].StoreHistoryCount = count
			}
			if item.AssignedStoreID != nil {
				if price, ok := assignedStorePriceMap[*item.ProductID+"|"+*item.AssignedStoreID]; ok {
					p := price
					resp.Items[i].AssignedStorePrice = &p
				}
			}
		}
	}

	return c.JSON(http.StatusOK, resp)
}

// Update modifies a shopping list (name, status).
// PUT /api/v1/lists/:id
func (h *ListHandler) Update(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	listID := c.Param("id")

	var req updateListRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	setClauses := make([]string, 0)
	args := make([]interface{}, 0)

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "name cannot be empty"})
		}
		setClauses = append(setClauses, "name = ?")
		args = append(args, name)
	}
	if req.Status != nil {
		status := *req.Status
		if status != "active" && status != "completed" && status != "archived" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "status must be active, completed, or archived"})
		}
		setClauses = append(setClauses, "status = ?")
		args = append(args, status)
	}
	if req.PreferredStoreID != nil {
		if *req.PreferredStoreID == "" {
			setClauses = append(setClauses, "preferred_store_id = NULL")
		} else {
			setClauses = append(setClauses, "preferred_store_id = ?")
			args = append(args, *req.PreferredStoreID)
		}
	}

	if len(setClauses) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "no fields to update"})
	}

	now := time.Now().UTC()
	setClauses = append(setClauses, "updated_at = ?")
	args = append(args, now)
	args = append(args, listID, householdID)

	query := fmt.Sprintf(
		"UPDATE shopping_lists SET %s WHERE id = ? AND household_id = ?",
		strings.Join(setClauses, ", "),
	)

	if err := h.requireLock(c, listID); err != nil {
		return err
	}

	result, err := h.DB.Exec(query, args...)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "list not found"})
	}

	var resp listSummaryResponse
	err = h.DB.QueryRow(
		`SELECT sl.id, sl.household_id, sl.name, sl.created_by, sl.status,
		        sl.created_at, sl.updated_at,
		        (SELECT COUNT(*) FROM shopping_list_items WHERE list_id = sl.id) AS item_count,
		        (SELECT COUNT(*) FROM shopping_list_items WHERE list_id = sl.id AND checked = TRUE) AS checked_count
		 FROM shopping_lists sl WHERE sl.id = ?`,
		listID,
	).Scan(&resp.ID, &resp.HouseholdID, &resp.Name, &resp.CreatedBy, &resp.Status,
		&resp.CreatedAt, &resp.UpdatedAt, &resp.ItemCount, &resp.CheckedCount)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusOK, resp)
}

// Delete removes a shopping list (CASCADE deletes items).
// DELETE /api/v1/lists/:id
func (h *ListHandler) Delete(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	listID := c.Param("id")

	result, err := h.DB.Exec(
		"DELETE FROM shopping_lists WHERE id = ? AND household_id = ?",
		listID, householdID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "list not found"})
	}

	return c.NoContent(http.StatusNoContent)
}

// insertListItem inserts a single shopping list item into listID on behalf of
// householdID, validating product/group cross-household ownership. It does NOT
// verify that listID belongs to householdID (the caller is expected to have
// checked that already), does NOT touch shopping_lists.updated_at, and does
// NOT broadcast any WebSocket event — those are the caller's responsibility
// so the same helper can be used inside a transaction for bulk inserts.
//
// Validation errors are returned as *echo.HTTPError with 400; unexpected DB
// errors are returned as plain errors.
func insertListItem(ctx context.Context, tx DBTX, householdID, listID string, req createListItemRequest) (listItemResponse, error) {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return listItemResponse{}, echo.NewHTTPError(http.StatusBadRequest, "name is required")
	}
	if req.ProductID != nil && req.ProductGroupID != nil {
		return listItemResponse{}, echo.NewHTTPError(http.StatusBadRequest, "cannot set both product_id and product_group_id")
	}

	// Validate product ownership if set.
	if req.ProductID != nil && *req.ProductID != "" {
		var one int
		err := tx.QueryRowContext(ctx,
			`SELECT 1 FROM products WHERE id = ? AND household_id = ?`,
			*req.ProductID, householdID,
		).Scan(&one)
		if err == sql.ErrNoRows {
			return listItemResponse{}, echo.NewHTTPError(http.StatusBadRequest, "invalid product")
		}
		if err != nil {
			return listItemResponse{}, fmt.Errorf("verify product ownership: %w", err)
		}
	}

	// Validate group ownership if set.
	if req.ProductGroupID != nil && *req.ProductGroupID != "" {
		var groupHouseholdID string
		err := tx.QueryRowContext(ctx,
			`SELECT household_id FROM product_groups WHERE id = ?`,
			*req.ProductGroupID,
		).Scan(&groupHouseholdID)
		if err == sql.ErrNoRows || (err == nil && groupHouseholdID != householdID) {
			return listItemResponse{}, echo.NewHTTPError(http.StatusBadRequest, "invalid product group")
		}
		if err != nil && err != sql.ErrNoRows {
			return listItemResponse{}, fmt.Errorf("verify product_group ownership: %w", err)
		}
	}

	// Validate assigned store ownership if set.
	if req.AssignedStoreID != nil && *req.AssignedStoreID != "" {
		var storeHouseholdID string
		err := tx.QueryRowContext(ctx,
			`SELECT household_id FROM stores WHERE id = ?`,
			*req.AssignedStoreID,
		).Scan(&storeHouseholdID)
		if err == sql.ErrNoRows || (err == nil && storeHouseholdID != householdID) {
			return listItemResponse{}, echo.NewHTTPError(http.StatusBadRequest, "invalid assigned store")
		}
		if err != nil && err != sql.ErrNoRows {
			return listItemResponse{}, fmt.Errorf("verify assigned_store ownership: %w", err)
		}
	}

	quantity := "1"
	if req.Quantity != nil {
		quantity = *req.Quantity
	}

	// Normalize empty-string assigned_store_id to nil so it's stored as NULL.
	var assignedStoreID *string
	if req.AssignedStoreID != nil && *req.AssignedStoreID != "" {
		assignedStoreID = req.AssignedStoreID
	}

	now := time.Now().UTC()
	var id string
	err := tx.QueryRowContext(ctx,
		`INSERT INTO shopping_list_items (id, list_id, product_id, product_group_id, name, quantity, unit, notes, assigned_store_id, created_at)
		 VALUES (lower(hex(randomblob(16))), ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 RETURNING id`,
		listID, req.ProductID, req.ProductGroupID, req.Name, quantity, req.Unit, req.Notes, assignedStoreID, now,
	).Scan(&id)
	if err != nil {
		return listItemResponse{}, fmt.Errorf("insert shopping_list_item: %w", err)
	}

	return listItemResponse{
		ID:              id,
		ListID:          listID,
		ProductID:       req.ProductID,
		ProductGroupID:  req.ProductGroupID,
		Name:            req.Name,
		Quantity:        quantity,
		Unit:            req.Unit,
		Checked:         false,
		Notes:           req.Notes,
		AssignedStoreID: assignedStoreID,
		CreatedAt:       now.Format(time.RFC3339),
	}, nil
}

// AddItem adds an item to a shopping list.
// POST /api/v1/lists/:id/items
func (h *ListHandler) AddItem(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	listID := c.Param("id")
	ctx := c.Request().Context()

	// Verify list belongs to household.
	var exists int
	err := h.DB.QueryRow(
		"SELECT 1 FROM shopping_lists WHERE id = ? AND household_id = ?",
		listID, householdID,
	).Scan(&exists)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "list not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	if err := h.requireLock(c, listID); err != nil {
		return err
	}

	var req createListItemRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	item, err := insertListItem(ctx, h.DB, householdID, listID, req)
	if err != nil {
		if he, ok := err.(*echo.HTTPError); ok {
			return c.JSON(he.Code, map[string]string{"error": fmt.Sprint(he.Message)})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// Touch list updated_at (best-effort, matches pre-refactor behavior).
	now := time.Now().UTC()
	h.DB.Exec("UPDATE shopping_lists SET updated_at = ? WHERE id = ?", now, listID)

	// Broadcast WebSocket event.
	h.Hub.Broadcast(ws.Message{
		Type:      ws.EventListItemAdded,
		Household: householdID,
		Payload: map[string]interface{}{
			"list_id": listID,
			"item":    item,
		},
	})

	return c.JSON(http.StatusCreated, item)
}

// --- Bulk add ---

type bulkAddItemsRequest struct {
	Items []createListItemRequest `json:"items"`
}

type bulkAddItemsResponse struct {
	Items []listItemResponse `json:"items"`
	List  listDetailResponse `json:"list"`
}

// BulkAddItems adds up to 100 items to a shopping list in a single transaction.
// POST /api/v1/lists/:id/items/bulk
func (h *ListHandler) BulkAddItems(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	listID := c.Param("id")
	ctx := c.Request().Context()

	// Verify list belongs to household.
	var exists int
	err := h.DB.QueryRowContext(ctx,
		"SELECT 1 FROM shopping_lists WHERE id = ? AND household_id = ?",
		listID, householdID,
	).Scan(&exists)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "list not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	if err := h.requireLock(c, listID); err != nil {
		return err
	}

	var req bulkAddItemsRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	if len(req.Items) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "items is required"})
	}
	if len(req.Items) > 100 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "cannot add more than 100 items in one request"})
	}

	// --- Batch ownership preflight (outside transaction). ---
	productIDs := make([]string, 0, len(req.Items))
	productIDSet := make(map[string]bool)
	groupIDs := make([]string, 0, len(req.Items))
	groupIDSet := make(map[string]bool)
	storeIDs := make([]string, 0, len(req.Items))
	storeIDSet := make(map[string]bool)
	for _, it := range req.Items {
		if it.ProductID != nil && *it.ProductID != "" && !productIDSet[*it.ProductID] {
			productIDs = append(productIDs, *it.ProductID)
			productIDSet[*it.ProductID] = true
		}
		if it.ProductGroupID != nil && *it.ProductGroupID != "" && !groupIDSet[*it.ProductGroupID] {
			groupIDs = append(groupIDs, *it.ProductGroupID)
			groupIDSet[*it.ProductGroupID] = true
		}
		if it.AssignedStoreID != nil && *it.AssignedStoreID != "" && !storeIDSet[*it.AssignedStoreID] {
			storeIDs = append(storeIDs, *it.AssignedStoreID)
			storeIDSet[*it.AssignedStoreID] = true
		}
	}

	if len(productIDs) > 0 {
		found, err := selectIDsInHousehold(ctx, h.DB, "products", productIDs, householdID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		missing := missingIDs(productIDs, found)
		if len(missing) > 0 {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("unknown product_id(s): %s", strings.Join(missing, ",")),
			})
		}
	}
	if len(groupIDs) > 0 {
		found, err := selectIDsInHousehold(ctx, h.DB, "product_groups", groupIDs, householdID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		missing := missingIDs(groupIDs, found)
		if len(missing) > 0 {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("unknown product_group_id(s): %s", strings.Join(missing, ",")),
			})
		}
	}
	if len(storeIDs) > 0 {
		found, err := selectIDsInHousehold(ctx, h.DB, "stores", storeIDs, householdID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		missing := missingIDs(storeIDs, found)
		if len(missing) > 0 {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("unknown assigned_store_id(s): %s", strings.Join(missing, ",")),
			})
		}
	}

	// --- Transaction: insert items. ---
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()

	inserted := make([]listItemResponse, 0, len(req.Items))
	for i, itemReq := range req.Items {
		item, err := insertListItem(ctx, tx, householdID, listID, itemReq)
		if err != nil {
			if he, ok := err.(*echo.HTTPError); ok {
				return c.JSON(he.Code, map[string]string{
					"error": fmt.Sprintf("item %d: %v", i, he.Message),
				})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		inserted = append(inserted, item)
	}

	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, "UPDATE shopping_lists SET updated_at = ? WHERE id = ?", now, listID); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	if err := tx.Commit(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to commit"})
	}
	committed = true

	// --- Broadcast one event per inserted item (consistent with AddItem). ---
	for _, item := range inserted {
		h.Hub.Broadcast(ws.Message{
			Type:      ws.EventListItemAdded,
			Household: householdID,
			Payload: map[string]interface{}{
				"list_id": listID,
				"item":    item,
			},
		})
	}

	// --- Build updated list resource (reuses Get's shape). ---
	listResp, err := h.loadListDetail(ctx, householdID, listID)
	if err != nil {
		// Still return the inserted items; the client can refetch the list.
		return c.JSON(http.StatusCreated, bulkAddItemsResponse{
			Items: inserted,
			List:  listDetailResponse{ID: listID, HouseholdID: householdID},
		})
	}

	return c.JSON(http.StatusCreated, bulkAddItemsResponse{
		Items: inserted,
		List:  listResp,
	})
}

// selectIDsInHousehold returns the subset of ids present in the given table
// that belong to householdID. Table name is a trusted literal — only callers
// in this package pass it.
func selectIDsInHousehold(ctx context.Context, db DBTX, table string, ids []string, householdID string) (map[string]bool, error) {
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]interface{}, 0, len(ids)+1)
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, householdID)
	q := fmt.Sprintf("SELECT id FROM %s WHERE id IN (%s) AND household_id = ?", table, placeholders)
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found := make(map[string]bool, len(ids))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		found[id] = true
	}
	return found, rows.Err()
}

// missingIDs returns the ids not present in the found set, preserving order.
func missingIDs(ids []string, found map[string]bool) []string {
	missing := make([]string, 0)
	for _, id := range ids {
		if !found[id] {
			missing = append(missing, id)
		}
	}
	return missing
}

// loadListDetail returns a listDetailResponse for listID in householdID. It
// mirrors the shape returned by Get but without the extensive price JOINs;
// callers who need prices should re-fetch via GET. This keeps the bulk path
// fast and avoids duplicating Get's price-enrichment logic.
func (h *ListHandler) loadListDetail(ctx context.Context, householdID, listID string) (listDetailResponse, error) {
	var resp listDetailResponse
	err := h.DB.QueryRowContext(ctx,
		`SELECT sl.id, sl.household_id, sl.name, sl.created_by, sl.status,
		        sl.created_at, sl.updated_at, sl.preferred_store_id, s.name
		 FROM shopping_lists sl
		 LEFT JOIN stores s ON sl.preferred_store_id = s.id
		 WHERE sl.id = ? AND sl.household_id = ?`,
		listID, householdID,
	).Scan(&resp.ID, &resp.HouseholdID, &resp.Name, &resp.CreatedBy, &resp.Status,
		&resp.CreatedAt, &resp.UpdatedAt, &resp.PreferredStoreID, &resp.PreferredStoreName)
	if err != nil {
		return resp, err
	}

	rows, err := h.DB.QueryContext(ctx,
		`SELECT sli.id, sli.list_id, sli.product_id, p.name,
		        sli.name, sli.quantity, sli.unit, sli.checked, sli.checked_by,
		        sli.sort_order, sli.notes, sli.created_at,
		        sli.product_group_id, pg.name AS product_group_name,
		        sli.assigned_store_id, ast.name AS assigned_store_name
		 FROM shopping_list_items sli
		 LEFT JOIN products p ON sli.product_id = p.id
		 LEFT JOIN product_groups pg ON sli.product_group_id = pg.id
		 LEFT JOIN stores ast ON sli.assigned_store_id = ast.id
		 WHERE sli.list_id = ?
		 ORDER BY sli.sort_order, sli.created_at`,
		listID,
	)
	if err != nil {
		return resp, err
	}
	defer rows.Close()

	resp.Items = make([]listItemResponse, 0)
	for rows.Next() {
		var item listItemResponse
		var createdAt time.Time
		if err := rows.Scan(&item.ID, &item.ListID, &item.ProductID, &item.ProductName,
			&item.Name, &item.Quantity, &item.Unit, &item.Checked, &item.CheckedBy,
			&item.SortOrder, &item.Notes, &createdAt,
			&item.ProductGroupID, &item.ProductGroupName,
			&item.AssignedStoreID, &item.AssignedStoreName); err != nil {
			return resp, err
		}
		item.CreatedAt = createdAt.Format(time.RFC3339)
		resp.Items = append(resp.Items, item)
	}
	return resp, rows.Err()
}

// UpdateItem modifies a shopping list item.
// PUT /api/v1/lists/:id/items/:itemId
func (h *ListHandler) UpdateItem(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	listID := c.Param("id")
	itemID := c.Param("itemId")

	// Verify list belongs to household.
	var exists int
	err := h.DB.QueryRow(
		"SELECT 1 FROM shopping_lists WHERE id = ? AND household_id = ?",
		listID, householdID,
	).Scan(&exists)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "list not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	if err := h.requireLock(c, listID); err != nil {
		return err
	}

	var req updateListItemRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	setClauses := make([]string, 0)
	args := make([]interface{}, 0)
	isCheckUpdate := false

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "name cannot be empty"})
		}
		setClauses = append(setClauses, "name = ?")
		args = append(args, name)
	}
	if req.ProductID != nil && req.ProductGroupID != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "cannot set both product_id and product_group_id"})
	}
	if req.ProductID != nil {
		setClauses = append(setClauses, "product_id = ?")
		args = append(args, *req.ProductID)
		// Clear group when setting a specific product.
		setClauses = append(setClauses, "product_group_id = NULL")
	}
	if req.ProductGroupID != nil {
		setClauses = append(setClauses, "product_group_id = ?")
		args = append(args, *req.ProductGroupID)
		// Clear product_id when setting a group.
		setClauses = append(setClauses, "product_id = NULL")
	}
	if req.Quantity != nil {
		setClauses = append(setClauses, "quantity = ?")
		args = append(args, *req.Quantity)
	}
	if req.Unit != nil {
		setClauses = append(setClauses, "unit = ?")
		args = append(args, *req.Unit)
	}
	if req.Checked != nil {
		setClauses = append(setClauses, "checked = ?")
		args = append(args, *req.Checked)
		isCheckUpdate = true
	}
	if req.CheckedBy != nil {
		setClauses = append(setClauses, "checked_by = ?")
		args = append(args, *req.CheckedBy)
	}
	if req.Notes != nil {
		setClauses = append(setClauses, "notes = ?")
		args = append(args, *req.Notes)
	}
	if req.SortOrder != nil {
		setClauses = append(setClauses, "sort_order = ?")
		args = append(args, *req.SortOrder)
	}
	if req.AssignedStoreID != nil {
		if *req.AssignedStoreID == "" {
			setClauses = append(setClauses, "assigned_store_id = NULL")
		} else {
			// Validate store ownership.
			var storeHouseholdID string
			err := h.DB.QueryRow(
				`SELECT household_id FROM stores WHERE id = ?`,
				*req.AssignedStoreID,
			).Scan(&storeHouseholdID)
			if err == sql.ErrNoRows || (err == nil && storeHouseholdID != householdID) {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid assigned store"})
			}
			if err != nil && err != sql.ErrNoRows {
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
			}
			setClauses = append(setClauses, "assigned_store_id = ?")
			args = append(args, *req.AssignedStoreID)
		}
	}

	if len(setClauses) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "no fields to update"})
	}

	args = append(args, itemID, listID)
	query := fmt.Sprintf(
		"UPDATE shopping_list_items SET %s WHERE id = ? AND list_id = ?",
		strings.Join(setClauses, ", "),
	)

	result, err := h.DB.Exec(query, args...)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "item not found"})
	}

	// Touch list updated_at.
	now := time.Now().UTC()
	h.DB.Exec("UPDATE shopping_lists SET updated_at = ? WHERE id = ?", now, listID)

	// Read back the updated item for response and broadcast.
	var item listItemResponse
	var createdAt time.Time
	err = h.DB.QueryRow(
		`SELECT sli.id, sli.list_id, sli.product_id, p.name,
		        sli.name, sli.quantity, sli.unit, sli.checked, sli.checked_by,
		        sli.sort_order, sli.notes, sli.created_at,
		        sli.product_group_id, pg.name AS product_group_name,
		        sli.assigned_store_id, ast.name AS assigned_store_name
		 FROM shopping_list_items sli
		 LEFT JOIN products p ON sli.product_id = p.id
		 LEFT JOIN product_groups pg ON sli.product_group_id = pg.id
		 LEFT JOIN stores ast ON sli.assigned_store_id = ast.id
		 WHERE sli.id = ?`,
		itemID,
	).Scan(&item.ID, &item.ListID, &item.ProductID, &item.ProductName,
		&item.Name, &item.Quantity, &item.Unit, &item.Checked, &item.CheckedBy,
		&item.SortOrder, &item.Notes, &createdAt,
		&item.ProductGroupID, &item.ProductGroupName,
		&item.AssignedStoreID, &item.AssignedStoreName)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	item.CreatedAt = createdAt.Format(time.RFC3339)

	// Broadcast appropriate WebSocket event.
	if isCheckUpdate {
		h.Hub.Broadcast(ws.Message{
			Type:      ws.EventListItemChecked,
			Household: householdID,
			Payload: map[string]interface{}{
				"list_id":    listID,
				"item_id":    itemID,
				"checked":    item.Checked,
				"checked_by": item.CheckedBy,
			},
		})
	} else {
		h.Hub.Broadcast(ws.Message{
			Type:      ws.EventListItemUpdated,
			Household: householdID,
			Payload: map[string]interface{}{
				"list_id": listID,
				"item":    item,
			},
		})
	}

	return c.JSON(http.StatusOK, item)
}

// DeleteItem removes an item from a shopping list.
// DELETE /api/v1/lists/:id/items/:itemId
func (h *ListHandler) DeleteItem(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	listID := c.Param("id")
	itemID := c.Param("itemId")

	// Verify list belongs to household.
	var exists int
	err := h.DB.QueryRow(
		"SELECT 1 FROM shopping_lists WHERE id = ? AND household_id = ?",
		listID, householdID,
	).Scan(&exists)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "list not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	if err := h.requireLock(c, listID); err != nil {
		return err
	}

	result, err := h.DB.Exec(
		"DELETE FROM shopping_list_items WHERE id = ? AND list_id = ?",
		itemID, listID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "item not found"})
	}

	// Touch list updated_at.
	now := time.Now().UTC()
	h.DB.Exec("UPDATE shopping_lists SET updated_at = ? WHERE id = ?", now, listID)

	// Broadcast WebSocket event.
	h.Hub.Broadcast(ws.Message{
		Type:      ws.EventListItemRemoved,
		Household: householdID,
		Payload: map[string]interface{}{
			"list_id": listID,
			"item_id": itemID,
		},
	})

	return c.NoContent(http.StatusNoContent)
}

// ReorderItems updates sort_order for multiple items in a single transaction.
// PUT /api/v1/lists/:id/reorder
func (h *ListHandler) ReorderItems(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	listID := c.Param("id")

	// Verify list belongs to household.
	var exists int
	err := h.DB.QueryRow(
		"SELECT 1 FROM shopping_lists WHERE id = ? AND household_id = ?",
		listID, householdID,
	).Scan(&exists)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "list not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	if err := h.requireLock(c, listID); err != nil {
		return err
	}

	var req reorderListItemsRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	if len(req.Items) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "items array is required"})
	}

	tx, err := h.DB.Begin()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer tx.Rollback()

	for _, entry := range req.Items {
		_, err := tx.Exec(
			"UPDATE shopping_list_items SET sort_order = ? WHERE id = ? AND list_id = ?",
			entry.SortOrder, entry.ID, listID,
		)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
	}

	now := time.Now().UTC()
	tx.Exec("UPDATE shopping_lists SET updated_at = ? WHERE id = ?", now, listID)

	if err := tx.Commit(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to commit"})
	}

	return c.NoContent(http.StatusNoContent)
}

// --- Bulk update ---

// bulkUpdateItemsRequest is the request body for PATCH /lists/:id/items/bulk.
// Patch is a free-form object so we can reject unknown fields explicitly with
// a 400 (rather than silently ignoring them). Supported fields: "assigned_store_id"
// (string|null — null/empty clears) and "checked" (bool).
type bulkUpdateItemsRequest struct {
	ItemIDs []string               `json:"item_ids"`
	Patch   map[string]interface{} `json:"patch"`
}

// BulkUpdateItems applies a small set of patch fields to multiple list items
// in a single transaction and broadcasts one WS event after commit.
//
// PATCH /api/v1/lists/:id/items/bulk
func (h *ListHandler) BulkUpdateItems(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	listID := c.Param("id")
	ctx := c.Request().Context()

	// Verify list belongs to household.
	var exists int
	err := h.DB.QueryRowContext(ctx,
		"SELECT 1 FROM shopping_lists WHERE id = ? AND household_id = ?",
		listID, householdID,
	).Scan(&exists)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "list not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	if err := h.requireLock(c, listID); err != nil {
		return err
	}

	var req bulkUpdateItemsRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	if len(req.ItemIDs) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "item_ids is required"})
	}
	if len(req.ItemIDs) > 500 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "cannot update more than 500 items in one request"})
	}
	if len(req.Patch) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "patch is required"})
	}

	// --- Validate patch fields. Only "assigned_store_id" and "checked" are
	// supported in phase 1; any other key must 400. ---
	setClauses := make([]string, 0, len(req.Patch))
	patchArgs := make([]interface{}, 0, len(req.Patch))
	var storeIDToValidate *string

	for key, raw := range req.Patch {
		switch key {
		case "assigned_store_id":
			if raw == nil {
				setClauses = append(setClauses, "assigned_store_id = NULL")
				continue
			}
			s, ok := raw.(string)
			if !ok {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "assigned_store_id must be a string or null"})
			}
			if s == "" {
				setClauses = append(setClauses, "assigned_store_id = NULL")
				continue
			}
			storeIDToValidate = &s
			setClauses = append(setClauses, "assigned_store_id = ?")
			patchArgs = append(patchArgs, s)
		case "checked":
			b, ok := raw.(bool)
			if !ok {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "checked must be a boolean"})
			}
			setClauses = append(setClauses, "checked = ?")
			patchArgs = append(patchArgs, b)
		default:
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("unsupported patch field: %s", key),
			})
		}
	}

	// Validate store_id belongs to household (outside tx; cheap).
	if storeIDToValidate != nil {
		found, err := selectIDsInHousehold(ctx, h.DB, "stores", []string{*storeIDToValidate}, householdID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		if !found[*storeIDToValidate] {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("unknown assigned_store_id: %s", *storeIDToValidate),
			})
		}
	}

	// Validate every item_id belongs to the list.
	placeholders := strings.Repeat("?,", len(req.ItemIDs))
	placeholders = placeholders[:len(placeholders)-1]
	validateArgs := make([]interface{}, 0, len(req.ItemIDs)+1)
	for _, id := range req.ItemIDs {
		validateArgs = append(validateArgs, id)
	}
	validateArgs = append(validateArgs, listID)

	rows, err := h.DB.QueryContext(ctx,
		fmt.Sprintf("SELECT id FROM shopping_list_items WHERE id IN (%s) AND list_id = ?", placeholders),
		validateArgs...,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer rows.Close()
	foundItems := make(map[string]bool, len(req.ItemIDs))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		foundItems[id] = true
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	missing := missingIDs(req.ItemIDs, foundItems)
	if len(missing) > 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("unknown item_id(s): %s", strings.Join(missing, ",")),
		})
	}

	// --- Transaction: apply the patch to every item. ---
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()

	// Build the final UPDATE statement. We apply the same patch to every
	// item in one statement using IN (...).
	updateArgs := make([]interface{}, 0, len(patchArgs)+len(req.ItemIDs)+1)
	updateArgs = append(updateArgs, patchArgs...)
	for _, id := range req.ItemIDs {
		updateArgs = append(updateArgs, id)
	}
	updateArgs = append(updateArgs, listID)

	query := fmt.Sprintf(
		"UPDATE shopping_list_items SET %s WHERE id IN (%s) AND list_id = ?",
		strings.Join(setClauses, ", "),
		placeholders,
	)
	if _, err := tx.ExecContext(ctx, query, updateArgs...); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, "UPDATE shopping_lists SET updated_at = ? WHERE id = ?", now, listID); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	if err := tx.Commit(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to commit"})
	}
	committed = true

	// Broadcast one WS message so subscribers refetch the list detail.
	h.Hub.Broadcast(ws.Message{
		Type:      ws.EventListItemsBulkUpdated,
		Household: householdID,
		Payload: map[string]interface{}{
			"list_id":  listID,
			"item_ids": req.ItemIDs,
		},
	})

	return c.JSON(http.StatusOK, map[string]interface{}{
		"list_id":  listID,
		"item_ids": req.ItemIDs,
	})
}

// --- Bulk delete ---

// bulkDeleteItemsRequest is the request body for DELETE /lists/:id/items/bulk.
type bulkDeleteItemsRequest struct {
	ItemIDs []string `json:"item_ids"`
}

// BulkDeleteItems removes multiple list items in a single transaction and
// broadcasts one WS event after commit. Mirrors the validation and
// transaction pattern of BulkUpdateItems.
//
// DELETE /api/v1/lists/:id/items/bulk
func (h *ListHandler) BulkDeleteItems(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	listID := c.Param("id")
	ctx := c.Request().Context()

	// Verify list belongs to household.
	var exists int
	err := h.DB.QueryRowContext(ctx,
		"SELECT 1 FROM shopping_lists WHERE id = ? AND household_id = ?",
		listID, householdID,
	).Scan(&exists)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "list not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	var req bulkDeleteItemsRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	if len(req.ItemIDs) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "item_ids is required"})
	}
	if len(req.ItemIDs) > 500 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "cannot delete more than 500 items in one request"})
	}

	// Validate every item_id belongs to the list.
	placeholders := strings.Repeat("?,", len(req.ItemIDs))
	placeholders = placeholders[:len(placeholders)-1]
	validateArgs := make([]interface{}, 0, len(req.ItemIDs)+1)
	for _, id := range req.ItemIDs {
		validateArgs = append(validateArgs, id)
	}
	validateArgs = append(validateArgs, listID)

	rows, err := h.DB.QueryContext(ctx,
		fmt.Sprintf("SELECT id FROM shopping_list_items WHERE id IN (%s) AND list_id = ?", placeholders),
		validateArgs...,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer rows.Close()
	foundItems := make(map[string]bool, len(req.ItemIDs))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		foundItems[id] = true
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	missing := missingIDs(req.ItemIDs, foundItems)
	if len(missing) > 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("unknown item_id(s): %s", strings.Join(missing, ",")),
		})
	}

	// --- Transaction: delete every item in one statement. ---
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()

	deleteArgs := make([]interface{}, 0, len(req.ItemIDs)+1)
	for _, id := range req.ItemIDs {
		deleteArgs = append(deleteArgs, id)
	}
	deleteArgs = append(deleteArgs, listID)

	query := fmt.Sprintf(
		"DELETE FROM shopping_list_items WHERE id IN (%s) AND list_id = ?",
		placeholders,
	)
	if _, err := tx.ExecContext(ctx, query, deleteArgs...); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, "UPDATE shopping_lists SET updated_at = ? WHERE id = ?", now, listID); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	if err := tx.Commit(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to commit"})
	}
	committed = true

	// Broadcast one WS message so subscribers refetch the list detail.
	h.Hub.Broadcast(ws.Message{
		Type:      ws.EventListItemsBulkRemoved,
		Household: householdID,
		Payload: map[string]interface{}{
			"list_id":  listID,
			"item_ids": req.ItemIDs,
		},
	})

	return c.JSON(http.StatusOK, map[string]interface{}{
		"list_id":  listID,
		"item_ids": req.ItemIDs,
	})
}
