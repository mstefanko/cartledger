package spreadsheet

import "testing"

func TestNormalizeMoney(t *testing.T) {
	cases := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"4.99", 499, false},
		{"$4.99", 499, false},
		{"1,234.56", 123456, false},
		{"$1,234.56", 123456, false},
		{"(1.23)", -123, false},
		{"-$1.23", -123, false},
		{"€1.23", 123, false},
		{"", 0, true},
		{"foo", 0, true},
	}
	for _, tc := range cases {
		got, err := NormalizeMoney(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("NormalizeMoney(%q) = %d, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeMoney(%q) error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("NormalizeMoney(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeDate(t *testing.T) {
	cases := []struct {
		in      string
		df      DateFormat
		want    string
		wantErr bool
	}{
		{"2026-03-12", DateFmtISO, "2026-03-12", false},
		{"03/12/2026", DateFmtUS, "2026-03-12", false},
		{"12/03/2026", DateFmtEU, "2026-03-12", false},
		{"2026/03/12", DateFmtISOSlash, "2026-03-12", false},
		// Excel serial sanity check: epoch is 1899-12-30, so serial 45729
		// yields 45729 days later. This is the value Excel writes for
		// "2025-03-13" on a modern (non-1904) workbook. The "Excel 1900
		// leap year bug" only shifts values <= 60; post-March-1-1900 serials
		// round-trip cleanly with a plain (epoch + days) calculation.
		{"45729", DateFmtExcelSerial, "2025-03-13", false},
		{"", DateFmtISO, "", true},
		{"not a date", DateFmtISO, "", true},
	}
	for _, tc := range cases {
		got, err := NormalizeDate(tc.in, tc.df)
		if tc.wantErr {
			if err == nil {
				t.Errorf("NormalizeDate(%q,%s) = %q, want error", tc.in, tc.df, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeDate(%q,%s) error: %v", tc.in, tc.df, err)
			continue
		}
		if got != tc.want {
			t.Errorf("NormalizeDate(%q,%s) = %q, want %q", tc.in, tc.df, got, tc.want)
		}
	}
}

func TestNormalizeStoreName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Whole Foods", "Whole Foods"},
		{"  Whole Foods  ", "Whole Foods"},
		{"Whole  Foods", "Whole Foods"},
		{"Whole Foods #123", "Whole Foods"},
		{"Costco#42", "Costco"},
		{"Aldi - #17", "Aldi"},
	}
	for _, tc := range cases {
		got := NormalizeStoreName(tc.in)
		if got != tc.want {
			t.Errorf("NormalizeStoreName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeQty(t *testing.T) {
	qty, unit, err := NormalizeQty("2.3 lb", "ea")
	if err != nil {
		t.Fatalf("NormalizeQty: %v", err)
	}
	if qty != 2.3 {
		t.Errorf("qty = %v, want 2.3", qty)
	}
	if unit != "lb" {
		t.Errorf("unit = %q, want lb", unit)
	}

	// Plain number with no unit — caller should fall back to default.
	qty, unit, err = NormalizeQty("1", "ea")
	if err != nil {
		t.Fatalf("NormalizeQty(1): %v", err)
	}
	if qty != 1.0 || unit != "" {
		t.Errorf("NormalizeQty(1) = (%v,%q), want (1.0,\"\")", qty, unit)
	}
}

func TestNormalizeRow_CleanArchetypeA(t *testing.T) {
	raw := RawRow{
		Index: 0,
		Cells: []string{"2026-03-12", "Whole Foods", "Bananas", "2.3", "lb", "0.59", "1.36"},
	}
	m := Mapping{
		RoleDate: 0, RoleStore: 1, RoleItem: 2, RoleQty: 3,
		RoleUnit: 4, RoleUnitPrice: 5, RoleTotalPrice: 6,
	}
	pv := NormalizeRow(raw, m, DefaultUnitOptions(), Grouping{}, DateFmtISO)

	if pv.Date != "2026-03-12" {
		t.Errorf("Date = %q, want 2026-03-12", pv.Date)
	}
	if pv.Store != "Whole Foods" {
		t.Errorf("Store = %q, want Whole Foods", pv.Store)
	}
	if pv.Item != "Bananas" {
		t.Errorf("Item = %q, want Bananas", pv.Item)
	}
	if pv.Qty != 2.3 {
		t.Errorf("Qty = %v, want 2.3", pv.Qty)
	}
	if pv.Unit != "lb" {
		t.Errorf("Unit = %q, want lb", pv.Unit)
	}
	if pv.TotalCents != 136 {
		t.Errorf("TotalCents = %d, want 136", pv.TotalCents)
	}
	// trust_total strategy: unit price recomputed from total/qty.
	// 136 / 2.3 = 59.13... → rounds to 59 cents.
	if pv.UnitPriceCents != 59 {
		t.Errorf("UnitPriceCents = %d, want 59 (trust_total recompute)", pv.UnitPriceCents)
	}
	if len(pv.CellErrors) != 0 {
		t.Errorf("unexpected cell errors: %v", pv.CellErrors)
	}
}

func TestApplyUnitOptions_PriceMultiplier(t *testing.T) {
	opts := DefaultUnitOptions()
	opts.PriceMultiplier = 0.01 // integer cents → dollars-ish
	m := Mapping{RoleTotalPrice: 1, RoleUnitPrice: 0}
	_, total, _ := ApplyUnitOptions(1.0, 499, 499, opts, m)
	if total != 5 {
		t.Errorf("total with 0.01 multiplier = %d, want 5", total)
	}
}

func TestApplyUnitOptions_NegativeReject(t *testing.T) {
	opts := DefaultUnitOptions()
	opts.NegativeHandling = "reject"
	m := Mapping{RoleTotalPrice: 0}
	_, _, err := ApplyUnitOptions(1.0, 0, -100, opts, m)
	if err == "" {
		t.Errorf("expected rejection error for negative total")
	}
}
