package worker

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	anthropic "github.com/anthropics/anthropic-sdk-go"

	"github.com/mstefanko/cartledger/internal/db"
	"github.com/mstefanko/cartledger/internal/llm"
	"github.com/mstefanko/cartledger/internal/ws"
)

type failingLLM struct {
	err error
}

func (f failingLLM) Provider() string { return "test" }

func (f failingLLM) ExtractReceipt(_ [][]byte) (*llm.ReceiptExtraction, error) {
	return nil, f.err
}

type staticLLM struct {
	extraction *llm.ReceiptExtraction
}

func (s staticLLM) Provider() string { return "test" }

func (s staticLLM) ExtractReceipt(_ [][]byte) (*llm.ReceiptExtraction, error) {
	return s.extraction, nil
}

func anthropic429(t *testing.T, retryAfter string) error {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	headers := http.Header{}
	if retryAfter != "" {
		headers.Set("Retry-After", retryAfter)
	}
	return &anthropic.Error{
		StatusCode: http.StatusTooManyRequests,
		Request:    req,
		Response: &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Status:     "429 Too Many Requests",
			Header:     headers,
			Request:    req,
			Body:       http.NoBody,
		},
	}
}

func TestProcessJobPersistsRateLimitMessage(t *testing.T) {
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
	receiptID := "receipt-rate-limit"
	if _, err := database.Exec(
		"INSERT INTO receipts (id, household_id, receipt_date, total, status) VALUES (?, ?, '2026-05-08', '10.00', 'processing')",
		receiptID, householdID,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}

	imageDir := filepath.Join(dir, "receipts", receiptID)
	if err := os.MkdirAll(imageDir, 0o755); err != nil {
		t.Fatalf("mkdir image dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(imageDir, "1.jpg"), []byte("fake image"), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}

	w := &ReceiptWorker{
		llmClient: failingLLM{err: fmt.Errorf("claude API call failed: %w", anthropic429(t, "42"))},
		db:        database,
		hub:       ws.NewHub(),
	}

	err = w.processJob(ReceiptJob{
		ReceiptID:   receiptID,
		HouseholdID: householdID,
		ImageDir:    imageDir,
	})
	if err == nil {
		t.Fatal("processJob() err = nil, want rate-limit error")
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
	if !errorMessage.Valid {
		t.Fatal("error_message is NULL, want rate-limit message")
	}
	if !strings.Contains(errorMessage.String, "rate-limited") || !strings.Contains(errorMessage.String, "42 seconds") {
		t.Fatalf("error_message = %q, want rate-limit message with retry hint", errorMessage.String)
	}
}

func TestRunJobPersistsGenericProcessingError(t *testing.T) {
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
	receiptID := "receipt-generic-error"
	if _, err := database.Exec(
		"INSERT INTO receipts (id, household_id, receipt_date, total, status) VALUES (?, ?, '2026-05-08', '10.00', 'processing')",
		receiptID, householdID,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}

	imageDir := filepath.Join(dir, "receipts", receiptID)
	if err := os.MkdirAll(imageDir, 0o755); err != nil {
		t.Fatalf("mkdir image dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(imageDir, "1.jpg"), []byte("fake image"), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}

	w := &ReceiptWorker{
		llmClient: failingLLM{err: fmt.Errorf("failed to parse tool_use input: json: cannot unmarshal string into Go struct field ReceiptExtraction.subtotal of type float64")},
		db:        database,
		hub:       ws.NewHub(),
	}

	w.runJob(ReceiptJob{
		ReceiptID:   receiptID,
		HouseholdID: householdID,
		ImageDir:    imageDir,
	})

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
	if !errorMessage.Valid {
		t.Fatal("error_message is NULL, want processing error")
	}
	if !strings.Contains(errorMessage.String, "ReceiptExtraction.subtotal") {
		t.Fatalf("error_message = %q, want parse detail", errorMessage.String)
	}
}

func TestProcessJobMarksEmptyLineItemExtractionAsError(t *testing.T) {
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
	receiptID := "receipt-empty-items"
	if _, err := database.Exec(
		"INSERT INTO receipts (id, household_id, receipt_date, total, status) VALUES (?, ?, '2026-05-08', '10.00', 'processing')",
		receiptID, householdID,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}

	imageDir := filepath.Join(dir, "receipts", receiptID)
	if err := os.MkdirAll(imageDir, 0o755); err != nil {
		t.Fatalf("mkdir image dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(imageDir, "1.jpg"), []byte("fake image"), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}

	itemsSold := 33
	w := &ReceiptWorker{
		llmClient: staticLLM{extraction: &llm.ReceiptExtraction{
			StoreName:      "Costco Wholesale",
			Date:           "2026-05-04",
			ItemsSoldCount: &itemsSold,
			Items:          nil,
			Subtotal:       346.41,
			Tax:            1.52,
			Total:          347.95,
			Confidence:     0.85,
		}},
		db:  database,
		hub: ws.NewHub(),
	}

	err = w.processJob(ReceiptJob{
		ReceiptID:   receiptID,
		HouseholdID: householdID,
		ImageDir:    imageDir,
	})
	if err == nil {
		t.Fatal("processJob() err = nil, want empty extraction error")
	}

	var status, rawJSON string
	var errorMessage sql.NullString
	var count int
	if err := database.QueryRow(
		"SELECT status, error_message, items_sold_count, raw_llm_json FROM receipts WHERE id = ?",
		receiptID,
	).Scan(&status, &errorMessage, &count, &rawJSON); err != nil {
		t.Fatalf("query receipt: %v", err)
	}
	if status != "error" {
		t.Fatalf("status = %q, want error", status)
	}
	if count != 33 {
		t.Fatalf("items_sold_count = %d, want 33", count)
	}
	if rawJSON == "" {
		t.Fatal("raw_llm_json is empty, want retained extraction metadata")
	}
	if !errorMessage.Valid || !strings.Contains(errorMessage.String, "33 items sold") {
		t.Fatalf("error_message = %q, want empty line items detail", errorMessage.String)
	}
}
