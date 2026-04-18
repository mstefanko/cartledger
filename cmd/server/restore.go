package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/mstefanko/cartledger/internal/backup"
	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
)

// newRestoreCmd builds `cartledger restore <archive.tar.gz>`.
//
// Restore refuses to overwrite an existing cartledger.db unless --force is
// set. It aborts before writing anything if the archive's MANIFEST declares
// a schema_version newer than this binary knows about.
func newRestoreCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "restore <file>",
		Short: "Restore a backup archive into DATA_DIR",
		Long: `Restore extracts a cartledger backup archive into DATA_DIR and runs
migrations. DATA_DIR is created if missing.

Safety rules:
  - Refuses if DATA_DIR already contains cartledger.db (pass --force to override)
  - Rejects archives whose schema_version is newer than this binary supports
  - Rejects tar entries with absolute paths or ".." traversal
  - Never overwrites individual files silently (unless --force)

After extraction, migrations run to bring the restored DB to the current
schema (no-op if it's already current).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRestore(args[0], force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false,
		"overwrite an existing cartledger.db / existing files in DATA_DIR")
	return cmd
}

func runRestore(archivePath string, force bool) error {
	initLogger()
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}
	absArchive, err := filepath.Abs(archivePath)
	if err != nil {
		return fmt.Errorf("resolve archive path: %w", err)
	}

	maxVersion, err := db.MaxMigrationVersion()
	if err != nil {
		return fmt.Errorf("enumerate migrations: %w", err)
	}

	// Validate the archive with the same guards the HTTP / startup surfaces
	// use (symlink + hardlink rejection, SQLite-magic check on cartledger.db,
	// allowlist on entry names). db.Restore below also runs its own
	// zip-slip / schema-version guards via the shared ValidateTarEntryPath
	// helper; calling ValidateArchive first means the CLI fails fast on
	// malicious archives before touching DataDir.
	if _, err := backup.ValidateArchive(absArchive, maxVersion); err != nil {
		return fmt.Errorf("validate archive: %w", err)
	}

	res, err := db.Restore(db.RestoreOptions{
		ArchivePath:      absArchive,
		DataDir:          cfg.DataDir,
		MaxSchemaVersion: maxVersion,
		Force:            force,
	})
	if err != nil {
		return fmt.Errorf("restore: %w", err)
	}

	// Open the restored DB and bring it up to the binary's schema. This is
	// a no-op when the backup was taken from the same binary version, and
	// applies forward migrations when restoring an older backup.
	database, err := db.Open(cfg.DBPath())
	if err != nil {
		return fmt.Errorf("open restored database: %w", err)
	}
	defer database.Close()

	if err := db.RunMigrations(database); err != nil {
		return fmt.Errorf("post-restore migrations: %w", err)
	}
	postVersion, err := db.CurrentSchemaVersion(database)
	if err != nil {
		return fmt.Errorf("verify schema version: %w", err)
	}

	fmt.Printf("restore complete\n")
	fmt.Printf("  archive:             %s\n", absArchive)
	fmt.Printf("  data_dir:            %s\n", cfg.DataDir)
	fmt.Printf("  files_restored:      %d\n", res.FileCount)
	fmt.Printf("  bytes_restored:      %d\n", res.TotalBytes)
	fmt.Printf("  backup_schema:       %d\n", res.Manifest.SchemaVersion)
	fmt.Printf("  current_schema:      %d (max supported by this binary: %d)\n",
		postVersion, maxVersion)
	fmt.Printf("  backup_created_at:   %s\n", res.Manifest.CreatedAt.Format("2006-01-02T15:04:05Z07:00"))
	fmt.Printf("  backup_cartledger:   %s (commit %s)\n",
		res.Manifest.CartledgerVersion, res.Manifest.CartledgerCommit)
	return nil
}
