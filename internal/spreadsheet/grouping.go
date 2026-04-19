package spreadsheet

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// GroupRows collapses ParsedValues into receipt-level Groups according to
// g.Strategy. Group IDs are stable across preview calls for the same config
// ("g0", "g1", ...) so frontend can track selection state across refreshes.
//
// Empty dates (rows the user hasn't corrected yet) are intentionally *not*
// merged with valid-date rows — they land in a dedicated error group with
// Date="" so the UI can show "12 rows missing a date" as one card.
//
// Receipts with missing store AND no DefaultStoreID fall into their own
// placeholder group with Store="" — same reasoning.
func GroupRows(rows []ParsedValue, g Grouping) []Group {
	switch g.Strategy {
	case GroupTripIDColumn:
		return groupByTripID(rows)
	case GroupExplicitMarker:
		return groupByExplicitRanges(rows, g.ExplicitRanges)
	case GroupDateStoreTotal:
		groups := groupByDateStore(rows, g)
		return splitByTotalMarker(rows, groups)
	case GroupDateStore:
		fallthrough
	default:
		return groupByDateStore(rows, g)
	}
}

// groupByDateStore produces one group per unique (date, store) combination.
// When a row has an unmapped Store and no DefaultStoreID, it's grouped under
// store="" which surfaces in the UI as "Unassigned store".
func groupByDateStore(rows []ParsedValue, g Grouping) []Group {
	type key struct{ date, store string }
	acc := map[key]*Group{}
	// Keep insertion order so group IDs are deterministic across equivalent
	// inputs. Go maps iterate in random order, so we track keys in a slice.
	var order []key

	for _, r := range rows {
		store := r.Store
		if store == "" {
			store = g.DefaultStoreID
		}
		k := key{date: r.Date, store: store}
		grp, ok := acc[k]
		if !ok {
			grp = &Group{Store: store, Date: r.Date}
			acc[k] = grp
			order = append(order, k)
		}
		grp.RowIndices = append(grp.RowIndices, r.RowIndex)
		grp.TotalCents += r.TotalCents
	}

	return assignIDs(materialize(acc, order))
}

// groupByTripID uses the TripID role. Rows with an empty TripID form a
// single catch-all group with ID-shaped key "". This mirrors the "missing
// store" fallback in groupByDateStore — users get visibility before commit.
func groupByTripID(rows []ParsedValue) []Group {
	acc := map[string]*Group{}
	var order []string

	for _, r := range rows {
		grp, ok := acc[r.TripID]
		if !ok {
			grp = &Group{Store: r.Store, Date: r.Date}
			acc[r.TripID] = grp
			order = append(order, r.TripID)
		}
		grp.RowIndices = append(grp.RowIndices, r.RowIndex)
		grp.TotalCents += r.TotalCents
		// A trip might span multiple stores/dates by user error; keep the
		// first-seen pair for rendering.
	}

	out := make([]*Group, 0, len(order))
	for _, id := range order {
		out = append(out, acc[id])
	}
	return assignIDs(out)
}

// groupByExplicitRanges bucketizes rows into the user-supplied inclusive
// ranges over ParsedValue.RowIndex. Rows outside any range land in a
// trailing "ungrouped" bucket so they're still visible.
func groupByExplicitRanges(rows []ParsedValue, ranges [][2]int) []Group {
	// Sort ranges for binary search later.
	sorted := make([][2]int, len(ranges))
	copy(sorted, ranges)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i][0] < sorted[j][0] })

	rangeGroups := make([]*Group, len(sorted))
	for i := range sorted {
		rangeGroups[i] = &Group{}
	}
	var ungrouped *Group

	for _, r := range rows {
		idx := findRangeIndex(sorted, r.RowIndex)
		var g *Group
		if idx < 0 {
			if ungrouped == nil {
				ungrouped = &Group{Store: r.Store, Date: r.Date}
			}
			g = ungrouped
		} else {
			g = rangeGroups[idx]
			if g.Store == "" {
				g.Store = r.Store
			}
			if g.Date == "" {
				g.Date = r.Date
			}
		}
		g.RowIndices = append(g.RowIndices, r.RowIndex)
		g.TotalCents += r.TotalCents
	}

	out := make([]*Group, 0, len(rangeGroups)+1)
	for _, g := range rangeGroups {
		if len(g.RowIndices) > 0 {
			out = append(out, g)
		}
	}
	if ungrouped != nil {
		out = append(out, ungrouped)
	}
	return assignIDs(out)
}

// findRangeIndex returns the index of the range containing rowIdx, or -1.
func findRangeIndex(ranges [][2]int, rowIdx int) int {
	for i, r := range ranges {
		if rowIdx >= r[0] && rowIdx <= r[1] {
			return i
		}
	}
	return -1
}

