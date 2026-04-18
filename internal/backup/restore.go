// Restore (Phase B) — staged restore surface.
//
// This file provides the three surfaces described in PLAN-backup-and-export.md
// §Restore flow:
//
//   - StageRestore                  — HTTP upload target; writes
//                                     pending.tar.gz + pending.manifest.json,
//                                     rejects malicious archives before the
//                                     server is restarted.
//   - ValidateArchive               — the shared validator used by all three
//                                     surfaces (HTTP stage, CLI cold-restore,
//                                     startup re-validation).
//   - ApplyStagedRestoreIfPresent   — called by cmd/server/serve.go BEFORE
//                                     db.Open on every boot; if a pending
//                                     restore is present, re-validates and
//                                     extracts it over DataDir after moving
//                                     the live DB aside as a pre-restore
//                                     backup.
//
// Zip-slip / symlink / hardlink / allowlist guards live here. The low-level
// path-containment check is shared with internal/db/backup.go via
// db.ValidateTarEntryPath so both surfaces apply identical semantics.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
)

// Sentinel errors for restore staging. Mapped to HTTP status codes in the
// api.BackupHandler.Restore handler.
var (
	// ErrArchiveTooLarge indicates the uploaded archive exceeded the configured
	// maxBytes cap in StageRestore. Mapped to 413.
	ErrArchiveTooLarge = errors.New("restore: archive exceeds size limit")
	// ErrArchiveInvalid is the catch-all for validator rejections (bad path,
	// symlink entry, missing manifest, bad SQLite magic, forward schema).
	// Mapped to 400.
	ErrArchiveInvalid = errors.New("restore: archive is invalid")
	// ErrDiskFull is returned when writing the staged archive fails with
	// ENOSPC. Mapped to 507.
	ErrDiskFull = errors.New("restore: insufficient disk space")
)

// sqliteMagic is the 16-byte header every SQLite 3 database starts with.
// Validator spot-checks the cartledger.db tar entry against this so hand-
// edited archives can't slip a non-DB file past us.
var sqliteMagic = []byte("SQLite format 3\x00")

// StagedRestore returns the on-disk paths used by the staged-restore flow.
// Kept separate from config.RestorePendingDir so tests can reason about both
// the directory AND the two files in it.
type stagedPaths struct {
	Dir      string // RestorePendingDir
	Archive  string // pending.tar.gz
	Manifest string // pending.manifest.json
}

func stagedPathsFor(cfg *config.Config) stagedPaths {
	dir := cfg.RestorePendingDir()
	return stagedPaths{
		Dir:      dir,
		Archive:  filepath.Join(dir, "pending.tar.gz"),
		Manifest: filepath.Join(dir, "pending.manifest.json"),
	}
}

// StageRestore writes an uploaded archive to RestorePendingDir/pending.tar.gz,
// validates it, and writes pending.manifest.json next to it. Any validation
// failure removes the pending directory entirely so a half-written / rejected
// upload never lingers between boots.
//
// maxBytes is the cap on the archive size; exceeding it returns
// ErrArchiveTooLarge. Callers should pick a number that matches the HTTP
// route-level BodyLimit (5GB for the admin restore surface today).
func StageRestore(cfg *config.Config, log *slog.Logger, src io.Reader, maxBytes int64) error {
	if cfg == nil {
		return errors.New("stage restore: cfg is required")
	}
	if log == nil {
		log = slog.Default()
	}
	if src == nil {
		return errors.New("stage restore: src is nil")
	}
	if maxBytes <= 0 {
		return errors.New("stage restore: maxBytes must be positive")
	}

	paths := stagedPathsFor(cfg)

	// Fresh start: remove any leftover pending dir from a prior attempt.
	if err := os.RemoveAll(paths.Dir); err != nil {
		return fmt.Errorf("stage restore: clean pending dir: %w", err)
	}
	if err := os.MkdirAll(paths.Dir, 0o755); err != nil {
		return fmt.Errorf("stage restore: mkdir pending dir: %w", err)
	}

	// Buffered writer with size cap. io.LimitReader returns EOF at the cap,
	// which we can't distinguish from "archive was exactly maxBytes and ended
	// cleanly" without reading one extra byte. Read maxBytes+1 so truncation
	// is detectable.
	limited := io.LimitReader(src, maxBytes+1)

	f, err := os.OpenFile(paths.Archive, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = os.RemoveAll(paths.Dir)
		return fmt.Errorf("stage restore: create pending archive: %w", err)
	}
	n, copyErr := io.Copy(f, limited)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.RemoveAll(paths.Dir)
		if isNoSpace(copyErr) {
			return fmt.Errorf("%w: %v", ErrDiskFull, copyErr)
		}
		return fmt.Errorf("stage restore: write pending archive: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.RemoveAll(paths.Dir)
		return fmt.Errorf("stage restore: close pending archive: %w", closeErr)
	}
	if n > maxBytes {
		_ = os.RemoveAll(paths.Dir)
		return fmt.Errorf("%w: cap=%d bytes", ErrArchiveTooLarge, maxBytes)
	}

	// Validate before persisting the manifest sidecar.
	maxVersion, err := db.MaxMigrationVersion()
	if err != nil {
		_ = os.RemoveAll(paths.Dir)
		return fmt.Errorf("stage restore: read migrations: %w", err)
	}

	manifest, err := ValidateArchive(paths.Archive, maxVersion)
	if err != nil {
		_ = os.RemoveAll(paths.Dir)
		return fmt.Errorf("%w: %v", ErrArchiveInvalid, err)
	}

	// Write sidecar. This file is what ApplyStagedRestoreIfPresent keys off
	// to tell "I staged this" from "someone dropped a random file here".
	sidecar, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		_ = os.RemoveAll(paths.Dir)
		return fmt.Errorf("stage restore: marshal manifest: %w", err)
	}
	if err := os.WriteFile(paths.Manifest, sidecar, 0o600); err != nil {
		_ = os.RemoveAll(paths.Dir)
		return fmt.Errorf("stage restore: write manifest sidecar: %w", err)
	}

	log.Info("restore staged — awaiting server restart",
		"archive", paths.Archive,
		"schema_version", manifest.SchemaVersion,
		"app_version", manifest.CartledgerVersion,
		"bytes", n,
	)
	return nil
}

