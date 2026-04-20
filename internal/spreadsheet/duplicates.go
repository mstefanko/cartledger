package spreadsheet

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// DuplicateMap maps an input group ID to the ID of an existing receipt that
// looks like a duplicate, or — for groups with no duplicate candidate —
// simply has no entry. Callers iterate the input groups and check presence
// in the map; they MUST NOT rely on len(DuplicateMap) == len(groups).
type DuplicateMap map[string]string

// dupTotalToleranceCents is the ±window on total-cents comparison used to
// classify two receipts as the same. Fifty cents covers tax-rounding drift
// between a sheet's recorded subtotal and the committed receipt's total —
// common when users enter subtotals by hand or when an XLSX round-trip
// dropped a trailing cent. Wider than this and legitimately different
// receipts on the same date at the same store start colliding (think: two
// small Amazon/Walmart trips same day).
const dupTotalToleranceCents = 50

// CheckDuplicates finds committed receipts in household that match any of
// the given groups on the triple (date, normalized store name, rounded
// total ± dupTotalToleranceCents). The returned map contains entries only
// for groups that matched something — absence means "not a duplicate".
//
// Implementation is read-only and operates outside any transaction: it
// joins receipts to stores to pick up the display name for normalization,
// filters in SQL by (household, receipt_date) only, and does the final
// (normalized_store, total_within_tolerance) comparison in Go. That keeps
// the SQL simple and dodges the TEXT-money-column range-predicate trap
// (total is stored as "4.99", not 499 cents).
//
// Groups with an empty Date or empty Store are skipped — they can never
// match an existing receipt by design (PLAN §Receipt Grouping — such
// groups are surfaced to the user as error cards, not committable).
func CheckDuplicates(ctx context.Context, db *sql.DB, householdID string, groups []Group) (DuplicateMap, error) {
	out := DuplicateMap{}
	if len(groups) == 0 || householdID == "" {
		return out, nil
	}

	// Collect the distinct set of receipt_date values we need to query.
	// Skip groups with no date or no store — they can't be duplicates.
	dates := map[string]struct{}{}
	eligible := make([]Group, 0, len(groups))
	for _, g := range groups {
		if g.Date == "" || strings.TrimSpace(g.Store) == "" {
			continue
		}
		if _, err := time.Parse("2006-01-02", g.Date); err != nil {
			continue
		}
		eligible = append(eligible, g)
		dates[g.Date] = struct{}{}
	}
	if len(eligible) == 0 {
		return out, nil
	}

	// Build an IN placeholder list for the date set.
	dateList := make([]string, 0, len(dates))
	for d := range dates {
		dateList = append(dateList, d)
	}

	placeholders := strings.Repeat("?,", len(dateList))
	placeholders = strings.TrimSuffix(placeholders, ",")

	args := make([]any, 0, 1+len(dateList))
	args = append(args, householdID)
	for _, d := range dateList {
		args = append(args, d)
	}

	// NOTE: receipt_date is DATE in schema but modernc.org/sqlite serializes
	// time.Time via Go's default String() → "2026-03-12 00:00:00 +0000 UTC".
	// A raw text IN-compare would never match "2026-03-12". substr(…,1,10)
	// extracts the YYYY-MM-DD prefix so this works for both the scan worker
	// (time.Time write) and future writes that use a clean date string.
	query := fmt.Sprintf(
		`SELECT r.id, r.receipt_date, r.total, s.name
		 FROM receipts r
		 LEFT JOIN stores s ON s.id = r.store_id
		 WHERE r.household_id = ?
		   AND substr(r.receipt_date, 1, 10) IN (%s)`,
		placeholders,
	)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("duplicates: query receipts: %w", err)
	}
	defer rows.Close()

	// Bucket candidates by (date, normalized_store) for O(g) lookup below.
	type candidate struct {
		receiptID string
		cents     int64
	}
	bucket := map[string][]candidate{}

	for rows.Next() {
		var (
			id      string
			rdate   time.Time
			totalS  sql.NullString
			storeNS sql.NullString
		)
		if err := rows.Scan(&id, &rdate, &totalS, &storeNS); err != nil {
			return nil, fmt.Errorf("duplicates: scan: %w", err)
		}
		if !storeNS.Valid || strings.TrimSpace(storeNS.String) == "" {
			// A receipt with no store can't match a group (groups require a
			// non-empty store to even be eligible — see above).
			continue
		}
		cents, ok := decimalStringToCents(totalS.String)
		if !ok {
			// Row with an unparseable total — can't safely compare.
			continue
		}
		key := dupKey(rdate.Format("2006-01-02"), storeNS.String)
		bucket[key] = append(bucket[key], candidate{receiptID: id, cents: cents})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("duplicates: iterate: %w", err)
	}

	// For each eligible group, consult the bucket. Match the first
	// candidate within tolerance — if multiple receipts qualify, the
	// earliest-created one (the first row SQLite returned) wins; exposing
	// all of them would only confuse the duplicate-resolution UI.
	for _, g := range eligible {
		key := dupKey(g.Date, g.Store)
		cands, ok := bucket[key]
		if !ok {
			continue
		}
		for _, c := range cands {
			delta := c.cents - g.TotalCents
			if delta < 0 {
				delta = -delta
			}
			if delta <= dupTotalToleranceCents {
				out[g.ID] = c.receiptID
				break
			}
		}
	}

	return out, nil
}

// dupKey builds the bucket key for duplicate comparison. Both sides pass
// through NormalizeStoreName so a committed "Whole Foods #42" collides
// cleanly with an imported "whole foods ".
func dupKey(date, store string) string {
	return date + "|" + strings.ToLower(NormalizeStoreName(store))
}

// decimalStringToCents converts a money TEXT value ("4.99", "4.9", "5",
// "4.995") to integer cents. Returns (0, false) when the input is not
// parseable as a dollar amount. Rounds to the nearest cent to match the
// canonical form produced by centsToDecimalString.
func decimalStringToCents(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	negative := false
	if strings.HasPrefix(s, "-") {
		negative = true
		s = s[1:]
	}
	dot := strings.IndexByte(s, '.')
	var intPart, fracPart string
	if dot < 0 {
		intPart = s
		fracPart = ""
	} else {
		intPart = s[:dot]
		fracPart = s[dot+1:]
	}
	if intPart == "" && fracPart == "" {
		return 0, false
	}
	var cents int64
	for _, r := range intPart {
		if r < '0' || r > '9' {
			return 0, false
		}
		cents = cents*10 + int64(r-'0')
	}
	cents *= 100
	// Pad/truncate the fractional part to 3 digits so we can round-half-up
	// on the third digit.
	switch {
	case len(fracPart) == 0:
	case len(fracPart) == 1:
		fracPart = fracPart + "00"
	case len(fracPart) == 2:
		fracPart = fracPart + "0"
	case len(fracPart) > 3:
		fracPart = fracPart[:3]
	}
	if len(fracPart) == 3 {
		var frac int64
		for _, r := range fracPart {
			if r < '0' || r > '9' {
				return 0, false
			}
			frac = frac*10 + int64(r-'0')
		}
		// frac is in thousandths of a dollar; add tens-digit, round units.
		cents += frac / 10
		if frac%10 >= 5 {
			cents++
		}
	}
	if negative {
		cents = -cents
	}
	return cents, true
}
