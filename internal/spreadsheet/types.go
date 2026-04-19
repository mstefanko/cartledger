// Package spreadsheet parses and normalizes user-uploaded CSV/XLSX files into
// a staged, grouped form that higher layers (internal/api) commit into the
// database. This package is deliberately self-contained: it has no dependency
// on internal/api or internal/db, so its behavior is exercised by fixture-
// driven golden tests without touching a real server.
//
// See PLAN-spreadsheet-import.md for the full design. The key ideas:
//
//   - ParsedSheet is the cached, post-parse representation (headers + rows +
//     column samples + type coverage). It is computed once at upload.
//   - A TransformChain of deterministic edits (override_cell, skip_row) and
//     AI-authored edits (ai_normalize, split_row — Phase 10) is applied on top
//     of ParsedSheet on every preview/commit.
//   - NormalizeRow turns raw strings into typed ParsedValue fields, honoring
//     the user-selected Mapping, UnitOptions, date format, and store default.
//   - GroupRows emits receipt-level Groups from ParsedValues using one of
//     four strategies. Groups — not individual rows — are what commits, and
//     what duplicate detection matches against.
package spreadsheet

import "encoding/json"

// Role identifies the semantic meaning of a spreadsheet column. The user's
// configuration maps each Role to at most one column index; some Roles are
// optional (Qty/Unit/TripID/Notes), some are required depending on which
// grouping strategy and UnitOptions are selected.
type Role string

const (
	RoleDate       Role = "date"
	RoleStore      Role = "store"
	RoleItem       Role = "item"
	RoleQty        Role = "qty"
	RoleUnit       Role = "unit"
	RoleUnitPrice  Role = "unit_price"
	RoleTotalPrice Role = "total_price"
	RoleTripID     Role = "trip_id"
	RoleNotes      Role = "notes"
	RoleIgnore     Role = "ignore"
)

// Mapping associates each Role with a column index in the source sheet.
// A missing key OR a value of -1 means the Role is unmapped.
type Mapping map[Role]int

// Col returns the column index bound to r, or -1 if r is unmapped.
func (m Mapping) Col(r Role) int {
	if i, ok := m[r]; ok {
		return i
	}
	return -1
}

// DateFormat enumerates the date formats the parser attempts, in priority
// order. ExcelSerial handles XLSX numeric dates; ISO8601 handles timestamp-
// shaped strings like "2026-03-12T14:22:00Z".
type DateFormat string

const (
	DateFmtISO         DateFormat = "YYYY-MM-DD"
	DateFmtUS          DateFormat = "MM/DD/YYYY"
	DateFmtEU          DateFormat = "DD/MM/YYYY"
	DateFmtISOSlash    DateFormat = "YYYY/MM/DD"
	DateFmtExcelSerial DateFormat = "excel_serial"
	DateFmtISO8601     DateFormat = "ISO-8601"
)

// CSVOptions governs how a CSV is tokenized. Delimiter defaults to ',' when
// zero. HasHeader, SkipStart, and SkipEnd let callers tolerate sheets with
// title banners or footer totals rows.
type CSVOptions struct {
	Delimiter rune `json:"delimiter"`
	HasHeader bool `json:"has_header"`
	SkipStart int  `json:"skip_start"`
	SkipEnd   int  `json:"skip_end"`
}

// UnitOptions control how raw numeric cells convert to priced line items.
// See PLAN §Unit & Price Options.
type UnitOptions struct {
	// PriceMultiplier is applied to every money value post-parse. Default 1.0.
	// Use 0.01 when the sheet stores prices as integer cents (e.g. 499 → $4.99).
	PriceMultiplier float64 `json:"price_multiplier"`

	// TotalUnitPriceStrategy: "trust_total" | "compute_missing" | "ignore_qty".
	TotalUnitPriceStrategy string `json:"total_unit_price_strategy"`

	// NegativeHandling: "discount" | "refund" | "reject".
	NegativeHandling string `json:"negative_handling"`

	// DefaultUnit is used when the Unit role is unmapped or the cell is blank.
	// Default "ea".
	DefaultUnit string `json:"default_unit"`
}

// GroupingStrategy enumerates the receipt-boundary heuristics.
type GroupingStrategy string

const (
	GroupDateStore      GroupingStrategy = "date_store"
	GroupDateStoreTotal GroupingStrategy = "date_store_total"
	GroupTripIDColumn   GroupingStrategy = "trip_id_column"
	GroupExplicitMarker GroupingStrategy = "explicit_marker"
)

