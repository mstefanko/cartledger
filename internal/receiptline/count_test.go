package receiptline

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestCountContribution(t *testing.T) {
	tests := []struct {
		name     string
		quantity string
		unit     *string
		want     string
	}{
		{"whole each", "3", strPtr("each"), "3"},
		{"blank unit counts", "2", nil, "2"},
		{"count unit", "12", strPtr("ct"), "12"},
		{"weighted unit defaults one", "1.24", strPtr("lb"), "1"},
		{"fractional each defaults one", "1.5", strPtr("each"), "1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CountContribution(decimal.RequireFromString(tc.quantity), tc.unit)
			if got.String() != tc.want {
				t.Fatalf("CountContribution(%s, %v) = %s, want %s", tc.quantity, tc.unit, got, tc.want)
			}
		})
	}
}

func strPtr(s string) *string { return &s }
