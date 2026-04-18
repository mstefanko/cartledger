package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/mstefanko/cartledger/internal/models"
)

// BackupStore provides CRUD for the backups table. Modeled on
// internal/db/integrations.go — one file per resource, no ORM.
type BackupStore struct {
	DB *sql.DB
}

// NewBackupStore constructs a BackupStore.
func NewBackupStore(database *sql.DB) *BackupStore {
	return &BackupStore{DB: database}
}

// BackupUpdateOpts bundles optional updates applied by UpdateStatus. Nil
// fields are left untouched on the row, so callers can perform partial
// updates (e.g. "mark complete + set size + set completed_at" without having
// to know how to zero out Error).
type BackupUpdateOpts struct {
	SizeBytes     *int64
	MissingImages *int
	Error         *string
	CompletedAt   *time.Time
}

// Create inserts a new backup row with status='running' and created_at=now.
// Returns the generated ID (16-byte hex, same convention as integrations).
//
// The filename argument may be empty when the caller wants to stamp the ID
// into the archive name — callers in that mode use SetFilename immediately
// after Create. A non-empty filename is inserted as-is.
func (s *BackupStore) Create(ctx context.Context, filename string, schemaVersion int) (string, error) {
	id, err := newBackupID()
	if err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	now := time.Now().UTC()

	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO backups (id, status, filename, schema_version, missing_images, created_at)
		 VALUES (?, ?, ?, ?, 0, ?)`,
		id, models.BackupStatusRunning, filename, schemaVersion, now,
	)
	if err != nil {
		return "", fmt.Errorf("insert backup: %w", err)
	}
	return id, nil
}

// SetFilename updates a row's filename column. Used by the Runner to stamp
// the DB-generated id into the archive filename after Create (the id is only
// known after the INSERT returns). Returns an error if the row doesn't exist.
func (s *BackupStore) SetFilename(ctx context.Context, id, filename string) error {
	if id == "" {
		return errors.New("backup: id required")
	}
	if filename == "" {
		return errors.New("backup: filename required")
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE backups SET filename = ? WHERE id = ?`, filename, id,
	)
	if err != nil {
		return fmt.Errorf("update backup filename: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("backup %s: not found", id)
	}
	return nil
}

// UpdateStatus flips a row's status and applies any provided optional updates
// in a single UPDATE. Callers set status to one of models.BackupStatus*.
func (s *BackupStore) UpdateStatus(ctx context.Context, id, status string, opts BackupUpdateOpts) error {
	if id == "" {
		return errors.New("backup: id required")
	}
	switch status {
	case models.BackupStatusRunning, models.BackupStatusComplete, models.BackupStatusFailed:
	default:
		return fmt.Errorf("backup: invalid status %q", status)
	}

	query := "UPDATE backups SET status = ?"
	args := []any{status}

	if opts.SizeBytes != nil {
		query += ", size_bytes = ?"
		args = append(args, *opts.SizeBytes)
	}
	if opts.MissingImages != nil {
		query += ", missing_images = ?"
		args = append(args, *opts.MissingImages)
	}
	if opts.Error != nil {
		query += ", error = ?"
		args = append(args, *opts.Error)
	}
	if opts.CompletedAt != nil {
		query += ", completed_at = ?"
		args = append(args, *opts.CompletedAt)
	}
	query += " WHERE id = ?"
	args = append(args, id)

	res, err := s.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update backup: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("backup %s: not found", id)
	}
	return nil
}

// List returns all backup rows ordered by created_at DESC.
func (s *BackupStore) List(ctx context.Context) ([]models.Backup, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, status, filename, size_bytes, schema_version, missing_images,
		        error, created_at, completed_at
		 FROM backups
		 ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("query backups: %w", err)
	}
	defer rows.Close()

	out := make([]models.Backup, 0)
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iter backups: %w", err)
	}
	return out, nil
}

// Get returns a single backup row by ID. Returns (nil, nil) on miss so
// handlers can map to 404 without special-casing sql.ErrNoRows.
func (s *BackupStore) Get(ctx context.Context, id string) (*models.Backup, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, status, filename, size_bytes, schema_version, missing_images,
		        error, created_at, completed_at
		 FROM backups WHERE id = ?`,
		id,
	)
	b, err := scanBackup(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// Delete removes the row. The caller is responsible for removing the archive
// file from disk — keeping the split here so tests can exercise the store
// without a filesystem.
func (s *BackupStore) Delete(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM backups WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete backup: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("backup %s: not found", id)
	}
	return nil
}

// DeleteOldest trims complete rows past the `keep` newest, returning the
// removed rows so the caller can unlink the on-disk archive for each. Only
// rows with status='complete' are candidates — running/failed rows are left
// in place (running is in flight; failed is user-visible audit history).
func (s *BackupStore) DeleteOldest(ctx context.Context, keep int) ([]models.Backup, error) {
	if keep < 0 {
		return nil, errors.New("backup: keep must be >= 0")
	}

	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, status, filename, size_bytes, schema_version, missing_images,
		        error, created_at, completed_at
		 FROM backups
		 WHERE status = ?
		 ORDER BY created_at DESC`,
		models.BackupStatusComplete,
	)
	if err != nil {
		return nil, fmt.Errorf("list complete backups: %w", err)
	}
	defer rows.Close()

	var all []models.Backup
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		all = append(all, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iter complete backups: %w", err)
	}

	if len(all) <= keep {
		return nil, nil
	}

	removed := all[keep:]
	for _, r := range removed {
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM backups WHERE id = ?`, r.ID); err != nil {
			return nil, fmt.Errorf("delete old backup %s: %w", r.ID, err)
		}
	}
	return removed, nil
}

// ReconcileRunning marks any status='running' rows as failed. Called at
// server startup to clean up rows orphaned by a crash / kill mid-backup.
// Returns (possibly transient) DB errors to the caller; the calling code
// typically logs and continues (failure here shouldn't block boot).
func (s *BackupStore) ReconcileRunning(ctx context.Context) error {
	now := time.Now().UTC()
	_, err := s.DB.ExecContext(ctx,
		`UPDATE backups
		 SET status = ?, error = ?, completed_at = ?
		 WHERE status = ?`,
		models.BackupStatusFailed,
		"server restarted during backup",
		now,
		models.BackupStatusRunning,
	)
	if err != nil {
		return fmt.Errorf("reconcile running backups: %w", err)
	}
	return nil
}

// rowScanner abstracts *sql.Row vs *sql.Rows so scanBackup works for both.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanBackup(r rowScanner) (models.Backup, error) {
	var (
		b            models.Backup
		sizeBytes    sql.NullInt64
		errorStr     sql.NullString
		completedAt  sql.NullTime
	)
	err := r.Scan(
		&b.ID, &b.Status, &b.Filename, &sizeBytes, &b.SchemaVersion,
		&b.MissingImages, &errorStr, &b.CreatedAt, &completedAt,
	)
	if err != nil {
		return b, err
	}
	if sizeBytes.Valid {
		v := sizeBytes.Int64
		b.SizeBytes = &v
	}
	if errorStr.Valid {
		v := errorStr.String
		b.Error = &v
	}
	if completedAt.Valid {
		v := completedAt.Time
		b.CompletedAt = &v
	}
	return b, nil
}

// newBackupID returns a 32-char hex string (16 random bytes), matching the
// convention used elsewhere in the schema (integrations.id default expression).
func newBackupID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
