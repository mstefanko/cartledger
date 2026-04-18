package db

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedTestDB opens a migrated DB at path and writes a few rows so the backup
// has non-trivial content.
func seedTestDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	database, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := RunMigrations(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Insert a household so we're not backing up an empty schema.
	if _, err := database.Exec(
		`INSERT INTO households (id, name) VALUES (?, ?)`,
		"h1", "Test Household",
	); err != nil {
		t.Fatalf("seed household: %v", err)
	}
	return database
}

// seedReceiptsDir writes a couple of fake image files under DATA_DIR/receipts
// so the backup has image bytes to tar up.
func seedReceiptsDir(t *testing.T, dataDir string) {
	t.Helper()
	r1 := filepath.Join(dataDir, "receipts", "aaaa", "front.jpg")
	r2 := filepath.Join(dataDir, "receipts", "bbbb", "front.jpg")
	for _, p := range []string{r1, r2} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte("not really a jpeg, but tar-friendly bytes"), 0o644); err != nil {
			t.Fatalf("write receipt: %v", err)
		}
	}
}

func TestBackupProducesArchiveWithManifest(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "cartledger.db")
	database := seedTestDB(t, dbPath)
	defer database.Close()
	seedReceiptsDir(t, dataDir)

	outPath := filepath.Join(t.TempDir(), "backup.tar.gz")
	res, err := Backup(BackupOptions{
		DB:                database,
		DataDir:           dataDir,
		OutputPath:        outPath,
		CartledgerVersion: "test",
		CartledgerCommit:  "deadbeef",
		IncludeReceipts:   true,
	})
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if res.SHA256 == "" || len(res.SHA256) != 64 {
		t.Fatalf("expected 64-char sha256, got %q", res.SHA256)
	}
	if res.Bytes <= 0 {
		t.Fatalf("expected positive archive size, got %d", res.Bytes)
	}

	// Walk the archive to verify it contains cartledger.db, MANIFEST.json,
	// and both receipt files.
	have := map[string]bool{}
	var manifestBytes []byte

	f, err := os.Open(outPath)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer f.Close()
	gzr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("tar read: %v", err)
		}
		have[hdr.Name] = true
		if hdr.Name == "MANIFEST.json" {
			manifestBytes, err = io.ReadAll(tr)
			if err != nil {
				t.Fatalf("read manifest: %v", err)
			}
		}
	}
	for _, want := range []string{
		"cartledger.db",
		"MANIFEST.json",
		"receipts/aaaa/front.jpg",
		"receipts/bbbb/front.jpg",
	} {
		if !have[want] {
			t.Errorf("archive missing %q; have=%v", want, have)
		}
	}

	var m Manifest
	if err := json.Unmarshal(manifestBytes, &m); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if m.SchemaVersion == 0 {
		t.Errorf("manifest schema_version=0; expected >0 after migrations")
	}
	if m.FileCount < 3 {
		t.Errorf("expected file_count>=3 (db + 2 receipts), got %d", m.FileCount)
	}
	if m.CartledgerVersion != "test" {
		t.Errorf("version mismatch: %q", m.CartledgerVersion)
	}
}

// seedProductsDir writes a fake product image under DATA_DIR/products so a
// backup test can assert product images are archived alongside receipts.
func seedProductsDir(t *testing.T, dataDir string) {
	t.Helper()
	p := filepath.Join(dataDir, "products", "pppp", "thumb.png")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte("fake png bytes"), 0o644); err != nil {
		t.Fatalf("write product image: %v", err)
	}
}

