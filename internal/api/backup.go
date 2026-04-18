package api

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/backup"
	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
	"github.com/mstefanko/cartledger/internal/models"
)

// BackupHandler wires the backup admin surface (create/list/download/delete).
// Restore lives in Phase B and is deliberately not wired here.
type BackupHandler struct {
	DB      *sql.DB
	Cfg     *config.Config
	Runner  *backup.Runner
	Store   *db.BackupStore
	Log     *slog.Logger
	Limiter *RateLimiter
}

// RegisterRoutes mounts the backup endpoints. Every route is admin-only; the
// POST additionally draws from the backup-create bucket (1 per hour per user).
// Accepts the already-authenticated protected group — callers should not
// pre-apply any rate limiter besides the global one.
func (h *BackupHandler) RegisterRoutes(protected *echo.Group) {
	g := protected.Group("/backups", auth.RequireAdmin(h.DB))

	createMW := []echo.MiddlewareFunc{}
	if h.Limiter != nil {
		createMW = append(createMW, h.Limiter.Middleware(TierBackupCreate))
	}
	g.POST("", h.Create, createMW...)
	g.GET("", h.List)
	g.GET("/:id/download", h.Download)
	g.DELETE("/:id", h.Delete)
}

// createResponse is the 202 body for POST /backups.
type createResponse struct {
	ID string `json:"id"`
}

// Create kicks off an async backup and returns 202 with the new row ID.
// Sentinel errors map to:
//   - ErrAlreadyRunning    → 409 Conflict
//   - ErrInsufficientSpace → 507 Insufficient Storage
//   - anything else        → 500
func (h *BackupHandler) Create(c echo.Context) error {
	id, err := h.Runner.StartAsync(c.Request().Context())
	if err != nil {
		switch {
		case errors.Is(err, backup.ErrAlreadyRunning):
			return c.JSON(http.StatusConflict, map[string]string{
				"error": "a backup is already running",
			})
		case errors.Is(err, backup.ErrInsufficientSpace):
			return c.JSON(http.StatusInsufficientStorage, map[string]string{
				"error": "insufficient disk space for backup",
			})
		default:
			h.Log.Error("backup: create failed", "err", err)
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "failed to start backup",
			})
		}
	}
	return c.JSON(http.StatusAccepted, createResponse{ID: id})
}

// List returns every backup row (newest first). Response shape is the
// models.Backup struct directly — handlers stay thin; struct is JSON-tagged
// with *string / *int64 / *time.Time for nullable fields.
func (h *BackupHandler) List(c echo.Context) error {
	rows, err := h.Store.List(c.Request().Context())
	if err != nil {
		h.Log.Error("backup: list failed", "err", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	return c.JSON(http.StatusOK, rows)
}

// Download streams the archive for a complete backup. Returns 404 while the
// backup is still running or has failed — operators shouldn't be able to
// hand out a half-written file to users.
func (h *BackupHandler) Download(c echo.Context) error {
	id := c.Param("id")
	row, err := h.Store.Get(c.Request().Context(), id)
	if err != nil {
		h.Log.Error("backup: get failed", "id", id, "err", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if row == nil || row.Status != models.BackupStatusComplete {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "backup not found"})
	}

	// Belt-and-suspenders: filename comes from our own Create path but we
	// still guard against a row whose filename has somehow been tampered
	// with — only allow filenames whose clean form stays under BackupDir.
	archivePath := filepath.Join(h.Cfg.BackupDir(), row.Filename)
	absBase, _ := filepath.Abs(h.Cfg.BackupDir())
	absTarget, err := filepath.Abs(archivePath)
	if err != nil || (absTarget != absBase && !isSubpath(absTarget, absBase)) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid backup path"})
	}

	if _, err := os.Stat(archivePath); err != nil {
		if os.IsNotExist(err) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "backup file missing"})
		}
		h.Log.Error("backup: stat file failed", "id", id, "err", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "storage error"})
	}

	c.Response().Header().Set("Content-Type", "application/gzip")
	c.Response().Header().Set(
		"Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s"`, row.Filename),
	)
	return c.File(archivePath)
}

// Delete removes the row and its on-disk archive. 404 if no row; 200 on
// success. A missing file with a present row is still considered a successful
// delete — the row is gone and there's nothing left to clean up.
func (h *BackupHandler) Delete(c echo.Context) error {
	id := c.Param("id")
	row, err := h.Store.Get(c.Request().Context(), id)
	if err != nil {
		h.Log.Error("backup: get failed", "id", id, "err", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if row == nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "backup not found"})
	}

	if err := h.Store.Delete(c.Request().Context(), id); err != nil {
		h.Log.Error("backup: delete row failed", "id", id, "err", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	archivePath := filepath.Join(h.Cfg.BackupDir(), row.Filename)
	if err := os.Remove(archivePath); err != nil && !os.IsNotExist(err) {
		h.Log.Warn("backup: remove file after delete failed", "path", archivePath, "err", err)
	}
	return c.NoContent(http.StatusNoContent)
}

// isSubpath reports whether child lives under parent. Both must already be
// absolute. Uses the filepath.Separator to avoid prefix false-positives
// (/data vs /data2).
func isSubpath(child, parent string) bool {
	sep := string(filepath.Separator)
	return len(child) > len(parent) && child[:len(parent)] == parent && child[len(parent):len(parent)+1] == sep
}
