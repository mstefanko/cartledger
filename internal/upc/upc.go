package upc

import (
	"fmt"
	"strings"
)

func Normalize(value string) (string, error) {
	var b strings.Builder
	for _, r := range strings.TrimSpace(value) {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" {
		return "", nil
	}
	if len(out) < 8 || len(out) > 14 {
		return "", fmt.Errorf("upc must contain 8 to 14 digits")
	}
	return out, nil
}

func NormalizePointer(value *string) *string {
	if value == nil {
		return nil
	}
	normalized, err := Normalize(*value)
	if err != nil || normalized == "" {
		return nil
	}
	return &normalized
}
