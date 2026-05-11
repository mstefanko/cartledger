package receiptline

import (
	"strings"

	"github.com/shopspring/decimal"
)

func CountContribution(quantity decimal.Decimal, unit *string) decimal.Decimal {
	unitStr := ""
	if unit != nil {
		unitStr = strings.ToLower(strings.TrimSpace(*unit))
	}
	switch unitStr {
	case "", "each", "ea", "pack", "ct", "count", "gal", "qt", "pt":
		if quantity.GreaterThan(decimal.Zero) && quantity.Equal(quantity.Round(0)) {
			return quantity
		}
	}
	return decimal.NewFromInt(1)
}

func CountContributionFloat(quantity float64, unit *string) float64 {
	v, _ := CountContribution(decimal.NewFromFloat(quantity), unit).Float64()
	return v
}
