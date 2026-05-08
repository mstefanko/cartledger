package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
)

type passwordFixture struct {
	DB      *sql.DB
	Handler *PasswordHandler
	UserID  string
}

func newPasswordFixture(t *testing.T) *passwordFixture {
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
	passwordHash, err := auth.HashPassword("current-password")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if _, err := database.Exec(
		"INSERT INTO users (id, household_id, email, name, password_hash, is_admin) VALUES ('user', 'hh', 'user@example.com', 'User', ?, 0)",
		passwordHash,
	); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	return &passwordFixture{
		DB: database,
		Handler: &PasswordHandler{
			DB:  database,
			Cfg: &config.Config{JWTSecret: "test-secret", AppBaseURL: "http://example.test"},
		},
		UserID: "user",
	}
}

func (fx *passwordFixture) servePassword(t *testing.T, method, path string, body any, userID string) *httptest.ResponseRecorder {
	t.Helper()
	var r bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		r.Reset(b)
	}
	req := httptest.NewRequest(method, path, &r)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e := echo.New()
	c := e.NewContext(req, rec)
	if userID != "" {
		c.Set(auth.ContextKeyUserID, userID)
		c.Set(auth.ContextKeyHouseholdID, "hh")
	}

	var err error
	switch path {
	case "/password/reset/request":
		err = fx.Handler.RequestReset(c)
	case "/password/reset/confirm":
		err = fx.Handler.ConfirmReset(c)
	case "/password/change":
		err = fx.Handler.ChangePassword(c)
	default:
		t.Fatalf("unknown path %s", path)
	}
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	return rec
}

func TestPasswordResetRequestAlways204AndThrottles(t *testing.T) {
	fx := newPasswordFixture(t)

	rec := fx.servePassword(t, http.MethodPost, "/password/reset/request", map[string]string{"email": "user@example.com"}, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("known user status = %d, want 204", rec.Code)
	}
	rec = fx.servePassword(t, http.MethodPost, "/password/reset/request", map[string]string{"email": "missing@example.com"}, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("missing user status = %d, want 204", rec.Code)
	}
	if !fx.Handler.reserveResetThrottle("dupe@example.com") {
		t.Fatalf("first throttle reservation = false, want true")
	}
	if fx.Handler.reserveResetThrottle("dupe@example.com") {
		t.Fatalf("second throttle reservation = true, want false")
	}
}

func TestPasswordResetConfirmConsumesToken(t *testing.T) {
	fx := newPasswordFixture(t)
	token, err := auth.IssueToken(fx.DB, fx.UserID, auth.TokenKindPasswordReset, time.Hour)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	rec := fx.servePassword(t, http.MethodPost, "/password/reset/confirm", map[string]string{
		"token":        token,
		"new_password": "new-password",
	}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if len(rec.Result().Cookies()) == 0 {
		t.Fatalf("confirm did not set auth cookie")
	}

	rec = fx.servePassword(t, http.MethodPost, "/password/reset/confirm", map[string]string{
		"token":        token,
		"new_password": "new-password",
	}, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("reused token status = %d, want 401", rec.Code)
	}
}

func TestPasswordResetConfirmConcurrentSingleUse(t *testing.T) {
	fx := newPasswordFixture(t)
	token, err := auth.IssueToken(fx.DB, fx.UserID, auth.TokenKindPasswordReset, time.Hour)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	const attempts = 12
	start := make(chan struct{})
	var wg sync.WaitGroup
	var ok, unauthorized, other atomic.Int64

	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			rec := fx.servePassword(t, http.MethodPost, "/password/reset/confirm", map[string]string{
				"token":        token,
				"new_password": "new-password",
			}, "")
			switch rec.Code {
			case http.StatusOK:
				ok.Add(1)
			case http.StatusUnauthorized:
				unauthorized.Add(1)
			default:
				other.Add(1)
			}
		}()
	}

	close(start)
	wg.Wait()

	if ok.Load() != 1 {
		t.Fatalf("successful confirms = %d, want 1", ok.Load())
	}
	if unauthorized.Load() != attempts-1 {
		t.Fatalf("unauthorized confirms = %d, want %d", unauthorized.Load(), attempts-1)
	}
	if other.Load() != 0 {
		t.Fatalf("unexpected confirm statuses = %d, want 0", other.Load())
	}
}

func TestPasswordChangeRejectsWrongCurrentPassword(t *testing.T) {
	fx := newPasswordFixture(t)

	rec := fx.servePassword(t, http.MethodPost, "/password/change", map[string]string{
		"current_password": "wrong-password",
		"new_password":     "new-password",
	}, fx.UserID)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	rec = fx.servePassword(t, http.MethodPost, "/password/change", map[string]string{
		"current_password": "wrong-password",
		"new_password":     "new-password",
	}, fx.UserID)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want 429", rec.Code)
	}
}
