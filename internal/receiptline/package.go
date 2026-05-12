package receiptline

import (
	"regexp"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/mstefanko/cartledger/internal/units"
)

type PackageContent struct {
	Label    string
	Quantity decimal.Decimal
	Unit     string
}

var (
	packageUnitPattern = `fl\s*oz|fl_oz|floz|oz|lbs?|kg|g|ml|l|gal|qt|pt|ct|count|pk|pack|ea|each`
	multipackPattern   = regexp.MustCompile(`(?i)(^|[^[:alnum:]])([0-9]+(?:\.[0-9]+)?)\s*(?:x|/)\s*([0-9]+(?:\.[0-9]+)?)\s*(` + packageUnitPattern + `)\b`)
	simplePackPattern  = regexp.MustCompile(`(?i)(^|[^[:alnum:]])([0-9]+(?:\.[0-9]+)?)\s*(` + packageUnitPattern + `)\b`)
)

func ParsePackageContent(values ...string) (PackageContent, bool) {
	for _, value := range values {
		if parsed, ok := parsePackageContent(value); ok {
			return parsed, true
		}
	}
	return PackageContent{}, false
}

func parsePackageContent(value string) (PackageContent, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return PackageContent{}, false
	}

	if loc := multipackPattern.FindStringSubmatchIndex(value); loc != nil {
		left, errLeft := decimal.NewFromString(value[loc[4]:loc[5]])
		right, errRight := decimal.NewFromString(value[loc[6]:loc[7]])
		unit := canonicalPackageUnit(value[loc[8]:loc[9]])
		if errLeft == nil && errRight == nil && unit != "" {
			return PackageContent{
				Label:    strings.TrimSpace(value[loc[4]:loc[9]]),
				Quantity: left.Mul(right),
				Unit:     unit,
			}, true
		}
	}

	if loc := simplePackPattern.FindStringSubmatchIndex(value); loc != nil {
		qty, err := decimal.NewFromString(value[loc[4]:loc[5]])
		unit := canonicalPackageUnit(value[loc[6]:loc[7]])
		if err == nil && unit != "" {
			return PackageContent{
				Label:    strings.TrimSpace(value[loc[4]:loc[7]]),
				Quantity: qty,
				Unit:     unit,
			}, true
		}
	}

	return PackageContent{}, false
}

func canonicalPackageUnit(value string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(value), " "))
	switch normalized {
	case "fl oz", "fl_oz", "floz":
		return "fl_oz"
	case "ct", "count", "pk", "pack", "ea", "each":
		return "each"
	default:
		return units.NormalizeUnit(normalized)
	}
}
