package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ReconcileSummary reports receipt image metadata backfill work.
type ReconcileSummary struct {
	ReceiptsScanned   int
	RowsUpserted      int
	FilesCopied       int
	MissingFiles      int
	Warnings          int
	OrphanDirsRemoved int
}

// ReconcileReceiptImages backfills receipt_images from historical
// receipts.image_paths plus files already on disk. Legacy files already under
// DATA_DIR are referenced in place; external legacy files are copied into the
// canonical original/processed subdirectories.
func ReconcileReceiptImages(ctx context.Context, database *sql.DB, dataDir string, log *slog.Logger) (ReconcileSummary, error) {
	var summary ReconcileSummary
	if database == nil {
		return summary, errors.New("database is nil")
	}
	if log == nil {
		log = slog.Default()
	}
	exists, err := tableExists(ctx, database, "receipt_images")
	if err != nil {
		return summary, err
	}
	if !exists {
		return summary, nil
	}
	local, err := NewLocal(dataDir)
	if err != nil {
		return summary, err
	}

	rows, err := database.QueryContext(ctx,
		`SELECT id, image_paths, created_at FROM receipts ORDER BY created_at, id`,
	)
	if err != nil {
		return summary, fmt.Errorf("query receipts for image reconcile: %w", err)
	}
	defer rows.Close()

	seenReceipts := make(map[string]struct{})
	for rows.Next() {
		var receiptID string
		var rawImagePaths sql.NullString
		var createdAt time.Time
		if err := rows.Scan(&receiptID, &rawImagePaths, &createdAt); err != nil {
			return summary, err
		}
		seenReceipts[receiptID] = struct{}{}
		summary.ReceiptsScanned++
		refs := map[string]LegacyReceiptImageRef{}
		if rawImagePaths.Valid {
			for _, p := range ParseImagePaths(rawImagePaths.String) {
				ref, err := NormalizeLegacyReceiptImageReference(dataDir, receiptID, p)
				if err != nil {
					summary.Warnings++
					log.Warn("storage: skip legacy receipt image reference", "receipt_id", receiptID, "path", p, "err", err)
					continue
				}
				refs[refKey(ref)] = ref
			}
		}
		diskRefs, diskErr := scanReceiptImageFiles(dataDir, receiptID)
		if diskErr != nil {
			summary.Warnings++
			log.Warn("storage: scan receipt images failed", "receipt_id", receiptID, "err", diskErr)
		}
		for _, ref := range diskRefs {
			refs[refKey(ref)] = ref
		}

		ordered := make([]LegacyReceiptImageRef, 0, len(refs))
		for _, ref := range refs {
			ordered = append(ordered, ref)
		}
		sort.SliceStable(ordered, func(i, j int) bool {
			if ordered[i].Kind != ordered[j].Kind {
				return ordered[i].Kind < ordered[j].Kind
			}
			return ordered[i].PageNumber < ordered[j].PageNumber
		})

		for _, ref := range ordered {
			img, copied, missing, materializeErr := materializeReceiptImage(ctx, local, receiptID, ref, createdAt)
			if materializeErr != nil {
				if ctx.Err() != nil {
					return summary, ctx.Err()
				}
				summary.Warnings++
				log.Warn("storage: materialize receipt image failed",
					"receipt_id", receiptID,
					"storage_key", ref.StorageKey,
					"legacy_path", ref.LegacyPath,
					"err", materializeErr,
				)
				continue
			}
			if copied {
				summary.FilesCopied++
			}
			if missing {
				summary.MissingFiles++
			}
			if err := UpsertReceiptImage(ctx, database, img); err != nil {
				if ctx.Err() != nil {
					return summary, ctx.Err()
				}
				summary.Warnings++
				log.Warn("storage: upsert receipt image failed",
					"receipt_id", receiptID,
					"storage_key", img.StorageKey,
					"err", err,
				)
				continue
			}
			summary.RowsUpserted++
		}
	}
	if err := rows.Err(); err != nil {
		return summary, err
	}
	removed, warnings, err := sweepOrphanReceiptDirs(ctx, local, seenReceipts, log)
	if err != nil {
		return summary, err
	}
	summary.OrphanDirsRemoved += removed
	summary.Warnings += warnings

	if summary.RowsUpserted > 0 || summary.FilesCopied > 0 || summary.MissingFiles > 0 || summary.Warnings > 0 || summary.OrphanDirsRemoved > 0 {
		log.Info("storage: receipt image metadata reconciled",
			"receipts_scanned", summary.ReceiptsScanned,
			"rows_upserted", summary.RowsUpserted,
			"files_copied", summary.FilesCopied,
			"missing_files", summary.MissingFiles,
			"warnings", summary.Warnings,
			"orphan_dirs_removed", summary.OrphanDirsRemoved,
		)
	}
	return summary, nil
}

func tableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var name string
	err := db.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name = ?`,
		table,
	).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func refKey(ref LegacyReceiptImageRef) string {
	return fmt.Sprintf("%s/%d", ref.Kind, ref.PageNumber)
}

func scanReceiptImageFiles(dataDir, receiptID string) ([]LegacyReceiptImageRef, error) {
	root, err := LegacyReceiptDir(dataDir, receiptID)
	if err != nil {
		return nil, err
	}
	var refs []LegacyReceiptImageRef
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !isSupportedImageName(name) {
			return nil
		}
		rel, err := filepath.Rel(dataDir, path)
		if err != nil {
			return nil
		}
		ref, err := NormalizeLegacyReceiptImageReference(dataDir, receiptID, rel)
		if err != nil {
			return nil
		}
		ref.LegacyPath = path
		refs = append(refs, ref)
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return refs, err
}

func isSupportedImageName(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".jpg" || ext == ".jpeg" || ext == ".png"
}

func materializeReceiptImage(ctx context.Context, local *Local, receiptID string, ref LegacyReceiptImageRef, createdAt time.Time) (ReceiptImage, bool, bool, error) {
	img := ReceiptImage{
		ReceiptID:  receiptID,
		Kind:       ref.Kind,
		PageNumber: ref.PageNumber,
		StorageKey: ref.StorageKey,
		MimeType:   MimeTypeFromKey(ref.StorageKey),
		CreatedAt:  createdAt.UTC(),
	}

	canonicalPath, err := local.Path(ref.StorageKey)
	if err == nil {
		if info, statErr := os.Stat(canonicalPath); statErr == nil && info.Mode().IsRegular() {
			img.SizeBytes = info.Size()
			return img, false, false, nil
		}
	}

	if err := ctx.Err(); err != nil {
		return img, false, false, err
	}

	legacyPath := ref.LegacyPath
	if legacyPath != "" && !filepath.IsAbs(legacyPath) {
		legacyPath = filepath.Join(local.Root(), legacyPath)
	}
	if legacyPath != "" {
		if existingKey, ok := storageKeyForLocalPath(local, legacyPath); ok {
			if info, statErr := os.Stat(legacyPath); statErr == nil && info.Mode().IsRegular() {
				img.StorageKey = existingKey
				img.MimeType = MimeTypeFromKey(existingKey)
				img.SizeBytes = info.Size()
				return img, false, false, nil
			}
		}
		size, sha, err := copyFileToLocalAtomic(ctx, local, ref.StorageKey, legacyPath, 0o644)
		if err == nil {
			img.SizeBytes = size
			img.SHA256 = sql.NullString{String: sha, Valid: true}
			return img, true, false, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return img, false, false, err
		}
	}

	img.DeletedAt = sql.NullTime{Time: time.Now().UTC(), Valid: true}
	return img, false, true, nil
}

func storageKeyForLocalPath(local *Local, p string) (string, bool) {
	absPath, err := filepath.Abs(p)
	if err != nil {
		return "", false
	}
	sep := string(filepath.Separator)
	if absPath != local.absRoot && !strings.HasPrefix(absPath+sep, local.absRoot+sep) {
		return "", false
	}
	rel, err := filepath.Rel(local.absRoot, absPath)
	if err != nil || rel == "." {
		return "", false
	}
	key := filepath.ToSlash(rel)
	if err := ValidateKey(key); err != nil {
		return "", false
	}
	return key, true
}

func copyFileToLocalAtomic(ctx context.Context, local *Local, key, source string, perm os.FileMode) (int64, string, error) {
	if err := ctx.Err(); err != nil {
		return 0, "", err
	}
	src, err := os.Open(source)
	if err != nil {
		return 0, "", err
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return 0, "", err
	}
	if !info.Mode().IsRegular() {
		return 0, "", fmt.Errorf("source is not a regular file: %s", source)
	}

	dest, err := local.Path(key)
	if err != nil {
		return 0, "", err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return 0, "", fmt.Errorf("create storage dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), "."+filepath.Base(dest)+".*.tmp")
	if err != nil {
		return 0, "", fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	hash := sha256.New()
	buf := make([]byte, 128*1024)
	var copied int64
	for {
		if err := ctx.Err(); err != nil {
			_ = tmp.Close()
			return 0, "", err
		}
		n, readErr := src.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if _, err := tmp.Write(chunk); err != nil {
				_ = tmp.Close()
				return 0, "", fmt.Errorf("write temp file: %w", err)
			}
			if _, err := hash.Write(chunk); err != nil {
				_ = tmp.Close()
				return 0, "", fmt.Errorf("hash file: %w", err)
			}
			copied += int64(n)
		}
		if readErr == nil {
			continue
		}
		if !errors.Is(readErr, io.EOF) {
			_ = tmp.Close()
			return 0, "", fmt.Errorf("read source file: %w", readErr)
		}
		break
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return 0, "", fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return 0, "", fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return 0, "", fmt.Errorf("rename temp file: %w", err)
	}
	cleanup = false
	return copied, hex.EncodeToString(hash.Sum(nil)), nil
}

func sweepOrphanReceiptDirs(ctx context.Context, local *Local, seenReceipts map[string]struct{}, log *slog.Logger) (int, int, error) {
	root, err := local.Path("receipts")
	if err != nil {
		return 0, 0, err
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}

	var removed int
	var warnings int
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return removed, warnings, err
		}
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if _, ok := seenReceipts[name]; ok {
			continue
		}
		if _, err := uuid.Parse(name); err != nil {
			continue
		}
		if err := local.DeleteReceipt(name); err != nil {
			warnings++
			log.Warn("storage: remove orphan receipt image directory failed", "receipt_id", name, "err", err)
			continue
		}
		removed++
	}
	return removed, warnings, nil
}
