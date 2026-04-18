package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
)

// TestSetupPromotesFirstUserToAdmin verifies the bootstrap rule: the user
// created via /setup (which is gated on "users table is empty") gets
// is_admin=1. A subsequent user created via /join stays is_admin=0.
func TestSetupPromotesFirstUserToAdmin(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer database.Close()
	if err := db.RunMigrations(database); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	cfg := &config.Config{DataDir: dir, JWTSecret: "test-secret"}
	bootstrap, err := LoadOrGenerateBootstrapToken(database)
	if err != nil {
		t.Fatalf("LoadOrGenerateBootstrapToken: %v", err)
	}
	if !bootstrap.HasToken() {
		t.Fatalf("expected bootstrap token on empty DB")
	}
	h := &AuthHandler{DB: database, Cfg: cfg, Bootstrap: bootstrap}

	body, _ := json.Marshal(setupRequest{
		HouseholdName: "HH",
		UserName:      "First",
		Email:         "first@example.com",
		Password:      "password123",
	})
	req := httptest.NewRequest(http.MethodPost,
		"/setup?bootstrap="+bootstrap.Token(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e := echo.New()
	c := e.NewContext(req, rec)
	c.QueryParams().Set("bootstrap", bootstrap.Token())

	if err := h.Setup(c); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup status = %d, want 201 (body=%s)", rec.Code, rec.Body.String())
	}

	var firstAdmin bool
	if err := database.QueryRow(
		"SELECT is_admin FROM users WHERE email = ?", "first@example.com",
	).Scan(&firstAdmin); err != nil {
		t.Fatalf("scan first user: %v", err)
	}
	if !firstAdmin {
		t.Errorf("first user is_admin = false, want true")
	}

	var resp authResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if !resp.User.IsAdmin {
		t.Errorf("setup response is_admin = false, want true")
	}

	// Manually insert a second user (simulating /join — which does NOT pass
	// is_admin). The INSERT omits is_admin, so the column's default of 0
	// takes effect. The point of this half of the test is to confirm the
	// default is 0 (not 1).
	var householdID string
	if err := database.QueryRow("SELECT household_id FROM users WHERE email = ?",
		"first@example.com").Scan(&householdID); err != nil {
		t.Fatalf("get household_id: %v", err)
	}
	if _, err := database.Exec(
		"INSERT INTO users (id, household_id, email, name, password_hash) VALUES (?, ?, ?, ?, ?)",
		"u2", householdID, "second@example.com", "Second", "hash",
	); err != nil {
		t.Fatalf("insert second user: %v", err)
	}

	var secondAdmin bool
	if err := database.QueryRow(
		"SELECT is_admin FROM users WHERE email = ?", "second@example.com",
	).Scan(&secondAdmin); err != nil {
		t.Fatalf("scan second user: %v", err)
	}
	if secondAdmin {
		t.Errorf("second user is_admin = true, want false")
	}

	if strings.Contains(rec.Body.String(), `"is_admin":false`) {
		t.Errorf("setup response should not report is_admin=false for first user")
	}
}
