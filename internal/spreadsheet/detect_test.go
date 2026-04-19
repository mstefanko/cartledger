package spreadsheet

import "testing"

func TestFindHeaderRow_Simple(t *testing.T) {
	rows := []RawRow{
		{Index: 0, Cells: []string{"Date", "Store", "Item"}},
		{Index: 1, Cells: []string{"2026-03-12", "Whole Foods", "Milk"}},
	}
	got := FindHeaderRow(rows)
	if got != 0 {
		t.Errorf("FindHeaderRow = %d, want 0", got)
	}
}

func TestFindHeaderRow_SkipsBanner(t *testing.T) {
	rows := []RawRow{
		{Index: 0, Cells: []string{"My Grocery Log 2026", "", ""}},
		{Index: 1, Cells: []string{"", "", ""}},
		{Index: 2, Cells: []string{"Date", "Store", "Item"}},
		{Index: 3, Cells: []string{"2026-03-12", "Aldi", "Bread"}},
	}
	got := FindHeaderRow(rows)
	if got != 2 {
		t.Errorf("FindHeaderRow = %d, want 2", got)
	}
}

func TestSuggestMapping_ArchetypeAHeaders(t *testing.T) {
	sheet := &ParsedSheet{
		Headers:       []string{"Date", "Store", "Item", "Qty", "Unit", "Unit Price", "Total"},
		ColumnSamples: map[int][]string{},
		TypeCoverage:  map[int]TypeCoverage{},
	}
	m := SuggestMapping(sheet)
	cases := []struct {
		role Role
		want int
	}{
		{RoleDate, 0},
		{RoleStore, 1},
		{RoleItem, 2},
		{RoleQty, 3},
		{RoleUnit, 4},
		{RoleUnitPrice, 5},
		{RoleTotalPrice, 6},
	}
	for _, tc := range cases {
		if got := m.Col(tc.role); got != tc.want {
			t.Errorf("role %s: got col %d, want %d (mapping=%v)", tc.role, got, tc.want, m)
		}
	}
}

func TestSuggestMapping_PrefersLongerKeyword(t *testing.T) {
	// "Unit Price" and "Total Price" share the substring "price"; longest
	// match wins so UnitPrice binds "Unit Price" and TotalPrice binds
	// "Total Price".
	sheet := &ParsedSheet{
		Headers:      []string{"Date", "Unit Price", "Total Price"},
		TypeCoverage: map[int]TypeCoverage{},
	}
	m := SuggestMapping(sheet)
	if m.Col(RoleUnitPrice) != 1 {
		t.Errorf("RoleUnitPrice = %d, want 1", m.Col(RoleUnitPrice))
	}
	if m.Col(RoleTotalPrice) != 2 {
		t.Errorf("RoleTotalPrice = %d, want 2", m.Col(RoleTotalPrice))
	}
}

func TestDetectDateFormat(t *testing.T) {
	cases := []struct {
		name    string
		samples []string
		want    DateFormat
	}{
		{"iso", []string{"2026-03-12", "2026-04-01", "2026-05-20"}, DateFmtISO},
		{"us", []string{"03/12/2026", "04/01/2026", "05/20/2026"}, DateFmtUS},
		{"iso-slash", []string{"2026/03/12", "2026/04/01"}, DateFmtISOSlash},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectDateFormat(tc.samples)
			if got != tc.want {
				t.Errorf("DetectDateFormat(%v) = %s, want %s", tc.samples, got, tc.want)
			}
		})
	}
}

func TestLooksLikeMoney(t *testing.T) {
	truthy := []string{"$4.99", "1,234.56", "(1.23)", "-$4.99", "€1.23", "$1,000"}
	for _, s := range truthy {
		if !looksLikeMoney(s) {
			t.Errorf("looksLikeMoney(%q) = false, want true", s)
		}
	}
	falsy := []string{"2026", "foo", "", "1/1"}
	for _, s := range falsy {
		if looksLikeMoney(s) {
			t.Errorf("looksLikeMoney(%q) = true, want false", s)
		}
	}
}