// TestBackupIncludesProducts proves that when both DATA_DIR/receipts/ and
// DATA_DIR/products/ exist, the archive contains entries from both trees
// (products/ archived unconditionally — no operator opt-out).
func TestBackupIncludesProducts(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "cartledger.db")
	database := seedTestDB(t, dbPath)
	defer database.Close()
	seedReceiptsDir(t, dataDir)
	seedProductsDir(t, dataDir)

	outPath := filepath.Join(t.TempDir(), "backup.tar.gz")
	if _, err := Backup(BackupOptions{
		DB:              database,
		DataDir:         dataDir,
		OutputPath:      outPath,
		IncludeReceipts: true,
	}); err != nil {
		t.Fatalf("backup: %v", err)
	}

	have := map[string]bool{}
	f, err := os.Open(outPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	gzr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("tar read: %v", err)
		}
		// Guard: every entry path must be relative (zip-slip defense).
		if filepath.IsAbs(hdr.Name) || strings.HasPrefix(hdr.Name, "/") {
			t.Errorf("absolute tar entry: %q", hdr.Name)
		}
		have[hdr.Name] = true
	}
	for _, want := range []string{
		"receipts/aaaa/front.jpg",
		"receipts/bbbb/front.jpg",
		"products/pppp/thumb.png",
	} {
		if !have[want] {
			t.Errorf("archive missing %q; have=%v", want, have)
		}
	}
}

// TestBackupNoProductsDir proves that a fresh install (no products/ at all)
// still produces a valid backup without erroring out.
func TestBackupNoProductsDir(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "cartledger.db")
	database := seedTestDB(t, dbPath)
	defer database.Close()
	seedReceiptsDir(t, dataDir)
	// Intentionally do NOT create products/.

	outPath := filepath.Join(t.TempDir(), "backup.tar.gz")
	res, err := Backup(BackupOptions{
		DB:              database,
		DataDir:         dataDir,
		OutputPath:      outPath,
		IncludeReceipts: true,
	})
	if err != nil {
		t.Fatalf("backup on install-without-products failed: %v", err)
	}
	if res.Bytes <= 0 {
		t.Errorf("expected non-empty archive")
	}
}

func TestBackupSkipReceipts(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "cartledger.db")
	database := seedTestDB(t, dbPath)
	defer database.Close()
	seedReceiptsDir(t, dataDir)

	outPath := filepath.Join(t.TempDir(), "backup.tar.gz")
	if _, err := Backup(BackupOptions{
		DB:              database,
		DataDir:         dataDir,
		OutputPath:      outPath,
		IncludeReceipts: false, // explicit skip
	}); err != nil {
		t.Fatalf("backup: %v", err)
	}

	f, _ := os.Open(outPath)
	defer f.Close()
	gzr, _ := gzip.NewReader(f)
	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(hdr.Name, "receipts/") {
			t.Errorf("expected no receipts/ entries when IncludeReceipts=false, got %q", hdr.Name)
		}
	}
}

func TestRestoreRoundTrip(t *testing.T) {
	// Phase 1: seed + backup.
	srcDir := t.TempDir()
	srcDB := filepath.Join(srcDir, "cartledger.db")
	database := seedTestDB(t, srcDB)
	seedReceiptsDir(t, srcDir)

	archivePath := filepath.Join(t.TempDir(), "backup.tar.gz")
	if _, err := Backup(BackupOptions{
		DB:              database,
		DataDir:         srcDir,
		OutputPath:      archivePath,
		IncludeReceipts: true,
	}); err != nil {
		t.Fatalf("backup: %v", err)
	}
	database.Close()

	// Phase 2: restore into fresh dir.
	destDir := filepath.Join(t.TempDir(), "restored") // doesn't exist yet
	maxVer, err := MaxMigrationVersion()
	if err != nil {
		t.Fatalf("max version: %v", err)
	}
	res, err := Restore(RestoreOptions{
		ArchivePath:      archivePath,
		DataDir:          destDir,
		MaxSchemaVersion: maxVer,
	})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if res.FileCount < 3 {
		t.Errorf("expected >=3 files restored, got %d", res.FileCount)
	}

	// Phase 3: DB file exists and contains the seeded household.
	restoredDB := filepath.Join(destDir, "cartledger.db")
	if _, err := os.Stat(restoredDB); err != nil {
		t.Fatalf("restored db missing: %v", err)
	}
	db2, err := Open(restoredDB)
	if err != nil {
		t.Fatalf("open restored: %v", err)
	}
	defer db2.Close()
	var name string
	if err := db2.QueryRow(`SELECT name FROM households WHERE id='h1'`).Scan(&name); err != nil {
		t.Fatalf("query seeded household: %v", err)
	}
	if name != "Test Household" {
		t.Errorf("want %q, got %q", "Test Household", name)
	}

	// Receipts exist.
	if _, err := os.Stat(filepath.Join(destDir, "receipts", "aaaa", "front.jpg")); err != nil {
		t.Errorf("restored receipt missing: %v", err)
	}
}

