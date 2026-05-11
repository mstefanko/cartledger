package matcher

import "testing"

func TestClassifyStore(t *testing.T) {
	tests := []struct {
		name string
		want Chain
	}{
		{"Costco Wholesale #749", ChainCostco},
		{"COSTCO", ChainCostco},
		{"ShopRite of Hoboken", ChainShopRite},
		{"Shop Rite", ChainShopRite},
		{"Kroger Marketplace", ChainKroger},
		{"Walmart Supercenter", ChainWalmart},
		{"Target", ChainTarget},
		{"Neighborhood Grocery", ChainOther},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyStore(tc.name); got != tc.want {
				t.Fatalf("ClassifyStore(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestParseLineCostco(t *testing.T) {
	tests := []struct {
		raw  string
		code string
		desc string
	}{
		{"8 2% MILK 1GAL", "8", "2% MILK 1GAL"},
		{"1407506 LC SOFT TACO", "1407506", "LC SOFT TACO"},
		{"2023427 RAO'S 2/31.7", "2023427", "RAO'S 2/31.7"},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			got := ParseLine(tc.raw, ChainCostco)
			if got.StoreItemCode != tc.code || got.ReceiptDescription != tc.desc {
				t.Fatalf("ParseLine(%q) = %+v, want code=%q desc=%q", tc.raw, got, tc.code, tc.desc)
			}
		})
	}
}

func TestParseLineNonCostcoDoesNotStripLeadingDigits(t *testing.T) {
	got := ParseLine("3 LBS BANANAS", ChainOther)
	if got.StoreItemCode != "" || got.ReceiptDescription != "" {
		t.Fatalf("ParseLine non-Costco = %+v, want empty", got)
	}
}

func TestParseLineCostcoRejectsMultilineInput(t *testing.T) {
	got := ParseLine("8 2% MILK 1GAL\nTOTAL 2.92", ChainCostco)
	if got.StoreItemCode != "" || got.ReceiptDescription != "" {
		t.Fatalf("ParseLine multiline = %+v, want empty", got)
	}
}
