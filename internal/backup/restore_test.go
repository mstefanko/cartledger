package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
)

// builder is a tiny DSL for composing tar.gz archives in memory. Each test in
// the malicious-archive matrix starts from a "good" base (valid manifest +
// cartledger.db with magic header) and swaps in one anomaly — so each failure
// message proves exactly which guard triggered.
type builder struct {
	buf *bytes.Buffer
	gzw *gzip.Writer
	tw  *tar.Writer
}

func newBuilder() *builder {
	buf := &bytes.Buffer{}
	gzw := gzip.NewWriter(buf)
	tw := tar.NewWriter(gzw)
	return &builder{buf: buf, gzw: gzw, tw: tw}
}

// writeFile appends a regular tar entry. Returns b for chaining.
func (b *builder) writeFile(name string, body []byte) *builder {
	hdr := &tar.Header{
		Name:     name,
		Mode:     0o644,
		Size:     int64(len(body)),
		Typeflag: tar.TypeReg,
		ModTime:  time.Unix(0, 0),
	}
	if err := b.tw.WriteHeader(hdr); err != nil {
		panic(err)
	}
	if _, err := b.tw.Write(body); err != nil {
		panic(err)
	}
	return b
}

// writeHeader appends a non-regular tar header (symlink / hardlink / etc.).
func (b *builder) writeHeader(hdr *tar.Header) *builder {
	if err := b.tw.WriteHeader(hdr); err != nil {
		panic(err)
	}
	return b
}

// finish closes the tar + gzip writers and returns the raw archive bytes.
func (b *builder) finish() []byte {
	if err := b.tw.Close(); err != nil {
		panic(err)
	}
	if err := b.gzw.Close(); err != nil {
		panic(err)
	}
	return b.buf.Bytes()
}

// validManifest returns a MANIFEST.json body with schema_version = 1 and
// a non-empty app_version. Tests that need to diverge (e.g. schema bump)
// unmarshal + re-encode.
func validManifest(schemaVersion int) []byte {
	m := db.Manifest{
		SchemaVersion:     schemaVersion,
		CartledgerVersion: "v1.0.0",
		CreatedAt:         time.Unix(0, 0),
	}
	out, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return out
}

// validDBBytes returns a fake cartledger.db body that starts with the SQLite
// magic header. Body after the magic is irrelevant — the validator only spot-
// checks the prefix.
func validDBBytes() []byte {
	body := make([]byte, 0, 256)
	body = append(body, sqliteMagic...)
	body = append(body, bytes.Repeat([]byte{0}, 100)...)
	return body
}

// writeArchive persists raw archive bytes to a temp file and returns the path.
func writeArchive(t *testing.T, data []byte) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "archive.tar.gz")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	return p
}

// -----------------------------------------------------------------------------
// ValidateArchive — malicious-archive matrix
// -----------------------------------------------------------------------------

func TestValidateArchive_HappyPath(t *testing.T) {
	data := newBuilder().
		writeFile("MANIFEST.json", validManifest(1)).
		writeFile("cartledger.db", validDBBytes()).
		writeFile("receipts/abc/1.jpg", []byte("fake-image")).
		writeFile("products/xyz/img.png", []byte("fake-png")).
		finish()

	m, err := ValidateArchive(writeArchive(t, data), 10)
	if err != nil {
		t.Fatalf("happy path failed: %v", err)
	}
	if m.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", m.SchemaVersion)
	}
	if m.CartledgerVersion == "" {
		t.Errorf("app_version empty")
	}
}

func TestValidateArchive_HappyPathLowerCaseManifest(t *testing.T) {
	// Plan references "manifest.json"; current writer emits "MANIFEST.json".
	// Validator accepts both so a future writer normalizing casing doesn't
	// break existing validators.
	data := newBuilder().
		writeFile("manifest.json", validManifest(1)).
		writeFile("cartledger.db", validDBBytes()).
		finish()

	if _, err := ValidateArchive(writeArchive(t, data), 10); err != nil {
		t.Fatalf("lower-case manifest rejected: %v", err)
	}
}

