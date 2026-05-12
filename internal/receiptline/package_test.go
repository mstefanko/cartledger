package receiptline

import "testing"

func TestParsePackageContent(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		wantQty  string
		wantUnit string
		wantOK   bool
	}{
		{name: "compact gallon", input: []string{"2% MILK 1GAL"}, wantQty: "1", wantUnit: "gal", wantOK: true},
		{name: "count with space", input: []string{"TORTILLAS 16 CT"}, wantQty: "16", wantUnit: "each", wantOK: true},
		{name: "decimal ounces", input: []string{"OLIVE OIL 31.7 oz"}, wantQty: "31.7", wantUnit: "oz", wantOK: true},
		{name: "multipack count", input: []string{"BAR 2 x 8 ct"}, wantQty: "16", wantUnit: "each", wantOK: true},
		{name: "slash multipack", input: []string{"BOTTLES 2/31.7 OZ"}, wantQty: "63.4", wantUnit: "oz", wantOK: true},
		{name: "fallback description", input: []string{"1407506 LC SOFT TACO", "12 OZ"}, wantQty: "12", wantUnit: "oz", wantOK: true},
		{name: "no package unit", input: []string{"123456 KS WATER 2x8"}, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParsePackageContent(tt.input...)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if got.Quantity.String() != tt.wantQty || got.Unit != tt.wantUnit {
				t.Fatalf("package = %s %s, want %s %s", got.Quantity, got.Unit, tt.wantQty, tt.wantUnit)
			}
			if got.Label == "" {
				t.Fatalf("Label is empty")
			}
		})
	}
}
