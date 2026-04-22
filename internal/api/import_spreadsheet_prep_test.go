package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mstefanko/cartledger/internal/spreadsheet"
)

// syntheticSheet builds a ParsedSheet with 5 rows for testing.
//
//	Row 0: 2026-01-01, Store A, Apple, 1, ea, 1.00, 1.00
//	Row 1: 2026-01-02, Store B, Banana, 2, ea, 0.50, 1.00
//	Row 2: 2026-01-03, Store A, Carrot, 3, ea, 0.33, 0.99  ← transform overrides item
//	Row 3: 2026-01-04, Store B, Dates, 1, ea, 3.00, 3.00   ← will be skipped
//	Row 4: 2024-12-15, Store A, Elderberry, 1, ea, 5.00, 5.00  ← will be since-date-filtered
func syntheticSheet() *spreadsheet.ParsedSheet {
	makeRow := func(idx int, date, store, item, qty, unit, unitPrice, total string) spreadsheet.RawRow {
		return spreadsheet.RawRow{
			Index: idx,
			Cells: []string{date, store, item, qty, unit, unitPrice, total},
		}
	}
	return &spreadsheet.ParsedSheet{
		Name:    "Sheet1",
		Headers: []string{"Date", "Store", "Item", "Qty", "Unit", "UnitPrice", "Total"},
		Rows: []spreadsheet.RawRow{
			makeRow(0, "2026-01-01", "Store A", "Apple", "1", "ea", "1.00", "1.00"),
			makeRow(1, "2026-01-02", "Store B", "Banana", "2", "ea", "0.50", "1.00"),
			makeRow(2, "2026-01-03", "Store A", "Carrot", "3", "ea", "0.33", "0.99"),
			makeRow(3, "2026-01-04", "Store B", "Dates", "1", "ea", "3.00", "3.00"),
			makeRow(4, "2024-12-15", "Store A", "Elderberry", "1", "ea", "5.00", "5.00"),
		},
	}
}

// stdTestMapping maps each column by index to its role.
func stdTestMapping() spreadsheet.Mapping {
	return spreadsheet.Mapping{
		spreadsheet.RoleDate:       0,
		spreadsheet.RoleStore:      1,
		spreadsheet.RoleItem:       2,
		spreadsheet.RoleQty:        3,
		spreadsheet.RoleUnit:       4,
		spreadsheet.RoleUnitPrice:  5,
		spreadsheet.RoleTotalPrice: 6,
	}
}

// overrideCellPayload encodes an override_cell transform payload.
func overrideCellPayload(t *testing.T, rowIdx, colIdx int, newValue string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(spreadsheet.OverrideCellPayload{
		RowIndex: rowIdx,
		ColIndex: colIdx,
		NewValue: newValue,
	})
	if err != nil {
		t.Fatalf("marshal override payload: %v", err)
	}
	return b
}

// syntheticStaging returns a minimal Staging with a single override_cell
// transform that changes row 2 item from "Carrot" to "CARROT" (col 2).
func syntheticStaging(householdID string) *spreadsheet.Staging {
	return &spreadsheet.Staging{
		ImportID:    "test-import-id",
		HouseholdID: householdID,
		Chain: spreadsheet.TransformChain{
			Revision: 1,
			Transforms: []spreadsheet.Transform{
				{
					ID:   "t1",
					Kind: spreadsheet.KindOverrideCell,
					// RowIndex on Transform is informational; payload carries the real indices.
					Payload: mustMarshal(spreadsheet.OverrideCellPayload{
						RowIndex: 2,
						ColIndex: 2,
						NewValue: "CARROT",
					}),
				},
			},
		},
	}
}

func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// TestPrepareSpreadsheetImport_Pipeline validates:
//   - Transform is applied (row 2 item becomes "CARROT").
//   - SkipRowIndices removes row 3 from Transformed.Rows.
//   - SinceDate filters out row 4 (2024-12-15 < 2025-01-01) from Parsed.
//   - Groups count matches rows surviving both filters.
//   - Duplicates is empty for a fresh DB.
func TestPrepareSpreadsheetImport_Pipeline(t *testing.T) {
	f, cleanup := newImportFixture(t)
	defer cleanup()

	ps := syntheticSheet()
	s := syntheticStaging(f.HH1)
	mapping := stdTestMapping()

	args := prepareArgs{
		SkipRowIndices: []int{3}, // skip row index 3 (Dates)
		UnitOptions:    spreadsheet.UnitOptions{},
		Grouping:       spreadsheet.Grouping{Strategy: "by_date_store"},
		DateFormat:     spreadsheet.DateFmtISO,
		SinceDate:      "2025-01-01", // filters out row 4 (2024-12-15)
	}

	prep, err := f.Handler.prepareSpreadsheetImport(context.Background(), s, ps, mapping, args, true)
	if err != nil {
		t.Fatalf("prepareSpreadsheetImport: unexpected error: %v", err)
	}

	// --- Transformed: 5 rows → 1 transform (override, no removal) → 1 skip removes row 3 → 4 rows.
	if got := len(prep.Transformed.Rows); got != 4 {
		t.Errorf("Transformed.Rows: got %d, want 4", got)
	}

	// Verify the override_cell transform was applied to row 2.
	var row2Item string
	for _, r := range prep.Transformed.Rows {
		if r.Index == 2 {
			row2Item = r.Cells[2]
		}
	}
	if row2Item != "CARROT" {
		t.Errorf("transform not applied: row 2 item = %q, want %q", row2Item, "CARROT")
	}

	// Row 3 (Dates) should be absent from Transformed.
	for _, r := range prep.Transformed.Rows {
		if r.Index == 3 {
			t.Error("row 3 (skipped) should not appear in Transformed.Rows")
		}
	}

	// --- Parsed: 4 rows (post-skip) → row 4 (2024-12-15) since-filtered → 3 rows.
	if got := len(prep.Parsed); got != 3 {
		t.Errorf("Parsed: got %d, want 3", got)
	}
	for _, pv := range prep.Parsed {
		if pv.Date < "2025-01-01" {
			t.Errorf("since-date filter missed row with date %s", pv.Date)
		}
	}

	// --- Groups: rows 0, 1, 2 remain; by_date_store creates one group per unique (date, store).
	// Row 0: 2026-01-01/Store A, Row 1: 2026-01-02/Store B, Row 2: 2026-01-03/Store A → 3 groups.
	if got := len(prep.Groups); got != 3 {
		t.Errorf("Groups: got %d, want 3", got)
	}

	// --- Duplicates: fresh DB → empty.
	if got := len(prep.Duplicates); got != 0 {
		t.Errorf("Duplicates: got %d, want 0", got)
	}
}

