// Package backup orchestrates point-in-time database + receipt backups. It is
// intentionally split from internal/db/backup.go — the db package owns the
// low-level archive I/O (VACUUM INTO, tar.gz, zip-slip guards, schema-version
// check, SHA256), while this package is the orchestration layer: a per-process
// semaphore (one backup at a time), preflight disk checks, BackupStore row
// lifecycle (running → complete|failed), retention pruning, and metrics.
//
// Both the HTTP handler (internal/api/backup.go) and the `cartledger backup`
// CLI subcommand funnel through Runner.Start so production has exactly one
// code path.
package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
)

// Sentinel errors surfaced to callers; mapped to HTTP status codes in
// internal/api/backup.go (409 for ErrAlreadyRunning, 507 for ErrInsufficientSpace).
var (
	ErrAlreadyRunning     = errors.New("backup: another backup is already in progress")
	ErrInsufficientSpace  = errors.New("backup: insufficient disk space for backup")
)

// Recorder is the subset of metrics the runner emits. Kept here as an
// interface so internal/backup doesn't import internal/api (avoiding a
// potential cycle: internal/api -> internal/backup for the HTTP handler).
// Passing nil disables metric emission.
type Recorder interface {
	RecordBackupDuration(status string, d time.Duration)
	RecordBackupSize(bytes int64)
	RecordBackupMissingImages(n int)
}

// DiskChecker returns the number of free bytes available for a path. Pulled
// behind an interface so tests can inject a fake (platform-dependent Statfs
// is otherwise awkward to fake). The production implementation is in
// diskspace_unix.go / diskspace_windows.go build-tagged files.
type DiskChecker interface {
	FreeBytes(path string) (uint64, error)
}

// Runner orchestrates a single backup at a time. Concurrent calls to Start
// return ErrAlreadyRunning; the slot is released when the backup finishes
// (success or failure).
type Runner struct {
	db    *sql.DB
	store *db.BackupStore
	cfg   *config.Config
	log   *slog.Logger
	rec   Recorder
	disk  DiskChecker

	// sem: buffered chan of cap 1 used as a non-blocking mutex. A successful
	// send into sem acquires the slot; a full send means another Start is in
	// flight → caller gets ErrAlreadyRunning.
	sem chan struct{}

	// Build metadata passed through to the manifest (for debuggability of
	// which cartledger binary produced a given archive).
	version string
	commit  string

	// nowFn is injected for tests. Defaults to time.Now.
	nowFn func() time.Time

	mu sync.Mutex // guards version/commit if someone later adds hot reload
}

// NewRunner constructs a Runner. `rec` and `disk` may be nil; when nil the
// runner uses a default statfs-backed DiskChecker and emits no metrics.
func NewRunner(database *sql.DB, store *db.BackupStore, cfg *config.Config, log *slog.Logger, rec Recorder) *Runner {
	if log == nil {
		log = slog.Default()
	}
	return &Runner{
		db:    database,
		store: store,
		cfg:   cfg,
		log:   log,
		rec:   rec,
		disk:  defaultDiskChecker{},
		sem:   make(chan struct{}, 1),
		nowFn: time.Now,
	}
}

// SetBuildInfo threads goreleaser build vars through to the manifest. Called
// once at startup from cmd/server/serve.go.
func (r *Runner) SetBuildInfo(version, commit string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.version = version
	r.commit = commit
}

// SetDiskChecker swaps the disk-space checker. Tests use this to force
// ErrInsufficientSpace without needing a real statfs.
func (r *Runner) SetDiskChecker(d DiskChecker) {
	if d != nil {
		r.disk = d
	}
}

// Start kicks off a backup synchronously, returning the row ID only after
// the archive is written. The CLI and tests use this; HTTP handlers should
// prefer StartAsync so they can return 202 promptly.
//
// Flow (per PLAN-backup-and-export.md §Runner flow):
//  1. Non-blocking semaphore acquire.
//  2. Preflight disk-free check (need 2 × (db size + images size) free).
//  3. BackupStore.Create — row is now status='running'.
//  4. Invoke db.Backup, which does wal_checkpoint + VACUUM INTO + tar.
//  5. UpdateStatus → 'complete' with size/missing/completed_at.
//  6. DeleteOldest → remove files+rows beyond BackupRetainCount.
//  7. Emit metrics.
//
// On any failure after step 3: UpdateStatus → 'failed' + remove partial file.
func (r *Runner) Start(ctx context.Context) (string, error) {
	id, acquired, err := r.begin(ctx)
	if err != nil {
		return "", err
	}
	if !acquired {
		// begin acquired the sem but failed pre-row; nothing left to do.
		return "", nil
	}
	if err := r.runBody(ctx, id); err != nil {
		return id, err
	}
	return id, nil
}

