package auth

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/db"
)

func newMiddlewareTestDB(t *testing.T) *sql.DB {
	return newMiddlewareTestDBAt(t, time.Now().UTC().Add(-30*time.Minute))
}

func newMiddlewareTestDBAt(t *testing.T, passwordChangedAt time.Time) *sql.DB {
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
	if _, err := database.Exec(
		"INSERT INTO users (id, household_id, email, name, password_hash, is_admin, password_changed_at) VALUES ('admin', 'hh', 'admin@example.com', 'Admin', 'hash', 1, ?)",
		SQLiteDateTime(passwordChangedAt),
	); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	if _, err := database.Exec(
		"INSERT INTO users (id, household_id, email, name, password_hash, is_admin, password_changed_at) VALUES ('user', 'hh', 'user@example.com', 'User', 'hash', 0, ?)",
		SQLiteDateTime(passwordChangedAt),
	); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return database
}

func authTokenAt(t *testing.T, secret string, issuedAt time.Time) string {
	t.Helper()
	claims := Claims{
		UserID:      "user",
		HouseholdID: "hh",
		Type:        TokenTypeAuth,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(issuedAt.Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(issuedAt),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}
	return token
}

func TestRequireAdmin_AllowsAdmin(t *testing.T) {
	database := newMiddlewareTestDB(t)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/admin-thing", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set(ContextKeyUserID, "admin")

	handler := RequireAdmin(database)(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})
	if err := handler(c); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("admin: status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestRequireAdmin_ForbidsNonAdmin(t *testing.T) {
	database := newMiddlewareTestDB(t)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/admin-thing", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set(ContextKeyUserID, "user")

	called := false
	handler := RequireAdmin(database)(func(c echo.Context) error {
		called = true
		return c.String(http.StatusOK, "ok")
	})
	if err := handler(c); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("non-admin: status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
	}
	if called {
		t.Error("non-admin: downstream handler was invoked; expected short-circuit")
	}
	if body := rec.Body.String(); !strings.Contains(body, "admin required") {
		t.Errorf("non-admin: body=%q, want 'admin required'", body)
	}
}

func TestRequireAdmin_RejectsMissingUserID(t *testing.T) {
	database := newMiddlewareTestDB(t)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/admin-thing", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := RequireAdmin(database)(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})
	if err := handler(c); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("missing user_id: status = %d, want 401", rec.Code)
	}
}

func TestJWTMiddlewareRejectsTokenIssuedBeforePasswordChange(t *testing.T) {
	passwordChangedAt := time.Now().UTC().Add(-30 * time.Minute)
	database := newMiddlewareTestDBAt(t, passwordChangedAt)
	const secret = "test-secret"

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+authTokenAt(t, secret, passwordChangedAt.Add(-time.Minute)))
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := JWTMiddleware(secret, database)(func(c echo.Context) error {
		return c.NoContent(http.StatusOK)
	})
	if err := handler(c); err != nil {
		t.Fatalf("middleware: %v", err)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

func TestJWTMiddlewareAcceptsTokenIssuedAfterPasswordChange(t *testing.T) {
	passwordChangedAt := time.Now().UTC().Add(-30 * time.Minute)
	database := newMiddlewareTestDBAt(t, passwordChangedAt)
	const secret = "test-secret"

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+authTokenAt(t, secret, passwordChangedAt.Add(time.Minute)))
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := JWTMiddleware(secret, database)(func(c echo.Context) error {
		if UserIDFrom(c) != "user" || HouseholdIDFrom(c) != "hh" {
			t.Fatalf("context claims not populated")
		}
		return c.NoContent(http.StatusOK)
	})
	if err := handler(c); err != nil {
		t.Fatalf("middleware: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}
