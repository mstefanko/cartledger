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
	"github.com/labstack/echo/v4/middleware"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/backup"
	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
	"github.com/mstefanko/cartledger/internal/models"
)

// restoreBodyLimit caps the multipart body for the restore endpoint. This is
// the single largest upload surface in the app — the entire archive is a
// gzipped SQLite DB + all receipt/product images — so the cap is permissive.
// Operators can tighten it via a reverse-proxy body limit if needed.
const restoreBodyLimit = "5G"

// restoreMaxBytes is the raw-byte equivalent of restoreBodyLimit, passed into
// backup.StageRestore as the size cap for the archive reader. Echo's
// middleware.BodyLimit rejects oversize bodies at the framework layer before
// our handler sees them; this constant is a belt-and-suspenders second cap
// inside the handler so unit tests of StageRestore don't need Echo middleware.
const restoreMaxBytes = int64(5) << 30 // 5 GiB

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

	// POST /backups/restore — staged restore upload. Route-level BodyLimit
	// overrides the global multipart cap; the restore archive can be multi-GB
	// and restricting it at the global cap would force operators to split
	// archives. Rate-limited via TierBackupCreate (1/hr per user) because a
	// restore is at least as destructive as a create and shares the same
	// threat model (accidental click-loop, brute-force re-auth).
	restoreMW := []echo.MiddlewareFunc{middleware.BodyLimit(restoreBodyLimit)}
	if h.Limiter != nil {
		restoreMW = append(restoreMW, h.Limiter.Middleware(TierBackupCreate))
	}
	g.POST("/restore", h.Restore, restoreMW...)
}

// Restore accepts a multipart upload of a backup archive plus a password
// re-auth field. The archive is staged under $DATA_DIR/restore-pending/ and
// applied on the next server restart (see backup.ApplyStagedRestoreIfPresent).
// Hot-restore is explicitly out of scope — the worker pool and WS hub hold
// *sql.DB references that can't safely be swapped underneath.
//
// Flow:
//  1. Re-auth: compare the posted password against the current user's
//     bcrypt hash. 401 on mismatch.
//  2. Open the multipart file, hand it to backup.StageRestore which applies
//     the full allowlist / traversal / symlink / SQLite-magic validator.
//  3. 202 with a restart-required message on success.
//
// Sentinel error mapping:
//   - ErrArchiveTooLarge → 413 Request Entity Too Large
//   - ErrArchiveInvalid  → 400 Bad Request (with the validator's message)
//   - ErrDiskFull        → 507 Insufficient Storage
//   - anything else      → 500
func (h *BackupHandler) Restore(c echo.Context) error {
	userID := auth.UserIDFrom(c)
	if userID == "" {
		// Shouldn't happen — JWTMiddleware runs upstream — but guard anyway.
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing or invalid token"})
	}

	// 1. Re-auth. The password field is a plain multipart form value;
	// c.FormValue reads it without parsing the file part (which would
	// buffer the whole upload in memory).
	password := c.FormValue("password")
	if password == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "password is required"})
	}

	var passwordHash string
	err := h.DB.QueryRow("SELECT password_hash FROM users WHERE id = ?", userID).Scan(&passwordHash)
	if err == sql.ErrNoRows {
		// RequireAdmin already passed so a missing user row is a server-side
		// anomaly, not a client error.
		h.Log.Error("restore: user row missing after admin check", "user_id", userID)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "user not found"})
	}
	if err != nil {
		h.Log.Error("restore: password hash lookup failed", "user_id", userID, "err", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if err := auth.CheckPassword(passwordHash, password); err != nil {
		// Log at Warn so operators can correlate brute-force attempts; do
		// NOT echo the user's password anywhere.
		h.Log.Warn("restore: password re-auth failed",
			"user_id", userID,
			"ip", c.RealIP(),
		)
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "password incorrect"})
	}

	// 2. Open the archive part and stream into StageRestore. Echo's
	// FormFile returns the multipart.FileHeader; .Open gives us an io.Reader
	// that streams directly from the request body (no full buffer).
	fh, err := c.FormFile("archive")
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "archive field is required"})
	}
	src, err := fh.Open()
	if err != nil {
		h.Log.Error("restore: open uploaded archive failed", "err", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "read upload"})
	}
	defer src.Close()

	if err := backup.StageRestore(h.Cfg, h.Log, src, restoreMaxBytes); err != nil {
		switch {
		case errors.Is(err, backup.ErrArchiveTooLarge):
			return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{
				"error": "archive exceeds the 5GB size limit",
			})
		case errors.Is(err, backup.ErrDiskFull):
			return c.JSON(http.StatusInsufficientStorage, map[string]string{
				"error": "insufficient disk space to stage archive",
			})
		case errors.Is(err, backup.ErrArchiveInvalid):
			// Surface the validator's specific message so operators can
			// diagnose the rejection (per plan: "respond 400 with specific
			// error"). The wrapped error from StageRestore already includes
			// the underlying cause.
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		default:
			h.Log.Error("restore: stage failed", "user_id", userID, "err", err)
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "failed to stage archive",
			})
		}
	}

	h.Log.Warn("restore: staged by admin — restart required to apply",
		"user_id", userID,
		"ip", c.RealIP(),
	)
	return c.JSON(http.StatusAccepted, map[string]string{
		"message": "Restart server to complete restore",
	})
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
