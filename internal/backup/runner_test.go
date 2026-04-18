package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
	"github.com/mstefanko/cartledger/internal/models"
)

// fakeDisk implements DiskChecker with a programmable free-byte value for
// preflight tests that don't want to touch the host filesystem.
type fakeDisk struct {
	free uint64
	err  error
}

func (f fakeDisk) FreeBytes(path string) (uint64, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.free, nil
}

// setup stands up a fresh DATA_DIR with a migrated DB, writes two receipt
// image files referenced by a receipts row, and returns (runner, cleanup).
func setup(t *testing.T) (*Runner, *config.Config, *sql.DB, func()) {
	t.Helper()

	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "cartledger.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.RunMigrations(database); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Seed a household + receipt referencing two image paths.
	if _, err := database.Exec(
		`INSERT INTO households (id, name) VALUES (?, ?)`, "h1", "Test",
	); err != nil {
		t.Fatalf("seed household: %v", err)
	}
	// Two receipt images: both present on disk initially.
	r1 := filepath.Join(dataDir, "receipts", "aaaa", "front.jpg")
	r2 := filepath.Join(dataDir, "receipts", "aaaa", "back.jpg")
	for _, p := range []string{r1, r2} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte("fake jpg bytes"), 0o644); err != nil {
			t.Fatalf("write image: %v", err)
		}
	}
	// Store as absolute paths to match how receipts.go writes them.
	imagesJSON := `["` + r1 + `","` + r2 + `"]`
	if _, err := database.Exec(
		`INSERT INTO receipts (id, household_id, receipt_date, image_paths)
		 VALUES (?, ?, '2026-04-18', ?)`,
		"rcpt-1", "h1", imagesJSON,
	); err != nil {
		t.Fatalf("seed receipt: %v", err)
	}

	cfg := &config.Config{
		Port:              "8079",
		DataDir:           dataDir,
		BackupRetainCount: 3,
	}
	// Create BackupDir ourselves since we're not going through config.Load.
	if err := os.MkdirAll(cfg.BackupDir(), 0o755); err != nil {
		t.Fatalf("mkdir backup: %v", err)
	}

	store := db.NewBackupStore(database)
	r := NewRunner(database, store, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	// Plenty of free bytes so preflight always passes unless a test overrides.
	r.SetDiskChecker(fakeDisk{free: 1 << 40})

	cleanup := func() { database.Close() }
	return r, cfg, database, cleanup
}

func TestRunner_Start_HappyPath(t *testing.T) {
	r, cfg, database, cleanup := setup(t)
	defer cleanup()

	id, err := r.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if id == "" {
		t.Fatalf("expected non-empty id")
	}

	// Row must be complete with size > 0 and missing_images = 0.
	row, err := db.NewBackupStore(database).Get(context.Background(), id)
	if err != nil || row == nil {
		t.Fatalf("Get: err=%v row=%v", err, row)
	}
	if row.Status != models.BackupStatusComplete {
		t.Errorf("status = %q, want complete", row.Status)
	}
	if row.SizeBytes == nil || *row.SizeBytes <= 0 {
		t.Errorf("size_bytes invalid: %v", row.SizeBytes)
	}
	if row.MissingImages != 0 {
		t.Errorf("missing_images = %d, want 0", row.MissingImages)
	}

	// File exists under BackupDir.
	archivePath := filepath.Join(cfg.BackupDir(), row.Filename)
	info, err := os.Stat(archivePath)
	if err != nil {
		t.Fatalf("stat archive: %v", err)
	}
	if info.Size() <= 0 {
		t.Errorf("archive empty")
	}

	// Archive is extractable and contains cartledger.db + the two images.
	have := map[string]bool{}
	f, _ := os.Open(archivePath)
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
	}
	if !have["cartledger.db"] {
		t.Errorf("archive missing cartledger.db: %v", have)
	}
	if !have["receipts/aaaa/front.jpg"] || !have["receipts/aaaa/back.jpg"] {
		t.Errorf("archive missing receipt images: %v", have)
	}
}

