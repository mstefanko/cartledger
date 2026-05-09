package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ReconcileSummary reports receipt image metadata backfill work.
type ReconcileSummary struct {
	ReceiptsScanned int
	RowsUpserted    int
	FilesCopied     int
	MissingFiles    int
	Warnings        int
}

// ReconcileReceiptImages backfills receipt_images from historical
// receipts.image_paths plus files already on disk. Legacy flat files are copied
// into the canonical original/processed subdirectories; the legacy file is left
// in place so the migration is additive.
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

	for rows.Next() {
		var receiptID string
		var rawImagePaths sql.NullString
		var createdAt time.Time
		if err := rows.Scan(&receiptID, &rawImagePaths, &createdAt); err != nil {
			return summary, err
		}
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
			img, copied, missing := materializeReceiptImage(ctx, local, receiptID, ref, createdAt)
			if copied {
				summary.FilesCopied++
			}
			if missing {
				summary.MissingFiles++
			}
			if err := UpsertReceiptImage(ctx, database, img); err != nil {
				return summary, fmt.Errorf("upsert receipt image: %w", err)
			}
			summary.RowsUpserted++
		}
	}
	if err := rows.Err(); err != nil {
		return summary, err
	}
	if summary.RowsUpserted > 0 || summary.FilesCopied > 0 || summary.MissingFiles > 0 || summary.Warnings > 0 {
		log.Info("storage: receipt image metadata reconciled",
			"receipts_scanned", summary.ReceiptsScanned,
			"rows_upserted", summary.RowsUpserted,
			"files_copied", summary.FilesCopied,
			"missing_files", summary.MissingFiles,
			"warnings", summary.Warnings,
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
	root := filepath.Join(dataDir, "receipts", receiptID)
	var refs []LegacyReceiptImageRef
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
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

func materializeReceiptImage(ctx context.Context, local *Local, receiptID string, ref LegacyReceiptImageRef, createdAt time.Time) (ReceiptImage, bool, bool) {
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
			return img, false, false
		}
	}

	legacyPath := ref.LegacyPath
	if legacyPath != "" && !filepath.IsAbs(legacyPath) {
		legacyPath = filepath.Join(local.Root(), legacyPath)
	}
	if legacyPath != "" {
		if data, err := os.ReadFile(legacyPath); err == nil {
			_ = ctx
			if werr := local.WriteFileAtomic(ref.StorageKey, data, 0o644); werr == nil {
				img.SizeBytes = int64(len(data))
				sha := SHA256Hex(data)
				img.SHA256 = sql.NullString{String: sha, Valid: true}
				return img, true, false
			}
		}
	}

	img.DeletedAt = sql.NullTime{Time: time.Now().UTC(), Valid: true}
	return img, false, true
}
