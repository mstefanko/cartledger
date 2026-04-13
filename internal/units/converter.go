package units

import (
	"database/sql"
	"fmt"

	"github.com/shopspring/decimal"
)

// Standard base units for each measurement category.
const (
	StandardWeight = "oz"
	StandardVolume = "fl_oz"
	StandardCount  = "each"
)

// UnitCategory classifies a unit into weight, volume, or count.
type UnitCategory string

const (
	CategoryWeight  UnitCategory = "weight"
	CategoryVolume  UnitCategory = "volume"
	CategoryCount   UnitCategory = "count"
	CategoryUnknown UnitCategory = "unknown"
)

// Built-in conversion factors to standard units.
// Weight units convert to oz; volume units convert to fl_oz.
var weightToOz = map[string]decimal.Decimal{
	"oz": decimal.NewFromInt(1),
	"lb": decimal.NewFromInt(16),
	"kg": decimal.NewFromFloat(35.274),
	"g":  decimal.NewFromFloat(0.03527),
}

var volumeToFlOz = map[string]decimal.Decimal{
	"fl_oz": decimal.NewFromInt(1),
	"gal":   decimal.NewFromInt(128),
	"qt":    decimal.NewFromInt(32),
	"pt":    decimal.NewFromInt(16),
	"cup":   decimal.NewFromInt(8),
	"tbsp":  decimal.NewFromFloat(0.5),
	"tsp":   decimal.NewFromFloat(0.167),
	"l":     decimal.NewFromFloat(33.814),
	"ml":    decimal.NewFromFloat(0.03381),
}

// Food-specific density table: product name -> oz per cup.
// Used to convert between weight and volume for common foods.
var foodDensity = map[string]decimal.Decimal{
	"flour":         decimal.NewFromFloat(4.25),
	"sugar":         decimal.NewFromFloat(7.05),
	"brown sugar":   decimal.NewFromFloat(7.7),
	"butter":        decimal.NewFromFloat(8.0),
	"milk":          decimal.NewFromFloat(8.6),
	"water":         decimal.NewFromFloat(8.345),
	"honey":         decimal.NewFromFloat(12.0),
	"oil":           decimal.NewFromFloat(7.7),
	"olive oil":     decimal.NewFromFloat(7.7),
	"vegetable oil": decimal.NewFromFloat(7.7),
	"rice":          decimal.NewFromFloat(7.05),
	"oats":          decimal.NewFromFloat(3.0),
	"salt":          decimal.NewFromFloat(10.0),
	"cocoa powder":  decimal.NewFromFloat(3.0),
}

// Classify returns the measurement category for a normalized unit.
func Classify(unit string) UnitCategory {
	if _, ok := weightToOz[unit]; ok {
		return CategoryWeight
	}
	if _, ok := volumeToFlOz[unit]; ok {
		return CategoryVolume
	}
	if unit == "each" {
		return CategoryCount
	}
	return CategoryUnknown
}

// StandardUnit returns the standard base unit for a given unit.
func StandardUnit(unit string) string {
	switch Classify(unit) {
	case CategoryWeight:
		return StandardWeight
	case CategoryVolume:
		return StandardVolume
	case CategoryCount:
		return StandardCount
	default:
		return unit
	}
}

// Convert transforms a quantity from one unit to another. It checks the
// database for product-specific overrides first (via the unit_conversions
// table), then falls back to built-in conversion tables.
//
// If db is nil, only built-in conversions are used.
func Convert(qty decimal.Decimal, fromUnit, toUnit string, productID string, db *sql.DB) (decimal.Decimal, error) {
	fromUnit = NormalizeUnit(fromUnit)
	toUnit = NormalizeUnit(toUnit)

	if fromUnit == toUnit {
		return qty, nil
	}

	// Check database for product-specific or generic conversions.
	if db != nil {
		factor, err := lookupConversion(db, fromUnit, toUnit, productID)
		if err == nil {
			return qty.Mul(factor), nil
		}
		// Also check the reverse direction.
		factor, err = lookupConversion(db, toUnit, fromUnit, productID)
		if err == nil {
			return qty.Div(factor), nil
		}
	}

	// Built-in same-category conversions.
	fromCat := Classify(fromUnit)
	toCat := Classify(toUnit)

	if fromCat == toCat && fromCat != CategoryUnknown {
		switch fromCat {
		case CategoryWeight:
			fromFactor := weightToOz[fromUnit]
			toFactor := weightToOz[toUnit]
			// qty * fromFactor / toFactor
			return qty.Mul(fromFactor).Div(toFactor), nil
		case CategoryVolume:
			fromFactor := volumeToFlOz[fromUnit]
			toFactor := volumeToFlOz[toUnit]
			return qty.Mul(fromFactor).Div(toFactor), nil
		case CategoryCount:
			return qty, nil
		}
	}

	return decimal.Zero, fmt.Errorf("cannot convert from %s to %s", fromUnit, toUnit)
}

// lookupConversion checks the unit_conversions table for a matching factor.
// It first checks product-specific conversions, then generic (NULL product_id).
func lookupConversion(db *sql.DB, fromUnit, toUnit, productID string) (decimal.Decimal, error) {
	var factorStr string

	// Product-specific conversion first.
	if productID != "" {
		err := db.QueryRow(
			`SELECT factor FROM unit_conversions
			 WHERE from_unit = ? AND to_unit = ? AND product_id = ?`,
			fromUnit, toUnit, productID,
		).Scan(&factorStr)
		if err == nil {
			return decimal.RequireFromString(factorStr), nil
		}
	}

	// Generic conversion (product_id IS NULL).
	err := db.QueryRow(
		`SELECT factor FROM unit_conversions
		 WHERE from_unit = ? AND to_unit = ? AND product_id IS NULL`,
		fromUnit, toUnit,
	).Scan(&factorStr)
	if err == nil {
		return decimal.RequireFromString(factorStr), nil
	}

	return decimal.Zero, fmt.Errorf("no conversion found")
}
