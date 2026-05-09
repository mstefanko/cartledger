package storage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestValidateKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
		ok   bool
	}{
		{"canonical receipt", "receipts/r1/original/1.jpg", true},
		{"spaces", "products/product one/image one.jpg", true},
		{"absolute", "/data/receipts/r1/1.jpg", false},
		{"backslash", `receipts\r1\1.jpg`, false},
		{"traversal", "receipts/r1/../secret.jpg", false},
		{"encoded traversal", "receipts/r1/%2e%2e/secret.jpg", false},
		{"duplicate slashes", "receipts//r1/1.jpg", false},
		{"dot segment", "receipts/r1/./1.jpg", false},
		{"newline", "receipts/r1/original/1.jpg\nx", false},
		{"carriage return", "receipts/r1/original/1.jpg\rx", false},
		{"too long", "receipts/r1/original/" + strings.Repeat("a", maxStorageKeyLength) + ".jpg", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateKey(tt.key)
			if tt.ok && err != nil {
				t.Fatalf("ValidateKey(%q): %v", tt.key, err)
			}
			if !tt.ok && err == nil {
				t.Fatalf("ValidateKey(%q) nil, want error", tt.key)
			}
		})
	}
}

func TestLegacyReceiptDir(t *testing.T) {
	root := t.TempDir()
	got, err := LegacyReceiptDir(root, "r1")
	if err != nil {
		t.Fatalf("LegacyReceiptDir: %v", err)
	}
	want := filepath.Join(root, "receipts", "r1")
	if got != want {
		t.Fatalf("LegacyReceiptDir = %q want %q", got, want)
	}
	if _, err := LegacyReceiptDir(root, "../r1"); err == nil {
		t.Fatalf("LegacyReceiptDir accepted traversal owner id")
	}
}

func TestLocalPathStaysUnderRoot(t *testing.T) {
	root := t.TempDir()
	local, err := NewLocal(root)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	if _, err := local.Path("receipts/r1/original/1.jpg"); err != nil {
		t.Fatalf("valid key rejected: %v", err)
	}
	if _, err := local.Path("../escape.jpg"); err == nil {
		t.Fatalf("escape key accepted")
	}
}

func TestWriteFileAtomic(t *testing.T) {
	root := t.TempDir()
	local, err := NewLocal(root)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	if err := local.WriteFileAtomic("receipts/r1/original/1.jpg", []byte("img"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "receipts", "r1", "original", "1.jpg"))
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(got) != "img" {
		t.Fatalf("content = %q", got)
	}
}

func TestNormalizeLegacyReceiptImageReference(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		raw            string
		wantStorageKey string
		wantLegacyPath string
	}{
		{"receipts/r1/1.jpg", "receipts/r1/original/1.jpg", "receipts/r1/1.jpg"},
		{"receipts/r1/processed_2.png", "receipts/r1/processed/2.png", "receipts/r1/processed_2.png"},
		{filepath.Join(root, "receipts", "r1", "processed_3.jpg"), "receipts/r1/processed/3.jpg", filepath.Join(root, "receipts", "r1", "processed_3.jpg")},
		{"r1/4.jpg", "receipts/r1/original/4.jpg", "receipts/r1/4.jpg"},
	}
	for _, tt := range tests {
		ref, err := NormalizeLegacyReceiptImageReference(root, "r1", tt.raw)
		if err != nil {
			t.Fatalf("NormalizeLegacyReceiptImageReference(%q): %v", tt.raw, err)
		}
		if ref.StorageKey != tt.wantStorageKey {
			t.Fatalf("NormalizeLegacyReceiptImageReference(%q) storage key = %q want %q", tt.raw, ref.StorageKey, tt.wantStorageKey)
		}
		if ref.LegacyPath != tt.wantLegacyPath {
			t.Fatalf("NormalizeLegacyReceiptImageReference(%q) legacy path = %q want %q", tt.raw, ref.LegacyPath, tt.wantLegacyPath)
		}
	}
	if _, err := NormalizeLegacyReceiptImageReference(root, "r1", "receipts/other/1.jpg"); err == nil {
		t.Fatalf("cross-receipt path accepted")
	}
	if _, err := NormalizeLegacyReceiptImageReference(root, "r1", "receipts/r1/%2e%2e/1.jpg"); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("encoded traversal err = %v", err)
	}
}

