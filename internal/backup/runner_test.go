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

	// First backup succeeds; second should prune the first. No sleep needed:
	// filenames embed the DB-generated id, so back-to-back backups in the
	// same UTC second are guaranteed distinct on disk.
	id1, err := r.Start(context.Background())
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
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

// TestRunner_FilenameUniquenessUnderBurst runs N backups serialized through
// the semaphore and asserts every resulting filename is distinct. Guards
// against the second-granularity collision described in P1-2: two backups
// started in the same UTC second must produce distinct on-disk archive
// names (the DB-generated id is stamped into the filename for this).
func TestRunner_FilenameUniquenessUnderBurst(t *testing.T) {
	r, _, database, cleanup := setup(t)
	defer cleanup()

	seen := make(map[string]bool)
	for i := 0; i < 5; i++ {
		id, err := r.Start(context.Background())
		if err != nil {
			t.Fatalf("Start #%d: %v", i, err)
		}
		row, err := db.NewBackupStore(database).Get(context.Background(), id)
		if err != nil || row == nil {
			t.Fatalf("Get #%d: err=%v row=%v", i, err, row)
		}
		if seen[row.Filename] {
			t.Fatalf("filename collision at i=%d: %q", i, row.Filename)
		}
		seen[row.Filename] = true
	}
	if len(seen) != 5 {
		t.Errorf("expected 5 distinct filenames, got %d", len(seen))
	}
}

// TestRunner_FlockAcrossRunners verifies that two Runner instances pointing
// at the same BackupDir cannot both start a backup concurrently. Simulates
// the server + CLI scenario from P1-1: when one process holds the flock,
// the second process gets ErrAlreadyRunning — NOT a silent parallel run,
// and NOT a spurious "server restarted during backup" reconcile.
//
// Two Runner instances means two in-process semaphores, so the only thing
// stopping the second from proceeding is the cross-process flock.
func TestRunner_FlockAcrossRunners(t *testing.T) {
	// Share one DATA_DIR (hence one BackupDir hence one lockfile) between
	// two Runners. The first runner is returned only so we share its config
	// / DB; we don't Start through it because we want r2 to race on the
	// lockfile itself, not on r1's in-process semaphore.
	_, cfg, database, cleanup := setup(t)
	defer cleanup()

	store := db.NewBackupStore(database)
	r2 := NewRunner(database, store, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	r2.SetDiskChecker(fakeDisk{free: 1 << 40})

	// Manually occupy r1's flock (without going through Start, which would
	// also occupy the sem and complete synchronously). We need r2 to race
	// *only* on the flock, not on r1's per-process sem.
	lockPath := filepath.Join(cfg.BackupDir(), lockFilename)
	lock, err := acquireFileLock(lockPath)
	if err != nil {
		t.Fatalf("acquire test lock: %v", err)
	}

	// r2.Start must now fail with ErrAlreadyRunning because it can't get the
	// flock, even though r2's in-process sem is free.
	if _, err := r2.Start(context.Background()); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("r2.Start: expected ErrAlreadyRunning while flock held, got %v", err)
	}

	// Release and confirm r2 now succeeds.
	lock.Release()
	if _, err := r2.Start(context.Background()); err != nil {
		t.Errorf("r2.Start after lock release: want nil, got %v", err)
	}
}

// TestRunner_LockfileCreatedAndReleased verifies the flock file itself is
// created under BackupDir and that successive Start calls can re-acquire it
// (i.e. the runner is releasing properly). Not as strong as the two-Runner
// test above but faster and independent of internal flock semantics.
func TestRunner_LockfileCreatedAndReleased(t *testing.T) {
	r, cfg, _, cleanup := setup(t)
	defer cleanup()

	lockPath := filepath.Join(cfg.BackupDir(), lockFilename)

	// First Start: lockfile must exist after completion (we keep it around
	// so subsequent acquires don't race on mkdir).
	if _, err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start #1: %v", err)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Errorf("lockfile missing after first Start: %v", err)
	}

	// Second Start: must succeed — lock released, no residual flock.
	if _, err := r.Start(context.Background()); err != nil {
		t.Errorf("Start #2 after release: %v", err)
	}

	// Sanity: after all Starts complete, a fresh flock attempt must succeed.
	lock, err := acquireFileLock(lockPath)
	if err != nil {
		t.Fatalf("manual acquire after Starts: %v", err)
	}
	lock.Release()
}