func TestRestoreRefusesExistingDB(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "cartledger.db"), []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Build a minimal valid archive by running a real backup first.
	srcDir := t.TempDir()
	database := seedTestDB(t, filepath.Join(srcDir, "cartledger.db"))
	defer database.Close()
	archivePath := filepath.Join(t.TempDir(), "b.tar.gz")
	if _, err := Backup(BackupOptions{
		DB:         database,
		DataDir:    srcDir,
		OutputPath: archivePath,
	}); err != nil {
		t.Fatalf("backup: %v", err)
	}

	_, err := Restore(RestoreOptions{
		ArchivePath:      archivePath,
		DataDir:          dataDir,
		MaxSchemaVersion: 9999,
	})
	if err == nil {
		t.Fatal("expected error restoring over existing cartledger.db, got nil")
	}
	if !strings.Contains(err.Error(), "refusing to restore") {
		t.Errorf("expected refusal error, got: %v", err)
	}
}

func TestRestoreRejectsFutureSchema(t *testing.T) {
	// Craft an archive by hand with manifest schema_version=999.
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	// Fake db file.
	if err := tw.WriteHeader(&tar.Header{Name: "cartledger.db", Size: 4, Mode: 0o644}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("fake")); err != nil {
		t.Fatal(err)
	}
	mBytes, _ := json.Marshal(Manifest{SchemaVersion: 999, CartledgerVersion: "future"})
	if err := tw.WriteHeader(&tar.Header{Name: "MANIFEST.json", Size: int64(len(mBytes)), Mode: 0o644}); err != nil {
		t.Fatal(err)
	}
	tw.Write(mBytes)
	tw.Close()
	gzw.Close()

	archive := filepath.Join(t.TempDir(), "future.tar.gz")
	if err := os.WriteFile(archive, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Restore(RestoreOptions{
		ArchivePath:      archive,
		DataDir:          t.TempDir(),
		MaxSchemaVersion: 10,
	})
	if err == nil {
		t.Fatal("expected schema-version abort, got nil")
	}
	if !strings.Contains(err.Error(), "schema_version=999") {
		t.Errorf("expected schema-version error, got: %v", err)
	}
}

func TestRestoreRejectsTraversal(t *testing.T) {
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	// Traversal entry.
	if err := tw.WriteHeader(&tar.Header{Name: "../../../etc/passwd", Size: 4, Mode: 0o644}); err != nil {
		t.Fatal(err)
	}
	tw.Write([]byte("pwnd"))
	mBytes, _ := json.Marshal(Manifest{SchemaVersion: 1, CartledgerVersion: "test"})
	tw.WriteHeader(&tar.Header{Name: "MANIFEST.json", Size: int64(len(mBytes)), Mode: 0o644})
	tw.Write(mBytes)
	tw.Close()
	gzw.Close()

	archive := filepath.Join(t.TempDir(), "evil.tar.gz")
	if err := os.WriteFile(archive, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Restore(RestoreOptions{
		ArchivePath:      archive,
		DataDir:          t.TempDir(),
		MaxSchemaVersion: 100,
	})
	if err == nil {
		t.Fatal("expected traversal rejection")
	}
	if !strings.Contains(err.Error(), "unsafe tar entry path") {
		t.Errorf("expected traversal error, got: %v", err)
	}
}

func TestMaxMigrationVersion(t *testing.T) {
	v, err := MaxMigrationVersion()
	if err != nil {
		t.Fatal(err)
	}
	if v < 15 {
		t.Errorf("expected >=15 migrations embedded, got %d", v)
	}
}
