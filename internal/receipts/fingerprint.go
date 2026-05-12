package receipts

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strconv"
	"strings"
)

func SourceFingerprint(pageSHA256 []string) string {
	cleaned := make([]string, 0, len(pageSHA256))
	for _, value := range pageSHA256 {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			cleaned = append(cleaned, value)
		}
	}
	if len(cleaned) == 0 {
		return ""
	}
	h := sha256.New()
	_, _ = io.WriteString(h, "cartledger:receipt-source-fingerprint:v1\n")
	for i, value := range cleaned {
		_, _ = io.WriteString(h, strconv.Itoa(i+1))
		_, _ = io.WriteString(h, ":")
		_, _ = io.WriteString(h, value)
		_, _ = io.WriteString(h, "\n")
	}
	return hex.EncodeToString(h.Sum(nil))
}
