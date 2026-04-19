package spreadsheet

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// sortCands is a small generic wrapper so the pair-scoring block in
// SuggestMapping stays readable.
func sortCands[T any](s []T, less func(a, b T) bool) {
	sort.Slice(s, func(i, j int) bool { return less(s[i], s[j]) })
}

// FindHeaderRow returns the index of the row most likely to be the column
// header, or 0 if rows is empty. Heuristic: score each row by what fraction
// of its non-empty cells look like "header words" (short, non-money, non-
// date, non-numeric, not containing punctuation other than spaces/slashes).
// We stop looking after the first 10 rows — banners never extend that far.
func FindHeaderRow(rows []RawRow) int {
	if len(rows) == 0 {
		return 0
	}

	bestIdx := 0
	bestScore := -1.0
	limit := 10
	if len(rows) < limit {
		limit = len(rows)
	}

	for i := 0; i < limit; i++ {
		score := headerishness(rows[i].Cells)
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}
	return bestIdx
}

// headerishness scores a row by both (a) what fraction of its non-empty
// cells look like header labels and (b) how many cells are filled. A one-
// cell banner row like "My Grocery Log 2026" scores lower than a three-cell
// "Date,Store,Item" row even when both score 1.0 on the fraction metric,
// because the score is count-of-headerish-cells, not a ratio. Returns -1
// for an all-empty row.
func headerishness(cells []string) float64 {
	headerish := 0
	nonEmpty := 0
	for _, c := range cells {
		v := strings.TrimSpace(c)
		if v == "" {
			continue
		}
		nonEmpty++
		if looksLikeHeader(v) {
			headerish++
		}
	}
	if nonEmpty == 0 {
		return -1
	}
	// Require that at least half the non-empty cells look like headers —
	// otherwise this row is almost certainly data, not a header.
	ratio := float64(headerish) / float64(nonEmpty)
	if ratio < 0.5 {
		return -1
	}
	return float64(headerish)
}

// looksLikeHeader is conservative: short string, not numeric, not money-
// shaped, not date-shaped, no more than ~40 chars.
func looksLikeHeader(s string) bool {
	if len(s) > 40 {
		return false
	}
	if looksLikeMoney(s) || looksLikeDate(s) || looksLikeInteger(s) {
		return false
	}
	// A header should have at least one letter.
	hasLetter := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			hasLetter = true
			break
		}
	}
	return hasLetter
}

// roleKeywords maps a Role to its header-word hints. Matches are substring
// based on a fold-cased header; order within each slice matters — the
// scorer picks the role whose best hint has the longest matched keyword.
var roleKeywords = map[Role][]string{
	RoleDate:       {"date", "purchase date", "dt", "when", "day", "txn date"},
	RoleStore:      {"store", "merchant", "vendor", "shop", "where", "location"},
	RoleItem:       {"item", "product", "description", "name", "what"},
	RoleQty:        {"quantity", "qty", "count", "#"},
	RoleUnit:       {"unit", "uom", "measure"},
	RoleUnitPrice:  {"unit price", "price per", "per unit", "unit cost", "each", "price each"},
	RoleTotalPrice: {"total", "amount", "subtotal", "extended", "line total", "price", "cost"},
	RoleTripID:     {"trip", "trip id", "receipt id", "receipt", "txn", "transaction"},
	RoleNotes:      {"note", "notes", "comment", "memo"},
}

// SuggestMapping returns a best-effort Mapping derived from header fuzzy-
// matching and, failing that, from per-column type coverage. It never binds
// the same column to two roles.
//
// Resolution is global-best-match, not role-by-role, so we don't lock a
// role to a merely-OK column before a better-matching column for the same
// role is considered. "Unit Price" / "Total" / "Price" all share tokens;
// scoring every (role, column, keyword) triple at once and then picking
// the highest-scoring assignments avoids the trap where RoleTotalPrice's
// short keyword "price" binds column "Unit Price" before RoleUnitPrice
// gets a look-in.
func SuggestMapping(sheet *ParsedSheet) Mapping {
	m := Mapping{}
	usedCol := map[int]bool{}
	usedRole := map[Role]bool{}

	type cand struct {
		role Role
		col  int
		kwLn int
	}

	// Score every (role, column) pair by its longest matching keyword.
	var cands []cand
	for role, keywords := range roleKeywords {
		for c, h := range sheet.Headers {
			hn := strings.ToLower(strings.TrimSpace(h))
			if hn == "" {
				continue
			}
			bestKw := 0
			for _, kw := range keywords {
				if matchesKeyword(hn, kw) && len(kw) > bestKw {
					bestKw = len(kw)
				}
			}
			if bestKw > 0 {
				cands = append(cands, cand{role: role, col: c, kwLn: bestKw})
			}
		}
	}

	// Sort: longest keyword match first; break ties by role priority then column
	// index so results are deterministic across runs.
	rolePriority := map[Role]int{
		RoleDate: 0, RoleStore: 1, RoleItem: 2, RoleQty: 3, RoleUnit: 4,
		RoleUnitPrice: 5, RoleTotalPrice: 6, RoleTripID: 7, RoleNotes: 8,
	}
	sortCands(cands, func(a, b cand) bool {
		if a.kwLn != b.kwLn {
			return a.kwLn > b.kwLn
		}
		if rolePriority[a.role] != rolePriority[b.role] {
			return rolePriority[a.role] < rolePriority[b.role]
		}
		return a.col < b.col
	})

	for _, c := range cands {
		if usedCol[c.col] || usedRole[c.role] {
			continue
		}
		m[c.role] = c.col
		usedCol[c.col] = true
		usedRole[c.role] = true
	}

	used := usedCol // alias for pass 2 below

	// Pass 2: sample-value fallback. If we still don't have a date, find a
	// column whose coverage is majority date. Same for total price.
	if _, ok := m[RoleDate]; !ok {
		if c := firstColWithCoverage(sheet, used, func(tc TypeCoverage) bool { return tc.DatePct >= 0.6 }); c >= 0 {
			m[RoleDate] = c
			used[c] = true
		}
	}
	if _, ok := m[RoleTotalPrice]; !ok {
		if c := firstColWithCoverage(sheet, used, func(tc TypeCoverage) bool { return tc.MoneyPct >= 0.6 }); c >= 0 {
			m[RoleTotalPrice] = c
			used[c] = true
		}
	}
	if _, ok := m[RoleUnitPrice]; !ok {
		if c := firstColWithCoverage(sheet, used, func(tc TypeCoverage) bool { return tc.MoneyPct >= 0.6 }); c >= 0 {
			m[RoleUnitPrice] = c
			used[c] = true
		}
	}

	return m
}

