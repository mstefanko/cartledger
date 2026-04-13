package matcher

import (
	"strings"
	"unicode"
)

// Normalize lowercases, strips punctuation, collapses whitespace, and trims a string.
func Normalize(s string) string {
	s = strings.ToLower(s)

	// Replace punctuation with spaces.
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsPunct(r) || unicode.IsSymbol(r) {
			b.WriteRune(' ')
		} else {
			b.WriteRune(r)
		}
	}
	s = b.String()

	// Collapse whitespace and trim.
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}
