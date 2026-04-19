package spreadsheet

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"strings"

	"github.com/xuri/excelize/v2"
)

// utf8BOM is stripped from the first chunk of a CSV if present. Excel and
// many Google Sheets exports prepend a BOM that breaks stdlib csv.Reader's
// header parsing.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// ParseCSV reads r as a CSV using the supplied options and returns a
// ParsedSheet. It strips a leading UTF-8 BOM, tolerates ragged row widths
// (FieldsPerRecord = -1), skips blank rows, and honors SkipStart/SkipEnd for
// title/footer banners. The returned sheet's Name is empty; callers set it.
//
// Messy-sheet handling:
//   - BOM stripped (avoids corrupt first header cell).
//   - Ragged rows: shorter rows are padded to the widest observed width so
//     ColumnSamples indexing stays stable.
//   - Blank rows (every cell whitespace) are dropped before indexing so the
//     in-sheet row index matches what a human sees in their spreadsheet app
//     minus the banner offset.
func ParseCSV(r io.Reader, opts CSVOptions) (*ParsedSheet, error) {
	if opts.Delimiter == 0 {
		opts.Delimiter = ','
	}

	buf, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read csv: %w", err)
	}
	buf = bytes.TrimPrefix(buf, utf8BOM)

	reader := csv.NewReader(bytes.NewReader(buf))
	reader.Comma = opts.Delimiter
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = false // preserve leading space inside cells; we trim per-role during normalize

	var allRecords [][]string
	for {
		rec, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse csv row: %w", err)
		}
		allRecords = append(allRecords, rec)
	}

	// Apply skip-start / skip-end windowing before any blank-row filtering so
	// users can "skip 3 banner rows" without worrying about whether a banner
	// row is blank.
	if opts.SkipStart > 0 {
		if opts.SkipStart >= len(allRecords) {
			allRecords = nil
		} else {
			allRecords = allRecords[opts.SkipStart:]
		}
	}
	if opts.SkipEnd > 0 {
		if opts.SkipEnd >= len(allRecords) {
			allRecords = nil
		} else {
			allRecords = allRecords[:len(allRecords)-opts.SkipEnd]
		}
	}

	// Detect max width to pad ragged rows.
	maxCols := 0
	for _, rec := range allRecords {
		if len(rec) > maxCols {
			maxCols = len(rec)
		}
	}

	sheet := &ParsedSheet{
		ColumnSamples: map[int][]string{},
		TypeCoverage:  map[int]TypeCoverage{},
	}

	// Extract headers (if requested) and rows.
	start := 0
	if opts.HasHeader && len(allRecords) > 0 {
		sheet.Headers = padRow(allRecords[0], maxCols)
		start = 1
	} else {
		sheet.Headers = make([]string, maxCols)
	}

	rowIdx := 0
	for i := start; i < len(allRecords); i++ {
		cells := padRow(allRecords[i], maxCols)
		if isBlankRow(cells) {
			continue
		}
		sheet.Rows = append(sheet.Rows, RawRow{Index: rowIdx, Cells: cells})
		rowIdx++
	}

	populateSamplesAndCoverage(sheet)
	return sheet, nil
}

