package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	ReceiptImageKindOriginal  = "original"
	ReceiptImageKindProcessed = "processed"

	maxStorageKeyLength = 4096
)

// Local resolves app-relative storage keys under one filesystem root.
type Local struct {
	root    string
	absRoot string
}

// NewLocal constructs a local storage resolver rooted at DATA_DIR.
func NewLocal(root string) (*Local, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("storage root must not be empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve storage root: %w", err)
	}
	return &Local{root: root, absRoot: abs}, nil
}

// Root returns the configured filesystem root.
func (l *Local) Root() string {
	if l == nil {
		return ""
	}
	return l.root
}

// Path resolves a validated storage key to a filesystem path under the root.
func (l *Local) Path(key string) (string, error) {
	if l == nil {
		return "", errors.New("storage is nil")
	}
	if err := ValidateKey(key); err != nil {
		return "", err
	}
	target := filepath.Join(l.root, filepath.FromSlash(key))
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("resolve storage key %q: %w", key, err)
	}
	sep := string(filepath.Separator)
	if absTarget != l.absRoot && !strings.HasPrefix(absTarget+sep, l.absRoot+sep) {
		return "", fmt.Errorf("storage key %q escapes storage root", key)
	}
	return target, nil
}

// ReadFile reads a storage key after validating and resolving it.
func (l *Local) ReadFile(key string) ([]byte, error) {
	p, err := l.Path(key)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

// WriteFileAtomic writes data to key via a same-directory temporary file.
func (l *Local) WriteFileAtomic(key string, data []byte, perm os.FileMode) error {
	p, err := l.Path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("create storage dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), "."+filepath.Base(p)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, p); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	cleanup = false
	return nil
}

// DeleteReceipt removes the canonical receipt subtree by receipt id.
func (l *Local) DeleteReceipt(receiptID string) error {
	if err := ValidateOwnerID(receiptID); err != nil {
		return err
	}
	return l.removeSubtree("receipts/" + receiptID)
}

// DeleteProduct removes the canonical product subtree by product id.
func (l *Local) DeleteProduct(productID string) error {
	if err := ValidateOwnerID(productID); err != nil {
		return err
	}
	return l.removeSubtree("products/" + productID)
}

// LegacyReceiptsRoot returns the DATA_DIR-relative receipts root. It exists
// for compatibility paths that still need to inspect pre-receipt_images files.
func LegacyReceiptsRoot(dataDir string) (string, error) {
	local, err := NewLocal(dataDir)
	if err != nil {
		return "", err
	}
	return local.Path("receipts")
}

// LegacyReceiptDir returns the DATA_DIR-relative directory for one receipt.
func LegacyReceiptDir(dataDir, receiptID string) (string, error) {
	if err := ValidateOwnerID(receiptID); err != nil {
		return "", err
	}
	local, err := NewLocal(dataDir)
	if err != nil {
		return "", err
	}
	return local.Path("receipts/" + receiptID)
}

// LegacyProductsRoot returns the DATA_DIR-relative products root.
func LegacyProductsRoot(dataDir string) (string, error) {
	local, err := NewLocal(dataDir)
	if err != nil {
		return "", err
	}
	return local.Path("products")
}

func (l *Local) removeSubtree(key string) error {
	p, err := l.Path(key)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(p); err != nil {
		return fmt.Errorf("remove storage subtree %q: %w", key, err)
	}
	return nil
}

// ReceiptOriginalKey returns the canonical storage key for an original page.
func ReceiptOriginalKey(receiptID string, pageNumber int, ext string) (string, error) {
	return receiptImageKey(receiptID, ReceiptImageKindOriginal, pageNumber, ext)
}

// ReceiptProcessedKey returns the canonical storage key for a processed page.
func ReceiptProcessedKey(receiptID string, pageNumber int, ext string) (string, error) {
	return receiptImageKey(receiptID, ReceiptImageKindProcessed, pageNumber, ext)
}

func receiptImageKey(receiptID, kind string, pageNumber int, ext string) (string, error) {
	if err := ValidateOwnerID(receiptID); err != nil {
		return "", err
	}
	if kind != ReceiptImageKindOriginal && kind != ReceiptImageKindProcessed {
		return "", fmt.Errorf("invalid receipt image kind %q", kind)
	}
	if pageNumber <= 0 {
		return "", fmt.Errorf("page number must be positive")
	}
	ext = strings.ToLower(strings.TrimSpace(ext))
	if ext == "" {
		ext = ".jpg"
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	switch ext {
	case ".jpg", ".jpeg", ".png":
	default:
		return "", fmt.Errorf("unsupported image extension %q", ext)
	}
	key := fmt.Sprintf("receipts/%s/%s/%d%s", receiptID, kind, pageNumber, ext)
	return key, ValidateKey(key)
}

// ProductImageKey returns the canonical storage key for a product image.
func ProductImageKey(productID, imageID, ext string) (string, error) {
	if err := ValidateOwnerID(productID); err != nil {
		return "", err
	}
	if err := ValidateOwnerID(imageID); err != nil {
		return "", err
	}
	ext = strings.ToLower(strings.TrimSpace(ext))
	if ext == "" {
		ext = ".jpg"
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	switch ext {
	case ".jpg", ".jpeg", ".png":
	default:
		return "", fmt.Errorf("unsupported image extension %q", ext)
	}
	key := fmt.Sprintf("products/%s/%s%s", productID, imageID, ext)
	return key, ValidateKey(key)
}

// ValidateOwnerID validates a typed owner id used as one path segment.
func ValidateOwnerID(id string) error {
	if id == "" {
		return errors.New("owner id must not be empty")
	}
	if strings.ContainsAny(id, `/\`+"\x00") || containsASCIIControl(id) || id == "." || id == ".." {
		return fmt.Errorf("invalid owner id %q", id)
	}
	return nil
}

// ValidateKey ensures key is a clean, slash-separated, relative storage key.
func ValidateKey(key string) error {
	if key == "" {
		return errors.New("storage key must not be empty")
	}
	if len(key) > maxStorageKeyLength {
		return fmt.Errorf("storage key length %d exceeds limit %d", len(key), maxStorageKeyLength)
	}
	if strings.Contains(key, "\x00") {
		return fmt.Errorf("storage key %q contains NUL", key)
	}
	if containsASCIIControl(key) {
		return fmt.Errorf("storage key %q contains control characters", key)
	}
	if strings.Contains(key, `\`) {
		return fmt.Errorf("storage key %q must use forward slashes", key)
	}
	if strings.HasPrefix(key, "/") || filepath.IsAbs(key) {
		return fmt.Errorf("storage key %q must be relative", key)
	}
	if strings.Contains(key, "//") {
		return fmt.Errorf("storage key %q contains duplicate slashes", key)
	}
	if key != path.Clean(key) {
		return fmt.Errorf("storage key %q must be clean", key)
	}
	if hasUnsafeSegments(key) {
		return fmt.Errorf("storage key %q contains unsafe path segments", key)
	}
	if decoded, err := url.PathUnescape(key); err == nil && decoded != key {
		if strings.Contains(decoded, `\`) || strings.HasPrefix(decoded, "/") || hasUnsafeSegments(decoded) {
			return fmt.Errorf("storage key %q contains escaped unsafe path segments", key)
		}
	}
	return nil
}

func containsASCIIControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func hasUnsafeSegments(key string) bool {
	for _, seg := range strings.Split(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return true
		}
	}
	return false
}

// ParseImagePaths accepts historical image_paths forms: JSON arrays,
// comma-separated strings, and single path strings.
func ParseImagePaths(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, "[") {
		var arr []string
		if err := json.Unmarshal([]byte(raw), &arr); err == nil {
			out := make([]string, 0, len(arr))
			for _, p := range arr {
				p = strings.TrimSpace(p)
				if p != "" {
					out = append(out, p)
				}
			}
			return out
		}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// LegacyReceiptImageRef describes a legacy receipt image path normalized to
// a canonical storage key.
type LegacyReceiptImageRef struct {
	Kind       string
	PageNumber int
	StorageKey string
	LegacyPath string
	Ext        string
}

// NormalizeLegacyReceiptImageReference converts absolute, DATA_DIR-relative,
// receipt-relative, and canonical receipt image references to canonical keys.
func NormalizeLegacyReceiptImageReference(dataDir, receiptID, raw string) (LegacyReceiptImageRef, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return LegacyReceiptImageRef{}, errors.New("empty receipt image reference")
	}
	slashed := filepath.ToSlash(raw)
	if idx := strings.LastIndex(slashed, "receipts/"); idx >= 0 {
		slashed = slashed[idx:]
	}
	slashed = strings.TrimLeft(slashed, "/")
	if !strings.HasPrefix(slashed, "receipts/") {
		slashed = "receipts/" + strings.TrimLeft(slashed, "/")
	}

	ref, err := canonicalizeReceiptImageKey(receiptID, slashed)
	if err != nil {
		return LegacyReceiptImageRef{}, err
	}
	if filepath.IsAbs(raw) {
		ref.LegacyPath = raw
	} else {
		ref.LegacyPath = slashed
	}

	if filepath.IsAbs(raw) && dataDir != "" {
		local, err := NewLocal(dataDir)
		if err == nil {
			resolved, rerr := local.Path(ref.StorageKey)
			if rerr == nil {
				absRaw, _ := filepath.Abs(raw)
				absResolved, _ := filepath.Abs(resolved)
				if absRaw == absResolved {
					ref.LegacyPath = ""
				}
			}
		}
	}
	return ref, nil
}

func canonicalizeReceiptImageKey(receiptID, key string) (LegacyReceiptImageRef, error) {
	if err := ValidateKey(key); err != nil {
		return LegacyReceiptImageRef{}, err
	}
	parts := strings.Split(key, "/")
	if len(parts) < 3 || parts[0] != "receipts" {
		return LegacyReceiptImageRef{}, fmt.Errorf("not a receipt image key: %q", key)
	}
	if receiptID != "" && parts[1] != receiptID {
		return LegacyReceiptImageRef{}, fmt.Errorf("receipt image key %q does not belong to receipt %q", key, receiptID)
	}
	receiptID = parts[1]
	if len(parts) == 4 && (parts[2] == ReceiptImageKindOriginal || parts[2] == ReceiptImageKindProcessed) {
		page, ext, err := ParsePageFilename(parts[3])
		if err != nil {
			return LegacyReceiptImageRef{}, err
		}
		return LegacyReceiptImageRef{
			Kind:       parts[2],
			PageNumber: page,
			StorageKey: key,
			Ext:        ext,
		}, nil
	}
	if len(parts) != 3 {
		return LegacyReceiptImageRef{}, fmt.Errorf("unsupported receipt image key shape: %q", key)
	}

	name := parts[2]
	kind := ReceiptImageKindOriginal
	if strings.HasPrefix(name, "processed_") {
		kind = ReceiptImageKindProcessed
		name = strings.TrimPrefix(name, "processed_")
	}
	page, ext, err := ParsePageFilename(name)
	if err != nil {
		return LegacyReceiptImageRef{}, err
	}
	var canonical string
	if kind == ReceiptImageKindOriginal {
		canonical, err = ReceiptOriginalKey(receiptID, page, ext)
	} else {
		canonical, err = ReceiptProcessedKey(receiptID, page, ext)
	}
	if err != nil {
		return LegacyReceiptImageRef{}, err
	}
	return LegacyReceiptImageRef{
		Kind:       kind,
		PageNumber: page,
		StorageKey: canonical,
		Ext:        ext,
	}, nil
}

// ParsePageFilename extracts page number and extension from "1.jpg".
func ParsePageFilename(name string) (int, string, error) {
	ext := strings.ToLower(filepath.Ext(name))
	if ext == "" {
		return 0, "", fmt.Errorf("image filename %q has no extension", name)
	}
	switch ext {
	case ".jpg", ".jpeg", ".png":
	default:
		return 0, "", fmt.Errorf("unsupported image extension %q", ext)
	}
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	page, err := strconv.Atoi(stem)
	if err != nil || page <= 0 {
		return 0, "", fmt.Errorf("image filename %q does not contain a positive page number", name)
	}
	return page, ext, nil
}

// SHA256Hex returns the lowercase SHA256 digest of data.
func SHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
