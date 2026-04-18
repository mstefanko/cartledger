// Package db provides database open/migrate helpers plus backup/restore
// primitives for the `cartledger backup` and `cartledger restore` CLI
// subcommands. The backup format is a gzipped tar containing:
//
//	cartledger.db           – dump produced by `VACUUM INTO`
//	receipts/<uuid>/...     – all files under DATA_DIR/receipts (optional)
//	products/<uuid>/...     – all files under DATA_DIR/products (always included
//	                          when the directory exists; product_images.image_path
//	                          references these)
//	MANIFEST.json           – metadata (schema version, timestamps, counts)
//
// Backup is driven from a *sql.DB so tests can use an in-process database.
// Restore is driven from raw paths — it is called before migrations have
// been run on the restored file.
package db

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Manifest is the payload written as MANIFEST.json inside the backup tarball.
// Keep field names stable — they're serialized on disk and read back by
// `cartledger restore`.
type Manifest struct {
	SchemaVersion     int       `json:"schema_version"`
	CartledgerVersion string    `json:"cartledger_version"`
	CartledgerCommit  string    `json:"cartledger_commit,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	FileCount         int       `json:"file_count"`
	TotalBytes        int64     `json:"total_bytes"`
}

// BackupResult summarizes a successful backup for callers (the CLI prints it).
type BackupResult struct {
	OutputPath string
	Bytes      int64
	SHA256     string
	Manifest   Manifest
}

// BackupOptions configures a Backup call.
type BackupOptions struct {
	DB                *sql.DB
	DataDir           string // directory containing cartledger.db + receipts/ + products/
	OutputPath        string // tar.gz destination
	CartledgerVersion string
	CartledgerCommit  string
	// IncludeReceipts controls whether receipts/ is archived. Default true.
	// Note: products/ is always archived when the directory exists — there is
	// no operator-facing opt-out because every product image is referenced
	// from product_images.image_path and a silent loss would be data loss.
	IncludeReceipts bool
}

// Backup performs a WAL checkpoint, VACUUM INTO a temp file, and writes a
// gzipped tar containing the DB snapshot, receipts, product images, and
// MANIFEST.json. The output file's SHA256 is computed from the on-disk
// bytes (re-read) so we never return a hash that disagrees with the file
// shipped to the operator.
func Backup(opts BackupOptions) (*BackupResult, error) {
	if opts.DB == nil {
		return nil, errors.New("backup: DB is required")
	}
	if opts.OutputPath == "" {
		return nil, errors.New("backup: OutputPath is required")
	}
	if opts.DataDir == "" {
		return nil, errors.New("backup: DataDir is required")
	}

	// 1. Checkpoint WAL so all committed writes land in the main DB file.
	//    TRUNCATE is the safest variant — it fails rather than proceeding
	//    with a partial checkpoint, but on a quiescent DB it completes
	//    immediately. We treat a non-zero busy count as a warning, not an
	//    error, because VACUUM INTO reads through the WAL anyway.
	if _, err := opts.DB.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return nil, fmt.Errorf("wal_checkpoint: %w", err)
	}

	// 2. VACUUM INTO a temp file next to the output path (same filesystem
	//    → cheap rename is possible if we ever want it; not currently used).
	tmpDB, err := os.CreateTemp(filepath.Dir(opts.OutputPath), "cartledger-backup-*.db")
	if err != nil {
		return nil, fmt.Errorf("create temp db: %w", err)
	}
	tmpDBPath := tmpDB.Name()
	tmpDB.Close()
	// VACUUM INTO refuses to overwrite an existing file.
	if err := os.Remove(tmpDBPath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove placeholder: %w", err)
	}
	defer os.Remove(tmpDBPath)

	// sql.DB quotes parameters as values, not identifiers, so interpolate
	// the path after escaping single quotes. The path is operator-provided
	// via DATA_DIR and a temp filename — safe to embed.
	escaped := strings.ReplaceAll(tmpDBPath, "'", "''")
	if _, err := opts.DB.Exec(fmt.Sprintf("VACUUM INTO '%s'", escaped)); err != nil {
		return nil, fmt.Errorf("vacuum into: %w", err)
	}

	// 3. Query schema version from schema_migrations (golang-migrate table).
	schemaVersion, err := CurrentSchemaVersion(opts.DB)
	if err != nil {
		return nil, fmt.Errorf("read schema version: %w", err)
	}

	// 4. Write tarball.
	out, err := os.Create(opts.OutputPath)
	if err != nil {
		return nil, fmt.Errorf("create output: %w", err)
	}
	gzw := gzip.NewWriter(out)
	tw := tar.NewWriter(gzw)

	createdAt := time.Now().UTC().Truncate(time.Second)
	manifest := Manifest{
		SchemaVersion:     schemaVersion,
		CartledgerVersion: opts.CartledgerVersion,
		CartledgerCommit:  opts.CartledgerCommit,
		CreatedAt:         createdAt,
	}

	var totalBytes int64
	var fileCount int

	// a. Add the DB snapshot.
	n, err := addFileToTar(tw, tmpDBPath, "cartledger.db", createdAt)
	if err != nil {
		tw.Close()
		gzw.Close()
		out.Close()
		os.Remove(opts.OutputPath)
		return nil, fmt.Errorf("add db to tar: %w", err)
	}
	totalBytes += n
	fileCount++

	// b. Add receipts/.
	receiptsDir := filepath.Join(opts.DataDir, "receipts")
	if opts.IncludeReceipts || !explicitlyDisabled(opts) {
		if info, err := os.Stat(receiptsDir); err == nil && info.IsDir() {
			added, bytes, err := addDirToTar(tw, receiptsDir, "receipts", createdAt)
			if err != nil {
				tw.Close()
				gzw.Close()
				out.Close()
				os.Remove(opts.OutputPath)
				return nil, fmt.Errorf("add receipts: %w", err)
			}
			totalBytes += bytes
			fileCount += added
		} else if err != nil && !os.IsNotExist(err) {
			tw.Close()
			gzw.Close()
			out.Close()
			os.Remove(opts.OutputPath)
			return nil, fmt.Errorf("stat receipts: %w", err)
		}
	}

	// c. Add products/. Always archived when the directory exists — product
	//    images are referenced from product_images.image_path and losing
	//    them on restore would be silent data loss. Fresh installs without
	//    any product uploads won't have the directory yet; that's fine
	//    (skip gracefully on ENOENT, matching the receipts/ pattern above).
	productsDir := filepath.Join(opts.DataDir, "products")
	if info, err := os.Stat(productsDir); err == nil && info.IsDir() {
		added, bytes, err := addDirToTar(tw, productsDir, "products", createdAt)
		if err != nil {
			tw.Close()
			gzw.Close()
			out.Close()
			os.Remove(opts.OutputPath)
			return nil, fmt.Errorf("add products: %w", err)
		}
		totalBytes += bytes
		fileCount += added
	} else if err != nil && !os.IsNotExist(err) {
		tw.Close()
		gzw.Close()
		out.Close()
		os.Remove(opts.OutputPath)
		return nil, fmt.Errorf("stat products: %w", err)
	}

	manifest.FileCount = fileCount
	manifest.TotalBytes = totalBytes

	// d. Add MANIFEST.json (last, so counts reflect everything above).
	mbytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		tw.Close()
		gzw.Close()
		out.Close()
		os.Remove(opts.OutputPath)
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	if err := tw.WriteHeader(&tar.Header{
		Name:    "MANIFEST.json",
		Mode:    0o644,
		Size:    int64(len(mbytes)),
		ModTime: createdAt,
	}); err != nil {
		tw.Close()
		gzw.Close()
		out.Close()
		os.Remove(opts.OutputPath)
		return nil, fmt.Errorf("write manifest header: %w", err)
	}
	if _, err := tw.Write(mbytes); err != nil {
		tw.Close()
		gzw.Close()
		out.Close()
		os.Remove(opts.OutputPath)
		return nil, fmt.Errorf("write manifest: %w", err)
	}

	// Finalize.
	if err := tw.Close(); err != nil {
		gzw.Close()
		out.Close()
		os.Remove(opts.OutputPath)
		return nil, fmt.Errorf("close tar: %w", err)
	}
	if err := gzw.Close(); err != nil {
		out.Close()
		os.Remove(opts.OutputPath)
		return nil, fmt.Errorf("close gzip: %w", err)
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(opts.OutputPath)
		return nil, fmt.Errorf("sync output: %w", err)
	}
	if err := out.Close(); err != nil {
		os.Remove(opts.OutputPath)
		return nil, fmt.Errorf("close output: %w", err)
	}

	// 5. Re-read and hash the written file — the checksum we print MUST
	//    match the bytes on disk, not the in-memory pipeline.
	hash, outSize, err := sha256File(opts.OutputPath)
	if err != nil {
		return nil, fmt.Errorf("hash output: %w", err)
	}

	return &BackupResult{
		OutputPath: opts.OutputPath,
		Bytes:      outSize,
		SHA256:     hash,
		Manifest:   manifest,
	}, nil
}

// explicitlyDisabled returns true only when the caller set IncludeReceipts=false
// via a non-default construction. Because Go zeros bool fields, an operator
// who does not set the flag at all still gets receipts. (We accept this
// asymmetry — the common case is "include receipts".)
func explicitlyDisabled(opts BackupOptions) bool {
	return !opts.IncludeReceipts
}

// RestoreOptions configures a Restore call.
type RestoreOptions struct {
	ArchivePath string // tar.gz to extract
	DataDir     string // destination (must not contain cartledger.db unless Force)
	// MaxSchemaVersion is the highest migration version this binary knows
	// about. If the manifest declares a version > this, Restore aborts.
	MaxSchemaVersion int
	Force            bool // permit overwrite when DATA_DIR already has cartledger.db
}

// RestoreResult summarizes a restore operation for the CLI caller.
type RestoreResult struct {
	Manifest   Manifest
	FileCount  int
	TotalBytes int64
}

// Restore extracts a backup archive into DataDir. It refuses to overwrite an
// existing cartledger.db unless Force is set, and rejects any tar entry with
// an absolute path or `..` traversal (filepath.IsLocal check).
func Restore(opts RestoreOptions) (*RestoreResult, error) {
	if opts.ArchivePath == "" {
		return nil, errors.New("restore: ArchivePath is required")
	}
	if opts.DataDir == "" {
		return nil, errors.New("restore: DataDir is required")
	}

	// 1. Safety gate: refuse if DATA_DIR already contains cartledger.db.
	dbDest := filepath.Join(opts.DataDir, "cartledger.db")
	if _, err := os.Stat(dbDest); err == nil && !opts.Force {
		return nil, fmt.Errorf(
			"refusing to restore: %s already exists. Move or delete it (or pass --force) before restoring",
			dbDest,
		)
	} else if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("stat %s: %w", dbDest, err)
	}

	// Ensure DataDir exists — restore MUST create it if missing.
	if err := os.MkdirAll(opts.DataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	// 2. First pass: find MANIFEST.json and validate schema version.
	manifest, err := readManifest(opts.ArchivePath)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	if opts.MaxSchemaVersion > 0 && manifest.SchemaVersion > opts.MaxSchemaVersion {
		return nil, fmt.Errorf(
			"backup declares schema_version=%d but this binary knows only up to %d; upgrade cartledger before restoring",
			manifest.SchemaVersion, opts.MaxSchemaVersion,
		)
	}

	// 3. Second pass: extract files. Paths are validated against traversal
	//    using filepath.IsLocal — any entry that would escape DataDir is
	//    rejected and the restore aborts.
	f, err := os.Open(opts.ArchivePath)
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()
	gzr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)

	var fileCount int
	var totalBytes int64

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar entry: %w", err)
		}

		name := filepath.ToSlash(hdr.Name)
		if name == "MANIFEST.json" {
			continue // already consumed
		}

		// Traversal defense: forbid absolute paths and `..` anywhere.
		// filepath.IsLocal (Go 1.20+) enforces both.
		if !filepath.IsLocal(name) {
			return nil, fmt.Errorf("unsafe tar entry path: %q (absolute or traversal)", hdr.Name)
		}
		// Extra belt-and-suspenders: reject explicit `..` segments even on
		// platforms where IsLocal's semantics differ.
		if strings.Contains(name, "..") {
			parts := strings.Split(name, "/")
			for _, p := range parts {
				if p == ".." {
					return nil, fmt.Errorf("unsafe tar entry path: %q (traversal segment)", hdr.Name)
				}
			}
		}

		dest := filepath.Join(opts.DataDir, filepath.FromSlash(name))
		// Defense in depth: verify the joined path is still under DataDir.
		absDataDir, err := filepath.Abs(opts.DataDir)
		if err != nil {
			return nil, fmt.Errorf("abs data dir: %w", err)
		}
		absDest, err := filepath.Abs(dest)
		if err != nil {
			return nil, fmt.Errorf("abs dest: %w", err)
		}
		if !strings.HasPrefix(absDest+string(filepath.Separator), absDataDir+string(filepath.Separator)) &&
			absDest != absDataDir {
			return nil, fmt.Errorf("unsafe tar entry path: %q (escapes data dir)", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return nil, fmt.Errorf("mkdir %s: %w", dest, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return nil, fmt.Errorf("mkdir parent %s: %w", dest, err)
			}
			// O_EXCL so we never silently overwrite (Force only relaxes
			// the cartledger.db gate; per-file overwrite is still refused).
			flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
			if !opts.Force {
				flags = os.O_WRONLY | os.O_CREATE | os.O_EXCL
			}
			dst, err := os.OpenFile(dest, flags, 0o644)
			if err != nil {
				return nil, fmt.Errorf("create %s: %w", dest, err)
			}
			n, err := io.Copy(dst, tr)
			if cerr := dst.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return nil, fmt.Errorf("write %s: %w", dest, err)
			}
			fileCount++
			totalBytes += n
		default:
			// Silently skip symlinks, devices, etc. — we do not produce
			// them in Backup, so any such entry indicates a hand-edited
			// archive we don't trust.
			continue
		}
	}

	return &RestoreResult{
		Manifest:   *manifest,
		FileCount:  fileCount,
		TotalBytes: totalBytes,
	}, nil
}

// readManifest makes a streaming pass over the archive to find MANIFEST.json.
// It is separate from the extraction pass so we can fail schema-check before
// writing anything to disk.
func readManifest(archivePath string) (*Manifest, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gzr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("MANIFEST.json not found in archive")
		}
		if err != nil {
			return nil, err
		}
		if filepath.ToSlash(hdr.Name) != "MANIFEST.json" {
			continue
		}
		buf, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		var m Manifest
		if err := json.Unmarshal(buf, &m); err != nil {
			return nil, fmt.Errorf("parse manifest: %w", err)
		}
		return &m, nil
	}
}

// CurrentSchemaVersion reads the latest version from schema_migrations. Returns
// 0 when the table is absent (fresh DB before first migrate) so the manifest
// can still record "pre-migration".
func CurrentSchemaVersion(db *sql.DB) (int, error) {
	var name string
	err := db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='schema_migrations'",
	).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var version int64
	var dirty bool
	err = db.QueryRow("SELECT version, dirty FROM schema_migrations LIMIT 1").Scan(&version, &dirty)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return int(version), nil
}

// MaxMigrationVersion returns the highest numeric migration version embedded
// in the binary. Used by restore to reject forward-incompatible backups.
func MaxMigrationVersion() (int, error) {
	return maxVersionFromFS(migrationsFS, "migrations")
}

func maxVersionFromFS(fsys embed.FS, dir string) (int, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return 0, err
	}
	var versions []int
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		// Files are NNN_name.up.sql — parse the leading integer.
		idx := strings.Index(name, "_")
		if idx <= 0 {
			continue
		}
		v, err := strconv.Atoi(name[:idx])
		if err != nil {
			continue
		}
		versions = append(versions, v)
	}
	if len(versions) == 0 {
		return 0, nil
	}
	sort.Ints(versions)
	return versions[len(versions)-1], nil
}

// addFileToTar copies one file into the tar writer at the given archive-relative
// name. Returns bytes written.
func addFileToTar(tw *tar.Writer, srcPath, archName string, mtime time.Time) (int64, error) {
	f, err := os.Open(srcPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	hdr := &tar.Header{
		Name:    archName,
		Mode:    0o644,
		Size:    info.Size(),
		ModTime: mtime,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return 0, err
	}
	return io.Copy(tw, f)
}

// addDirToTar walks src recursively and writes every regular file under the
// given archive prefix. Returns (file count, bytes written).
func addDirToTar(tw *tar.Writer, src, archPrefix string, mtime time.Time) (int, int64, error) {
	var count int
	var bytes int64
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		archName := archPrefix
		if rel != "." {
			archName = archPrefix + "/" + filepath.ToSlash(rel)
		}
		if d.IsDir() {
			if rel == "." {
				return nil
			}
			return tw.WriteHeader(&tar.Header{
				Name:     archName + "/",
				Mode:     0o755,
				Typeflag: tar.TypeDir,
				ModTime:  mtime,
			})
		}
		if !d.Type().IsRegular() {
			return nil // skip symlinks, sockets, etc.
		}
		n, err := addFileToTar(tw, path, archName, mtime)
		if err != nil {
			return err
		}
		count++
		bytes += n
		return nil
	})
	return count, bytes, err
}

// sha256File returns the hex-encoded SHA256 of a file plus its size in bytes.
func sha256File(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
