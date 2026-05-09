package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ReceiptImage is one row from receipt_images.
type ReceiptImage struct {
	ID         string
	ReceiptID  string
	Kind       string
	PageNumber int
	StorageKey string
	MimeType   string
	SizeBytes  int64
	SHA256     sql.NullString
	CreatedAt  time.Time
	DeletedAt  sql.NullTime
}

type execContexter interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type queryContexter interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// UpsertReceiptImage creates or updates the row for one receipt image page.
func UpsertReceiptImage(ctx context.Context, db execContexter, img ReceiptImage) error {
	if db == nil {
		return errors.New("db is nil")
	}
	if img.ID == "" {
		img.ID = uuid.New().String()
	}
	if img.CreatedAt.IsZero() {
		img.CreatedAt = time.Now().UTC()
	}
	if img.MimeType == "" {
		img.MimeType = MimeTypeFromKey(img.StorageKey)
	}
	if img.Kind != ReceiptImageKindOriginal && img.Kind != ReceiptImageKindProcessed {
		return fmt.Errorf("invalid receipt image kind %q", img.Kind)
	}
	if img.PageNumber <= 0 {
		return fmt.Errorf("page number must be positive")
	}
	if err := ValidateKey(img.StorageKey); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO receipt_images
		    (id, receipt_id, kind, page_number, storage_key, mime_type, size_bytes, sha256, created_at, deleted_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(receipt_id, kind, page_number) DO UPDATE SET
		    storage_key = excluded.storage_key,
		    mime_type = excluded.mime_type,
		    size_bytes = excluded.size_bytes,
		    sha256 = excluded.sha256,
		    deleted_at = excluded.deleted_at`,
		img.ID, img.ReceiptID, img.Kind, img.PageNumber, img.StorageKey, img.MimeType,
		img.SizeBytes, nullStringArg(img.SHA256), img.CreatedAt, nullTimeArg(img.DeletedAt),
	)
	return err
}

// ListReceiptImages returns active image rows in stable page order. If kind is
// empty, both original and processed rows are returned.
func ListReceiptImages(ctx context.Context, db queryContexter, receiptID, kind string) ([]ReceiptImage, error) {
	if db == nil {
		return nil, errors.New("db is nil")
	}
	args := []any{receiptID}
	where := "receipt_id = ? AND deleted_at IS NULL"
	if kind != "" {
		where += " AND kind = ?"
		args = append(args, kind)
	}
	rows, err := db.QueryContext(ctx,
		`SELECT id, receipt_id, kind, page_number, storage_key, mime_type, size_bytes, sha256, created_at, deleted_at
		   FROM receipt_images
		  WHERE `+where+`
		  ORDER BY kind, page_number`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ReceiptImage
	for rows.Next() {
		var img ReceiptImage
		if err := rows.Scan(
			&img.ID, &img.ReceiptID, &img.Kind, &img.PageNumber, &img.StorageKey,
			&img.MimeType, &img.SizeBytes, &img.SHA256, &img.CreatedAt, &img.DeletedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, img)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ListExistingReceiptOriginals returns active original rows whose files exist.
func ListExistingReceiptOriginals(ctx context.Context, db queryContexter, local *Local, receiptID string) ([]ReceiptImage, error) {
	rows, err := ListReceiptImages(ctx, db, receiptID, ReceiptImageKindOriginal)
	if err != nil {
		return nil, err
	}
	out := make([]ReceiptImage, 0, len(rows))
	for _, row := range rows {
		p, err := local.Path(row.StorageKey)
		if err != nil {
			continue
		}
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
			out = append(out, row)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].PageNumber < out[j].PageNumber })
	return out, nil
}

// MimeTypeFromKey returns an image mime type from a storage key extension.
func MimeTypeFromKey(key string) string {
	switch ext := strings.ToLower(filepath.Ext(key)); ext {
	case ".png":
		return "image/png"
	default:
		return "image/jpeg"
	}
}

func nullStringArg(ns sql.NullString) any {
	if !ns.Valid {
		return nil
	}
	return ns.String
}

func nullTimeArg(nt sql.NullTime) any {
	if !nt.Valid {
		return nil
	}
	return nt.Time
}
