package api

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
	appmail "github.com/mstefanko/cartledger/internal/mail"
)

type InvitesHandler struct {
	DB          *sql.DB
	Cfg         *config.Config
	Mailer      appmail.Mailer
	SendContext context.Context
}

type createInviteRequest struct {
	Email   string `json:"email"`
	TTLDays int    `json:"ttl_days"`
}

type inviteResponse struct {
	ID        string `json:"id"`
	Token     string `json:"token"`
	URL       string `json:"url"`
	Link      string `json:"link"`
	ExpiresAt string `json:"expires_at"`
	ExpiresIn string `json:"expires_in"`
}

type inviteListItem struct {
	ID         string  `json:"id"`
	Email      *string `json:"email"`
	ExpiresAt  string  `json:"expires_at"`
	CreatedAt  string  `json:"created_at"`
	ConsumedAt *string `json:"consumed_at"`
	RevokedAt  *string `json:"revoked_at"`
	Status     string  `json:"status"`
}

func (h *InvitesHandler) RegisterRoutes(public *echo.Group, protected *echo.Group) {
	public.GET("/invite/:token/validate", h.Validate)
	adminOnly := auth.RequireAdmin(h.DB)
	protected.POST("/invite", h.Create, adminOnly)
	protected.GET("/invites", h.List, adminOnly)
	protected.DELETE("/invites/:id", h.Revoke, adminOnly)
	protected.POST("/invites/:id/send", h.Resend, adminOnly)
}