// TestRunner_Start_ArchivesProductsAlongsideReceipts proves that when both
// DATA_DIR/receipts/<uuid>/img.png AND DATA_DIR/products/<uuid>/img.png are
// present on disk, the runner-produced archive contains both at the expected
// relative paths. This is the regression guard for the Phase A verifier
// finding: backups previously dropped products/ silently, so a restore would
// lose every product image even though product_images.image_path still pointed
// at them.
func TestRunner_Start_ArchivesProductsAlongsideReceipts(t *testing.T) {
	r, cfg, database, cleanup := setup(t)
	defer cleanup()

	// Seed a product image alongside the receipt images already placed by setup().
	productImg := filepath.Join(cfg.DataDir, "products", "pppp", "thumb.png")
	if err := os.MkdirAll(filepath.Dir(productImg), 0o755); err != nil {
		t.Fatalf("mkdir products: %v", err)
	}
	if err := os.WriteFile(productImg, []byte("fake product png"), 0o644); err != nil {
		t.Fatalf("write product image: %v", err)
	}

	id, err := r.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	row, err := db.NewBackupStore(database).Get(context.Background(), id)
	if err != nil || row == nil {
		t.Fatalf("Get: err=%v row=%v", err, row)
	}

	archivePath := filepath.Join(cfg.BackupDir(), row.Filename)
	f, err := os.Open(archivePath)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer f.Close()
	gzr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	tr := tar.NewReader(gzr)
	have := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("tar read: %v", err)
		}
		have[hdr.Name] = true
	}
	for _, want := range []string{
		"cartledger.db",
		"receipts/aaaa/front.jpg",
		"receipts/aaaa/back.jpg",
		"products/pppp/thumb.png",
	} {
		if !have[want] {
			t.Errorf("archive missing %q; have=%v", want, have)
		}
	}
}

func TestRunner_Start_MissingImagesCounted(t *testing.T) {
	r, cfg, database, cleanup := setup(t)
	defer cleanup()

	// Delete one referenced image before the backup runs.
	orphan := filepath.Join(cfg.DataDir, "receipts", "aaaa", "back.jpg")
	if err := os.Remove(orphan); err != nil {
		t.Fatalf("remove orphan: %v", err)
	}

	id, err := r.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	row, _ := db.NewBackupStore(database).Get(context.Background(), id)
	if row.MissingImages != 1 {
		t.Errorf("missing_images = %d, want 1", row.MissingImages)
	}
	if row.Status != models.BackupStatusComplete {
		t.Errorf("status = %q, want complete (missing images is not a failure)", row.Status)
	}
}

func TestRunner_Start_ConcurrentReturnsAlreadyRunning(t *testing.T) {
	r, _, _, cleanup := setup(t)
	defer cleanup()

	// Manually occupy the semaphore so the next Start loses the race
	// deterministically — a tight goroutine race relying on scheduler
	// timing would be flaky.
	r.sem <- struct{}{}
	defer func() { <-r.sem }()

	_, err := r.Start(context.Background())
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("expected ErrAlreadyRunning, got %v", err)
	}
}

func TestRunner_Start_SemaphoreSerializes(t *testing.T) {
	// Exercises real concurrent Start calls. Exactly one should succeed;
	// the other must return ErrAlreadyRunning. After both complete, a third
	// call should succeed (slot released).
	r, _, _, cleanup := setup(t)
	defer cleanup()

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := r.Start(context.Background())
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	var ok, busy int
	for err := range results {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrAlreadyRunning):
			busy++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	// Either one succeeded and one was busy, or (if the first finished before
	// the second started) both succeeded. Both are correct interleavings.
	if ok < 1 {
		t.Errorf("expected at least one successful Start, got ok=%d busy=%d", ok, busy)
	}
	if ok+busy != 2 {
		t.Errorf("expected ok+busy=2, got ok=%d busy=%d", ok, busy)
	}

	// Third call after everyone drained: slot must be free.
	if _, err := r.Start(context.Background()); err != nil {
		t.Errorf("expected third Start to succeed, got %v", err)
	}
}

func TestRunner_Start_InsufficientSpace(t *testing.T) {
	r, _, _, cleanup := setup(t)
	defer cleanup()
	r.SetDiskChecker(fakeDisk{free: 1}) // 1 byte free → always insufficient

	_, err := r.Start(context.Background())
	if !errors.Is(err, ErrInsufficientSpace) {
		t.Fatalf("expected ErrInsufficientSpace, got %v", err)
	}
}

func TestRunner_Start_Prunes_OlderThanRetention(t *testing.T) {
	r, cfg, database, cleanup := setup(t)
	defer cleanup()
	cfg.BackupRetainCount = 1

	// First backup succeeds; second should prune the first.
	id1, err := r.Start(context.Background())
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	time.Sleep(1100 * time.Millisecond) // ensure the second archive name differs (1-second timestamp resolution)
	id2, err := r.Start(context.Background())
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if id1 == id2 {
		t.Fatalf("expected distinct ids")
	}

	list, _ := db.NewBackupStore(database).List(context.Background())
	if len(list) != 1 {
		t.Fatalf("expected 1 row after prune, got %d (%+v)", len(list), list)
	}
	if list[0].ID != id2 {
		t.Errorf("pruned wrong row: kept %s, want %s", list[0].ID, id2)
	}

	// File for id1 should be gone from disk.
	row1, _ := db.NewBackupStore(database).Get(context.Background(), id1)
	if row1 != nil {
		t.Errorf("expected id1 row deleted, got %+v", row1)
	}
}
