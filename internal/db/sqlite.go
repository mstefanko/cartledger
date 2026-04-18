package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Open opens a SQLite database at the given path with performance pragmas configured.
// It creates the parent directory if it does not exist.
func Open(path string) (*sql.DB, error) {
	// Ensure the directory exists.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// Performance pragmas for a small self-hosted app.
	pragmas := []string{
		"PRAGMA journal_mode=WAL",       // concurrent reads
		"PRAGMA synchronous=NORMAL",     // faster writes, safe with WAL
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=10000",     // 10s wait on locks
		"PRAGMA cache_size=-20000",      // 20MB cache
		"PRAGMA mmap_size=268435456",    // 256MB memory-mapped I/O
		"PRAGMA temp_store=MEMORY",      // temp B-trees in RAM
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("exec pragma %q: %w", p, err)
		}
	}

	return db, nil
}
