// Package imaging — retention.go implements a background janitor that
// ages out original receipt image files.
//
// Rationale: receipt storage grows linearly with scans. Originals are only
// strictly needed when the user re-runs extraction on a receipt (see
// issue 2.3 — "reprocess"). The processed_* preprocessed versions are
// smaller, are what the review UI displays, and MUST be kept forever —
// the janitor never touches them.
//
// When ImageRetentionDays > 0, the janitor runs every
// ImageRetentionSweepInterval and walks DATA_DIR/receipts/. For each
// UUID-named subdirectory, it lists files and deletes any whose basename
// does NOT start with "processed_" and whose mtime is older than
// ImageRetentionDays*24h. If a later reprocess request arrives for a
// receipt whose original has been deleted, the handler already returns
// 410 Gone (established behavior).
package imaging

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// uuidV4Len is the fixed length of a UUID v4 canonical string
// (8-4-4-4-12 + 4 dashes). Used as a cheap sanity filter to skip any
// directory that's not receipt-shaped — we do NOT descend into
// non-UUID subfolders even if they exist under DATA_DIR/receipts/.
const uuidV4Len = 36

// processedPrefix is the filename prefix worker/receipt.go writes on
// preprocessed images (see worker.runJob). Files starting with this
// prefix are NEVER deleted by the janitor.
const processedPrefix = "processed_"

// RetentionMetricsRecorder is the subset of api.Metrics the janitor
// needs. Keeping it as an interface avoids an internal/imaging →
// internal/api import cycle.
type RetentionMetricsRecorder interface {
	RecordRetentionDeleted(reason string, n int)
}

// Janitor sweeps original receipt images older than a retention window.
//
// Zero-value Janitor is NOT usable — call NewJanitor. Start launches
// the background goroutine; Stop cancels it and waits for the current
// sweep to exit. Stop is idempotent.
type Janitor struct {
	dataDir  string
	ttl      time.Duration // retention window (days * 24h); janitor is disabled when 0
	interval time.Duration // how often to sweep

	// metrics, when non-nil, receives a RecordRetentionDeleted call once
	// per sweep with the total deleted count.
	metrics RetentionMetricsRecorder

	// now is overridable in tests; defaults to time.Now.
	now func() time.Time

	// lifecycle
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewJanitor constructs a Janitor for the given DATA_DIR root. When
// days <= 0 the returned Janitor is a no-op (Start does nothing).
func NewJanitor(dataDir string, days int, interval time.Duration) *Janitor {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	j := &Janitor{
		dataDir:  dataDir,
		interval: interval,
		now:      time.Now,
	}
	if days > 0 {
		j.ttl = time.Duration(days) * 24 * time.Hour
	}
	return j
}

// SetMetrics wires a metrics recorder for deletion counters. Optional.
func (j *Janitor) SetMetrics(m RetentionMetricsRecorder) {
	j.metrics = m
}

// enabled reports whether the janitor has a positive retention window.
// When false, Start returns immediately.
func (j *Janitor) enabled() bool {
	return j.ttl > 0 && j.dataDir != ""
}

// Start launches the janitor goroutine. Safe to call with a nil receiver
// or a disabled janitor (both are no-ops). The provided context governs
// shutdown: when ctx is cancelled, the goroutine exits after the current
// sweep. Stop also cancels the goroutine.
//
// Runs one sweep immediately, then ticks at j.interval.
func (j *Janitor) Start(ctx context.Context) {
	if j == nil || !j.enabled() {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.cancel != nil {
		// Already started.
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	j.cancel = cancel
	j.done = done

	// Pass `done` explicitly so the goroutine does not read j.done
	// under no lock — Stop mutates j.done, which would race.
	go j.run(runCtx, done)
}

// Stop cancels the janitor and blocks until the goroutine exits.
// Safe to call on a never-started or already-stopped janitor.
func (j *Janitor) Stop() {
	if j == nil {
		return
	}
	j.mu.Lock()
	cancel := j.cancel
	done := j.done
	j.cancel = nil
	j.done = nil
	j.mu.Unlock()

	if cancel == nil {
		return
	}
	cancel()
	if done != nil {
		<-done
	}
}

// run is the janitor loop. One immediate sweep at boot, then on j.interval.
// `done` is passed in (rather than read from j.done) so Stop's mutation of
// j.done under j.mu cannot race with this goroutine.
func (j *Janitor) run(ctx context.Context, done chan struct{}) {
	defer close(done)

	// Immediate sweep at start so operators don't wait a full interval
	// on first boot to see the behavior.
	j.sweep(ctx)

	t := time.NewTicker(j.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			j.sweep(ctx)
		}
	}
}

// sweep walks DATA_DIR/receipts/<uuid>/ and deletes originals whose mtime
// is older than j.ttl. Returns nothing; errors are logged. Exported for
// tests (see retention_test.go).
func (j *Janitor) sweep(ctx context.Context) {
	if !j.enabled() {
		return
	}
	root := filepath.Join(j.dataDir, "receipts")
	cutoff := j.now().Add(-j.ttl)

	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// First-boot / no receipts yet. Not an error.
			return
		}
		slog.Warn("retention: read receipts dir failed", "root", root, "err", err)
		return
	}

	var deleted int
	var bytesReclaimed int64
	for _, e := range entries {
		if ctx.Err() != nil {
			break
		}
		// Safety: only descend into UUID-shaped directories. Skip files
		// at the top level and any subfolder whose name doesn't look
		// like a UUID v4 (e.g. accidental operator touch, .DS_Store).
		if !e.IsDir() || len(e.Name()) != uuidV4Len {
			continue
		}
		d, b := j.sweepReceiptDir(filepath.Join(root, e.Name()), cutoff)
		deleted += d
		bytesReclaimed += b
	}

	if deleted > 0 {
		slog.Info("retention: swept",
			"root", root,
			"deleted_files", deleted,
			"bytes_reclaimed", bytesReclaimed,
			"ttl", j.ttl,
		)
		if j.metrics != nil {
			j.metrics.RecordRetentionDeleted("age", deleted)
		}
	} else {
		slog.Debug("retention: swept (nothing to delete)",
			"root", root,
			"ttl", j.ttl,
		)
	}
}