// ParseXLSX opens r as an XLSX workbook via excelize and returns every sheet
// parsed as a ParsedSheet. Merged ranges are backfilled into every row in the
// range (see PLAN §Store Handling Case 3) so sectioned archetype-C layouts
// surface the store header on each item row.
//
// Header detection defers to FindHeaderRow when the chosen row index is
// unclear; the first non-empty row is the default.
func ParseXLSX(r io.Reader) ([]string, map[string]*ParsedSheet, error) {
	f, err := excelize.OpenReader(r)
	if err != nil {
		return nil, nil, fmt.Errorf("open xlsx: %w", err)
	}
	defer f.Close()

	sheetNames := f.GetSheetList()
	parsed := make(map[string]*ParsedSheet, len(sheetNames))

	for _, name := range sheetNames {
		rows, err := f.GetRows(name)
		if err != nil {
			return nil, nil, fmt.Errorf("read xlsx sheet %q: %w", name, err)
		}

		// Backfill merged cells. excelize MergeCell values are the cell
		// addresses (e.g. "A2:A10") and the value is whatever lives in the
		// top-left cell. We expand each merge's value into every cell in the
		// range so downstream code never sees a blank.
		merges, err := f.GetMergeCells(name)
		if err != nil {
			return nil, nil, fmt.Errorf("read xlsx merges for %q: %w", name, err)
		}
		for _, m := range merges {
			startCol, startRow, err := excelize.CellNameToCoordinates(m.GetStartAxis())
			if err != nil {
				continue
			}
			endCol, endRow, err := excelize.CellNameToCoordinates(m.GetEndAxis())
			if err != nil {
				continue
			}
			val := m.GetCellValue()
			// excelize returns 1-based; GetRows is 0-based (row slice index).
			for ri := startRow - 1; ri < endRow && ri < len(rows); ri++ {
				for ci := startCol - 1; ci < endCol; ci++ {
					if ri < 0 || ci < 0 {
						continue
					}
					// Grow the row to accommodate the cell.
					for len(rows[ri]) <= ci {
						rows[ri] = append(rows[ri], "")
					}
					if rows[ri][ci] == "" {
						rows[ri][ci] = val
					}
				}
			}
		}

		maxCols := 0
		for _, rec := range rows {
			if len(rec) > maxCols {
				maxCols = len(rec)
			}
		}

		sheet := &ParsedSheet{
			Name:          name,
			ColumnSamples: map[int][]string{},
			TypeCoverage:  map[int]TypeCoverage{},
		}

		// XLSX: find the header row by heuristic rather than assuming row 0,
		// since many user sheets have banner rows / merged-title rows above
		// the actual column names.
		rawRows := make([]RawRow, 0, len(rows))
		for i, rec := range rows {
			rawRows = append(rawRows, RawRow{Index: i, Cells: padRow(rec, maxCols)})
		}
		headerIdx := FindHeaderRow(rawRows)

		if headerIdx >= 0 && headerIdx < len(rawRows) {
			sheet.Headers = rawRows[headerIdx].Cells
		} else {
			sheet.Headers = make([]string, maxCols)
		}

		// Rows below the header that are non-blank become the data rows. We
		// reindex them from 0 so the transform chain references "row 0 of the
		// data body" rather than a sheet-wide offset.
		dataIdx := 0
		for i := headerIdx + 1; i < len(rawRows); i++ {
			cells := rawRows[i].Cells
			if isBlankRow(cells) {
				continue
			}
			sheet.Rows = append(sheet.Rows, RawRow{Index: dataIdx, Cells: cells})
			dataIdx++
		}

		populateSamplesAndCoverage(sheet)
		parsed[name] = sheet
	}

	return sheetNames, parsed, nil
}

// padRow ensures row has exactly n columns; shorter rows are extended with
// empty strings. Longer rows are truncated (shouldn't happen after maxCols
// measurement but kept for defensiveness).
func padRow(row []string, n int) []string {
	if len(row) == n {
		return row
	}
	if len(row) > n {
		return row[:n]
	}
	out := make([]string, n)
	copy(out, row)
	return out
}

// isBlankRow returns true if every cell is empty after trimming whitespace.
func isBlankRow(cells []string) bool {
	for _, c := range cells {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

// populateSamplesAndCoverage fills ColumnSamples (up to 3 non-empty values
// per column) and TypeCoverage (percentages of date/money/integer/text/
// non-blank per column) on the sheet's current rows.
//
// Keeping these two computations in a single pass matters for large sheets:
// walking 10k rows once for samples and again for coverage is measurably
// slower than one pass that tracks both.
func populateSamplesAndCoverage(sheet *ParsedSheet) {
	width := 0
	for _, h := range sheet.Headers {
		_ = h
	}
	if len(sheet.Headers) > width {
		width = len(sheet.Headers)
	}
	for _, r := range sheet.Rows {
		if len(r.Cells) > width {
			width = len(r.Cells)
		}
	}

	type colStats struct {
		nonBlank int
		date     int
		money    int
		integer  int
		text     int
	}
	stats := make([]colStats, width)
	totalRows := len(sheet.Rows)

	for c := 0; c < width; c++ {
		sheet.ColumnSamples[c] = []string{}
	}

	for _, r := range sheet.Rows {
		for c := 0; c < width; c++ {
			var v string
			if c < len(r.Cells) {
				v = strings.TrimSpace(r.Cells[c])
			}
			if v == "" {
				continue
			}
			stats[c].nonBlank++
			if len(sheet.ColumnSamples[c]) < 3 {
				sheet.ColumnSamples[c] = append(sheet.ColumnSamples[c], v)
			}
			// Type sniffing — every non-blank cell gets classified. A value
			// can contribute to multiple buckets only when a simpler rule
			// would over-report (e.g. "2026" looks like an integer but also
			// like a date; we let integer-only values *not* count as date).
			switch {
			case looksLikeMoney(v):
				stats[c].money++
			case looksLikeDate(v):
				stats[c].date++
			case looksLikeInteger(v):
				stats[c].integer++
			default:
				stats[c].text++
			}
		}
	}

	for c := 0; c < width; c++ {
		cov := TypeCoverage{}
		if totalRows > 0 {
			cov.NonBlankPct = float64(stats[c].nonBlank) / float64(totalRows)
			cov.DatePct = float64(stats[c].date) / float64(totalRows)
			cov.MoneyPct = float64(stats[c].money) / float64(totalRows)
			cov.IntegerPct = float64(stats[c].integer) / float64(totalRows)
			cov.TextPct = float64(stats[c].text) / float64(totalRows)
		}
		sheet.TypeCoverage[c] = cov
	}
}
