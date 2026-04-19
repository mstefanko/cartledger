package spreadsheet

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mstefanko/cartledger/internal/units"
)

// NormalizeRow converts a RawRow's cells into a ParsedValue using the user's
// mapping, unit options, grouping options, and detected date format. It
// populates CellErrors per-role (not per-column) so the preview UI can mark
// the specific cells that failed to parse.
func NormalizeRow(raw RawRow, m Mapping, opts UnitOptions, g Grouping, df DateFormat) ParsedValue {
	pv := ParsedValue{RowIndex: raw.Index}
	errs := map[Role]string{}

	cell := func(r Role) string {
		c := m.Col(r)
		if c < 0 || c >= len(raw.Cells) {
			return ""
		}
		return strings.TrimSpace(raw.Cells[c])
	}

	// Date
	if c := m.Col(RoleDate); c >= 0 {
		v := cell(RoleDate)
		if v == "" {
			errs[RoleDate] = "blank"
		} else if d, err := NormalizeDate(v, df); err != nil {
			errs[RoleDate] = err.Error()
		} else {
			pv.Date = d
		}
	}

	// Store — either from column or fall back to default.
	if c := m.Col(RoleStore); c >= 0 {
		pv.Store = NormalizeStoreName(cell(RoleStore))
		if pv.Store == "" {
			errs[RoleStore] = "blank"
		}
	} else {
		pv.Store = g.DefaultStoreID // caller supplies the resolved name/id
	}

	// Item, Trip ID, Notes — free text.
	pv.Item = cell(RoleItem)
	pv.TripID = cell(RoleTripID)
	pv.Notes = cell(RoleNotes)

	// Unit (role may be unmapped; fall back to opts.DefaultUnit then "ea").
	if c := m.Col(RoleUnit); c >= 0 {
		pv.Unit = strings.ToLower(strings.TrimSpace(cell(RoleUnit)))
	}
	if pv.Unit == "" {
		pv.Unit = opts.DefaultUnit
	}
	if pv.Unit == "" {
		pv.Unit = "ea"
	}

	// Quantity — let units package parse "1 1/2 cup" and kin.
	if c := m.Col(RoleQty); c >= 0 {
		v := cell(RoleQty)
		if v != "" {
			qty, unitFromQty, err := NormalizeQty(v, pv.Unit)
			if err != nil {
				errs[RoleQty] = err.Error()
			} else {
				pv.Qty = qty
				// If the qty string carried its own unit (e.g. "2.3 lb"), it
				// wins over the mapped Unit column — that's the human intent.
				if unitFromQty != "" {
					pv.Unit = unitFromQty
				}
			}
		}
	}
	if pv.Qty == 0 {
		// Default qty = 1 so "unit price × qty" math works for rows with no
		// quantity column.
		pv.Qty = 1
	}

	// Prices
	if c := m.Col(RoleUnitPrice); c >= 0 {
		v := cell(RoleUnitPrice)
		if v != "" {
			cents, err := NormalizeMoney(v)
			if err != nil {
				errs[RoleUnitPrice] = err.Error()
			} else {
				pv.UnitPriceCents = cents
			}
		}
	}
	if c := m.Col(RoleTotalPrice); c >= 0 {
		v := cell(RoleTotalPrice)
		if v != "" {
			cents, err := NormalizeMoney(v)
			if err != nil {
				errs[RoleTotalPrice] = err.Error()
			} else {
				pv.TotalCents = cents
			}
		}
	}

	// Apply unit-price / total reconciliation AFTER raw cells are parsed.
	finalUnitPrice, finalTotal, perr := ApplyUnitOptions(pv.Qty, pv.UnitPriceCents, pv.TotalCents, opts, m)
	if perr != "" {
		errs[RoleTotalPrice] = perr
	}
	pv.UnitPriceCents = finalUnitPrice
	pv.TotalCents = finalTotal

	if len(errs) > 0 {
		pv.CellErrors = errs
	}
	return pv
}

