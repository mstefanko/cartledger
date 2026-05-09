package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
		ok   bool
	}{
		{"canonical receipt", "receipts/r1/original/1.jpg", true},
		{"spaces", "products/product one/image one.jpg", true},
		{"absolute", "/data/receipts/r1/1.jpg", false},
		{"backslash", `receipts\r1\1.jpg`, false},
		{"traversal", "receipts/r1/../secret.jpg", false},
		{"encoded traversal", "receipts/r1/%2e%2e/secret.jpg", false},
		{"duplicate slashes", "receipts//r1/1.jpg", false},
		{"dot segment", "receipts/r1/./1.jpg", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateKey(tt.key)
			if tt.ok && err != nil {
				t.Fatalf("ValidateKey(%q): %v", tt.key, err)
			}
			if !tt.ok && err == nil {
				t.Fatalf("ValidateKey(%q) nil, want error", tt.key)
			}
		})
	}
}

func TestLocalPathStaysUnderRoot(t *testing.T) {
	root := t.TempDir()
	local, err := NewLocal(root)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	if _, err := local.Path("receipts/r1/original/1.jpg"); err != nil {
		t.Fatalf("valid key rejected: %v", err)
	}
	if _, err := local.Path("../escape.jpg"); err == nil {
		t.Fatalf("escape key accepted")
	}
}

func TestWriteFileAtomic(t *testing.T) {
	root := t.TempDir()
	local, err := NewLocal(root)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	if err := local.WriteFileAtomic("receipts/r1/original/1.jpg", []byte("img"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "receipts", "r1", "original", "1.jpg"))
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(got) != "img" {
		t.Fatalf("content = %q", got)
	}
}

func TestNormalizeLegacyReceiptImageReference(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		raw  string
		want string
	}{
		{"receipts/r1/1.jpg", "receipts/r1/original/1.jpg"},
		{"receipts/r1/processed_2.png", "receipts/r1/processed/2.png"},
		{filepath.Join(root, "receipts", "r1", "processed_3.jpg"), "receipts/r1/processed/3.jpg"},
		{"r1/4.jpg", "receipts/r1/original/4.jpg"},
	}
	for _, tt := range tests {
		ref, err := NormalizeLegacyReceiptImageReference(root, "r1", tt.raw)
		if err != nil {
			t.Fatalf("NormalizeLegacyReceiptImageReference(%q): %v", tt.raw, err)
		}
		if ref.StorageKey != tt.want {
			t.Fatalf("NormalizeLegacyReceiptImageReference(%q) = %q want %q", tt.raw, ref.StorageKey, tt.want)
		}
	}
	if _, err := NormalizeLegacyReceiptImageReference(root, "r1", "receipts/other/1.jpg"); err == nil {
		t.Fatalf("cross-receipt path accepted")
	}
	if _, err := NormalizeLegacyReceiptImageReference(root, "r1", "receipts/r1/%2e%2e/1.jpg"); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("encoded traversal err = %v", err)
	}
}
