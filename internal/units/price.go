package units

import (
	"fmt"

	"github.com/shopspring/decimal"
)

// NormalizePrice calculates the price per standard unit (per oz for weight,
// per fl_oz for volume, per each for count). It uses Convert() internally,
// which checks product-specific overrides in the database.
//
// The db parameter accepts both *sql.DB and *sql.Tx via the Querier interface,
// so callers within a transaction can pass the tx to avoid reading stale data.
//
// Returns the normalized price per standard unit and the standard unit name.
func NormalizePrice(totalPrice, quantity decimal.Decimal, unit string, productID string, db Querier) (decimal.Decimal, string, error) {
	unit = NormalizeUnit(unit)

	if quantity.IsZero() {
		return decimal.Zero, "", fmt.Errorf("quantity cannot be zero")
	}

	stdUnit := StandardUnit(unit)
	cat := Classify(unit)

	if cat == CategoryUnknown {
		// Cannot normalize unknown units; return price per original unit.
		pricePerUnit := totalPrice.Div(quantity)
		return pricePerUnit, unit, nil
	}

	// Convert quantity to standard units.
	stdQty, err := Convert(quantity, unit, stdUnit, productID, db)
	if err != nil {
		return decimal.Zero, "", fmt.Errorf("conversion failed: %w", err)
	}

	if stdQty.IsZero() {
		return decimal.Zero, "", fmt.Errorf("converted quantity is zero")
	}

	pricePerStdUnit := totalPrice.Div(stdQty)
	return pricePerStdUnit, stdUnit, nil
}
