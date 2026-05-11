package prices

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestNormalizeLineItemPrice(t *testing.T) {
	tests := []struct {
		name           string
		total          string
		lineQty        string
		lineUnit       *string
		packQty        *decimal.Decimal
		packUnit       *string
		overrideQty    *decimal.Decimal
		overrideUnit   *string
		wantUnitPrice  string
		wantNormalized string
		wantUnit       *string
		wantSource     string
	}{
		{
			name:           "weight receipt unit normalizes directly",
			total:          "5.82",
			lineQty:        "2.34",
			lineUnit:       strPtr("lb"),
			wantUnitPrice:  "2.4871794871794872",
			wantNormalized: "0.1554487179487179",
			wantUnit:       strPtr("oz"),
			wantSource:     SourceLineUnit,
		},
		{
			name:           "volume receipt unit normalizes directly",
			total:          "4.00",
			lineQty:        "1",
			lineUnit:       strPtr("gal"),
			wantUnitPrice:  "4",
			wantNormalized: "0.03125",
			wantUnit:       strPtr("fl_oz"),
			wantSource:     SourceLineUnit,
		},
		{
			name:           "count receipt unit uses product package size",
			total:          "8.99",
			lineQty:        "1",
			lineUnit:       strPtr("each"),
			packQty:        decPtr("32"),
			packUnit:       strPtr("oz"),
			wantUnitPrice:  "8.99",
			wantNormalized: "0.2809375",
			wantUnit:       strPtr("oz"),
			wantSource:     SourceProductPack,
		},
		{
			name:           "line quantity multiplies product package size",
			total:          "3.00",
			lineQty:        "2",
			lineUnit:       strPtr("each"),
			packQty:        decPtr("5.3"),
			packUnit:       strPtr("oz"),
			wantUnitPrice:  "1.5",
			wantNormalized: "0.2830188679245283",
			wantUnit:       strPtr("oz"),
			wantSource:     SourceProductPack,
		},
		{
			name:           "line override beats product package size",
			total:          "6.00",
			lineQty:        "1",
			lineUnit:       strPtr("each"),
			packQty:        decPtr("12"),
			packUnit:       strPtr("oz"),
			overrideQty:    decPtr("24"),
			overrideUnit:   strPtr("oz"),
			wantUnitPrice:  "6",
			wantNormalized: "0.25",
			wantUnit:       strPtr("oz"),
			wantSource:     SourceLineOverride,
		},
		{
			name:          "missing product package leaves normalized null",
			total:         "8.99",
			lineQty:       "1",
			lineUnit:      strPtr("each"),
			wantUnitPrice: "8.99",
			wantSource:    SourceMissingPack,
		},
		{
			name:          "unknown units do not normalize",
			total:         "10.00",
			lineQty:       "2",
			lineUnit:      strPtr("bundle"),
			wantUnitPrice: "5",
			wantSource:    SourceMissingPack,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			total := mustDec(t, tt.total)
			lineQty := mustDec(t, tt.lineQty)
			got, err := NormalizeLineItemPrice(total, lineQty, tt.lineUnit, tt.packQty, tt.packUnit, tt.overrideQty, tt.overrideUnit)
			if err != nil {
				t.Fatalf("NormalizeLineItemPrice error: %v", err)
			}
			if !got.UnitPrice.Equal(mustDec(t, tt.wantUnitPrice)) {
				t.Fatalf("UnitPrice = %s, want %s", got.UnitPrice, tt.wantUnitPrice)
			}
			if got.Source != tt.wantSource {
				t.Fatalf("Source = %q, want %q", got.Source, tt.wantSource)
			}
			if tt.wantUnit == nil {
				if got.NormalizedPrice != nil || got.NormalizedUnit != nil {
					t.Fatalf("normalized = %v/%v, want nil", got.NormalizedPrice, got.NormalizedUnit)
				}
				return
			}
			if got.NormalizedPrice == nil || !got.NormalizedPrice.Equal(mustDec(t, tt.wantNormalized)) {
				t.Fatalf("NormalizedPrice = %v, want %s", got.NormalizedPrice, tt.wantNormalized)
			}
			if got.NormalizedUnit == nil || *got.NormalizedUnit != *tt.wantUnit {
				t.Fatalf("NormalizedUnit = %v, want %s", got.NormalizedUnit, *tt.wantUnit)
			}
		})
	}
}

func decPtr(s string) *decimal.Decimal {
	d, err := decimal.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return &d
}

func strPtr(s string) *string {
	return &s
}

func mustDec(t *testing.T, s string) decimal.Decimal {
	t.Helper()
	d, err := decimal.NewFromString(s)
	if err != nil {
		t.Fatalf("decimal %q: %v", s, err)
	}
	return d
}