// splitByTotalMarker walks each date_store group and breaks it whenever a
// row's TotalCents roughly equals the sum of items preceding it within the
// same group (tolerance ±$0.50). This catches trip-per-day sheets that have
// a "TOTAL" row between trips.
//
// The implementation is a second pass over the pre-computed groups: we
// preserve original group boundaries, then for each group we walk its rows
// in order and emit sub-groups each time a total marker is hit. Stable
// ordering of input is essential — we rely on row indices being
// monotonically increasing within a group.
func splitByTotalMarker(rows []ParsedValue, groups []Group) []Group {
	byIdx := make(map[int]ParsedValue, len(rows))
	for _, r := range rows {
		byIdx[r.RowIndex] = r
	}

	out := make([]Group, 0, len(groups))
	for _, g := range groups {
		// Preserve user's row ordering within the group.
		sort.Ints(g.RowIndices)
		current := Group{Store: g.Store, Date: g.Date}
		runningSum := int64(0)

		for _, ri := range g.RowIndices {
			pv := byIdx[ri]
			if isTotalMarker(pv, runningSum) && len(current.RowIndices) > 0 {
				// Close the current sub-group, start a new one *after* the
				// total row. The total row itself is dropped from line items
				// (it's not an item; it's a marker) but we keep it in the
				// row index list so UI can still reference it if needed.
				current.RowIndices = append(current.RowIndices, ri)
				out = append(out, current)
				current = Group{Store: g.Store, Date: g.Date}
				runningSum = 0
				continue
			}
			current.RowIndices = append(current.RowIndices, ri)
			current.TotalCents += pv.TotalCents
			runningSum += pv.TotalCents
		}
		if len(current.RowIndices) > 0 {
			out = append(out, current)
		}
	}
	return assignIDs(toPtrSlice(out))
}

// isTotalMarker returns true when pv looks like a "subtotal" row — its
// TotalCents is within ±$0.50 of the running sum of preceding items AND it
// either has no item text or the item text contains "total"/"subtotal".
func isTotalMarker(pv ParsedValue, runningSum int64) bool {
	if pv.TotalCents == 0 || runningSum == 0 {
		return false
	}
	delta := pv.TotalCents - runningSum
	if delta < 0 {
		delta = -delta
	}
	if delta > 50 { // 50 cents
		return false
	}
	it := strings.ToLower(pv.Item)
	if it == "" {
		return true
	}
	return strings.Contains(it, "total") || strings.Contains(it, "subtotal")
}

// ApplySplitSuggested sets g.SplitSuggested on each group when any of:
//   - len(RowIndices) > 40, or
//   - There's a mid-group row whose TotalCents ≈ sum of preceding items
//     within the same group (within 50 cents).
//
// The time-column > 2h heuristic from the plan is deferred to post-v1 since
// no time role exists yet.
func ApplySplitSuggested(rows []ParsedValue, groups []Group) []Group {
	byIdx := make(map[int]ParsedValue, len(rows))
	for _, r := range rows {
		byIdx[r.RowIndex] = r
	}

	out := make([]Group, len(groups))
	copy(out, groups)
	for i := range out {
		g := &out[i]
		if len(g.RowIndices) > 40 {
			g.SplitSuggested = true
			continue
		}
		if hasInlineTotalSignal(g, byIdx) {
			g.SplitSuggested = true
		}
	}
	return out
}

// hasInlineTotalSignal detects a mid-group total-marker (not at the end).
// Walks rows in stable order, tracking the running sum; triggers only when
// a candidate total sits strictly *before* the last row.
func hasInlineTotalSignal(g *Group, byIdx map[int]ParsedValue) bool {
	indices := make([]int, len(g.RowIndices))
	copy(indices, g.RowIndices)
	sort.Ints(indices)

	var running int64
	for i, ri := range indices {
		pv := byIdx[ri]
		if i < len(indices)-1 && isTotalMarker(pv, running) {
			return true
		}
		running += pv.TotalCents
	}
	return false
}

// ---- helpers --------------------------------------------------------------

func assignIDs(groups []*Group) []Group {
	out := make([]Group, len(groups))
	for i, g := range groups {
		if g == nil {
			continue
		}
		g.ID = fmt.Sprintf("g%d", i)
		// Sort RowIndices for stable rendering.
		sort.Ints(g.RowIndices)
		out[i] = *g
	}
	return out
}

// materialize returns an ordered pointer slice of *Group keyed by the
// insertion-order slice `order`. Used by groupByDateStore where acc is keyed
// by a struct. Unused here but kept for clarity.
func materialize[K comparable](acc map[K]*Group, order []K) []*Group {
	out := make([]*Group, 0, len(order))
	for _, k := range order {
		out = append(out, acc[k])
	}
	return out
}

func toPtrSlice(groups []Group) []*Group {
	out := make([]*Group, len(groups))
	for i := range groups {
		g := groups[i]
		out[i] = &g
	}
	return out
}

// roundCentsFloat rounds a float dollar amount to the nearest cent.
// Kept as a file-local helper in case future strategies need it.
var _ = math.Round
