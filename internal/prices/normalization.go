package prices

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/mstefanko/cartledger/internal/units"
)

const (
	SourceLineUnit     = "line_unit"
	SourceLineOverride = "line_override"
	SourceProductPack  = "product_pack"
	SourceMissingPack  = "missing_pack"
)

type NormalizedLinePrice struct {
	UnitPrice       decimal.Decimal
	NormalizedPrice *decimal.Decimal
	NormalizedUnit  *string
	Source          string
}

func NormalizeLineItemPrice(
	totalPrice decimal.Decimal,
	lineQuantity decimal.Decimal,
	lineUnit *string,
	productPackQuantity *decimal.Decimal,
	productPackUnit *string,
	linePackOverrideQuantity *decimal.Decimal,
	linePackOverrideUnit *string,
) (NormalizedLinePrice, error) {
	if lineQuantity.IsNegative() {
		return NormalizedLinePrice{}, fmt.Errorf("line quantity cannot be negative")
	}
	if lineQuantity.IsZero() {
		lineQuantity = decimal.NewFromInt(1)
	}

	result := NormalizedLinePrice{
		UnitPrice: totalPrice.Div(lineQuantity),
		Source:    SourceMissingPack,
	}

	if unit := normalizedUnitValue(lineUnit); unit != "" {
		switch units.Classify(unit) {
		case units.CategoryWeight, units.CategoryVolume:
			if normalized, ok := normalizeContent(result, totalPrice, lineQuantity, unit, SourceLineUnit); ok {
				return normalized, nil
			}
		}
	}

	if linePackOverrideQuantity != nil && linePackOverrideUnit != nil {
		if normalized, ok := normalizePackQuantity(totalPrice, lineQuantity, *linePackOverrideQuantity, *linePackOverrideUnit, SourceLineOverride); ok {
			normalized.UnitPrice = result.UnitPrice
			return normalized, nil
		}
	}

	if productPackQuantity != nil && productPackUnit != nil {
		if normalized, ok := normalizePackQuantity(totalPrice, lineQuantity, *productPackQuantity, *productPackUnit, SourceProductPack); ok {
			normalized.UnitPrice = result.UnitPrice
			return normalized, nil
		}
	}

	return result, nil
}

func normalizePackQuantity(totalPrice, lineQuantity, packQuantity decimal.Decimal, packUnit string, source string) (NormalizedLinePrice, bool) {
	if !packQuantity.GreaterThan(decimal.Zero) {
		return NormalizedLinePrice{}, false
	}
	unit := units.NormalizeUnit(packUnit)
	if strings.TrimSpace(unit) == "" || units.Classify(unit) == units.CategoryUnknown {
		return NormalizedLinePrice{}, false
	}
	return normalizeContent(NormalizedLinePrice{}, totalPrice, lineQuantity.Mul(packQuantity), unit, source)
}

func normalizeContent(result NormalizedLinePrice, totalPrice, quantity decimal.Decimal, unit string, source string) (NormalizedLinePrice, bool) {
	if !quantity.GreaterThan(decimal.Zero) {
		return result, false
	}
	stdUnit := units.StandardUnit(unit)
	stdQty, err := units.Convert(quantity, unit, stdUnit, "", nil)
	if err != nil || stdQty.IsZero() {
		return result, false
	}
	normalizedPrice := totalPrice.Div(stdQty)
	result.NormalizedPrice = &normalizedPrice
	result.NormalizedUnit = &stdUnit
	result.Source = source
	return result, true
}

func normalizedUnitValue(unit *string) string {
	if unit == nil {
		return ""
	}
	return units.NormalizeUnit(strings.TrimSpace(*unit))
}
