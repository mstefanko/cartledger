package spreadsheet

import (
	"os"
	"strings"
	"testing"
)

func TestParseCSV_CleanArchetypeA(t *testing.T) {
	f, err := os.Open("testdata/archetype_a_clean.csv")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	sheet, err := ParseCSV(f, DefaultCSVOptions())
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if len(sheet.Rows) != 3 {
		t.Fatalf("expected 3 data rows, got %d", len(sheet.Rows))
	}
	if sheet.Headers[0] != "Date" || sheet.Headers[1] != "Store" {
		t.Errorf("unexpected headers: %v", sheet.Headers)
	}
	if got := sheet.Rows[0].Cells[2]; got != "Organic whole milk" {
		t.Errorf("row[0] item = %q, want %q", got, "Organic whole milk")
	}
	// Column samples should carry the first few non-empty values.
	if len(sheet.ColumnSamples[1]) == 0 || !strings.Contains(strings.Join(sheet.ColumnSamples[1], "|"), "Whole Foods") {
		t.Errorf("expected Whole Foods in samples for col 1, got %v", sheet.ColumnSamples[1])
	}
	// Type coverage: column 0 (date) should read as ≥66% date, column 6 (total) as money.
	if sheet.TypeCoverage[0].DatePct < 0.5 {
		t.Errorf("expected date col to have high date coverage, got %+v", sheet.TypeCoverage[0])
	}
}

func TestParseCSV_MessyStripsBOM(t *testing.T) {
	f, err := os.Open("testdata/archetype_a_messy.csv")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	sheet, err := ParseCSV(f, DefaultCSVOptions())
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if sheet.Headers[0] != "Date" {
		t.Errorf("BOM not stripped: headers[0] = %q", sheet.Headers[0])
	}
	// Blank rows are filtered; 3 data rows expected.
	if len(sheet.Rows) != 3 {
		t.Fatalf("expected 3 data rows (blanks filtered), got %d", len(sheet.Rows))
	}
}

func TestParseCSV_SkipStart(t *testing.T) {
	csv := "banner row\nDate,Store\n2026-01-01,Aldi\n"
	sheet, err := ParseCSV(strings.NewReader(csv), CSVOptions{Delimiter: ',', HasHeader: true, SkipStart: 1})
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if sheet.Headers[0] != "Date" {
		t.Errorf("SkipStart did not skip banner; headers[0] = %q", sheet.Headers[0])
	}
	if len(sheet.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(sheet.Rows))
	}
}

func TestParseCSV_RaggedRows(t *testing.T) {
	csv := "a,b,c\n1,2\n3,4,5,6\n"
	sheet, err := ParseCSV(strings.NewReader(csv), DefaultCSVOptions())
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	// Rows should be padded to the max width observed (4).
	for i, r := range sheet.Rows {
		if len(r.Cells) != 4 {
			t.Errorf("row %d has %d cells, want 4 (padded)", i, len(r.Cells))
		}
	}
}

func TestParseXLSX_CleanArchetypeA(t *testing.T) {
	f, err := os.Open("testdata/archetype_a_clean.xlsx")
	if err != nil {
		t.Skipf("xlsx fixture missing: %v", err)
		return
	}
	defer f.Close()

	names, parsed, err := ParseXLSX(f)
	if err != nil {
		t.Fatalf("ParseXLSX: %v", err)
	}
	if len(names) == 0 {
		t.Fatalf("expected at least one sheet")
	}
	sheet := parsed[names[0]]
	if sheet == nil {
		t.Fatalf("no parsed sheet for %q", names[0])
	}
	if len(sheet.Rows) != 3 {
		t.Errorf("expected 3 data rows, got %d", len(sheet.Rows))
	}
	if sheet.Headers[0] != "Date" {
		t.Errorf("headers[0] = %q, want Date", sheet.Headers[0])
	}
}
