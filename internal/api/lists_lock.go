package api

import (
	"database/sql"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/locks"
	"github.com/mstefanko/cartledger/internal/ws"
)

// --- Lock endpoints (Phase 7) ---

// lockResponse is the JSON body returned by the lock endpoints.
type lockResponse struct {
	Holder *locks.Holder `json:"holder"`
}

// verifyListInHousehold confirms listID belongs to householdID. Returns an
// *echo.HTTPError with the appropriate status on miss.
func (h *ListHandler) verifyListInHousehold(c echo.Context, listID, householdID string) error {
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
	return nil
}

// userNameByID fetches a user's display name. Empty string if missing — not a
// hard error for lock semantics; we'd rather show "is editing" with a blank
// name than drop the lock.
func (h *ListHandler) userNameByID(userID string) string {
	var name string
	err := h.DB.QueryRow("SELECT name FROM users WHERE id = ?", userID).Scan(&name)
	if err != nil {
		return ""
	}
	return name
}

// AcquireLock attempts to take the edit lock for a list.
// POST /api/v1/lists/:id/lock
//
// 200 {holder} if acquired (or already owned and refreshed).
// 409 {holder} if another user currently holds the lock.
func (h *ListHandler) AcquireLock(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	userID := auth.UserIDFrom(c)
	listID := c.Param("id")

	if err := h.verifyListInHousehold(c, listID, householdID); err != nil {
		return err
	}

	userName := h.userNameByID(userID)
	ok, holder := h.Locks.Acquire(listID, userID, userName, householdID)
	if !ok {
		return c.JSON(http.StatusConflict, lockResponse{Holder: holder})
	}
	h.Hub.Broadcast(ws.Message{
		Type:      ws.EventListLockAcquired,
		Household: householdID,
		Payload: map[string]interface{}{
			"list_id":   listID,
			"user_id":   userID,
			"user_name": userName,
		},
	})
	return c.JSON(http.StatusOK, lockResponse{Holder: holder})
}

// TouchLock refreshes the holder's LastTouched timestamp.
// POST /api/v1/lists/:id/lock/heartbeat
//
// 204 on success, 409 if the caller is not the current holder (stale session).
func (h *ListHandler) TouchLock(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	userID := auth.UserIDFrom(c)
	listID := c.Param("id")

	if err := h.verifyListInHousehold(c, listID, householdID); err != nil {
		return err
	}

	if !h.Locks.Touch(listID, userID) {
		return c.JSON(http.StatusConflict, lockResponse{Holder: h.Locks.Current(listID)})
	}
	return c.NoContent(http.StatusNoContent)
}

// ReleaseLock drops the lock if the caller is the current holder.
// POST /api/v1/lists/:id/lock/release
//
// 204 always (release is best-effort; no-op if caller is not holder).
func (h *ListHandler) ReleaseLock(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	userID := auth.UserIDFrom(c)
	listID := c.Param("id")

	if err := h.verifyListInHousehold(c, listID, householdID); err != nil {
		return err
	}

	// Only broadcast if the caller actually was the holder. Check before
	// Release so we don't emit spurious events from stale tabs.
	cur := h.Locks.Current(listID)
	h.Locks.Release(listID, userID)
	if cur != nil && cur.UserID == userID {
		h.Hub.Broadcast(ws.Message{
			Type:      ws.EventListLockReleased,
			Household: householdID,
			Payload: map[string]interface{}{
				"list_id": listID,
				"user_id": userID,
			},
		})
	}
	return c.NoContent(http.StatusNoContent)
}

// TakeOverLock force-transfers the lock to the caller.
// POST /api/v1/lists/:id/lock/takeover
//
// 200 {holder} with the new holder.
func (h *ListHandler) TakeOverLock(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	userID := auth.UserIDFrom(c)
	listID := c.Param("id")

	if err := h.verifyListInHousehold(c, listID, householdID); err != nil {
		return err
	}

	userName := h.userNameByID(userID)
	prior := h.Locks.TakeOver(listID, userID, userName, householdID)
	cur := h.Locks.Current(listID)

	payload := map[string]interface{}{
		"list_id":       listID,
		"new_user_id":   userID,
		"new_user_name": userName,
	}
	if prior != nil {
		payload["prior_user_id"] = prior.UserID
	}
	h.Hub.Broadcast(ws.Message{
		Type:      ws.EventListLockTakenOver,
		Household: householdID,
		Payload:   payload,
	})
	return c.JSON(http.StatusOK, lockResponse{Holder: cur})
}

// requireLock is the implicit-lock helper used by the write handlers. It
// returns nil if the caller holds (or just acquired) the lock for listID; it
// writes a 409 response and returns the echo error otherwise.
//
// Semantics:
//   - Free lock: caller acquires it implicitly, WS event broadcast, returns nil.
//   - Caller already holds: Touch refreshes LastTouched, returns nil.
//   - Another user holds: 409 with the current holder, returns a non-nil error.
//
// This is the enforcement point — without calling it from each write handler,
// the lock is decorative.
func (h *ListHandler) requireLock(c echo.Context, listID string) error {
	if h.Locks == nil {
		// Tests may wire a ListHandler without a lock store; treat as disabled.
		return nil
	}
	householdID := auth.HouseholdIDFrom(c)
	userID := auth.UserIDFrom(c)

	// Fast path: already holder.
	if h.Locks.Touch(listID, userID) {
		return nil
	}
	// Slow path: try to acquire.
	userName := h.userNameByID(userID)
	ok, holder := h.Locks.Acquire(listID, userID, userName, householdID)
	if !ok {
		return c.JSON(http.StatusConflict, map[string]interface{}{
			"error":  "list is being edited by another user",
			"holder": holder,
		})
	}
	// Fresh acquisition — broadcast so other windows render the banner.
	h.Hub.Broadcast(ws.Message{
		Type:      ws.EventListLockAcquired,
		Household: householdID,
		Payload: map[string]interface{}{
			"list_id":   listID,
			"user_id":   userID,
			"user_name": userName,
		},
	})
	return nil
}
