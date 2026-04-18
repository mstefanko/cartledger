package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/mstefanko/cartledger/internal/backup"
	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
)

// newBackupCmd builds `cartledger backup [--out <file>]`.
//
// The CLI and HTTP surfaces share the same internal/backup.Runner so there
// is exactly one code path in production. By default the output path is
// $DATA_DIR/backups/backup-<UTC timestamp>.tar.gz — the directory is created
// by config.Load() so operators don't have to mkdir it manually.
func newBackupCmd() *cobra.Command {
	var outPath string
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Write a point-in-time backup of the database + receipts",
		Long: `Backup writes a gzipped tar archive containing:
  - cartledger.db  (produced by WAL checkpoint + VACUUM INTO)
  - receipts/      (all receipt images from DATA_DIR/receipts)
  - MANIFEST.json  (schema_version, cartledger_version, created_at, counts)

With no --out flag, the archive is written to
$DATA_DIR/backups/backup-<UTC timestamp>.tar.gz and a row is recorded in
the backups table so the web UI can list / download / prune it.

With --out, the archive is written to the provided path, bypassing the
backups table. Useful for ad-hoc snapshots piped through a network copy.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBackup(outPath)
		},
	}
	cmd.Flags().StringVar(&outPath, "out", "",
		"output path (default: $DATA_DIR/backups/backup-<timestamp>.tar.gz)")
	return cmd
}

func runBackup(outPath string) error {
	initLogger()
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}

	database, err := db.Open(cfg.DBPath())
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	// IMPORTANT: do NOT run migrations here. Backup must operate on the DB
	// as-is — we don't want an operator running `cartledger backup` to
	// inadvertently apply schema changes if they're on a newer binary than
	// the running server.

	if outPath != "" {
		return runLegacyBackupToPath(cfg, database, outPath)
	}
	return runManagedBackup(cfg, database)
}

// runManagedBackup invokes the Runner, writing the archive under BackupDir
// and recording a row in the backups table. This mirrors the HTTP surface.
func runManagedBackup(cfg *config.Config, database *sql.DB) error {
	store := db.NewBackupStore(database)
	// Startup hygiene the server does too — clean up any crashed-run rows so
	// the CLI never trips over a leftover status='running' ghost.
	recCtx, recCancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = store.ReconcileRunning(recCtx)
	recCancel()

	runner := backup.NewRunner(database, store, cfg, slog.Default(), nil)
	runner.SetBuildInfo(version, commit)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	id, err := runner.Start(ctx)
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	row, err := store.Get(context.Background(), id)
	if err != nil || row == nil {
		return fmt.Errorf("backup: fetch row after completion: %w", err)
	}

	archive := filepath.Join(cfg.BackupDir(), row.Filename)
	fmt.Printf("backup complete\n")
	fmt.Printf("  id:              %s\n", row.ID)
	fmt.Printf("  path:            %s\n", archive)
	if row.SizeBytes != nil {
		fmt.Printf("  bytes:           %d\n", *row.SizeBytes)
	}
	fmt.Printf("  schema_version:  %d\n", row.SchemaVersion)
	fmt.Printf("  missing_images:  %d\n", row.MissingImages)
	if row.CompletedAt != nil {
		fmt.Printf("  completed_at:    %s\n", row.CompletedAt.UTC().Format("2006-01-02T15:04:05Z07:00"))
	}
	return nil
}

// runLegacyBackupToPath writes a one-off archive to an operator-specified
// path, bypassing the backups table. Kept so existing `--out` tooling (cron
// jobs, manual invocations) continues to work.
func runLegacyBackupToPath(cfg *config.Config, database *sql.DB, outPath string) error {
	absOut, err := filepath.Abs(outPath)
	if err != nil {
		return fmt.Errorf("resolve output path: %w", err)
	}
	if _, err := os.Stat(filepath.Dir(absOut)); err != nil {
		return fmt.Errorf("output directory: %w", err)
	}
	res, err := db.Backup(db.BackupOptions{
		DB:                database,
		DataDir:           cfg.DataDir,
		OutputPath:        absOut,
		CartledgerVersion: version,
		CartledgerCommit:  commit,
		IncludeReceipts:   true,
	})
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}

	fmt.Printf("backup complete\n")
	fmt.Printf("  path:            %s\n", res.OutputPath)
	fmt.Printf("  bytes:           %d\n", res.Bytes)
	fmt.Printf("  sha256:          %s\n", res.SHA256)
	fmt.Printf("  schema_version:  %d\n", res.Manifest.SchemaVersion)
	fmt.Printf("  cartledger:      %s (commit %s)\n",
		res.Manifest.CartledgerVersion, res.Manifest.CartledgerCommit)
	fmt.Printf("  files_archived:  %d\n", res.Manifest.FileCount)
	fmt.Printf("  archive_bytes:   %d (pre-compression)\n", res.Manifest.TotalBytes)
	return nil
}