// StartAsync is the HTTP-friendly entry point. It performs the preflight,
// semaphore acquisition, and row-creation synchronously (so callers get 202
// only after we know the backup is actually in flight) and runs the archive
// write in a goroutine. Returns the new row ID or one of ErrAlreadyRunning /
// ErrInsufficientSpace (so handlers can map those to 409 / 507 respectively).
func (r *Runner) StartAsync(ctx context.Context) (string, error) {
	id, acquired, err := r.begin(ctx)
	if err != nil {
		return "", err
	}
	if !acquired {
		return "", nil
	}
	// Run body on a detached context: the HTTP request's context will be
	// canceled as soon as we return 202, but the backup must keep going.
	bodyCtx := context.Background()
	go func() {
		if err := r.runBody(bodyCtx, id); err != nil {
			r.log.Error("backup: async body failed", "id", id, "err", err)
		}
	}()
	return id, nil
}

// begin holds the bookkeeping shared by Start and StartAsync: sem acquire,
// preflight, schema-version read, and row create. Returns (id, acquired, err).
// On (err != nil) the semaphore is already released. On (acquired==false)
// there was no error AND no work to do (shouldn't happen today, but keeps
// the contract explicit for future callers).
func (r *Runner) begin(ctx context.Context) (string, bool, error) {
	// 1. Acquire semaphore non-blocking.
	select {
	case r.sem <- struct{}{}:
	default:
		return "", false, ErrAlreadyRunning
	}

	// On any begin-time failure we must release the semaphore ourselves
	// because runBody (which owns the deferred release) won't run.
	releaseOnErr := func() { <-r.sem }

	// 2. Preflight disk space.
	if err := r.preflight(); err != nil {
		releaseOnErr()
		return "", false, err
	}

	// 3. Schema version for the manifest row.
	schemaVersion, err := db.CurrentSchemaVersion(r.db)
	if err != nil {
		releaseOnErr()
		return "", false, fmt.Errorf("read schema version: %w", err)
	}

	started := r.nowFn()
	filename := formatBackupFilename(started)

	id, err := r.store.Create(ctx, filename, schemaVersion)
	if err != nil {
		releaseOnErr()
		return "", false, fmt.Errorf("create backup row: %w", err)
	}
	return id, true, nil
}

