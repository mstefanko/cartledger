package matcher

import (
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

var titleCaser = cases.Title(language.English)

var brandAbbrevs = map[string]string{
	"ks":  "Kirkland Signature",
	"gv":  "Great Value",
	"g&g": "Good & Gather",
	"mp":  "Market Pantry",
}

var brandOverrides = map[string]string{
	"heb":       "HEB",
	"h-e-b":     "H-E-B",
	"mccormick": "McCormick",
}

// NormalizeBrand standardizes a brand name using abbreviation expansion,
// known overrides, and title-casing.
func NormalizeBrand(brand string) string {
	brand = strings.TrimSpace(brand)
	if brand == "" {
		return ""
	}
	brand = strings.Join(strings.Fields(brand), " ")
	lower := strings.ToLower(brand)
	if expanded, ok := brandAbbrevs[lower]; ok {
		return expanded
	}
	if override, ok := brandOverrides[lower]; ok {
		return override
	}
	return titleCaser.String(lower)
}