func TestValidateArchive_RejectsTraversalDotDot(t *testing.T) {
	data := newBuilder().
		writeFile("MANIFEST.json", validManifest(1)).
		writeFile("cartledger.db", validDBBytes()).
		writeFile("../etc/passwd", []byte("pwned")).
		finish()

	_, err := ValidateArchive(writeArchive(t, data), 10)
	if err == nil {
		t.Fatalf("expected rejection for .. traversal")
	}
	if !strings.Contains(err.Error(), "traversal") && !strings.Contains(err.Error(), "absolute") {
		t.Errorf("unexpected error (want traversal/absolute message): %v", err)
	}
}

func TestValidateArchive_RejectsAbsolutePath(t *testing.T) {
	data := newBuilder().
		writeFile("MANIFEST.json", validManifest(1)).
		writeFile("cartledger.db", validDBBytes()).
		writeFile("/etc/passwd", []byte("pwned")).
		finish()

	_, err := ValidateArchive(writeArchive(t, data), 10)
	if err == nil {
		t.Fatalf("expected rejection for absolute path")
	}
	if !strings.Contains(err.Error(), "absolute") && !strings.Contains(err.Error(), "traversal") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateArchive_RejectsSymlink(t *testing.T) {
	b := newBuilder().
		writeFile("MANIFEST.json", validManifest(1)).
		writeFile("cartledger.db", validDBBytes())
	b.writeHeader(&tar.Header{
		Name:     "receipts/evil",
		Linkname: "/etc/passwd",
		Typeflag: tar.TypeSymlink,
		Mode:     0o777,
		ModTime:  time.Unix(0, 0),
	})

	_, err := ValidateArchive(writeArchive(t, b.finish()), 10)
	if err == nil {
		t.Fatalf("expected rejection for symlink")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("want symlink in error, got: %v", err)
	}
}

func TestValidateArchive_RejectsHardlink(t *testing.T) {
	b := newBuilder().
		writeFile("MANIFEST.json", validManifest(1)).
		writeFile("cartledger.db", validDBBytes())
	b.writeHeader(&tar.Header{
		Name:     "receipts/evil",
		Linkname: "cartledger.db",
		Typeflag: tar.TypeLink,
		Mode:     0o644,
		ModTime:  time.Unix(0, 0),
	})

	_, err := ValidateArchive(writeArchive(t, b.finish()), 10)
	if err == nil {
		t.Fatalf("expected rejection for hardlink")
	}
	if !strings.Contains(err.Error(), "hard link") {
		t.Errorf("want hard link in error, got: %v", err)
	}
}

func TestValidateArchive_RejectsMissingManifest(t *testing.T) {
	data := newBuilder().
		writeFile("cartledger.db", validDBBytes()).
		finish()

	_, err := ValidateArchive(writeArchive(t, data), 10)
	if err == nil {
		t.Fatalf("expected rejection when manifest is missing")
	}
	if !strings.Contains(err.Error(), "MANIFEST.json") {
		t.Errorf("want manifest-missing message, got: %v", err)
	}
}

func TestValidateArchive_RejectsForwardSchema(t *testing.T) {
	data := newBuilder().
		writeFile("MANIFEST.json", validManifest(99)).
		writeFile("cartledger.db", validDBBytes()).
		finish()

	_, err := ValidateArchive(writeArchive(t, data), 5)
	if err == nil {
		t.Fatalf("expected rejection for forward-incompatible schema")
	}
	if !strings.Contains(err.Error(), "schema_version=99") {
		t.Errorf("want forward-schema message, got: %v", err)
	}
}

func TestValidateArchive_RejectsMissingSchemaVersion(t *testing.T) {
	// schema_version omitted entirely → zero-value after unmarshal → reject.
	m, err := json.Marshal(map[string]any{
		"cartledger_version": "v1.0.0",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	data := newBuilder().
		writeFile("MANIFEST.json", m).
		writeFile("cartledger.db", validDBBytes()).
		finish()

	_, verr := ValidateArchive(writeArchive(t, data), 10)
	if verr == nil {
		t.Fatalf("expected rejection for missing schema_version")
	}
}

func TestValidateArchive_RejectsEmptyAppVersion(t *testing.T) {
	m, err := json.Marshal(map[string]any{
		"schema_version":     1,
		"cartledger_version": "",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	data := newBuilder().
		writeFile("MANIFEST.json", m).
		writeFile("cartledger.db", validDBBytes()).
		finish()

	_, verr := ValidateArchive(writeArchive(t, data), 10)
	if verr == nil {
		t.Fatalf("expected rejection for empty app_version")
	}
	if !strings.Contains(verr.Error(), "app_version") {
		t.Errorf("want app_version message, got: %v", verr)
	}
}

func TestValidateArchive_RejectsBadSQLiteMagic(t *testing.T) {
	data := newBuilder().
		writeFile("MANIFEST.json", validManifest(1)).
		writeFile("cartledger.db", []byte("not a sqlite database at all")).
		finish()

	_, err := ValidateArchive(writeArchive(t, data), 10)
	if err == nil {
		t.Fatalf("expected rejection for bad SQLite magic")
	}
	if !strings.Contains(err.Error(), "SQLite magic") {
		t.Errorf("want SQLite-magic message, got: %v", err)
	}
}

func TestValidateArchive_RejectsMissingDB(t *testing.T) {
	data := newBuilder().
		writeFile("MANIFEST.json", validManifest(1)).
		writeFile("receipts/abc/1.jpg", []byte("img")).
		finish()

	_, err := ValidateArchive(writeArchive(t, data), 10)
	if err == nil {
		t.Fatalf("expected rejection when cartledger.db is absent")
	}
	if !strings.Contains(err.Error(), "cartledger.db") {
		t.Errorf("want missing-db message, got: %v", err)
	}
}

func TestValidateArchive_RejectsEntryOutsideAllowlist(t *testing.T) {
	data := newBuilder().
		writeFile("MANIFEST.json", validManifest(1)).
		writeFile("cartledger.db", validDBBytes()).
		writeFile("random/file.txt", []byte("not allowed")).
		finish()

	_, err := ValidateArchive(writeArchive(t, data), 10)
	if err == nil {
		t.Fatalf("expected rejection for non-allowlisted entry")
	}
	if !strings.Contains(err.Error(), "allowlist") {
		t.Errorf("want allowlist message, got: %v", err)
	}
}

// -----------------------------------------------------------------------------
// StageRestore — size cap + happy path + cleanup behavior
// -----------------------------------------------------------------------------

func makeCfg(t *testing.T) *config.Config {
	t.Helper()
	dataDir := t.TempDir()
	return &config.Config{
		Port:              "8079",
		DataDir:           dataDir,
		BackupRetainCount: 3,
	}
}

func TestStageRestore_HappyPath(t *testing.T) {
	cfg := makeCfg(t)
	data := newBuilder().
		writeFile("MANIFEST.json", validManifest(1)).
		writeFile("cartledger.db", validDBBytes()).
		finish()

	err := StageRestore(cfg, discardLogger(), bytes.NewReader(data), int64(len(data)+10))
	if err != nil {
		t.Fatalf("StageRestore: %v", err)
	}

	paths := stagedPathsFor(cfg)
	for _, p := range []string{paths.Archive, paths.Manifest} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected file %s: %v", p, err)
		}
	}
}

func TestStageRestore_SizeCap(t *testing.T) {
	cfg := makeCfg(t)
	data := newBuilder().
		writeFile("MANIFEST.json", validManifest(1)).
		writeFile("cartledger.db", validDBBytes()).
		writeFile("receipts/big", bytes.Repeat([]byte("x"), 1024)).
		finish()

	// Set cap well below the archive size so the reader truncates.
	err := StageRestore(cfg, discardLogger(), bytes.NewReader(data), 64)
	if err == nil {
		t.Fatalf("expected size-cap rejection")
	}
	if !errors.Is(err, ErrArchiveTooLarge) {
		t.Errorf("want ErrArchiveTooLarge, got: %v", err)
	}
	// Pending dir must be cleaned.
	if _, err := os.Stat(stagedPathsFor(cfg).Dir); err == nil {
		t.Errorf("expected pending dir removed after size-cap rejection")
	}
}

func TestStageRestore_ValidationFailureCleansPendingDir(t *testing.T) {
	cfg := makeCfg(t)
	// Archive is syntactically a tar.gz but missing MANIFEST.json.
	data := newBuilder().
		writeFile("cartledger.db", validDBBytes()).
		finish()

	err := StageRestore(cfg, discardLogger(), bytes.NewReader(data), int64(len(data)+10))
	if err == nil {
		t.Fatalf("expected validator rejection")
	}
	if !errors.Is(err, ErrArchiveInvalid) {
		t.Errorf("want ErrArchiveInvalid, got: %v", err)
	}
	if _, err := os.Stat(stagedPathsFor(cfg).Dir); err == nil {
		t.Errorf("expected pending dir removed after validation failure")
	}
}

// -----------------------------------------------------------------------------
// ApplyStagedRestoreIfPresent
// -----------------------------------------------------------------------------

func TestApplyStagedRestoreIfPresent_NoPendingDir(t *testing.T) {
	cfg := makeCfg(t)
	if err := ApplyStagedRestoreIfPresent(cfg, discardLogger()); err != nil {
		t.Errorf("no-op case should return nil, got: %v", err)
	}
}

func TestApplyStagedRestoreIfPresent_HappyPath(t *testing.T) {
	cfg := makeCfg(t)

	// Pre-existing live DB that must be moved aside.
	liveDB := filepath.Join(cfg.DataDir, "cartledger.db")
	if err := os.WriteFile(liveDB, []byte("old db contents"), 0o644); err != nil {
		t.Fatalf("seed live db: %v", err)
	}

	// Stage a valid archive that includes a new DB + one receipt image.
	data := newBuilder().
		writeFile("MANIFEST.json", validManifest(1)).
		writeFile("cartledger.db", validDBBytes()).
		writeFile("receipts/new/front.jpg", []byte("new-image")).
		finish()
	if err := StageRestore(cfg, discardLogger(), bytes.NewReader(data), int64(len(data)+10)); err != nil {
		t.Fatalf("stage: %v", err)
	}

	if err := ApplyStagedRestoreIfPresent(cfg, discardLogger()); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Live DB now matches the fake magic header.
	got, err := os.ReadFile(liveDB)
	if err != nil {
		t.Fatalf("read restored db: %v", err)
	}
	if !bytes.HasPrefix(got, sqliteMagic) {
		t.Errorf("restored db missing SQLite magic; got prefix %q", got[:min(len(got), 16)])
	}

	// Old DB was moved aside with a pre-restore- prefix.
	entries, err := os.ReadDir(cfg.DataDir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	var foundPreRestore bool
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "cartledger.db.pre-restore-") {
			foundPreRestore = true
			body, _ := os.ReadFile(filepath.Join(cfg.DataDir, e.Name()))
			if string(body) != "old db contents" {
				t.Errorf("pre-restore db body = %q, want %q", body, "old db contents")
			}
		}
	}
	if !foundPreRestore {
		t.Errorf("expected a cartledger.db.pre-restore-* file")
	}

	// Receipt image was extracted.
	if _, err := os.Stat(filepath.Join(cfg.DataDir, "receipts", "new", "front.jpg")); err != nil {
		t.Errorf("expected receipt image extracted: %v", err)
	}

	// Pending dir was cleaned.
	if _, err := os.Stat(stagedPathsFor(cfg).Dir); err == nil {
		t.Errorf("expected pending dir removed")
	}
}

func TestApplyStagedRestoreIfPresent_InvalidArchiveLeavesLiveDBIntact(t *testing.T) {
	cfg := makeCfg(t)
	liveDB := filepath.Join(cfg.DataDir, "cartledger.db")
	originalBody := []byte("intact live db")
	if err := os.WriteFile(liveDB, originalBody, 0o644); err != nil {
		t.Fatalf("seed live db: %v", err)
	}

	// Manually drop an invalid pending archive + sidecar — bypass StageRestore
	// so the validator only fires inside ApplyStagedRestoreIfPresent.
	paths := stagedPathsFor(cfg)
	if err := os.MkdirAll(paths.Dir, 0o755); err != nil {
		t.Fatalf("mkdir pending: %v", err)
	}
	badArchive := newBuilder().
		writeFile("cartledger.db", []byte("not sqlite")).
		finish()
	if err := os.WriteFile(paths.Archive, badArchive, 0o600); err != nil {
		t.Fatalf("write bad archive: %v", err)
	}
	if err := os.WriteFile(paths.Manifest, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}

	err := ApplyStagedRestoreIfPresent(cfg, discardLogger())
	if err == nil {
		t.Fatalf("expected apply to fail")
	}

	// Live DB must be untouched: a failed re-validation happens BEFORE any
	// rename, so the operator's data is safe.
	got, rerr := os.ReadFile(liveDB)
	if rerr != nil {
		t.Fatalf("read live db: %v", rerr)
	}
	if !bytes.Equal(got, originalBody) {
		t.Errorf("live DB mutated during failed restore; got %q", got)
	}
}

// -----------------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------------

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
