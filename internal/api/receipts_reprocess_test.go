package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
	"github.com/mstefanko/cartledger/internal/llm"
	"github.com/mstefanko/cartledger/internal/matcher"
	"github.com/mstefanko/cartledger/internal/worker"
	"github.com/mstefanko/cartledger/internal/ws"
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

func TestReprocessRollbackPersistsSanitizedMessageAndHouseholdGuard(t *testing.T) {
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer database.Close()
	if err := db.RunMigrations(database); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	var householdID, otherHouseholdID string
	if err := database.QueryRow("INSERT INTO households (name) VALUES ('Test') RETURNING id").Scan(&householdID); err != nil {
		t.Fatalf("insert household: %v", err)
	}
	if err := database.QueryRow("INSERT INTO households (name) VALUES ('Other') RETURNING id").Scan(&otherHouseholdID); err != nil {
		t.Fatalf("insert other household: %v", err)
	}
	receiptID := "receipt-images-gone"
	if _, err := database.Exec(
		"INSERT INTO receipts (id, household_id, receipt_date, total, status, error_message) VALUES (?, ?, '2026-05-08', '10.00', 'error', 'old error')",
		receiptID, householdID,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	if _, err := database.Exec(
		"INSERT INTO receipts (id, household_id, receipt_date, total, status, error_message) VALUES (?, ?, '2026-05-08', '10.00', 'error', 'other household error')",
		receiptID+"-other", otherHouseholdID,
	); err != nil {
		t.Fatalf("insert other receipt: %v", err)
	}

	h := &ReceiptHandler{
		DB:  database,
		Cfg: &config.Config{DataDir: dir},
		Worker: worker.NewReceiptWorker(
			0,
			llm.NewMockClient(),
			nil,
			matcher.NewEngine(database),
			database,
			ws.NewHub(),
			&config.Config{DataDir: dir},
		),
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
	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410; body=%s", rec.Code, rec.Body.String())
	}

	var status string
	var errorMessage sql.NullString
	if err := database.QueryRow(
		"SELECT status, error_message FROM receipts WHERE id = ? AND household_id = ?",
		receiptID, householdID,
	).Scan(&status, &errorMessage); err != nil {
		t.Fatalf("query receipt: %v", err)
	}
	if status != "error" {
		t.Fatalf("status = %q, want error", status)
	}
	if !errorMessage.Valid || errorMessage.String != "receipt images are no longer on disk; please re-upload the receipt" {
		t.Fatalf("error_message = %q, want sanitized images-gone message", errorMessage.String)
	}

	var otherMessage string
	if err := database.QueryRow(
		"SELECT error_message FROM receipts WHERE id = ? AND household_id = ?",
		receiptID+"-other", otherHouseholdID,
	).Scan(&otherMessage); err != nil {
		t.Fatalf("query other receipt: %v", err)
	}
	if otherMessage != "other household error" {
		t.Fatalf("other household message = %q, want untouched", otherMessage)
	}
}

func TestReprocessDuplicatePendingRetryIsIdempotent(t *testing.T) {
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
	receiptID := "receipt-pending"
	if _, err := database.Exec(
		"INSERT INTO receipts (id, household_id, receipt_date, total, status) VALUES (?, ?, '2026-05-08', '10.00', 'pending')",
		receiptID, householdID,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	imageDir := filepath.Join(dir, "receipts", receiptID)
	if err := os.MkdirAll(imageDir, 0o755); err != nil {
		t.Fatalf("mkdir image dir: %v", err)
	}

	receiptWorker := worker.NewReceiptWorker(
		0,
		llm.NewMockClient(),
		nil,
		matcher.NewEngine(database),
		database,
		ws.NewHub(),
		&config.Config{DataDir: dir},
	)
	h := &ReceiptHandler{
		DB:     database,
		Cfg:    &config.Config{DataDir: dir},
		Worker: receiptWorker,
	}

	for i := 0; i < 2; i++ {
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/receipts/"+receiptID+"/reprocess", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.Set(auth.ContextKeyHouseholdID, householdID)
		c.SetParamNames("id")
		c.SetParamValues(receiptID)

		if err := h.Reprocess(c); err != nil {
			t.Fatalf("Reprocess #%d: %v", i+1, err)
		}
		if rec.Code != http.StatusAccepted {
			t.Fatalf("status #%d = %d, want 202; body=%s", i+1, rec.Code, rec.Body.String())
		}
	}

	if depth := receiptWorker.QueueDepth(); depth != 1 {
		t.Fatalf("queue depth = %d, want one enqueued job", depth)
	}
}
