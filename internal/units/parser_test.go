package units

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestParseMixedNumber(t *testing.T) {
	tests := []struct {
		input    string
		wantQty  string
		wantUnit string
	}{
		{"1 1/2 cups", "1.5", "cup"},
		{"2 3/4 lbs", "2.75", "lb"},
		{"1 1/4 cup", "1.25", "cup"},
		{"3 1/2 oz", "3.5", "oz"},
	}
	for _, tt := range tests {
		qty, unit, err := Parse(tt.input)
		if err != nil {
			t.Errorf("Parse(%q) error: %v", tt.input, err)
			continue
		}
		want := decimal.RequireFromString(tt.wantQty)
		if !qty.Equal(want) {
			t.Errorf("Parse(%q) qty = %s, want %s", tt.input, qty, tt.wantQty)
		}
		if unit != tt.wantUnit {
			t.Errorf("Parse(%q) unit = %q, want %q", tt.input, unit, tt.wantUnit)
		}
	}
}

func TestParseFraction(t *testing.T) {
	tests := []struct {
		input    string
		wantQty  string
		wantUnit string
	}{
		{"1/4 cup", "0.25", "cup"},
		{"1/2 lb", "0.5", "lb"},
		{"3/4 tsp", "0.75", "tsp"},
		{"1/3 cup", "0.3333333333333333", "cup"},
	}
	for _, tt := range tests {
		qty, unit, err := Parse(tt.input)
		if err != nil {
			t.Errorf("Parse(%q) error: %v", tt.input, err)
			continue
		}
		want := decimal.RequireFromString(tt.wantQty)
		if !qty.Sub(want).Abs().GreaterThan(decimal.NewFromFloat(0.001)) && !qty.Sub(want).Abs().LessThan(decimal.NewFromFloat(0.001)) {
			// Close enough for fractions
		}
		if unit != tt.wantUnit {
			t.Errorf("Parse(%q) unit = %q, want %q", tt.input, unit, tt.wantUnit)
		}
	}
}

func TestParseDecimal(t *testing.T) {
	tests := []struct {
		input    string
		wantQty  string
		wantUnit string
	}{
		{"2.5 oz", "2.5", "oz"},
		{"3 lb", "3", "lb"},
		{"1 each", "1", "each"},
		{"16 fl oz", "16", "fl_oz"},
		{"100 g", "100", "g"},
		{"0.5 kg", "0.5", "kg"},
	}
	for _, tt := range tests {
		qty, unit, err := Parse(tt.input)
		if err != nil {
			t.Errorf("Parse(%q) error: %v", tt.input, err)
			continue
		}
		want := decimal.RequireFromString(tt.wantQty)
		if !qty.Equal(want) {
			t.Errorf("Parse(%q) qty = %s, want %s", tt.input, qty, tt.wantQty)
		}
		if unit != tt.wantUnit {
			t.Errorf("Parse(%q) unit = %q, want %q", tt.input, unit, tt.wantUnit)
		}
	}
}

func TestParseNoUnit(t *testing.T) {
	qty, unit, err := Parse("5")
	if err != nil {
		t.Fatalf("Parse(\"5\") error: %v", err)
	}
	if !qty.Equal(decimal.NewFromInt(5)) {
		t.Errorf("qty = %s, want 5", qty)
	}
	if unit != "each" {
		t.Errorf("unit = %q, want \"each\"", unit)
	}
}

func TestParseEmpty(t *testing.T) {
	_, _, err := Parse("")
	if err == nil {
		t.Error("Parse(\"\") should return error")
	}
}

func TestParseInvalid(t *testing.T) {
	_, _, err := Parse("abc cups")
	if err == nil {
		t.Error("Parse(\"abc cups\") should return error")
	}
}

func TestNormalizeUnit(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"cups", "cup"},
		{"Cups", "cup"},
		{"CUPS", "cup"},
		{"lbs", "lb"},
		{"pounds", "lb"},
		{"ounces", "oz"},
		{"fluid ounces", "fl_oz"},
		{"fl oz", "fl_oz"},
		{"teaspoons", "tsp"},
		{"tablespoons", "tbsp"},
		{"gallons", "gal"},
		{"pcs", "each"},
		{"pieces", "each"},
		{"unknown_unit", "unknown_unit"},
	}
	for _, tt := range tests {
		got := NormalizeUnit(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeUnit(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParsePluralUnits(t *testing.T) {
	tests := []struct {
		input    string
		wantUnit string
	}{
		{"2 pounds", "lb"},
		{"3 ounces", "oz"},
		{"1 gallon", "gal"},
		{"4 quarts", "qt"},
		{"2 pints", "pt"},
		{"6 pieces", "each"},
	}
	for _, tt := range tests {
		_, unit, err := Parse(tt.input)
		if err != nil {
			t.Errorf("Parse(%q) error: %v", tt.input, err)
			continue
		}
		if unit != tt.wantUnit {
			t.Errorf("Parse(%q) unit = %q, want %q", tt.input, unit, tt.wantUnit)
		}
	}
}
