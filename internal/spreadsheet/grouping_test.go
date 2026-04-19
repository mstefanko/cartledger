package spreadsheet

import "testing"

func TestGroupRows_DateStore(t *testing.T) {
	rows := []ParsedValue{
		{RowIndex: 0, Date: "2026-03-12", Store: "Whole Foods", TotalCents: 499},
		{RowIndex: 1, Date: "2026-03-12", Store: "Whole Foods", TotalCents: 136},
		{RowIndex: 2, Date: "2026-03-13", Store: "Costco", TotalCents: 699},
	}
	groups := GroupRows(rows, Grouping{Strategy: GroupDateStore})
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if groups[0].TotalCents != 635 {
		t.Errorf("first group total = %d, want 635", groups[0].TotalCents)
	}
	if len(groups[0].RowIndices) != 2 {
		t.Errorf("first group rows = %v, want 2 entries", groups[0].RowIndices)
	}
	if groups[0].ID != "g0" || groups[1].ID != "g1" {
		t.Errorf("group IDs should be sequential: %q, %q", groups[0].ID, groups[1].ID)
	}
}

func TestGroupRows_DateStore_DefaultStore(t *testing.T) {
	rows := []ParsedValue{
		{RowIndex: 0, Date: "2026-03-12", Store: "", TotalCents: 499},
		{RowIndex: 1, Date: "2026-03-12", Store: "", TotalCents: 136},
	}
	groups := GroupRows(rows, Grouping{Strategy: GroupDateStore, DefaultStoreID: "Costco"})
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if groups[0].Store != "Costco" {
		t.Errorf("default store not applied: %q", groups[0].Store)
	}
}

func TestGroupRows_TripIDColumn(t *testing.T) {
	rows := []ParsedValue{
		{RowIndex: 0, TripID: "T1", TotalCents: 100},
		{RowIndex: 1, TripID: "T1", TotalCents: 200},
		{RowIndex: 2, TripID: "T2", TotalCents: 300},
	}
	groups := GroupRows(rows, Grouping{Strategy: GroupTripIDColumn})
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups by trip id, got %d", len(groups))
	}
	if groups[0].TotalCents != 300 || groups[1].TotalCents != 300 {
		t.Errorf("unexpected totals: %d, %d", groups[0].TotalCents, groups[1].TotalCents)
	}
}

func TestGroupRows_ExplicitMarker(t *testing.T) {
	rows := []ParsedValue{
		{RowIndex: 0, Store: "Whole Foods", Date: "2026-03-12", TotalCents: 100},
		{RowIndex: 1, Store: "Whole Foods", Date: "2026-03-12", TotalCents: 200},
		{RowIndex: 2, Store: "Costco", Date: "2026-03-13", TotalCents: 300},
		{RowIndex: 3, Store: "Aldi", Date: "2026-03-14", TotalCents: 400}, // outside any range
	}
	groups := GroupRows(rows, Grouping{
		Strategy:       GroupExplicitMarker,
		ExplicitRanges: [][2]int{{0, 1}, {2, 2}},
	})
	if len(groups) != 3 { // two explicit + one ungrouped
		t.Fatalf("expected 3 groups, got %d", len(groups))
	}
	if groups[0].TotalCents != 300 {
		t.Errorf("first group total = %d, want 300", groups[0].TotalCents)
	}
	if groups[2].TotalCents != 400 {
		t.Errorf("ungrouped total = %d, want 400", groups[2].TotalCents)
	}
}

func TestApplySplitSuggested_LargeGroup(t *testing.T) {
	var rows []ParsedValue
	for i := 0; i < 45; i++ {
		rows = append(rows, ParsedValue{RowIndex: i, Date: "2026-03-12", Store: "Costco", TotalCents: 100})
	}
	groups := GroupRows(rows, Grouping{Strategy: GroupDateStore})
	groups = ApplySplitSuggested(rows, groups)
	if !groups[0].SplitSuggested {
		t.Errorf("45-row group should have SplitSuggested=true")
	}
}

func TestApplySplitSuggested_InlineTotal(t *testing.T) {
	// 3 items + mid-group "subtotal" marker equal to sum of preceding, then more items.
	rows := []ParsedValue{
		{RowIndex: 0, Date: "2026-03-12", Store: "X", TotalCents: 100},
		{RowIndex: 1, Date: "2026-03-12", Store: "X", TotalCents: 200},
		{RowIndex: 2, Date: "2026-03-12", Store: "X", Item: "subtotal", TotalCents: 300},
		{RowIndex: 3, Date: "2026-03-12", Store: "X", TotalCents: 150},
	}
	groups := GroupRows(rows, Grouping{Strategy: GroupDateStore})
	groups = ApplySplitSuggested(rows, groups)
	if !groups[0].SplitSuggested {
		t.Errorf("inline total marker should trigger SplitSuggested")
	}
}

func TestGroupRows_DateStoreTotal_SplitsAtTotal(t *testing.T) {
	// Two trips same day, same store, separated by a total row.
	rows := []ParsedValue{
		{RowIndex: 0, Date: "2026-03-12", Store: "X", TotalCents: 100},
		{RowIndex: 1, Date: "2026-03-12", Store: "X", TotalCents: 200},
		{RowIndex: 2, Date: "2026-03-12", Store: "X", Item: "TOTAL", TotalCents: 300},
		{RowIndex: 3, Date: "2026-03-12", Store: "X", TotalCents: 150},
		{RowIndex: 4, Date: "2026-03-12", Store: "X", TotalCents: 150},
	}
	groups := GroupRows(rows, Grouping{Strategy: GroupDateStoreTotal})
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups split at total, got %d: %+v", len(groups), groups)
	}
}