// sweepReceiptDir deletes aged original files inside one <uuid>/ dir.
// Returns (files deleted, bytes reclaimed). Does NOT remove the
// directory itself — even if every file inside is aged out, the
// directory is left in place (simpler, and metadata tracking in SQLite
// points at it).
func (j *Janitor) sweepReceiptDir(dir string, cutoff time.Time) (int, int64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("retention: read receipt dir failed", "dir", dir, "err", err)
		}
		return 0, 0
	}

	var deleted int
	var bytesReclaimed int64
	for _, e := range entries {
		name := e.Name()
		// Never touch processed_* files — those are the only copy the
		// review UI can display.
		if strings.HasPrefix(name, processedPrefix) {
			continue
		}
		// Skip subdirectories; we don't recurse into nested folders
		// under a receipt.
		if e.IsDir() {
			continue
		}

		full := filepath.Join(dir, name)
		// Use Lstat so a symlink resolves to the link itself and is
		// skipped rather than followed into some unrelated target.
		info, err := os.Lstat(full)
		if err != nil {
			// File disappeared between ReadDir and Lstat (e.g. worker
			// cleanup raced us). Not an error worth surfacing.
			if !errors.Is(err, os.ErrNotExist) {
				slog.Debug("retention: lstat failed", "path", full, "err", err)
			}
			continue
		}

		// Skip symlinks entirely — we never chase a link to delete
		// something outside the receipts tree.
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		// Skip anything that isn't a regular file (devices, sockets,
		// named pipes — shouldn't exist here, but be defensive).
		if !info.Mode().IsRegular() {
			continue
		}

		if !info.ModTime().Before(cutoff) {
			// Fresh enough — keep.
			// (Files currently being written by the worker upload
			// handler have an mtime of "now", so they are safe: now
			// is not before cutoff=now-ttl.)
			continue
		}

		size := info.Size()
		if err := os.Remove(full); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			slog.Warn("retention: remove failed", "path", full, "err", err)
			continue
		}
		deleted++
		bytesReclaimed += size
	}
	return deleted, bytesReclaimed
}