// NormalizeDate converts s into ISO "YYYY-MM-DD" using df. Returns an error
// when the string can't be parsed.
func NormalizeDate(s string, df DateFormat) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("blank date")
	}

	switch df {
	case DateFmtISO:
		if t, err := time.Parse("2006-01-02", s); err == nil {
			return t.Format("2006-01-02"), nil
		}
	case DateFmtISOSlash:
		if t, err := time.Parse("2006/01/02", s); err == nil {
			return t.Format("2006-01-02"), nil
		}
	case DateFmtUS:
		// Accept both 1- and 2-digit month/day.
		for _, layout := range []string{"01/02/2006", "1/2/2006", "01-02-2006", "1-2-2006"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t.Format("2006-01-02"), nil
			}
		}
	case DateFmtEU:
		for _, layout := range []string{"02/01/2006", "2/1/2006", "02-01-2006", "2-1-2006"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t.Format("2006-01-02"), nil
			}
		}
	case DateFmtISO8601:
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t.Format("2006-01-02"), nil
		}
		if t, err := time.Parse("2006-01-02T15:04:05", s); err == nil {
			return t.Format("2006-01-02"), nil
		}
	case DateFmtExcelSerial:
		// Excel stores dates as days since 1899-12-30 on the "1900" system,
		// accounting for the erroneous 1900 leap day. Converting via the
		// 1899-12-30 epoch naturally compensates because Excel's day 60
		// ("1900-02-29") falls through normal Go date arithmetic — we
		// simply add the integer days to the epoch.
		n, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return "", fmt.Errorf("not a number: %q", s)
		}
		// Reject absurd values (Excel dates before 1900 or after 2200).
		if n < 1 || n > 110000 {
			return "", fmt.Errorf("excel serial out of range: %v", n)
		}
		days := int(math.Trunc(n))
		epoch := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
		return epoch.AddDate(0, 0, days).Format("2006-01-02"), nil
	}

	return "", fmt.Errorf("unparseable date %q (format=%s)", s, df)
}

// moneyCleanRe strips currency symbols, spaces, and thousands commas.
// Parentheses are stripped separately because they indicate negative.
var moneyCleanRe = regexp.MustCompile(`[\$€£¥,\s]`)

// NormalizeMoney parses a human-written money string into integer cents.
// Accepts "$1,234.56", "1234.56", "(1.23)", "-$1.23", "€1.23". Rejects
// strings that contain any non-numeric tail or second decimal point.
func NormalizeMoney(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("blank money")
	}

	negative := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		negative = true
		s = s[1 : len(s)-1]
	}
	s = moneyCleanRe.ReplaceAllString(s, "")
	if strings.HasPrefix(s, "-") {
		negative = !negative
		s = s[1:]
	} else if strings.HasPrefix(s, "+") {
		s = s[1:]
	}

	if s == "" || s == "." {
		return 0, fmt.Errorf("no digits in money value")
	}

	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("unparseable money: %w", err)
	}
	// Round to the nearest cent to avoid 0.1+0.2 style float drift.
	cents := int64(math.Round(f * 100))
	if negative {
		cents = -cents
	}
	return cents, nil
}

// NormalizeQty parses a human-written quantity (possibly with an inline unit)
// into a decimal count plus normalized unit name. Delegates to internal/units
// where possible; falls back to a local permissive parser only when units
// can't handle the input. Returns (qty, unit, error) — unit is "" when the
// input had no inline unit and the caller should use its default.
func NormalizeQty(s string, defaultUnit string) (float64, string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, "", fmt.Errorf("blank qty")
	}

	qty, unit, err := units.Parse(s)
	if err == nil {
		f, _ := qty.Float64()
		// units.Parse defaults to "each" when no unit is present — preserve
		// that signal by returning "" so the caller can prefer its default.
		if unit == "each" && !containsUnitHint(s) {
			return f, "", nil
		}
		return f, unit, nil
	}

	// Fallback: plain float (e.g. "2.5" with no unit).
	f, ferr := strconv.ParseFloat(s, 64)
	if ferr == nil {
		return f, "", nil
	}

	return 0, "", fmt.Errorf("unparseable qty %q: %w", s, err)
}