// Grouping configures how rows collapse into receipts.
type Grouping struct {
	Strategy GroupingStrategy `json:"strategy"`

	// DefaultStoreID is stamped onto every row when the Store role is unmapped
	// (Case 2 in §Store Handling). Ignored when the Store role is mapped.
	DefaultStoreID string `json:"default_store_id"`

	// ExplicitRanges is consulted only for GroupExplicitMarker. Each entry is
	// an inclusive [startRow, endRow] pair over ParsedValue indices.
	ExplicitRanges [][2]int `json:"explicit_ranges"`
}

// RawRow is a single post-parse, pre-normalize row. Index is the row's
// position in the source sheet (0-based, not counting skipped preamble rows).
type RawRow struct {
	Index int      `json:"index"`
	Cells []string `json:"cells"`
}

// TypeCoverage is the per-column stats packet the frontend renders as badges
// under each header in the mapping UI. Percentages are of non-skipped rows.
type TypeCoverage struct {
	NonBlankPct float64 `json:"nonblank_pct"`
	DatePct     float64 `json:"date_pct"`
	MoneyPct    float64 `json:"money_pct"`
	IntegerPct  float64 `json:"integer_pct"`
	TextPct     float64 `json:"text_pct"`
}

// ParsedSheet is the cached representation of a single sheet after parsing.
// It lives in staging_json; preview/commit run their transform chain on a
// shallow copy of Rows and never re-parse the source file.
type ParsedSheet struct {
	Name          string                  `json:"name"`
	Headers       []string                `json:"headers"`
	Rows          []RawRow                `json:"rows"`
	ColumnSamples map[int][]string        `json:"column_samples"`
	TypeCoverage  map[int]TypeCoverage    `json:"type_coverage"`
}

// Transform is a single edit applied on top of the parsed rows. v1 implements
// KindOverrideCell and KindSkipRow. KindAINormalize and KindSplitRow are
// Phase 10 stubs.
type Transform struct {
	ID       string          `json:"id"`
	Kind     string          `json:"kind"`
	RowIndex int             `json:"row_index"`
	Payload  json.RawMessage `json:"payload"`
}

const (
	KindOverrideCell = "override_cell"
	KindSkipRow      = "skip_row"
	KindAINormalize  = "ai_normalize"
	KindSplitRow     = "split_row"
)

// TransformChain is the ordered list of transforms for a staging session.
// Revision is a monotonic counter the client echoes back on each preview so
// the server can detect stale chains.
type TransformChain struct {
	Revision   int         `json:"revision"`
	Transforms []Transform `json:"transforms"`
}

// OverrideCellPayload is the JSON payload for KindOverrideCell.
type OverrideCellPayload struct {
	RowIndex int    `json:"row_index"`
	ColIndex int    `json:"col_index"`
	NewValue string `json:"new_value"`
}

// SkipRowPayload is the JSON payload for KindSkipRow.
type SkipRowPayload struct {
	RowIndices []int `json:"row_indices"`
}

// ParsedValue is one row after normalization. CellErrors is keyed by Role so
// the preview UI can render red borders only on the cells that failed to
// parse, rather than dumping a row-level error string.
type ParsedValue struct {
	RowIndex       int             `json:"row_index"`
	Date           string          `json:"date"`             // "YYYY-MM-DD" or ""
	Store          string          `json:"store"`            // normalized via NormalizeStoreName
	Item           string          `json:"item"`
	Qty            float64         `json:"qty"`
	Unit           string          `json:"unit"`
	UnitPriceCents int64           `json:"unit_price_cents"`
	TotalCents     int64           `json:"total_cents"`
	TripID         string          `json:"trip_id"`
	Notes          string          `json:"notes"`
	CellErrors     map[Role]string `json:"cell_errors,omitempty"`
}

// Group is a single receipt-to-be. RowIndices references the original
// ParsedValue.RowIndex values (not offsets into a filtered list) so it
// survives skip/split/merge transforms deterministically.
type Group struct {
	ID             string `json:"id"`
	Store          string `json:"store"`
	Date           string `json:"date"`
	RowIndices     []int  `json:"row_indices"`
	TotalCents     int64  `json:"total_cents"`
	SplitSuggested bool   `json:"split_suggested"`
}

// DefaultCSVOptions returns a sensible zero-configuration CSVOptions.
func DefaultCSVOptions() CSVOptions {
	return CSVOptions{Delimiter: ',', HasHeader: true}
}

// DefaultUnitOptions returns the plan's default UnitOptions:
// multiplier=1.0, strategy="trust_total", negatives="discount", unit="ea".
func DefaultUnitOptions() UnitOptions {
	return UnitOptions{
		PriceMultiplier:        1.0,
		TotalUnitPriceStrategy: "trust_total",
		NegativeHandling:       "discount",
		DefaultUnit:            "ea",
	}
}
