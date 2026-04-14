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

// StoreHandler holds dependencies for store-related endpoints.
type StoreHandler struct {
	DB  *sql.DB
	Cfg *config.Config
}

// --- Request types ---

type createStoreRequest struct {
	Name string  `json:"name"`
	Icon *string `json:"icon,omitempty"`
}

type updateStoreRequest struct {
	Name     string  `json:"name"`
	Icon     *string `json:"icon,omitempty"`
	Nickname *string `json:"nickname,omitempty"`
	Address  *string `json:"address,omitempty"`
	City     *string `json:"city,omitempty"`
	State    *string `json:"state,omitempty"`
	Zip      *string `json:"zip,omitempty"`
}

type reorderStoreItem struct {
	ID           string `json:"id"`
	DisplayOrder int    `json:"display_order"`
}

type reorderStoresRequest struct {
	Stores []reorderStoreItem `json:"stores"`
}

// --- Response types ---

type storeResponse struct {
	ID           string    `json:"id"`
	HouseholdID  string    `json:"household_id"`
	Name         string    `json:"name"`
	DisplayOrder int       `json:"display_order"`
	Icon         *string   `json:"icon,omitempty"`
	Address      *string   `json:"address,omitempty"`
	City         *string   `json:"city,omitempty"`
	State        *string   `json:"state,omitempty"`
	Zip          *string   `json:"zip,omitempty"`
	StoreNumber  *string   `json:"store_number,omitempty"`
	Nickname     *string   `json:"nickname,omitempty"`
	Latitude     *float64  `json:"latitude,omitempty"`
	Longitude    *float64  `json:"longitude,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// RegisterRoutes mounts store endpoints onto the protected group.
func (h *StoreHandler) RegisterRoutes(protected *echo.Group) {
	stores := protected.Group("/stores")
	stores.GET("", h.List)
	stores.POST("", h.Create)
	stores.PUT("/reorder", h.Reorder)
	stores.PUT("/:id", h.Update)
	stores.DELETE("/:id", h.Delete)
}

// List returns all stores for the authenticated household.
// GET /api/v1/stores
func (h *StoreHandler) List(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)

	rows, err := h.DB.Query(
		`SELECT id, household_id, name, display_order, icon,
		        address, city, state, zip, store_number, nickname, latitude, longitude,
		        created_at, updated_at
		 FROM stores WHERE household_id = ? ORDER BY display_order, name`,
		householdID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer rows.Close()

	stores := make([]storeResponse, 0)
	for rows.Next() {
		var s storeResponse
		if err := rows.Scan(&s.ID, &s.HouseholdID, &s.Name, &s.DisplayOrder, &s.Icon,
			&s.Address, &s.City, &s.State, &s.Zip, &s.StoreNumber, &s.Nickname, &s.Latitude, &s.Longitude,
			&s.CreatedAt, &s.UpdatedAt); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		stores = append(stores, s)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusOK, stores)
}

// Create adds a new store for the household.
// POST /api/v1/stores
func (h *StoreHandler) Create(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)

	var req createStoreRequest
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
		`INSERT INTO stores (id, household_id, name, icon, created_at, updated_at)
		 VALUES (lower(hex(randomblob(16))), ?, ?, ?, ?, ?)
		 RETURNING id`,
		householdID, req.Name, req.Icon, now, now,
	).Scan(&id)
	// Note: new columns (address, city, state, zip, etc.) default to NULL on insert
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return c.JSON(http.StatusConflict, map[string]string{"error": "store name already exists"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	var s storeResponse
	err = h.DB.QueryRow(
		`SELECT id, household_id, name, display_order, icon,
		        address, city, state, zip, store_number, nickname, latitude, longitude,
		        created_at, updated_at
		 FROM stores WHERE id = ?`,
		id,
	).Scan(&s.ID, &s.HouseholdID, &s.Name, &s.DisplayOrder, &s.Icon,
		&s.Address, &s.City, &s.State, &s.Zip, &s.StoreNumber, &s.Nickname, &s.Latitude, &s.Longitude,
		&s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusCreated, s)
}

// Update modifies an existing store.
// PUT /api/v1/stores/:id
func (h *StoreHandler) Update(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	storeID := c.Param("id")

	var req updateStoreRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "name is required"})
	}

	now := time.Now().UTC()
	result, err := h.DB.Exec(
		`UPDATE stores SET name = ?, icon = ?, nickname = ?, address = ?, city = ?, state = ?, zip = ?,
		        updated_at = ?
		 WHERE id = ? AND household_id = ?`,
		req.Name, req.Icon, req.Nickname, req.Address, req.City, req.State, req.Zip,
		now, storeID, householdID,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return c.JSON(http.StatusConflict, map[string]string{"error": "store name already exists"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "store not found"})
	}

	var s storeResponse
	err = h.DB.QueryRow(
		`SELECT id, household_id, name, display_order, icon,
		        address, city, state, zip, store_number, nickname, latitude, longitude,
		        created_at, updated_at
		 FROM stores WHERE id = ?`,
		storeID,
	).Scan(&s.ID, &s.HouseholdID, &s.Name, &s.DisplayOrder, &s.Icon,
		&s.Address, &s.City, &s.State, &s.Zip, &s.StoreNumber, &s.Nickname, &s.Latitude, &s.Longitude,
		&s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusOK, s)
}

// Delete removes a store.
// DELETE /api/v1/stores/:id
func (h *StoreHandler) Delete(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	storeID := c.Param("id")

	result, err := h.DB.Exec(
		"DELETE FROM stores WHERE id = ? AND household_id = ?",
		storeID, householdID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "store not found"})
	}

	return c.NoContent(http.StatusNoContent)
}

// Reorder updates display_order for multiple stores in a single transaction.
// PUT /api/v1/stores/reorder
func (h *StoreHandler) Reorder(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)

	var req reorderStoresRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	if len(req.Stores) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "stores array is required"})
	}

	tx, err := h.DB.Begin()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	for _, item := range req.Stores {
		_, err := tx.Exec(
			"UPDATE stores SET display_order = ?, updated_at = ? WHERE id = ? AND household_id = ?",
			item.DisplayOrder, now, item.ID, householdID,
		)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
	}

	if err := tx.Commit(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to commit"})
	}

	return c.NoContent(http.StatusNoContent)
}
