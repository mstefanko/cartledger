package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
	"github.com/mstefanko/cartledger/internal/llm"
)

func TestReprocessBreakerOpenPersistsReceiptMessage(t *testing.T) {
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer database.Close()
	if err := db.RunMigrations(database); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	var householdID string
	if err := database.QueryRow("INSERT INTO households (name) VALUES ('Test') RETURNING id").Scan(&householdID); err != nil {
		t.Fatalf("insert household: %v", err)
	}
	receiptID := "receipt-breaker-open"
	if _, err := database.Exec(
		"INSERT INTO receipts (id, household_id, receipt_date, total, status) VALUES (?, ?, '2026-05-08', '10.00', 'error')",
		receiptID, householdID,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}

	breaker := llm.NewBreaker(1, time.Minute, time.Minute, time.Minute)
	breaker.OnRateLimit()
	h := &ReceiptHandler{
		DB: database,
		Cfg: &config.Config{
			DataDir: dir,
		},
		Guard: llm.NewGuardedExtractor(llm.NewMockClient(), database, 0, breaker),
	}

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/receipts/"+receiptID+"/reprocess", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set(auth.ContextKeyHouseholdID, householdID)
	c.SetParamNames("id")
	c.SetParamValues(receiptID)

	if err := h.Reprocess(c); err != nil {
		t.Fatalf("Reprocess: %v", err)
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}

	var status string
	var errorMessage sql.NullString
	if err := database.QueryRow(
		"SELECT status, error_message FROM receipts WHERE id = ?",
		receiptID,
	).Scan(&status, &errorMessage); err != nil {
		t.Fatalf("query receipt: %v", err)
	}
	if status != "error" {
		t.Fatalf("status = %q, want error", status)
	}
	if !errorMessage.Valid || !strings.Contains(errorMessage.String, "rate-limiting") {
		t.Fatalf("error_message = %q, want persisted rate-limit message", errorMessage.String)
	}
}