func TestMaterializeReceiptImageUsesExistingDataDirFile(t *testing.T) {
	root := t.TempDir()
	local, err := NewLocal(root)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	legacyPath := filepath.Join(root, "receipts", "r1", "1.jpg")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(legacyPath, []byte("legacy"), 0o644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	ref, err := NormalizeLegacyReceiptImageReference(root, "r1", legacyPath)
	if err != nil {
		t.Fatalf("NormalizeLegacyReceiptImageReference: %v", err)
	}
	img, copied, missing, err := materializeReceiptImage(context.Background(), local, "r1", ref, time.Now())
	if err != nil {
		t.Fatalf("materializeReceiptImage: %v", err)
	}
	if copied || missing {
		t.Fatalf("copied=%v missing=%v, want false/false", copied, missing)
	}
	if img.StorageKey != "receipts/r1/1.jpg" {
		t.Fatalf("StorageKey = %q want legacy key", img.StorageKey)
	}
	if _, err := os.Stat(filepath.Join(root, "receipts", "r1", "original", "1.jpg")); !os.IsNotExist(err) {
		t.Fatalf("canonical copy unexpectedly created; stat err=%v", err)
	}
}

func TestReconcileReceiptImagesWarnsAndContinuesOnRowFailure(t *testing.T) {
	root := t.TempDir()
	database, err := sql.Open("sqlite", filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer database.Close()
	if _, err := database.Exec(`
		CREATE TABLE receipts (
			id TEXT PRIMARY KEY,
			image_paths TEXT,
			created_at DATETIME NOT NULL
		);
		CREATE TABLE receipt_images (
			id TEXT PRIMARY KEY,
			receipt_id TEXT NOT NULL,
			kind TEXT NOT NULL CHECK (kind = 'processed'),
			page_number INTEGER NOT NULL,
			storage_key TEXT NOT NULL,
			mime_type TEXT NOT NULL,
			size_bytes INTEGER NOT NULL DEFAULT 0,
			sha256 TEXT,
			created_at DATETIME NOT NULL,
			deleted_at DATETIME,
			UNIQUE(receipt_id, kind, page_number)
		);
	`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	legacyPath := filepath.Join(root, "receipts", "r1", "1.jpg")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(legacyPath, []byte("legacy"), 0o644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	if _, err := database.Exec(
		"INSERT INTO receipts (id, image_paths, created_at) VALUES ('r1', ?, ?)",
		legacyPath, time.Now().UTC(),
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}

	summary, err := ReconcileReceiptImages(context.Background(), database, root, nil)
	if err != nil {
		t.Fatalf("ReconcileReceiptImages returned fatal error: %v", err)
	}
	if summary.Warnings == 0 {
		t.Fatalf("Warnings = 0, want row failure warning")
	}
	if summary.RowsUpserted != 0 {
		t.Fatalf("RowsUpserted = %d want 0", summary.RowsUpserted)
	}
}

func TestReconcileReceiptImagesRemovesUUIDOrphanDirs(t *testing.T) {
	root := t.TempDir()
	database, err := sql.Open("sqlite", filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer database.Close()
	if _, err := database.Exec(`
		CREATE TABLE receipts (
			id TEXT PRIMARY KEY,
			image_paths TEXT,
			created_at DATETIME NOT NULL
		);
		CREATE TABLE receipt_images (
			id TEXT PRIMARY KEY,
			receipt_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			page_number INTEGER NOT NULL,
			storage_key TEXT NOT NULL,
			mime_type TEXT NOT NULL,
			size_bytes INTEGER NOT NULL DEFAULT 0,
			sha256 TEXT,
			created_at DATETIME NOT NULL,
			deleted_at DATETIME,
			UNIQUE(receipt_id, kind, page_number)
		);
	`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	knownID := "11111111-2222-4333-8444-555555555555"
	orphanID := "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	if _, err := database.Exec(
		"INSERT INTO receipts (id, image_paths, created_at) VALUES (?, '', ?)",
		knownID, time.Now().UTC(),
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	for _, id := range []string{knownID, orphanID, "not-a-uuid"} {
		p := filepath.Join(root, "receipts", id, "1.jpg")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", id, err)
		}
		if err := os.WriteFile(p, []byte(id), 0o644); err != nil {
			t.Fatalf("write %s: %v", id, err)
		}
	}

	summary, err := ReconcileReceiptImages(context.Background(), database, root, nil)
	if err != nil {
		t.Fatalf("ReconcileReceiptImages: %v", err)
	}
	if summary.OrphanDirsRemoved != 1 {
		t.Fatalf("OrphanDirsRemoved = %d want 1", summary.OrphanDirsRemoved)
	}
	if _, err := os.Stat(filepath.Join(root, "receipts", orphanID)); !os.IsNotExist(err) {
		t.Fatalf("orphan UUID dir should be removed; stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "receipts", knownID)); err != nil {
		t.Fatalf("known receipt dir should remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "receipts", "not-a-uuid")); err != nil {
		t.Fatalf("non-UUID dir should remain: %v", err)
	}
}