func (h *InvitesHandler) Create(c echo.Context) error {
	var req createInviteRequest
	if err := c.Bind(&req); err != nil {
		if !errors.Is(err, io.EOF) {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		}
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Email != "" && !emailRegex.MatchString(req.Email) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid email format"})
	}
	ttlDays := req.TTLDays
	if ttlDays == 0 {
		ttlDays = 7
	}
	if ttlDays < 1 || ttlDays > 30 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "ttl_days must be between 1 and 30"})
	}

	resp, err := h.createInvite(auth.HouseholdIDFrom(c), auth.UserIDFrom(c), req.Email, time.Duration(ttlDays)*24*time.Hour)
	if err != nil {
		slog.Warn("invite create failed", "err", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if req.Email != "" {
		h.sendInviteEmail(req.Email, resp.ID, resp.URL, auth.UserIDFrom(c), auth.HouseholdIDFrom(c))
	}
	return c.JSON(http.StatusOK, resp)
}

func (h *InvitesHandler) List(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	rows, err := h.DB.Query(
		`SELECT id, email, expires_at, created_at, consumed_at, revoked_at
		 FROM invite_links
		 WHERE household_id = ?
		 ORDER BY created_at DESC
		 LIMIT 100`,
		householdID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer rows.Close()

	now := auth.SQLiteDateTime(time.Now())
	items := make([]inviteListItem, 0)
	for rows.Next() {
		var item inviteListItem
		var email, consumedAt, revokedAt sql.NullString
		if err := rows.Scan(&item.ID, &email, &item.ExpiresAt, &item.CreatedAt, &consumedAt, &revokedAt); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		item.Email = nullStringPtr(email)
		item.ConsumedAt = nullStringPtr(consumedAt)
		item.RevokedAt = nullStringPtr(revokedAt)
		item.Status = inviteStatus(item.ExpiresAt, item.ConsumedAt, item.RevokedAt, now)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	return c.JSON(http.StatusOK, items)
}

func (h *InvitesHandler) Revoke(c echo.Context) error {
	res, err := h.DB.Exec(
		"UPDATE invite_links SET revoked_at = ? WHERE id = ? AND household_id = ? AND revoked_at IS NULL",
		auth.SQLiteDateTime(time.Now()), c.Param("id"), auth.HouseholdIDFrom(c),
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	n, err := res.RowsAffected()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if n == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "invite not found"})
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *InvitesHandler) Resend(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	userID := auth.UserIDFrom(c)

	tx, err := h.DB.Begin()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer tx.Rollback()

	var email sql.NullString
	var createdAt, expiresAt string
	err = tx.QueryRow(
		"SELECT email, created_at, expires_at FROM invite_links WHERE id = ? AND household_id = ?",
		c.Param("id"), householdID,
	).Scan(&email, &createdAt, &expiresAt)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "invite not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if !email.Valid || strings.TrimSpace(email.String) == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "no email associated with invite"})
	}
	ttl, err := inviteOriginalTTL(createdAt, expiresAt)
	if err != nil {
		slog.Warn("invite resend ttl parse failed", "invite_id", c.Param("id"), "err", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if _, err := tx.Exec(
		"UPDATE invite_links SET revoked_at = ? WHERE id = ? AND household_id = ?",
		auth.SQLiteDateTime(time.Now()), c.Param("id"), householdID,
	); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	resp, err := h.createInviteInTx(tx, householdID, userID, strings.ToLower(strings.TrimSpace(email.String)), ttl)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if err := tx.Commit(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	h.sendInviteEmail(email.String, resp.ID, resp.URL, userID, householdID)
	return c.JSON(http.StatusOK, resp)
}

func (h *InvitesHandler) Validate(c echo.Context) error {
	tokenHash := auth.HashToken(strings.TrimSpace(c.Param("token")))
	var householdName, inviterName string
	var email sql.NullString
	err := h.DB.QueryRow(
		`SELECT hh.name, u.name, i.email
		 FROM invite_links i
		 JOIN households hh ON hh.id = i.household_id
		 JOIN users u ON u.id = i.inviter_id
		 WHERE i.token_hash = ?
		   AND i.expires_at > ?
		   AND i.consumed_at IS NULL
		   AND i.revoked_at IS NULL`,
		tokenHash, auth.SQLiteDateTime(time.Now()),
	).Scan(&householdName, &inviterName, &email)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid or expired invite"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	resp := map[string]string{
		"household_name": householdName,
		"invited_by":     inviterName,
	}
	if email.Valid && email.String != "" {
		resp["email"] = email.String
	}
	return c.JSON(http.StatusOK, resp)
}

func (h *InvitesHandler) createInvite(householdID, inviterID, email string, ttl time.Duration) (inviteResponse, error) {
	tx, err := h.DB.Begin()
	if err != nil {
		return inviteResponse{}, err
	}
	defer tx.Rollback()
	resp, err := h.createInviteInTx(tx, householdID, inviterID, email, ttl)
	if err != nil {
		return inviteResponse{}, err
	}
	if err := tx.Commit(); err != nil {
		return inviteResponse{}, err
	}
	return resp, nil
}

func (h *InvitesHandler) createInviteInTx(tx *sql.Tx, householdID, inviterID, email string, ttl time.Duration) (inviteResponse, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	token, err := auth.GenerateToken()
	if err != nil {
		return inviteResponse{}, err
	}
	expiresAt := auth.SQLiteDateTime(time.Now().Add(ttl))
	var emailArg any
	if email != "" {
		emailArg = email
	}
	var id string
	err = tx.QueryRow(
		`INSERT INTO invite_links (household_id, inviter_id, email, token_hash, expires_at)
		 VALUES (?, ?, ?, ?, ?)
		 RETURNING id`,
		householdID, inviterID, emailArg, auth.HashToken(token), expiresAt,
	).Scan(&id)
	if err != nil {
		return inviteResponse{}, err
	}
	url := publicURL(h.Cfg, "/join/"+token)
	return inviteResponse{
		ID:        id,
		Token:     token,
		URL:       url,
		Link:      url,
		ExpiresAt: expiresAt,
		ExpiresIn: durationLabel(ttl),
	}, nil
}

func (h *InvitesHandler) sendInviteEmail(email, inviteID, inviteURL, inviterID, householdID string) {
	if h.Mailer == nil || !h.Mailer.Enabled() {
		slog.Info("mailer disabled, would have sent invite", "invite_id", inviteID)
		return
	}
	go func() {
		var householdName, inviterName string
		if err := h.DB.QueryRow("SELECT name FROM households WHERE id = ?", householdID).Scan(&householdName); err != nil {
			slog.Warn("invite email household lookup failed", "invite_id", inviteID, "err", err)
			return
		}
		if err := h.DB.QueryRow("SELECT name FROM users WHERE id = ?", inviterID).Scan(&inviterName); err != nil {
			slog.Warn("invite email inviter lookup failed", "invite_id", inviteID, "err", err)
			return
		}
		htmlBody, textBody, err := appmail.Render("invite", appmail.TemplateData{
			Household:   householdName,
			InviterName: inviterName,
			URL:         inviteURL,
		})
		if err != nil {
			slog.Warn("invite email template render failed", "invite_id", inviteID, "err", err)
			return
		}
		ctx, cancel := context.WithTimeout(h.mailContext(), 30*time.Second)
		defer cancel()
		if err := h.Mailer.Send(ctx, email, "Join a CartLedger household", htmlBody, textBody); err != nil {
			slog.Warn("invite email send failed", "invite_id", inviteID, "err", err)
		}
	}()
}

func (h *InvitesHandler) mailContext() context.Context {
	if h.SendContext != nil {
		return h.SendContext
	}
	return context.Background()
}

func inviteOriginalTTL(createdAt, expiresAt string) (time.Duration, error) {
	created, err := parseSQLiteDateTime(createdAt)
	if err != nil {
		return 0, err
	}
	expires, err := parseSQLiteDateTime(expiresAt)
	if err != nil {
		return 0, err
	}
	ttl := expires.Sub(created)
	if ttl <= 0 {
		return 0, errors.New("invite ttl must be positive")
	}
	return ttl, nil
}

func parseSQLiteDateTime(value string) (time.Time, error) {
	if t, err := time.Parse("2006-01-02 15:04:05", value); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, value)
}

func inviteStatus(expiresAt string, consumedAt, revokedAt *string, now string) string {
	switch {
	case revokedAt != nil:
		return "revoked"
	case consumedAt != nil:
		return "consumed"
	case expiresAt <= now:
		return "expired"
	default:
		return "pending"
	}
}

func nullStringPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

func durationLabel(ttl time.Duration) string {
	if ttl < 24*time.Hour {
		hours := int((ttl + time.Hour - time.Nanosecond) / time.Hour)
		if hours <= 1 {
			return "1 hour"
		}
		return strconv.Itoa(hours) + " hours"
	}
	days := int((ttl + 24*time.Hour - time.Nanosecond) / (24 * time.Hour))
	if days == 1 {
		return "1 day"
	}
	return strconv.Itoa(days) + " days"
}
