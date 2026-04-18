package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mstefanko/cartledger/internal/models"
)

// newBackupTestStore opens a fresh file-backed SQLite DB (modernc.org/sqlite
// does not support ":memory:" reliably across multiple connections in this
// project; we follow the integration-test pattern of a TempDir file).
func newBackupTestStore(t *testing.T) (*BackupStore, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := RunMigrations(database); err != nil {
		database.Close()
		t.Fatalf("RunMigrations: %v", err)
	}
	return NewBackupStore(database), func() { database.Close() }
}

func TestBackupStore_CreateAndGet(t *testing.T) {
	store, cleanup := newBackupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	id, err := store.Create(ctx, "backup-abc.tar.gz", 20)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id == "" {
		t.Fatalf("expected non-empty id")
	}

	got, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatalf("expected row, got nil")
	}
	if got.Status != models.BackupStatusRunning {
		t.Errorf("status = %q, want running", got.Status)
	}
	if got.Filename != "backup-abc.tar.gz" {
		t.Errorf("filename = %q", got.Filename)
	}
	if got.SchemaVersion != 20 {
		t.Errorf("schema_version = %d, want 20", got.SchemaVersion)
	}
	if got.SizeBytes != nil {
		t.Errorf("expected nil SizeBytes on running row, got %v", *got.SizeBytes)
	}
	if got.CompletedAt != nil {
		t.Errorf("expected nil CompletedAt on running row")
	}
}

func TestBackupStore_UpdateStatusComplete(t *testing.T) {
	store, cleanup := newBackupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	id, err := store.Create(ctx, "b.tar.gz", 20)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	size := int64(12345)
	missing := 2
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.UpdateStatus(ctx, id, models.BackupStatusComplete, BackupUpdateOpts{
		SizeBytes:     &size,
		MissingImages: &missing,
		CompletedAt:   &now,
	}); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	got, err := store.Get(ctx, id)
	if err != nil || got == nil {
		t.Fatalf("Get: %v / nil=%v", err, got == nil)
	}
	if got.Status != models.BackupStatusComplete {
		t.Errorf("status = %q", got.Status)
	}
	if got.SizeBytes == nil || *got.SizeBytes != size {
		t.Errorf("size_bytes mismatch: %v", got.SizeBytes)
	}
	if got.MissingImages != missing {
		t.Errorf("missing_images = %d, want %d", got.MissingImages, missing)
	}
	if got.CompletedAt == nil {
		t.Errorf("expected CompletedAt set")
	}
	if got.Error != nil {
		t.Errorf("expected nil Error, got %v", *got.Error)
	}
}

func TestBackupStore_UpdateStatusFailed(t *testing.T) {
	store, cleanup := newBackupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	id, err := store.Create(ctx, "b.tar.gz", 20)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	msg := "disk full"
	if err := store.UpdateStatus(ctx, id, models.BackupStatusFailed, BackupUpdateOpts{
		Error: &msg,
	}); err != nil {
		t.Fatalf("UpdateStatus failed: %v", err)
	}
	got, _ := store.Get(ctx, id)
	if got.Status != models.BackupStatusFailed {
		t.Errorf("status = %q", got.Status)
	}
	if got.Error == nil || *got.Error != msg {
		t.Errorf("error = %v, want %q", got.Error, msg)
	}
}

func TestBackupStore_UpdateStatusRejectsUnknown(t *testing.T) {
	store, cleanup := newBackupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	id, _ := store.Create(ctx, "b.tar.gz", 20)
	err := store.UpdateStatus(ctx, id, "bogus", BackupUpdateOpts{})
	if err == nil {
		t.Fatalf("expected error on invalid status")
	}
}

func TestBackupStore_ListOrderedDesc(t *testing.T) {
	store, cleanup := newBackupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	id1, _ := store.Create(ctx, "a.tar.gz", 20)
	time.Sleep(10 * time.Millisecond)
	id2, _ := store.Create(ctx, "b.tar.gz", 20)
	time.Sleep(10 * time.Millisecond)
	id3, _ := store.Create(ctx, "c.tar.gz", 20)

	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(list))
	}
	if list[0].ID != id3 || list[1].ID != id2 || list[2].ID != id1 {
		t.Errorf("order wrong: %q %q %q", list[0].ID, list[1].ID, list[2].ID)
	}
}

func TestBackupStore_Delete(t *testing.T) {
	store, cleanup := newBackupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	id, _ := store.Create(ctx, "b.tar.gz", 20)
	if err := store.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, _ := store.Get(ctx, id)
	if got != nil {
		t.Errorf("expected nil after delete, got %+v", got)
	}
	if err := store.Delete(ctx, id); err == nil {
		t.Errorf("expected error on second delete")
	}
}

