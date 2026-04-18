package imaging

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// uuidA / uuidB are syntactically valid length-36 UUID v4 strings. The
// janitor only cares about the length check, but using real-looking IDs
// keeps the test data readable in a dropped breakpoint.
const (
	uuidA = "11111111-2222-4333-8444-555555555555"
	uuidB = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
)

// writeFileAt creates a file under dir with the given relative path,
// contents, and mtime. Parent directories are created as needed.
func writeFileAt(t *testing.T, dir, rel string, contents string, mtime time.Time) string {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
	if err := os.Chtimes(full, mtime, mtime); err != nil {
		t.Fatalf("chtimes %s: %v", full, err)
	}
	return full
}

// countingRecorder captures RecordRetentionDeleted calls for verification.
type countingRecorder struct {
	mu      sync.Mutex
	reasons []string
	total   int
}

func (r *countingRecorder) RecordRetentionDeleted(reason string, n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reasons = append(r.reasons, reason)
	r.total += n
}

func (r *countingRecorder) Total() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.total
}

// TestJanitor_DeletesAgedOriginals — files older than the retention
// window are removed, and fresh files are preserved. Verifies the core
// happy path plus the metric call.
func TestJanitor_DeletesAgedOriginals(t *testing.T) {
	t.Parallel()

	data := t.TempDir()
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	// 100 days old — older than the 30-day window below.
	old := now.Add(-100 * 24 * time.Hour)
	// 5 days old — inside the window.
	fresh := now.Add(-5 * 24 * time.Hour)

	oldFile := writeFileAt(t, data, filepath.Join("receipts", uuidA, "0.jpg"), "old-original", old)
	freshFile := writeFileAt(t, data, filepath.Join("receipts", uuidA, "1.jpg"), "fresh-original", fresh)

	j := NewJanitor(data, 30, time.Hour)
	j.now = func() time.Time { return now }
	rec := &countingRecorder{}
	j.SetMetrics(rec)

	j.sweep(context.Background())

	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Errorf("expected aged original %s to be deleted; stat err=%v", oldFile, err)
	}
	if _, err := os.Stat(freshFile); err != nil {
		t.Errorf("expected fresh original %s to remain; stat err=%v", freshFile, err)
	}
	if rec.Total() != 1 {
		t.Errorf("expected metric record total=1, got %d (reasons=%v)", rec.Total(), rec.reasons)
	}
}

// TestJanitor_NeverDeletesProcessed — processed_* files are protected
// even when their mtime predates the retention window by a wide margin.
// This is the critical invariant: the review UI has nothing else to
// show.
func TestJanitor_NeverDeletesProcessed(t *testing.T) {
	t.Parallel()

	data := t.TempDir()
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	ancient := now.Add(-365 * 24 * time.Hour)

	// An ancient processed_* file — must survive.
	processed := writeFileAt(t, data, filepath.Join("receipts", uuidB, "processed_0.jpg"), "keep-me", ancient)
	// An ancient original — must be removed so we know the sweep DID
	// run and fire the mtime check (protecting against a no-op false
	// pass).
	original := writeFileAt(t, data, filepath.Join("receipts", uuidB, "0.jpg"), "delete-me", ancient)

	j := NewJanitor(data, 30, time.Hour)
	j.now = func() time.Time { return now }

	j.sweep(context.Background())

	if _, err := os.Stat(processed); err != nil {
		t.Errorf("processed_* file was deleted or unreadable: %v", err)
	}
	if _, err := os.Stat(original); !os.IsNotExist(err) {
		t.Errorf("expected ancient original to be deleted; stat err=%v", err)
	}
}

// TestJanitor_SkipsNonUUIDDirs — the janitor must not touch anything
// outside UUID-shaped subdirectories, even if aged. Defense against
// the operator dropping an unrelated folder under DATA_DIR/receipts/.
func TestJanitor_SkipsNonUUIDDirs(t *testing.T) {
	t.Parallel()

	data := t.TempDir()
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	ancient := now.Add(-365 * 24 * time.Hour)

	// Wrong length — skipped.
	stray := writeFileAt(t, data, filepath.Join("receipts", "not-a-uuid", "0.jpg"), "stray", ancient)
	// Top-level file — skipped.
	top := writeFileAt(t, data, filepath.Join("receipts", "README.txt"), "top-level", ancient)

	j := NewJanitor(data, 30, time.Hour)
	j.now = func() time.Time { return now }
	j.sweep(context.Background())

	if _, err := os.Stat(stray); err != nil {
		t.Errorf("stray file under non-uuid dir should be preserved: %v", err)
	}
	if _, err := os.Stat(top); err != nil {
		t.Errorf("top-level file in receipts/ should be preserved: %v", err)
	}
}

// TestJanitor_DisabledNoop — days<=0 disables the janitor entirely;
// Start returns without launching a goroutine and no files are touched.
func TestJanitor_DisabledNoop(t *testing.T) {
	t.Parallel()

	data := t.TempDir()
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	ancient := now.Add(-365 * 24 * time.Hour)
	f := writeFileAt(t, data, filepath.Join("receipts", uuidA, "0.jpg"), "keep", ancient)

	j := NewJanitor(data, 0, time.Hour)
	j.now = func() time.Time { return now }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	j.Start(ctx)
	j.Stop()

	if _, err := os.Stat(f); err != nil {
		t.Errorf("disabled janitor must not delete anything: %v", err)
	}
}

// TestJanitor_StartStop — Start launches the goroutine, Stop cancels
// and returns promptly.
func TestJanitor_StartStop(t *testing.T) {
	t.Parallel()

	data := t.TempDir()
	j := NewJanitor(data, 30, 50*time.Millisecond)
	j.now = time.Now

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	j.Start(ctx)

	// Let at least one sweep fire.
	time.Sleep(75 * time.Millisecond)

	stopped := make(chan struct{})
	go func() {
		j.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not return within 2s")
	}

	// Second Stop is a no-op (idempotency).
	j.Stop()
}
