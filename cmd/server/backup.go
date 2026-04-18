package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
)

// newBackupCmd builds `cartledger backup <output.tar.gz>`.
//
// Backup opens the configured database (same DATA_DIR + DBPath as serve),
// checkpoints WAL, VACUUM INTOs a temp file, and writes a gzipped tar
// containing the DB + receipts/ + MANIFEST.json. The SHA256 printed on
// success is computed by re-reading the final file.
func newBackupCmd() *cobra.Command {
	var (
		skipReceipts bool
	)
	cmd := &cobra.Command{
		Use:   "backup <file>",
		Short: "Write a point-in-time backup of the database + receipts",
		Long: `Backup writes a gzipped tar archive containing:
  - cartledger.db  (produced by WAL checkpoint + VACUUM INTO)
  - receipts/      (all receipt images from DATA_DIR/receipts)
  - MANIFEST.json  (schema_version, cartledger_version, created_at, counts)

The printed SHA256 is computed by re-reading the written file, so you can
verify the archive independently with: sha256sum <file>.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBackup(args[0], !skipReceipts)
		},
	}
	cmd.Flags().BoolVar(&skipReceipts, "skip-receipts", false,
		"omit DATA_DIR/receipts from the archive (DB only)")
	return cmd
}

func runBackup(outPath string, includeReceipts bool) error {
	initLogger()
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}

	// Absolute output path — cobra hands us whatever the shell resolved,
	// but we want to print an unambiguous path to the operator.
	absOut, err := filepath.Abs(outPath)
	if err != nil {
		return fmt.Errorf("resolve output path: %w", err)
	}

	// Parent dir must exist (we don't mkdir for the output — the operator
	// asked for a specific location).
	if _, err := os.Stat(filepath.Dir(absOut)); err != nil {
		return fmt.Errorf("output directory: %w", err)
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

	res, err := db.Backup(db.BackupOptions{
		DB:                database,
		DataDir:           cfg.DataDir,
		OutputPath:        absOut,
		CartledgerVersion: version,
		CartledgerCommit:  commit,
		IncludeReceipts:   includeReceipts,
	})
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}

	// Human-readable success block. Format is stable — scripts can parse
	// the `sha256=` line via grep.
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
