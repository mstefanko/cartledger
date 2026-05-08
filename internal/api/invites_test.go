package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
)

type inviteFixture struct {
	DB   *sql.DB
	Echo *echo.Echo
}

func newInviteFixture(t *testing.T) *inviteFixture {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := db.RunMigrations(database); err != nil {
		database.Close()
		t.Fatalf("RunMigrations: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	if _, err := database.Exec("INSERT INTO households (id, name) VALUES ('hh', 'Household')"); err != nil {
		t.Fatalf("seed household: %v", err)
	}
	adminHash, _ := auth.HashPassword("admin-password")
	if _, err := database.Exec(
		"INSERT INTO users (id, household_id, email, name, password_hash, is_admin) VALUES ('admin', 'hh', 'admin@example.com', 'Admin', ?, 1)",
		adminHash,
	); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	userHash, _ := auth.HashPassword("user-password")
	if _, err := database.Exec(
		"INSERT INTO users (id, household_id, email, name, password_hash, is_admin) VALUES ('user', 'hh', 'user@example.com', 'User', ?, 0)",
		userHash,
	); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	e := echo.New()
	public := e.Group("")
	protected := e.Group("", func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if uid := c.Request().Header.Get("X-Test-User-ID"); uid != "" {
				c.Set(auth.ContextKeyUserID, uid)
				c.Set(auth.ContextKeyHouseholdID, "hh")
			}
			return next(c)
		}
	})
	invites := &InvitesHandler{
		DB:  database,
		Cfg: &config.Config{AppBaseURL: "http://example.test"},
	}
	invites.RegisterRoutes(public, protected)
	authHandler := &AuthHandler{DB: database, Cfg: &config.Config{JWTSecret: "test-secret"}}
	public.POST("/join", authHandler.Join)

	return &inviteFixture{DB: database, Echo: e}
}

func (fx *inviteFixture) serve(t *testing.T, method, path string, body any, userID string) *httptest.ResponseRecorder {
	t.Helper()
	var r bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		r.Reset(b)
	}
	req := httptest.NewRequest(method, path, &r)
	req.Header.Set("Content-Type", "application/json")
	if userID != "" {
		req.Header.Set("X-Test-User-ID", userID)
	}
	rec := httptest.NewRecorder()
	fx.Echo.ServeHTTP(rec, req)
	return rec
}

func TestInviteCreateRequiresAdmin(t *testing.T) {
	fx := newInviteFixture(t)

	rec := fx.serve(t, http.MethodPost, "/invite", map[string]any{}, "user")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestInviteCreateValidateAndJoin(t *testing.T) {
	fx := newInviteFixture(t)

	rec := fx.serve(t, http.MethodPost, "/invite", map[string]any{
		"email":    "new@example.com",
		"ttl_days": 7,
	}, "admin")
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var invite inviteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &invite); err != nil {
		t.Fatalf("decode invite: %v", err)
	}
	if invite.ID == "" || invite.Token == "" || invite.URL == "" {
		t.Fatalf("missing invite fields: %+v", invite)
	}

	rec = fx.serve(t, http.MethodGet, "/invite/"+invite.Token+"/validate", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("validate status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	rec = fx.serve(t, http.MethodPost, "/join", map[string]string{
		"token":     invite.Token,
		"user_name": "New",
		"email":     "new@example.com",
		"password":  "new-password",
	}, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("join status = %d, want 201 (body=%s)", rec.Code, rec.Body.String())
	}

	var consumed string
	if err := fx.DB.QueryRow("SELECT consumed_at FROM invite_links WHERE id = ?", invite.ID).Scan(&consumed); err != nil {
		t.Fatalf("scan consumed_at: %v", err)
	}
	if consumed == "" {
		t.Fatalf("consumed_at is empty")
	}
}

func TestJoinRejectsRevokedInvite(t *testing.T) {
	fx := newInviteFixture(t)

	rec := fx.serve(t, http.MethodPost, "/invite", map[string]any{}, "admin")
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var invite inviteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &invite); err != nil {
		t.Fatalf("decode invite: %v", err)
	}
	rec = fx.serve(t, http.MethodDelete, "/invites/"+invite.ID, nil, "admin")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d, want 204", rec.Code)
	}

	rec = fx.serve(t, http.MethodPost, "/join", map[string]string{
		"token":     invite.Token,
		"user_name": "Blocked",
		"email":     "blocked@example.com",
		"password":  "new-password",
	}, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("join status = %d, want 401 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestInviteResendPreservesOriginalTTL(t *testing.T) {
	fx := newInviteFixture(t)

	rec := fx.serve(t, http.MethodPost, "/invite", map[string]any{
		"email":    "new@example.com",
		"ttl_days": 30,
	}, "admin")
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var invite inviteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &invite); err != nil {
		t.Fatalf("decode invite: %v", err)
	}

	rec = fx.serve(t, http.MethodPost, "/invites/"+invite.ID+"/send", nil, "admin")
	if rec.Code != http.StatusOK {
		t.Fatalf("resend status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var resent inviteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resent); err != nil {
		t.Fatalf("decode resend: %v", err)
	}
	if resent.ExpiresIn != "30 days" {
		t.Fatalf("resend expires_in = %q, want 30 days", resent.ExpiresIn)
	}
	if resent.ID == invite.ID {
		t.Fatalf("resend reused invite id %q, want new row", resent.ID)
	}
}
