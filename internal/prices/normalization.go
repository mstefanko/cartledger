package prices

import (
	"context"
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
	return NormalizeLineItemPriceWithScope(
		context.Background(),
		nil,
		totalPrice,
		lineQuantity,
		lineUnit,
		productPackQuantity,
		productPackUnit,
		linePackOverrideQuantity,
		linePackOverrideUnit,
		nil,
		units.ConversionScope{},
	)
}

func NormalizeLineItemPriceWithScope(
	ctx context.Context,
	db units.Querier,
	totalPrice decimal.Decimal,
	lineQuantity decimal.Decimal,
	lineUnit *string,
	productPackQuantity *decimal.Decimal,
	productPackUnit *string,
	linePackOverrideQuantity *decimal.Decimal,
	linePackOverrideUnit *string,
	comparisonUnit *string,
	scope units.ConversionScope,
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
			if normalized, ok := normalizeContent(ctx, db, result, totalPrice, lineQuantity, unit, comparisonUnit, scope, SourceLineUnit); ok {
				return normalized, nil
			}
		}
	}

	if linePackOverrideQuantity != nil && linePackOverrideUnit != nil {
		if normalized, ok := normalizePackQuantity(ctx, db, totalPrice, lineQuantity, *linePackOverrideQuantity, *linePackOverrideUnit, comparisonUnit, scope, SourceLineOverride); ok {
			normalized.UnitPrice = result.UnitPrice
			return normalized, nil
		}
	}

	if productPackQuantity != nil && productPackUnit != nil {
		if normalized, ok := normalizePackQuantity(ctx, db, totalPrice, lineQuantity, *productPackQuantity, *productPackUnit, comparisonUnit, scope, SourceProductPack); ok {
			normalized.UnitPrice = result.UnitPrice
			return normalized, nil
		}
	}

	return result, nil
}

func normalizePackQuantity(ctx context.Context, db units.Querier, totalPrice, lineQuantity, packQuantity decimal.Decimal, packUnit string, comparisonUnit *string, scope units.ConversionScope, source string) (NormalizedLinePrice, bool) {
	if !packQuantity.GreaterThan(decimal.Zero) {
		return NormalizedLinePrice{}, false
	}
	unit := units.NormalizeUnit(packUnit)
	if strings.TrimSpace(unit) == "" || units.Classify(unit) == units.CategoryUnknown {
		return NormalizedLinePrice{}, false
	}
	return normalizeContent(ctx, db, NormalizedLinePrice{}, totalPrice, lineQuantity.Mul(packQuantity), unit, comparisonUnit, scope, source)
}

func normalizeContent(ctx context.Context, db units.Querier, result NormalizedLinePrice, totalPrice, quantity decimal.Decimal, unit string, comparisonUnit *string, scope units.ConversionScope, source string) (NormalizedLinePrice, bool) {
	if !quantity.GreaterThan(decimal.Zero) {
		return result, false
	}
	targetUnit := units.StandardUnit(unit)
	if comparison := normalizedUnitValue(comparisonUnit); comparison != "" {
		targetUnit = comparison
	}
	stdQty, err := units.ConvertScoped(ctx, db, quantity, unit, targetUnit, scope)
	if err != nil || stdQty.IsZero() {
		return result, false
	}
	normalizedPrice := totalPrice.Div(stdQty)
	result.NormalizedPrice = &normalizedPrice
	result.NormalizedUnit = &targetUnit
	result.Source = source
	return result, true
}

func normalizedUnitValue(unit *string) string {
	if unit == nil {
		return ""
	}
	return units.NormalizeUnit(strings.TrimSpace(*unit))
}
