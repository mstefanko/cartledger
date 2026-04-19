package spreadsheet

// Golden-test note on the XLSX fixture:
//
// archetype_a_clean.xlsx is generated programmatically via excelize so the
// binary committed to testdata/ is deterministic for a given excelize
// version. If the fixture goes missing (e.g. on a fresh clone where the
// file was gitignored), TestParseXLSX_CleanArchetypeA skips with Skipf
// rather than failing — the CSV fixture tests still exercise the parser/
// detect/normalize pipeline end-to-end.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestApplyTransforms_OverrideCell(t *testing.T) {
	parsed := &ParsedSheet{
		Headers: []string{"A", "B"},
		Rows: []RawRow{
			{Index: 0, Cells: []string{"old", "x"}},
			{Index: 1, Cells: []string{"y", "z"}},
		},
	}
	payload, _ := json.Marshal(OverrideCellPayload{RowIndex: 0, ColIndex: 0, NewValue: "new"})
	chain := TransformChain{
		Revision: 1,
		Transforms: []Transform{
			{ID: "t0", Kind: KindOverrideCell, RowIndex: 0, Payload: payload},
		},
	}
	out := ApplyTransforms(parsed, chain)
	if out.Rows[0].Cells[0] != "new" {
		t.Errorf("override not applied: %v", out.Rows[0].Cells)
	}
	// Original slice must not be mutated.
	if parsed.Rows[0].Cells[0] != "old" {
		t.Errorf("ApplyTransforms mutated input: %v", parsed.Rows[0].Cells)
	}
}

func TestApplyTransforms_SkipRow(t *testing.T) {
	parsed := &ParsedSheet{
		Headers: []string{"A"},
		Rows: []RawRow{
			{Index: 0, Cells: []string{"a"}},
			{Index: 1, Cells: []string{"b"}},
			{Index: 2, Cells: []string{"c"}},
		},
	}
	payload, _ := json.Marshal(SkipRowPayload{RowIndices: []int{1}})
	chain := TransformChain{Transforms: []Transform{{ID: "t0", Kind: KindSkipRow, Payload: payload}}}
	out := ApplyTransforms(parsed, chain)
	if len(out.Rows) != 2 {
		t.Fatalf("expected 2 rows after skip, got %d", len(out.Rows))
	}
	if out.Rows[0].Index != 0 || out.Rows[1].Index != 2 {
		t.Errorf("unexpected rows after skip: %+v", out.Rows)
	}
}

func TestApplyTransforms_IgnoresPhase10Kinds(t *testing.T) {
	parsed := &ParsedSheet{Rows: []RawRow{{Index: 0, Cells: []string{"a"}}}}
	chain := TransformChain{Transforms: []Transform{
		{ID: "t0", Kind: KindAINormalize, Payload: json.RawMessage(`{}`)},
		{ID: "t1", Kind: KindSplitRow, Payload: json.RawMessage(`{}`)},
	}}
	out := ApplyTransforms(parsed, chain)
	// Phase 10 kinds are no-ops — output is unchanged.
	if len(out.Rows) != 1 || out.Rows[0].Cells[0] != "a" {
		t.Errorf("Phase 10 kinds should be no-ops, got %+v", out.Rows)
	}
}

func TestSaveLoadStaging(t *testing.T) {
	dir := t.TempDir()
	s := &Staging{
		ImportID:     "imp1",
		RawPath:      filepath.Join(dir, "imp1", "raw.csv"),
		Parsed:       map[string]*ParsedSheet{"Sheet1": {Name: "Sheet1", Headers: []string{"A"}}},
		Chain:        TransformChain{Revision: 1},
		CreatedAt:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		LastActiveAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	}
	if err := SaveStaging(dir, s); err != nil {
		t.Fatalf("SaveStaging: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "imp1", "staging.json")); err != nil {
		t.Fatalf("staging.json not created: %v", err)
	}
	loaded, err := LoadStaging(dir, "imp1")
	if err != nil {
		t.Fatalf("LoadStaging: %v", err)
	}
	if loaded.ImportID != "imp1" || loaded.Chain.Revision != 1 {
		t.Errorf("roundtrip mismatch: %+v", loaded)
	}

	// Delete.
	if err := DeleteStaging(dir, "imp1"); err != nil {
		t.Fatalf("DeleteStaging: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "imp1")); !os.IsNotExist(err) {
		t.Errorf("expected staging dir removed, stat err = %v", err)
	}
}

// TestEndToEnd_ArchetypeA_Clean wires parse → detect → normalize → group on
// the clean fixture and asserts the high-level contract: one receipt per
// (date, store), correct row counts, no cell errors.
func TestEndToEnd_ArchetypeA_Clean(t *testing.T) {
	f, err := os.Open("testdata/archetype_a_clean.csv")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	sheet, err := ParseCSV(f, DefaultCSVOptions())
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	m := SuggestMapping(sheet)
	df := DateFmtISO

	var pvs []ParsedValue
	for _, r := range sheet.Rows {
		pv := NormalizeRow(r, m, DefaultUnitOptions(), Grouping{}, df)
		pvs = append(pvs, pv)
	}
	groups := GroupRows(pvs, Grouping{Strategy: GroupDateStore})

	if len(groups) != 2 {
		t.Fatalf("expected 2 receipt groups (WF 3/12, Costco 3/13), got %d", len(groups))
	}
	if groups[0].Store != "Whole Foods" || groups[0].Date != "2026-03-12" {
		t.Errorf("group 0 = (%q,%q), want (Whole Foods, 2026-03-12)", groups[0].Store, groups[0].Date)
	}
	if groups[0].TotalCents != 635 { // 4.99 + 1.36
		t.Errorf("group 0 total = %d, want 635", groups[0].TotalCents)
	}
	// No row should have CellErrors on clean data.
	for _, pv := range pvs {
		if len(pv.CellErrors) > 0 {
			t.Errorf("row %d unexpected cell errors: %v", pv.RowIndex, pv.CellErrors)
		}
	}
}