// runBody is the archive-writing half of Start / StartAsync. It assumes the
// caller already acquired the semaphore and created a status='running' row;
// it owns the sem release + metric emission via defer.
func (r *Runner) runBody(ctx context.Context, id string) error {
	started := r.nowFn()
	finalStatus := "failed"
	defer func() {
		<-r.sem
		if r.rec != nil {
			r.rec.RecordBackupDuration(finalStatus, r.nowFn().Sub(started))
		}
	}()

	// Fetch the row to get the filename (assigned by begin).
	row, err := r.store.Get(ctx, id)
	if err != nil || row == nil {
		r.log.Error("backup: runBody could not load row", "id", id, "err", err)
		return fmt.Errorf("load backup row: %w", err)
	}
	archivePath := filepath.Join(r.cfg.BackupDir(), row.Filename)

	// Snapshot the image file list BEFORE the DB vacuum so our "missing
	// images" accounting reflects files present at the start of the run
	// (not an arbitrarily racy subset once vacuum is done). We don't enforce
	// anything on the snapshot — it's informational for the missing-image
	// diff below.
	preSnapshot := r.snapshotImageFiles()

	// 4. Call into the low-level archive writer. Note IncludeReceipts=true
	// so receipts/ is tarred; products/ is always tarred when present (no
	// opt-out — product_images.image_path would otherwise be dangling after
	// a restore). db.Backup's WalkDir tolerates individual file read errors
	// only implicitly; we compute missing_images by diffing the "referenced
	// from DB" set against the post-snapshot filesystem list.
	result, backupErr := db.Backup(db.BackupOptions{
		DB:                r.db,
		DataDir:           r.cfg.DataDir,
		OutputPath:        archivePath,
		CartledgerVersion: r.version,
		CartledgerCommit:  r.commit,
		IncludeReceipts:   true,
	})
	if backupErr != nil {
		r.markFailed(ctx, id, archivePath, backupErr)
		return fmt.Errorf("backup archive: %w", backupErr)
	}

	missingImages := r.countMissingImages(ctx, preSnapshot)

	completedAt := r.nowFn().UTC()
	size := result.Bytes
	if err := r.store.UpdateStatus(ctx, id, "complete", db.BackupUpdateOpts{
		SizeBytes:     &size,
		MissingImages: &missingImages,
		CompletedAt:   &completedAt,
	}); err != nil {
		// Row update failed but file is on disk; keep the file — it's still
		// usable. Log and return the error so the caller can surface the
		// partial-success state.
		r.log.Error("backup: complete row update failed", "id", id, "err", err)
		return fmt.Errorf("update backup row: %w", err)
	}

	// 6. Retention prune. Log failures but do not cascade-fail the backup.
	r.prune(ctx)

	// 7. Success-path metrics.
	finalStatus = "complete"
	if r.rec != nil {
		r.rec.RecordBackupSize(size)
		r.rec.RecordBackupMissingImages(missingImages)
	}

	r.log.Info("backup complete",
		"id", id,
		"path", archivePath,
		"bytes", size,
		"missing_images", missingImages,
		"schema_version", row.SchemaVersion,
	)

	return nil
}

// preflight verifies the filesystem hosting BackupDir has at least 2×
// (db size + images dir size) free bytes. The 2× multiplier covers the
// vacuum temp file + the tar+gzip output concurrently on disk.
func (r *Runner) preflight() error {
	dbSize := fileSizeOrZero(r.cfg.DBPath())
	imagesSize := dirSizeOrZero(filepath.Join(r.cfg.DataDir, "receipts")) +
		dirSizeOrZero(filepath.Join(r.cfg.DataDir, "products"))
	need := uint64(2 * (dbSize + imagesSize))

	free, err := r.disk.FreeBytes(r.cfg.BackupDir())
	if err != nil {
		// Soft-fail: if we can't check free space, continue — better than
		// refusing every backup on an exotic filesystem. Log at Warn so
		// operators notice.
		r.log.Warn("backup: free-bytes check failed; proceeding", "err", err)
		return nil
	}
	if free < need {
		return fmt.Errorf("%w: need ~%d bytes, have %d", ErrInsufficientSpace, need, free)
	}
	return nil
}

// snapshotImageFiles walks DATA_DIR/receipts and DATA_DIR/products and
// returns the set of absolute paths it found. Returned as a map so the
// missing-image diff in countMissingImages is O(1) per DB reference.
func (r *Runner) snapshotImageFiles() map[string]struct{} {
	out := make(map[string]struct{})
	for _, sub := range []string{"receipts", "products"} {
		root := filepath.Join(r.cfg.DataDir, sub)
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				if errors.Is(walkErr, os.ErrNotExist) {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			out[path] = struct{}{}
			return nil
		})
	}
	return out
}