// TestPrepareSpreadsheetImport_Idempotent calls prepareSpreadsheetImport
// twice with the same inputs and asserts the outputs are identical.
func TestPrepareSpreadsheetImport_Idempotent(t *testing.T) {
	f, cleanup := newImportFixture(t)
	defer cleanup()

	ps := syntheticSheet()
	s := syntheticStaging(f.HH1)
	mapping := stdTestMapping()

	args := prepareArgs{
		SkipRowIndices: []int{3},
		UnitOptions:    spreadsheet.UnitOptions{},
		Grouping:       spreadsheet.Grouping{Strategy: "by_date_store"},
		DateFormat:     spreadsheet.DateFmtISO,
		SinceDate:      "2025-01-01",
	}

	prep1, err1 := f.Handler.prepareSpreadsheetImport(context.Background(), s, ps, mapping, args, true)
	prep2, err2 := f.Handler.prepareSpreadsheetImport(context.Background(), s, ps, mapping, args, true)

	if err1 != nil || err2 != nil {
		t.Fatalf("unexpected errors: %v, %v", err1, err2)
	}

	if len(prep1.Transformed.Rows) != len(prep2.Transformed.Rows) {
		t.Errorf("Transformed.Rows length mismatch: %d vs %d", len(prep1.Transformed.Rows), len(prep2.Transformed.Rows))
	}
	if len(prep1.Parsed) != len(prep2.Parsed) {
		t.Errorf("Parsed length mismatch: %d vs %d", len(prep1.Parsed), len(prep2.Parsed))
	}
	if len(prep1.Groups) != len(prep2.Groups) {
		t.Errorf("Groups length mismatch: %d vs %d", len(prep1.Groups), len(prep2.Groups))
	}
	if len(prep1.Duplicates) != len(prep2.Duplicates) {
		t.Errorf("Duplicates length mismatch: %d vs %d", len(prep1.Duplicates), len(prep2.Duplicates))
	}

	// Check group IDs match.
	ids1 := make(map[string]bool, len(prep1.Groups))
	for _, g := range prep1.Groups {
		ids1[g.ID] = true
	}
	for _, g := range prep2.Groups {
		if !ids1[g.ID] {
			t.Errorf("second call produced group ID %q not in first call", g.ID)
		}
	}
}

// TestPrepareSpreadsheetImport_ApplySplitFalse asserts that when applySplit=false
// the groups count may differ from applySplit=true (or at minimum no panic occurs).
func TestPrepareSpreadsheetImport_ApplySplitFalse(t *testing.T) {
	f, cleanup := newImportFixture(t)
	defer cleanup()

	ps := syntheticSheet()
	s := syntheticStaging(f.HH1)
	mapping := stdTestMapping()

	args := prepareArgs{
		SkipRowIndices: []int{},
		UnitOptions:    spreadsheet.UnitOptions{},
		Grouping:       spreadsheet.Grouping{Strategy: "by_date_store"},
		DateFormat:     spreadsheet.DateFmtISO,
		SinceDate:      "",
	}

	withSplit, err := f.Handler.prepareSpreadsheetImport(context.Background(), s, ps, mapping, args, true)
	if err != nil {
		t.Fatalf("withSplit: %v", err)
	}
	withoutSplit, err := f.Handler.prepareSpreadsheetImport(context.Background(), s, ps, mapping, args, false)
	if err != nil {
		t.Fatalf("withoutSplit: %v", err)
	}

	// Both must produce the same Parsed slice.
	if len(withSplit.Parsed) != len(withoutSplit.Parsed) {
		t.Errorf("Parsed count mismatch: %d vs %d", len(withSplit.Parsed), len(withoutSplit.Parsed))
	}

	// Groups may or may not differ; just assert no panic and both are non-nil.
	if withSplit.Groups == nil || withoutSplit.Groups == nil {
		t.Error("Groups must not be nil")
	}
}