// containsUnitHint returns true when s contains a non-digit/non-punctuation
// character that could be a unit suffix (e.g. "2.3 lb"). Used to distinguish
// "2" (implied each) from "2 each" (explicit each).
func containsUnitHint(s string) bool {
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return true
		}
	}
	return false
}

// NormalizeStoreName trims whitespace, case-folds to title-ish form via
// lowercase+collapse (not Title-casing — we preserve the user's own casing
// for display), collapses internal whitespace, and strips trailing location
// suffixes like "#1234" or " #42" that grocery chains add.
//
// Two strings that normalize equal should be treated as the same store when
// grouping. The returned string is the *display form*, not a slug.
func NormalizeStoreName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Strip trailing " #123", "#123", "- #123" suffix.
	s = storeSuffixRe.ReplaceAllString(s, "")
	// Collapse internal whitespace runs.
	s = strings.Join(strings.Fields(s), " ")
	return s
}

var storeSuffixRe = regexp.MustCompile(`\s*[-–—]?\s*#\s*\d+\s*$`)

// ApplyUnitOptions honors PriceMultiplier and reconciles TotalCents /
// UnitPriceCents / Qty according to opts.TotalUnitPriceStrategy. Negative
// values are handled per opts.NegativeHandling. Returns (unitPrice, total,
// errMsg) — errMsg is non-empty when NegativeHandling="reject" and a value
// went negative.
//
// Strategies:
//   - "trust_total": if both total and unit price are mapped and nonzero,
//     unit price is recomputed as total/qty. Otherwise same as
//     "compute_missing".
//   - "compute_missing" (default): whichever of {total, unit price} is zero
//     is derived from the other two. If total is zero and qty is nonzero,
//     total = unitPrice * qty. If unitPrice is zero and qty is nonzero,
//     unitPrice = total / qty.
//   - "ignore_qty": qty is forced to 1 in the math; total becomes the line
//     amount regardless of the qty column. Useful for archetype-B blob rows.
func ApplyUnitOptions(qty float64, unitPriceCents, totalCents int64, opts UnitOptions, m Mapping) (int64, int64, string) {
	mult := opts.PriceMultiplier
	if mult == 0 {
		mult = 1.0
	}
	if mult != 1.0 {
		unitPriceCents = int64(math.Round(float64(unitPriceCents) * mult))
		totalCents = int64(math.Round(float64(totalCents) * mult))
	}

	strategy := opts.TotalUnitPriceStrategy
	if strategy == "" {
		strategy = "trust_total"
	}

	hasUnit := m.Col(RoleUnitPrice) >= 0
	hasTotal := m.Col(RoleTotalPrice) >= 0

	switch strategy {
	case "ignore_qty":
		if hasTotal && totalCents != 0 {
			unitPriceCents = totalCents
		} else if hasUnit {
			totalCents = unitPriceCents
		}
	case "trust_total":
		if hasTotal && totalCents != 0 && qty != 0 {
			unitPriceCents = int64(math.Round(float64(totalCents) / qty))
		} else if hasUnit && qty != 0 {
			totalCents = int64(math.Round(float64(unitPriceCents) * qty))
		}
	default: // "compute_missing"
		if totalCents == 0 && hasUnit && qty != 0 {
			totalCents = int64(math.Round(float64(unitPriceCents) * qty))
		}
		if unitPriceCents == 0 && hasTotal && qty != 0 {
			unitPriceCents = int64(math.Round(float64(totalCents) / qty))
		}
	}

	// Negative handling.
	if unitPriceCents < 0 || totalCents < 0 {
		switch opts.NegativeHandling {
		case "reject":
			return unitPriceCents, totalCents, "negative value rejected"
		case "refund", "discount", "":
			// Leave sign intact; caller decides downstream semantics.
		}
	}

	return unitPriceCents, totalCents, ""
}