// countMissingImages walks receipts.image_paths (JSON array or comma-
// separated string — matching the tolerant parse in the frontend) and
// product_images.image_path, and counts entries whose referenced file is
// absent on disk. Absent columns / tables are tolerated silently so the
// check survives across schema versions — this counter is informational.
// The filesystem snapshot is consulted first (fast-path); we fall back to
// os.Stat for paths the snapshot's absolute form doesn't match.
func (r *Runner) countMissingImages(ctx context.Context, present map[string]struct{}) int {
	var paths []string

	// receipts.image_paths — may be JSON array, comma-delimited, or a single path.
	if rows, err := r.db.QueryContext(ctx,
		`SELECT image_paths FROM receipts WHERE image_paths IS NOT NULL AND image_paths != ''`,
	); err == nil {
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil || raw == "" {
				continue
			}
			paths = append(paths, parseImagePaths(raw)...)
		}
		rows.Close()
	} else if !isSchemaMiss(err) {
		r.log.Debug("backup: receipts image-paths query failed", "err", err)
	}

	// product_images.image_path — one path per row, single column.
	if rows, err := r.db.QueryContext(ctx,
		`SELECT image_path FROM product_images WHERE image_path IS NOT NULL AND image_path != ''`,
	); err == nil {
		for rows.Next() {
			var p string
			if err := rows.Scan(&p); err == nil && p != "" {
				paths = append(paths, p)
			}
		}
		rows.Close()
	} else if !isSchemaMiss(err) {
		r.log.Debug("backup: product_images image-path query failed", "err", err)
	}

	var missing int
	for _, p := range paths {
		// Resolve DB-stored path against DataDir for relative values.
		resolved := p
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(r.cfg.DataDir, p)
		}
		absResolved, err := filepath.Abs(resolved)
		if err != nil {
			continue
		}
		if _, ok := present[absResolved]; ok {
			continue
		}
		// Snapshot miss: confirm with stat so we don't false-flag paths the
		// walk didn't produce in identical absolute form (e.g. symlink tree
		// differences). Only ENOENT counts as "missing" — other errors (EACCES,
		// etc.) are ignored because they don't indicate the file was pruned.
		if _, err := os.Stat(absResolved); err != nil && os.IsNotExist(err) {
			missing++
			r.log.Info("backup: referenced image missing from disk", "path", p)
		}
	}
	return missing
}

// parseImagePaths accepts the three historical shapes stored in
// receipts.image_paths: a JSON array of strings, a comma-separated string,
// or a single path. Returns the list of non-empty path values in stored form
// (relative or absolute — the caller resolves).
func parseImagePaths(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, "[") {
		// Best-effort JSON parse; on failure fall through to comma split.
		var arr []string
		if jsonErr := json.Unmarshal([]byte(raw), &arr); jsonErr == nil {
			out := make([]string, 0, len(arr))
			for _, p := range arr {
				p = strings.TrimSpace(p)
				if p != "" {
					out = append(out, p)
				}
			}
			return out
		}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// isSchemaMiss reports whether an error from a tolerant optional query is
// "this column/table simply isn't in the current schema" vs. a real failure.
func isSchemaMiss(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no such column") || strings.Contains(msg, "no such table")
}

// prune deletes complete backups beyond cfg.BackupRetainCount, plus their
// on-disk archives. Errors removing a single archive are logged and do NOT
// fail the calling backup.
func (r *Runner) prune(ctx context.Context) {
	removed, err := r.store.DeleteOldest(ctx, r.cfg.BackupRetainCount)
	if err != nil {
		r.log.Warn("backup: retention prune failed", "err", err)
		return
	}
	for _, b := range removed {
		archive := filepath.Join(r.cfg.BackupDir(), b.Filename)
		if err := os.Remove(archive); err != nil && !os.IsNotExist(err) {
			r.log.Warn("backup: remove pruned archive failed", "path", archive, "err", err)
			continue
		}
		r.log.Info("backup: pruned old archive", "id", b.ID, "path", archive)
	}
}

// markFailed updates a row to status='failed' and removes any partial
// on-disk archive. Best-effort — errors are logged, not propagated.
func (r *Runner) markFailed(ctx context.Context, id, archivePath string, cause error) {
	msg := cause.Error()
	completedAt := r.nowFn().UTC()
	if err := r.store.UpdateStatus(ctx, id, "failed", db.BackupUpdateOpts{
		Error:       &msg,
		CompletedAt: &completedAt,
	}); err != nil {
		r.log.Error("backup: mark-failed update errored", "id", id, "err", err)
	}
	if err := os.Remove(archivePath); err != nil && !os.IsNotExist(err) {
		r.log.Warn("backup: remove partial archive failed", "path", archivePath, "err", err)
	}
}

// formatBackupFilename produces the canonical archive name. The timestamp is
// UTC; the id is assigned by the store and reflected into the filename for
// easy correlation in operator shells.
func formatBackupFilename(t time.Time) string {
	return fmt.Sprintf("backup-%s.tar.gz", t.UTC().Format("20060102T150405Z"))
}

// fileSizeOrZero returns the size of the file at path, or 0 on any error.
func fileSizeOrZero(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// dirSizeOrZero sums the size of every regular file under root, returning 0
// if root doesn't exist. Used only for the preflight estimate so a slightly
// stale figure is fine.
func dirSizeOrZero(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}