// ValidateArchive opens a tar.gz at path, enforces every rule from
// §Restore flow step 3-4 (manifest well-formed + schema bound + SQLite magic
// check + full header walk with allowlist / symlink / hardlink / traversal
// rejection). Returns the parsed manifest on success. Does NOT write or
// extract anything — safe to call before committing any destructive change.
//
// maxSchemaVersion is the highest migration number this binary knows about;
// archives declaring a higher schema_version are rejected (forward-incompat).
func ValidateArchive(path string, maxSchemaVersion int) (*db.Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer gzr.Close()

	// Sentinel absolute DataDir used only for containment math on tar names —
	// the validator never writes anything, but ValidateTarEntryPath needs an
	// absolute anchor that won't itself be a prefix of "."  or similar edge
	// cases. Using a non-existent absolute path is fine because IsLocal +
	// strings.HasPrefix don't require the directory to exist.
	const anchor = "/cartledger-restore-validation-anchor"

	tr := tar.NewReader(gzr)

	var manifest *db.Manifest
	sawDB := false
	sawManifest := false

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar entry: %w", err)
		}

		name := filepath.ToSlash(hdr.Name)

		// Reject link types up front — neither backup writer produces them,
		// so their presence indicates a hand-edited archive we shouldn't trust.
		switch hdr.Typeflag {
		case tar.TypeSymlink:
			return nil, fmt.Errorf("unsafe tar entry: %q is a symlink", hdr.Name)
		case tar.TypeLink:
			return nil, fmt.Errorf("unsafe tar entry: %q is a hard link", hdr.Name)
		case tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
			return nil, fmt.Errorf("unsafe tar entry: %q is a device/fifo node", hdr.Name)
		}

		// Zip-slip / traversal guard via the shared helper. Rejects absolute
		// paths, ".." segments, and any entry that would escape DataDir.
		if _, err := db.ValidateTarEntryPath(hdr.Name, anchor); err != nil {
			return nil, err
		}

		// Allowlist enforcement. The backup writer in internal/db/backup.go
		// emits exactly these entry shapes:
		//   MANIFEST.json
		//   cartledger.db
		//   receipts/…  (directories + files)
		//   products/…  (directories + files)
		// We accept both MANIFEST.json (current writer) and the plan's
		// lower-case "manifest.json" spelling so the surface is friendly to
		// any future writer that normalized the casing.
		if !allowedEntry(name) {
			return nil, fmt.Errorf("unsafe tar entry: %q not in allowlist", hdr.Name)
		}

		// Manifest + SQLite-magic checks on the specific payloads we care about.
		switch {
		case isManifestName(name):
			sawManifest = true
			buf, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("read manifest body: %w", err)
			}
			var m db.Manifest
			if err := json.Unmarshal(buf, &m); err != nil {
				return nil, fmt.Errorf("parse manifest: %w", err)
			}
			if m.SchemaVersion <= 0 {
				return nil, fmt.Errorf("manifest missing schema_version (%d)", m.SchemaVersion)
			}
			if maxSchemaVersion > 0 && m.SchemaVersion > maxSchemaVersion {
				return nil, fmt.Errorf(
					"archive declares schema_version=%d but this binary knows only up to %d; upgrade cartledger before restoring",
					m.SchemaVersion, maxSchemaVersion,
				)
			}
			if strings.TrimSpace(m.CartledgerVersion) == "" {
				return nil, errors.New("manifest missing app_version")
			}
			manifest = &m
		case name == "cartledger.db":
			sawDB = true
			// Read only as many bytes as needed to verify SQLite magic — keep
			// validator memory bounded on massive DBs.
			head := make([]byte, len(sqliteMagic))
			nRead, err := io.ReadFull(tr, head)
			if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
				return nil, fmt.Errorf("read cartledger.db head: %w", err)
			}
			if nRead < len(sqliteMagic) || string(head[:len(sqliteMagic)]) != string(sqliteMagic) {
				return nil, errors.New("cartledger.db does not start with SQLite magic header")
			}
		}
	}

	if !sawManifest {
		return nil, errors.New("archive missing MANIFEST.json")
	}
	if manifest == nil {
		return nil, errors.New("archive MANIFEST.json unreadable")
	}
	if !sawDB {
		return nil, errors.New("archive missing cartledger.db entry")
	}

	return manifest, nil
}

