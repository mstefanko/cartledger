package units

import (
	"fmt"
	"math/big"
	"regexp"
	"strings"

	"github.com/shopspring/decimal"
)

// Unit name normalization map: plural/alternate forms to canonical form.
var unitAliases = map[string]string{
	// Weight
	"pounds": "lb", "pound": "lb", "lbs": "lb", "lb": "lb",
	"ounces": "oz", "ounce": "oz", "oz": "oz",
	"kilograms": "kg", "kilogram": "kg", "kgs": "kg", "kg": "kg",
	"grams": "g", "gram": "g", "g": "g",

	// Volume
	"gallons": "gal", "gallon": "gal", "gal": "gal",
	"quarts": "qt", "quart": "qt", "qt": "qt",
	"pints": "pt", "pint": "pt", "pt": "pt",
	"cups": "cup", "cup": "cup",
	"tablespoons": "tbsp", "tablespoon": "tbsp", "tbsp": "tbsp", "tbs": "tbsp",
	"teaspoons": "tsp", "teaspoon": "tsp", "tsp": "tsp",
	"fluid ounces": "fl_oz", "fluid ounce": "fl_oz", "fl oz": "fl_oz", "fl_oz": "fl_oz", "floz": "fl_oz",
	"liters": "l", "liter": "l", "litres": "l", "litre": "l", "l": "l",
	"milliliters": "ml", "milliliter": "ml", "ml": "ml",

	// Count
	"each": "each", "ea": "each", "ct": "each", "count": "each", "pc": "each", "pcs": "each", "piece": "each", "pieces": "each",
}

// Regex patterns for parsing quantity strings, ordered by specificity.
var (
	mixedNumberRe = regexp.MustCompile(`^(\d+)\s+(\d+)/(\d+)\s*(.*)$`)
	fractionRe    = regexp.MustCompile(`^(\d+)/(\d+)\s*(.*)$`)
	decimalRe     = regexp.MustCompile(`^(\d+\.?\d*)\s*(.*)$`)
)

// Parse extracts a quantity and unit from a string like "1 1/2 cups", "3 lb",
// "1/4 cup", or "2.5 oz". Returns the numeric quantity, the normalized unit
// name, and any error.
func Parse(s string) (decimal.Decimal, string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return decimal.Zero, "", fmt.Errorf("empty input")
	}

	var qty decimal.Decimal
	var unitStr string

	// Try mixed number first: "1 1/2 cups"
	if m := mixedNumberRe.FindStringSubmatch(s); m != nil {
		whole := new(big.Rat)
		whole.SetString(m[1])
		frac := new(big.Rat)
		frac.SetFrac(
			new(big.Int).SetUint64(parseUint(m[2])),
			new(big.Int).SetUint64(parseUint(m[3])),
		)
		whole.Add(whole, frac)
		qty = ratToDecimal(whole)
		unitStr = m[4]
	} else if m := fractionRe.FindStringSubmatch(s); m != nil {
		// Fraction: "1/4 cup"
		frac := new(big.Rat)
		frac.SetFrac(
			new(big.Int).SetUint64(parseUint(m[1])),
			new(big.Int).SetUint64(parseUint(m[2])),
		)
		qty = ratToDecimal(frac)
		unitStr = m[3]
	} else if m := decimalRe.FindStringSubmatch(s); m != nil {
		// Decimal: "2.5 oz"
		var err error
		qty, err = decimal.NewFromString(m[1])
		if err != nil {
			return decimal.Zero, "", fmt.Errorf("invalid number %q: %w", m[1], err)
		}
		unitStr = m[2]
	} else {
		return decimal.Zero, "", fmt.Errorf("cannot parse quantity from %q", s)
	}

	unit := NormalizeUnit(strings.TrimSpace(unitStr))
	if unit == "" {
		unit = "each"
	}

	return qty, unit, nil
}

// NormalizeUnit converts a unit string to its canonical form.
func NormalizeUnit(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if canonical, ok := unitAliases[s]; ok {
		return canonical
	}
	return s
}

// parseUint is a helper that converts a string to uint64. Panics on failure
// since it is only called with regex-validated digit strings.
func parseUint(s string) uint64 {
	var n uint64
	for _, c := range s {
		n = n*10 + uint64(c-'0')
	}
	return n
}

// ratToDecimal converts a big.Rat to a shopspring decimal with up to 10
// digits of precision.
func ratToDecimal(r *big.Rat) decimal.Decimal {
	// Use float string representation with high precision.
	f, _ := r.Float64()
	return decimal.NewFromFloat(f)
}
