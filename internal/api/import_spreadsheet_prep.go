package api

import (
	"context"

	"github.com/mstefanko/cartledger/internal/spreadsheet"
)

// prepareArgs holds the per-request parameters shared by buildPreview and Commit
// that drive the normalization + grouping pipeline.
type prepareArgs struct {
	SkipRowIndices []int
	UnitOptions    spreadsheet.UnitOptions
	Grouping       spreadsheet.Grouping
	DateFormat     spreadsheet.DateFormat
	SinceDate      string
}

// preparedImport is the output of prepareSpreadsheetImport.
type preparedImport struct {
	Transformed *spreadsheet.ParsedSheet  // after transforms + skips
	Parsed      []spreadsheet.ParsedValue // after since-date filter
	Groups      []spreadsheet.Group
	Duplicates  spreadsheet.DuplicateMap
}

// prepareSpreadsheetImport runs the shared six-step pipeline used by both
// buildPreview and Commit:
//
//  1. ApplyTransforms — replay the persistent transform chain.
//  2. Per-request skip filter — honour SkipRowIndices (NOT persisted).
//  3. NormalizeRow + since-date filter — parse each surviving row and drop
//     rows whose date falls before args.SinceDate.
//  4. GroupRows — aggregate parsed rows into receipt groups.
//  5. ApplySplitSuggested — only when applySplit is true (preview only).
//  6. CheckDuplicates — read-only check against committed receipts.
//
// If CheckDuplicates fails, the error is returned together with a
// preparedImport whose Duplicates field is an empty map.  Callers own the
// slog.Warn call so they can include their own context (e.g. import_id).
func (h *ImportSpreadsheetHandler) prepareSpreadsheetImport(
	ctx context.Context,
	s *spreadsheet.Staging,
	ps *spreadsheet.ParsedSheet,
	mapping spreadsheet.Mapping,
	args prepareArgs,
	applySplit bool,
) (preparedImport, error) {
	// Step 1 — apply persistent transform chain.
	transformed := spreadsheet.ApplyTransforms(ps, s.Chain)

	// Step 2 — apply per-request skip filter.
	if len(args.SkipRowIndices) > 0 {
		skipSet := make(map[int]bool, len(args.SkipRowIndices))
		for _, i := range args.SkipRowIndices {
			skipSet[i] = true
		}
		filtered := transformed.Rows[:0:0]
		for _, r := range transformed.Rows {
			if !skipSet[r.Index] {
				filtered = append(filtered, r)
			}
		}
		transformed = &spreadsheet.ParsedSheet{
			Name:          transformed.Name,
			Headers:       transformed.Headers,
			Rows:          filtered,
			ColumnSamples: transformed.ColumnSamples,
			TypeCoverage:  transformed.TypeCoverage,
		}
	}

	// Step 3 — NormalizeRow loop with since-date filter.
	// Filter is applied AFTER NormalizeRow so we compare on the parsed date,
	// not a raw string that may not match the selected date format.
	parsed := make([]spreadsheet.ParsedValue, 0, len(transformed.Rows))
	for _, raw := range transformed.Rows {
		pv := spreadsheet.NormalizeRow(raw, mapping, args.UnitOptions, args.Grouping, args.DateFormat)
		if args.SinceDate != "" && pv.Date != "" && pv.Date < args.SinceDate {
			continue
		}
		parsed = append(parsed, pv)
	}

	// Step 4 — group rows.
	groups := spreadsheet.GroupRows(parsed, args.Grouping)

	// Step 5 — split suggestions (preview only).
	if applySplit {
		groups = spreadsheet.ApplySplitSuggested(parsed, groups)
	}

	// Step 6 — duplicate check (read-only; errors are non-fatal for callers).
	duplicates, err := spreadsheet.CheckDuplicates(ctx, h.DB, s.HouseholdID, groups)
	if err != nil {
		return preparedImport{
			Transformed: transformed,
			Parsed:      parsed,
			Groups:      groups,
			Duplicates:  spreadsheet.DuplicateMap{},
		}, err
	}

	return preparedImport{
		Transformed: transformed,
		Parsed:      parsed,
		Groups:      groups,
		Duplicates:  duplicates,
	}, nil
}