// ApplyStagedRestoreIfPresent runs at server boot, BEFORE db.Open. If
// $DATA_DIR/restore-pending/ has a valid pending.tar.gz + pending.manifest.json,
// it:
//  1. Re-runs the validator (belt-and-suspenders: the staged file may have
//     been tampered with between HTTP stage and boot).
//  2. Moves cartledger.db → cartledger.db.pre-restore-<ts> so the operator has
//     an emergency rollback.
//  3. Extracts allowed archive entries under DataDir.
//  4. Removes RestorePendingDir so subsequent boots are no-ops.
//
// Any failure is logged at Error and returned so the server refuses to start
// — silently leaving a staged restore un-applied would be the worst possible
// outcome (operator thinks DB is restored, it isn't).
//
// The pre-restore DB path is logged prominently so operators can roll back
// manually if the restored DB is wrong. See docs/ops/migration-recovery.md.
func ApplyStagedRestoreIfPresent(cfg *config.Config, log *slog.Logger) error {
	if cfg == nil {
		return errors.New("apply staged restore: cfg is required")
	}
	if log == nil {
		log = slog.Default()
	}

	paths := stagedPathsFor(cfg)

	// No-op if neither the archive nor the sidecar is present. We check the
	// sidecar specifically so a half-written upload (archive without sidecar)
	// doesn't get applied.
	if _, err := os.Stat(paths.Manifest); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat pending manifest: %w", err)
	}
	if _, err := os.Stat(paths.Archive); err != nil {
		if os.IsNotExist(err) {
			log.Warn("restore: sidecar present but archive missing; removing stale pending dir",
				"pending_dir", paths.Dir)
			_ = os.RemoveAll(paths.Dir)
			return nil
		}
		return fmt.Errorf("stat pending archive: %w", err)
	}

	// Re-validate.
	maxVersion, err := db.MaxMigrationVersion()
	if err != nil {
		return fmt.Errorf("read max migration: %w", err)
	}
	manifest, err := ValidateArchive(paths.Archive, maxVersion)
	if err != nil {
		log.Error("restore: staged archive failed re-validation — refusing to boot",
			"pending_dir", paths.Dir, "err", err)
		return fmt.Errorf("staged archive invalid: %w", err)
	}

	absDataDir, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("abs data dir: %w", err)
	}
	if err := os.MkdirAll(absDataDir, 0o755); err != nil {
		return fmt.Errorf("mkdir data dir: %w", err)
	}

	// Move current DB aside as pre-restore snapshot. No-op if the file is
	// absent (first-boot restore into an empty DATA_DIR).
	liveDB := filepath.Join(absDataDir, "cartledger.db")
	var preRestorePath string
	if _, err := os.Stat(liveDB); err == nil {
		ts := time.Now().UTC().Format("20060102T150405Z")
		preRestorePath = filepath.Join(absDataDir, "cartledger.db.pre-restore-"+ts)
		if err := os.Rename(liveDB, preRestorePath); err != nil {
			log.Error("restore: could not move live DB aside — refusing to proceed",
				"live_db", liveDB, "err", err)
			return fmt.Errorf("rename live db: %w", err)
		}
		// SQLite WAL/SHM companions: move them aside too so the freshly
		// extracted DB isn't joined to a stale WAL.
		for _, suffix := range []string{"-wal", "-shm"} {
			companion := liveDB + suffix
			if _, err := os.Stat(companion); err == nil {
				target := preRestorePath + suffix
				if err := os.Rename(companion, target); err != nil {
					log.Warn("restore: could not move db companion aside",
						"path", companion, "err", err)
				}
			}
		}
		log.Warn("restore: previous database moved aside for rollback",
			"pre_restore_path", preRestorePath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat live db: %w", err)
	}

	// Extract.
	if err := extractArchive(paths.Archive, absDataDir); err != nil {
		log.Error("restore: extraction failed — DB moved aside but not replaced",
			"pending_dir", paths.Dir,
			"pre_restore_path", preRestorePath,
			"err", err,
		)
		return fmt.Errorf("extract archive: %w", err)
	}

	if err := os.RemoveAll(paths.Dir); err != nil {
		// Non-fatal: logged but the restore itself succeeded. A lingering
		// pending dir would cause the next boot to try-and-fail, so surface
		// it loudly.
		log.Error("restore: applied but pending dir cleanup failed",
			"pending_dir", paths.Dir, "err", err)
	}

	log.Info("restore: staged archive applied",
		"schema_version", manifest.SchemaVersion,
		"app_version", manifest.CartledgerVersion,
		"pre_restore_path", preRestorePath,
	)
	return nil
}