func TestBackupStore_DeleteOldest_KeepsNewest(t *testing.T) {
	store, cleanup := newBackupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Create 5 complete backups.
	var ids []string
	for i := 0; i < 5; i++ {
		id, _ := store.Create(ctx, "b.tar.gz", 20)
		size := int64(10)
		now := time.Now().UTC()
		if err := store.UpdateStatus(ctx, id, models.BackupStatusComplete, BackupUpdateOpts{
			SizeBytes:   &size,
			CompletedAt: &now,
		}); err != nil {
			t.Fatalf("UpdateStatus: %v", err)
		}
		ids = append(ids, id)
		time.Sleep(5 * time.Millisecond)
	}

	removed, err := store.DeleteOldest(ctx, 3)
	if err != nil {
		t.Fatalf("DeleteOldest: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("expected 2 removed, got %d", len(removed))
	}
	// The two oldest are ids[0] and ids[1].
	if removed[0].ID != ids[1] && removed[1].ID != ids[0] {
		t.Errorf("removed the wrong rows: %+v (ids=%v)", removed, ids)
	}

	list, _ := store.List(ctx)
	if len(list) != 3 {
		t.Errorf("expected 3 rows remaining, got %d", len(list))
	}
}

func TestBackupStore_DeleteOldest_NoopWhenUnderLimit(t *testing.T) {
	store, cleanup := newBackupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	id, _ := store.Create(ctx, "b.tar.gz", 20)
	now := time.Now().UTC()
	size := int64(1)
	_ = store.UpdateStatus(ctx, id, models.BackupStatusComplete, BackupUpdateOpts{
		SizeBytes:   &size,
		CompletedAt: &now,
	})

	removed, err := store.DeleteOldest(ctx, 5)
	if err != nil {
		t.Fatalf("DeleteOldest: %v", err)
	}
	if len(removed) != 0 {
		t.Errorf("expected 0 removed, got %d", len(removed))
	}
}

func TestBackupStore_DeleteOldest_IgnoresRunningAndFailed(t *testing.T) {
	store, cleanup := newBackupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// One running, one failed, two complete.
	_, _ = store.Create(ctx, "run.tar.gz", 20)
	failID, _ := store.Create(ctx, "fail.tar.gz", 20)
	msg := "boom"
	_ = store.UpdateStatus(ctx, failID, models.BackupStatusFailed, BackupUpdateOpts{Error: &msg})

	for i := 0; i < 2; i++ {
		id, _ := store.Create(ctx, "c.tar.gz", 20)
		now := time.Now().UTC()
		size := int64(1)
		_ = store.UpdateStatus(ctx, id, models.BackupStatusComplete, BackupUpdateOpts{
			SizeBytes: &size, CompletedAt: &now,
		})
		time.Sleep(5 * time.Millisecond)
	}

	removed, err := store.DeleteOldest(ctx, 1)
	if err != nil {
		t.Fatalf("DeleteOldest: %v", err)
	}
	if len(removed) != 1 {
		t.Errorf("expected exactly 1 complete row removed, got %d", len(removed))
	}
	// Running + failed rows still present.
	list, _ := store.List(ctx)
	var running, failed int
	for _, b := range list {
		switch b.Status {
		case models.BackupStatusRunning:
			running++
		case models.BackupStatusFailed:
			failed++
		}
	}
	if running != 1 || failed != 1 {
		t.Errorf("expected 1 running + 1 failed preserved, got running=%d failed=%d", running, failed)
	}
}

func TestBackupStore_ReconcileRunning(t *testing.T) {
	store, cleanup := newBackupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	runID, _ := store.Create(ctx, "run.tar.gz", 20)

	// Add a complete one; reconcile must not touch it.
	completeID, _ := store.Create(ctx, "ok.tar.gz", 20)
	size := int64(1)
	now := time.Now().UTC()
	_ = store.UpdateStatus(ctx, completeID, models.BackupStatusComplete, BackupUpdateOpts{
		SizeBytes: &size, CompletedAt: &now,
	})

	if err := store.ReconcileRunning(ctx); err != nil {
		t.Fatalf("ReconcileRunning: %v", err)
	}

	gotRun, _ := store.Get(ctx, runID)
	if gotRun.Status != models.BackupStatusFailed {
		t.Errorf("running row not flipped: status=%q", gotRun.Status)
	}
	if gotRun.Error == nil || *gotRun.Error != "server restarted during backup" {
		t.Errorf("reconcile error message = %v", gotRun.Error)
	}
	if gotRun.CompletedAt == nil {
		t.Errorf("expected CompletedAt set after reconcile")
	}

	gotComplete, _ := store.Get(ctx, completeID)
	if gotComplete.Status != models.BackupStatusComplete {
		t.Errorf("complete row disturbed: status=%q", gotComplete.Status)
	}
}
