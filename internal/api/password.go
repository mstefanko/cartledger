package api

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
	appmail "github.com/mstefanko/cartledger/internal/mail"
)

type PasswordHandler struct {
	DB             *sql.DB
	Cfg            *config.Config
	Mailer         appmail.Mailer
	SendContext    context.Context
	resetThrottle  sync.Map
	changeThrottle sync.Map
}

type passwordResetRequest struct {
	Email string `json:"email"`
}

type passwordResetConfirmRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

type passwordChangeRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (h *PasswordHandler) RegisterRoutes(publicRateLimited *echo.Group, protected *echo.Group) {
	publicRateLimited.POST("/password/reset/request", h.RequestReset)
	publicRateLimited.POST("/password/reset/confirm", h.ConfirmReset)
	protected.POST("/password/change", h.ChangePassword)
}

func (h *PasswordHandler) RequestReset(c echo.Context) error {
	var req passwordResetRequest
	_ = c.Bind(&req)
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email != "" && emailRegex.MatchString(email) && h.reserveResetThrottle(email) {
		go h.processResetRequest(email)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *PasswordHandler) reserveResetThrottle(email string) bool {
	if _, loaded := h.resetThrottle.LoadOrStore(email, struct{}{}); loaded {
		return false
	}
	time.AfterFunc(2*time.Minute, func() {
		h.resetThrottle.Delete(email)
	})
	return true
}

func (h *PasswordHandler) reserveChangeThrottle(userID string) bool {
	if _, loaded := h.changeThrottle.LoadOrStore(userID, struct{}{}); loaded {
		return false
	}
	time.AfterFunc(2*time.Minute, func() {
		h.changeThrottle.Delete(userID)
	})
	return true
}

func (h *PasswordHandler) processResetRequest(email string) {
	var userID, name string
	err := h.DB.QueryRow("SELECT id, name FROM users WHERE email = ?", email).Scan(&userID, &name)
	if err == sql.ErrNoRows {
		_ = auth.CheckPassword(dummyPasswordHash, email)
		return
	}
	if err != nil {
		slog.Warn("password reset request lookup failed", "err", err)
		return
	}
	if h.Mailer == nil || !h.Mailer.Enabled() {
		slog.Info("mailer disabled, would have sent password reset", "user_id", userID)
		return
	}

	token, err := auth.IssueToken(h.DB, userID, auth.TokenKindPasswordReset, 30*time.Minute)
	if err != nil {
		slog.Warn("password reset token issue failed", "user_id", userID, "err", err)
		return
	}
	resetURL := publicURL(h.Cfg, "/reset-password?token="+token)
	htmlBody, textBody, err := appmail.Render("password_reset", appmail.TemplateData{
		Name: name,
		URL:  resetURL,
	})
	if err != nil {
		slog.Warn("password reset template render failed", "user_id", userID, "err", err)
		return
	}
	ctx, cancel := context.WithTimeout(h.mailContext(), 30*time.Second)
	defer cancel()
	if err := h.Mailer.Send(ctx, email, "Reset your CartLedger password", htmlBody, textBody); err != nil {
		slog.Warn("password reset email send failed", "user_id", userID, "err", err)
	}
}

func (h *PasswordHandler) ConfirmReset(c echo.Context) error {
	var req passwordResetConfirmRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	if strings.TrimSpace(req.Token) == "" || req.NewPassword == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "token and new_password are required"})
	}
	if err := auth.ValidateNewPassword(req.NewPassword); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	userID, err := auth.ConsumeToken(h.DB, auth.TokenKindPasswordReset, strings.TrimSpace(req.Token))
	if errors.Is(err, auth.ErrInvalidToken) {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid or expired reset token"})
	}
	if err != nil {
		slog.Warn("password reset token consume failed", "err", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	passwordHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to hash password"})
	}
	if _, err := h.DB.Exec(
		"UPDATE users SET password_hash = ?, password_changed_at = ? WHERE id = ?",
		passwordHash, auth.SQLiteDateTime(time.Now()), userID,
	); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	user, err := h.userResponse(userID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	token, err := auth.CreateAuthToken(h.Cfg.JWTSecret, user.ID, user.HouseholdID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create token"})
	}
	auth.SetAuthCookie(c, token)
	h.sendPasswordChanged(user.Email, user.Name, user.ID)

	return c.JSON(http.StatusOK, authResponse{User: user})
}

func (h *PasswordHandler) ChangePassword(c echo.Context) error {
	userID := auth.UserIDFrom(c)
	var req passwordChangeRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	if req.CurrentPassword == "" || req.NewPassword == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "current_password and new_password are required"})
	}
	if err := auth.ValidateNewPassword(req.NewPassword); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if !h.reserveChangeThrottle(userID) {
		return c.JSON(http.StatusTooManyRequests, map[string]string{"error": "password change is rate limited"})
	}

	var user userResponse
	var passwordHash string
	err := h.DB.QueryRow(
		"SELECT id, household_id, email, name, is_admin, password_hash FROM users WHERE id = ?",
		userID,
	).Scan(&user.ID, &user.HouseholdID, &user.Email, &user.Name, &user.IsAdmin, &passwordHash)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if err := auth.CheckPassword(passwordHash, req.CurrentPassword); err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "current password is incorrect"})
	}
	nextHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to hash password"})
	}
	if _, err := h.DB.Exec(
		"UPDATE users SET password_hash = ?, password_changed_at = ? WHERE id = ?",
		nextHash, auth.SQLiteDateTime(time.Now()), userID,
	); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	token, err := auth.CreateAuthToken(h.Cfg.JWTSecret, user.ID, user.HouseholdID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create token"})
	}
	auth.SetAuthCookie(c, token)
	h.sendPasswordChanged(user.Email, user.Name, user.ID)
	return c.JSON(http.StatusOK, map[string]string{"status": "updated"})
}

func (h *PasswordHandler) userResponse(userID string) (userResponse, error) {
	var user userResponse
	err := h.DB.QueryRow(
		"SELECT id, household_id, email, name, is_admin FROM users WHERE id = ?",
		userID,
	).Scan(&user.ID, &user.HouseholdID, &user.Email, &user.Name, &user.IsAdmin)
	return user, err
}

func (h *PasswordHandler) sendPasswordChanged(email, name, userID string) {
	if h.Mailer == nil || !h.Mailer.Enabled() {
		slog.Info("mailer disabled, would have sent password changed notification", "user_id", userID)
		return
	}
	go func() {
		htmlBody, textBody, err := appmail.Render("password_changed", appmail.TemplateData{Name: name})
		if err != nil {
			slog.Warn("password changed template render failed", "user_id", userID, "err", err)
			return
		}
		ctx, cancel := context.WithTimeout(h.mailContext(), 30*time.Second)
		defer cancel()
		if err := h.Mailer.Send(ctx, email, "Your CartLedger password changed", htmlBody, textBody); err != nil {
			slog.Warn("password changed email send failed", "user_id", userID, "err", err)
		}
	}()
}

func (h *PasswordHandler) mailContext() context.Context {
	if h.SendContext != nil {
		return h.SendContext
	}
	return context.Background()
}

func publicURL(cfg *config.Config, path string) string {
	base := "http://localhost:8079"
	if cfg != nil && cfg.AppBaseURL != "" {
		base = strings.TrimRight(cfg.AppBaseURL, "/")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}
