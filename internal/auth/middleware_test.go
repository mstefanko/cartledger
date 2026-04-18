package auth

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/db"
)

// newMiddlewareTestDB opens a migrated in-memory-ish SQLite DB with a
// household + two users: one admin, one non-admin. Returns both user ids.
func newMiddlewareTestDB(t *testing.T) (*sql.DB, string, string) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := db.RunMigrations(database); err != nil {
		database.Close()
		t.Fatalf("RunMigrations: %v", err)
	}

	var hh string
	if err := database.QueryRow(
		"INSERT INTO households (name) VALUES ('MWTest') RETURNING id",
	).Scan(&hh); err != nil {
		database.Close()
		t.Fatalf("insert household: %v", err)
	}
	if _, err := database.Exec(
		"INSERT INTO users (id, household_id, email, name, password_hash, is_admin) VALUES (?, ?, ?, ?, ?, 1)",
		"admin-id", hh, "admin@example.com", "Admin", "hash",
	); err != nil {
		database.Close()
		t.Fatalf("insert admin: %v", err)
	}
	if _, err := database.Exec(
		"INSERT INTO users (id, household_id, email, name, password_hash, is_admin) VALUES (?, ?, ?, ?, ?, 0)",
		"user-id", hh, "user@example.com", "User", "hash",
	); err != nil {
		database.Close()
		t.Fatalf("insert user: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database, "admin-id", "user-id"
}

func TestRequireAdmin_AllowsAdmin(t *testing.T) {
	database, adminID, _ := newMiddlewareTestDB(t)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/admin-thing", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set(ContextKeyUserID, adminID)

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
	database, _, userID := newMiddlewareTestDB(t)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/admin-thing", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set(ContextKeyUserID, userID)

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
	if body := rec.Body.String(); !contains(body, "admin required") {
		t.Errorf("non-admin: body=%q, want 'admin required'", body)
	}
}

func TestRequireAdmin_RejectsMissingUserID(t *testing.T) {
	database, _, _ := newMiddlewareTestDB(t)

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

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
