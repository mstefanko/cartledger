package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
	"github.com/mstefanko/cartledger/internal/storage"
)

func TestServeImageUsesReceiptScopedMetadata(t *testing.T) {
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
	receiptID := "receipt-image-route"
	if _, err := database.Exec(
		"INSERT INTO receipts (id, household_id, receipt_date, status) VALUES (?, ?, '2026-05-09', 'matched')",
		receiptID, householdID,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	localStore, err := storage.NewLocal(dir)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	key, err := storage.ReceiptProcessedKey(receiptID, 1, ".jpg")
	if err != nil {
		t.Fatalf("ReceiptProcessedKey: %v", err)
	}
	if err := localStore.WriteFileAtomic(key, []byte("image-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	if err := storage.UpsertReceiptImage(httptest.NewRequest(http.MethodGet, "/", nil).Context(), database, storage.ReceiptImage{
		ReceiptID:  receiptID,
		Kind:       storage.ReceiptImageKindProcessed,
		PageNumber: 1,
		StorageKey: key,
		MimeType:   "image/jpeg",
		SizeBytes:  int64(len("image-bytes")),
		CreatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertReceiptImage: %v", err)
	}

	h := &ReceiptHandler{DB: database, Cfg: &config.Config{DataDir: dir}}
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/receipts/"+receiptID+"/images/processed/1.jpg", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set(auth.ContextKeyHouseholdID, householdID)
	c.SetParamNames("id", "kind", "page")
	c.SetParamValues(receiptID, "processed", "1.jpg")
	if err := h.ServeImage(c); err != nil {
		t.Fatalf("ServeImage: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "image-bytes" {
		t.Fatalf("body = %q", got)
	}

	rec = httptest.NewRecorder()
	c = e.NewContext(req, rec)
	c.Set(auth.ContextKeyHouseholdID, otherHouseholdID)
	c.SetParamNames("id", "kind", "page")
	c.SetParamValues(receiptID, "processed", "1.jpg")
	if err := h.ServeImage(c); err != nil {
		t.Fatalf("ServeImage other household: %v", err)
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("other household status = %d, want 404", rec.Code)
	}

}