// extractArchive is the boot-time extractor used by ApplyStagedRestoreIfPresent.
// It applies the same allowlist + traversal + symlink rejections as the
// validator, so any tampering between stage-and-boot still fails closed.
func extractArchive(archivePath, absDataDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}

		name := filepath.ToSlash(hdr.Name)

		// Re-run the rejection rules. Paranoia: validator already saw this
		// file, but the file could have been swapped on disk between then
		// and now.
		switch hdr.Typeflag {
		case tar.TypeSymlink, tar.TypeLink, tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
			return fmt.Errorf("unsafe tar entry: %q link/device", hdr.Name)
		}
		if !allowedEntry(name) {
			return fmt.Errorf("unsafe tar entry: %q not in allowlist", hdr.Name)
		}
		if isManifestName(name) {
			// Manifest is informational; the sidecar already captured it.
			continue
		}

		dest, err := db.ValidateTarEntryPath(hdr.Name, absDataDir)
		if err != nil {
			return err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", dest, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return fmt.Errorf("mkdir parent %s: %w", dest, err)
			}
			// O_TRUNC: this is cold-recovery/restart-gated; overwriting
			// whatever was left of the extracted tree from a prior boot is
			// safe (the pre-restore snapshot already captured the live DB).
			dst, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
			if err != nil {
				return fmt.Errorf("create %s: %w", dest, err)
			}
			_, copyErr := io.Copy(dst, tr)
			closeErr := dst.Close()
			if copyErr != nil {
				return fmt.Errorf("write %s: %w", dest, copyErr)
			}
			if closeErr != nil {
				return fmt.Errorf("close %s: %w", dest, closeErr)
			}
		default:
			// Silently skip — we already rejected link/device types above,
			// so anything reaching here is a non-regular benign type (e.g.
			// global header). Not expected but not worth failing a restore.
			continue
		}
	}
}

// allowedEntry reports whether a tar entry name matches the archive
// allowlist: MANIFEST.json (any casing supported), cartledger.db, or
// receipts/… / products/… subtrees.
func allowedEntry(name string) bool {
	if isManifestName(name) {
		return true
	}
	if name == "cartledger.db" {
		return true
	}
	// Directory entries for the two allowed subtrees.
	if name == "receipts" || name == "receipts/" ||
		name == "products" || name == "products/" {
		return true
	}
	if strings.HasPrefix(name, "receipts/") || strings.HasPrefix(name, "products/") {
		return true
	}
	return false
}

// isManifestName accepts both the current writer's "MANIFEST.json" spelling
// and the plan's "manifest.json" lower-case variant, so future writers can
// normalize without breaking existing validators.
func isManifestName(name string) bool {
	return name == "MANIFEST.json" || name == "manifest.json"
}

// isNoSpace reports whether an error is ENOSPC. Kept as a helper so the HTTP
// handler can map it to 507 via errors.Is(err, ErrDiskFull).
func isNoSpace(err error) bool {
	if err == nil {
		return false
	}
	// syscall.ENOSPC on unix; on Windows os.WriteFile surfaces a different
	// error but self-hosters are overwhelmingly on Linux. Substring fallback
	// catches either path without adding a platform-specific file.
	msg := err.Error()
	return strings.Contains(msg, "no space left on device") ||
		strings.Contains(msg, "ENOSPC") ||
		strings.Contains(msg, "not enough space")
}