// matchesKeyword returns true when hn (already lowercased) contains kw as a
// whole-word or edge-anchored substring. We avoid bare substring because
// "pricing" should not match "price" in many cases — but in practice header
// words are short enough that substring containment is usually the right
// thing. We guard the common false positive ("price" vs "unit price") by
// matching longest keywords first (see SuggestMapping).
func matchesKeyword(hn, kw string) bool {
	kw = strings.ToLower(kw)
	if hn == kw {
		return true
	}
	if strings.Contains(hn, kw) {
		return true
	}
	return false
}

// firstColWithCoverage returns the lowest-index unused column whose type
// coverage satisfies pred, or -1.
func firstColWithCoverage(sheet *ParsedSheet, used map[int]bool, pred func(TypeCoverage) bool) int {
	for c := 0; c < len(sheet.Headers); c++ {
		if used[c] {
			continue
		}
		if pred(sheet.TypeCoverage[c]) {
			return c
		}
	}
	return -1
}

// DetectDateFormat returns the DateFormat that parses ≥80% of non-empty
// samples, preferring (in order): ISO, ISO-slash, Excel serial, US, EU,
// ISO-8601 timestamp. Returns DateFmtISO as a safe default when no format
// cleanly dominates.
func DetectDateFormat(samples []string) DateFormat {
	nonEmpty := 0
	for _, s := range samples {
		if strings.TrimSpace(s) != "" {
			nonEmpty++
		}
	}
	if nonEmpty == 0 {
		return DateFmtISO
	}

	candidates := []DateFormat{
		DateFmtISO,
		DateFmtISOSlash,
		DateFmtExcelSerial,
		DateFmtISO8601,
		DateFmtUS,
		DateFmtEU,
	}

	for _, df := range candidates {
		ok := 0
		for _, s := range samples {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			if _, err := NormalizeDate(s, df); err == nil {
				ok++
			}
		}
		if float64(ok)/float64(nonEmpty) >= 0.8 {
			return df
		}
	}
	return DateFmtISO
}

// ---- looks-like helpers — used by both detect and parser ------------------

var (
	// dateLike matches YYYY-MM-DD, YYYY/MM/DD, MM/DD/YYYY, DD/MM/YYYY, DD-MM-YYYY,
	// and 4-digit-year Excel timestamps. Deliberately permissive — NormalizeDate
	// rejects impossible month/day combinations.
	dateLikeRe = regexp.MustCompile(`^(\d{1,4}[/-]\d{1,2}[/-]\d{1,4})(T[\d:]+.*)?$`)

	// moneyLike matches optional sign, optional currency, digits-with-optional-
	// thousands-separators, optional decimal, or parenthesized negative.
	moneyLikeRe = regexp.MustCompile(`^\(?[\s]*[-+]?[\s]*[\$€£¥]?[\s]*\d{1,3}(,\d{3})*(\.\d+)?[\s]*\)?$|^\(?[\s]*[-+]?[\s]*[\$€£¥]?[\s]*\d+(\.\d+)?[\s]*\)?$`)
)

func looksLikeDate(s string) bool {
	s = strings.TrimSpace(s)
	return dateLikeRe.MatchString(s)
}

func looksLikeMoney(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	// Pure integers without any money-ish markers should NOT count as money —
	// a column of "2026" values is more likely years than dollars.
	if _, err := strconv.Atoi(s); err == nil {
		// Exception: a currency prefix like "$1234" will be caught by the
		// regex path; a bare integer falls through to integer bucket.
		return false
	}
	return moneyLikeRe.MatchString(s)
}

func looksLikeInteger(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	_, err := strconv.Atoi(s)
	return err == nil
}
