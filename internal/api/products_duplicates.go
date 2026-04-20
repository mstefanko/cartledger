package api

import (
	"context"
	"database/sql"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/matcher"
)

// --- Types ---

// duplicatePairSide is one product in a surfaced duplicate pair.
type duplicatePairSide struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Brand           *string    `json:"brand"`
	Category        *string    `json:"category"`
	PurchaseCount   int        `json:"purchase_count"`
	LastPurchasedAt *time.Time `json:"last_purchased_at"`
	SampleAliases   []string   `json:"sample_aliases"`
}

// duplicatePair carries a canonical A/B pair (A.id < B.id) plus the score.
type duplicatePair struct {
	A          duplicatePairSide `json:"a"`
	B          duplicatePairSide `json:"b"`
	Similarity float64           `json:"similarity"`
}

// duplicateCandidatesResponse is the envelope returned by the endpoint.
type duplicateCandidatesResponse struct {
	Pairs []duplicatePair `json:"pairs"`
	Count int             `json:"count"`
}

// notDuplicateRequest is the body for POST/DELETE not-duplicate-pairs.
type notDuplicateRequest struct {
	ProductAID string `json:"product_a_id"`
	ProductBID string `json:"product_b_id"`
}

// Thresholds. The live matcher uses 0.7 to auto-match; this surface band is
// deliberately wider on the low side so humans see things the matcher
// wouldn't commit to, and capped on the high side so we don't flood the list
// with trivially close names that the matcher already handles.
const (
	duplicateDefaultMinSim = 0.6
	duplicateDefaultMaxSim = 0.85
	duplicateDefaultLimit  = 50
	duplicateMaxLimit      = 200
	// Guard rail: the O(n²) comparison scales quadratically. For <=2000 products
	// that's ~2M compares of short strings — fine. Above that, bail and warn.
	duplicateMaxProducts = 2000
)

// --- Handler methods ---

// DuplicateCandidates surfaces product pairs within a similarity band for
// human review. Pairs are canonicalized so A.id < B.id (matching the
// not_duplicate_pairs CHECK constraint) and sorted by similarity desc.
//
// GET /api/v1/products/duplicate-candidates
func (h *ProductHandler) DuplicateCandidates(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	ctx := c.Request().Context()

	limit := parseIntQuery(c, "limit", duplicateDefaultLimit)
	if limit <= 0 {
		limit = duplicateDefaultLimit
	}
	if limit > duplicateMaxLimit {
		limit = duplicateMaxLimit
	}
	minSim := parseFloatQuery(c, "min_similarity", duplicateDefaultMinSim)
	maxSim := parseFloatQuery(c, "max_similarity", duplicateDefaultMaxSim)
	if minSim < 0 {
		minSim = 0
	}
	if maxSim > 1 {
		maxSim = 1
	}
	if minSim > maxSim {
		// Caller passed something silly; return an empty result rather than
		// a 400 — this is a browse/read endpoint.
		return c.JSON(http.StatusOK, duplicateCandidatesResponse{Pairs: []duplicatePair{}, Count: 0})
	}

	// Load the household's products once (id + name + brand + category). This
	// is the full catalog — the O(n²) loop runs in Go below.
	type prodRow struct {
		id       string
		name     string
		brand    *string
		category *string
	}
	rows, err := h.DB.QueryContext(ctx,
		`SELECT id, name, brand, category FROM products WHERE household_id = ? ORDER BY id`,
		householdID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer rows.Close()

	products := make([]prodRow, 0, 256)
	for rows.Next() {
		var p prodRow
		if err := rows.Scan(&p.id, &p.name, &p.brand, &p.category); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		products = append(products, p)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// Guard rail: skip large catalogs to keep the endpoint responsive. Logged
	// so ops can see when a household has outgrown the in-Go comparison and
	// we need a smarter index (FTS5 + prefix buckets or a real similarity
	// function in SQLite).
	if len(products) > duplicateMaxProducts {
		slog.Warn("duplicate-candidates: catalog too large, skipping comparison",
			"household_id", householdID,
			"product_count", len(products),
			"threshold", duplicateMaxProducts,
		)
		return c.JSON(http.StatusOK, duplicateCandidatesResponse{Pairs: []duplicatePair{}, Count: 0})
	}

	// Load the not_duplicate_pairs set for this household in one shot. An
	// EXISTS subquery inside the O(n²) loop would hammer SQLite; a small
	// in-memory set is simpler and faster.
	dismissed, err := loadDismissedPairs(ctx, h.DB, householdID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// Compute pairs. Canonical order: products are already sorted by id
	// (ORDER BY id) so i < j gives us A.id < B.id for free.
	type rawPair struct {
		aIdx int
		bIdx int
		sim  float64
	}
	candidates := make([]rawPair, 0, 64)
	for i := 0; i < len(products); i++ {
		for j := i + 1; j < len(products); j++ {
			sim := matcher.Similarity(products[i].name, products[j].name)
			if sim < minSim || sim > maxSim {
				continue
			}
			if _, dismissed := dismissed[pairKey(products[i].id, products[j].id)]; dismissed {
				continue
			}
			candidates = append(candidates, rawPair{aIdx: i, bIdx: j, sim: sim})
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].sim != candidates[j].sim {
			return candidates[i].sim > candidates[j].sim
		}
		// Tie-break by ids for a deterministic ordering across runs.
		if products[candidates[i].aIdx].id != products[candidates[j].aIdx].id {
			return products[candidates[i].aIdx].id < products[candidates[j].aIdx].id
		}
		return products[candidates[i].bIdx].id < products[candidates[j].bIdx].id
	})

	total := len(candidates)
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	// Hydrate stats for the surviving pairs. v1 households have <200 pairs
	// surfaced, so N+1 is acceptable; if we outgrow this, batch into one
	// IN(...) query keyed by product id.
	resp := duplicateCandidatesResponse{
		Pairs: make([]duplicatePair, 0, len(candidates)),
		Count: total,
	}
	for _, cand := range candidates {
		a := products[cand.aIdx]
		b := products[cand.bIdx]
		aSide, err := hydrateDuplicateSide(ctx, h.DB, a.id, a.name, a.brand, a.category)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		bSide, err := hydrateDuplicateSide(ctx, h.DB, b.id, b.name, b.brand, b.category)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		resp.Pairs = append(resp.Pairs, duplicatePair{
			A:          aSide,
			B:          bSide,
			Similarity: round4(cand.sim),
		})
	}

	return c.JSON(http.StatusOK, resp)
}

// MarkNotDuplicate records that a pair of products should not appear in the
// duplicate-candidates feed. Canonicalizes order before insert to satisfy the
// CHECK (product_a_id < product_b_id) constraint and uses INSERT OR IGNORE
// for idempotency.
//
// POST /api/v1/products/not-duplicate-pairs
func (h *ProductHandler) MarkNotDuplicate(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)

	req, stop := parseNotDuplicateRequest(c)
	if stop {
		return nil
	}
	if stop := validateHouseholdPair(c, h.DB, householdID, req.ProductAID, req.ProductBID); stop {
		return nil
	}

	a, b := canonicalPair(req.ProductAID, req.ProductBID)
	if _, err := h.DB.Exec(
		`INSERT OR IGNORE INTO not_duplicate_pairs (household_id, product_a_id, product_b_id) VALUES (?, ?, ?)`,
		householdID, a, b,
	); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	return c.NoContent(http.StatusNoContent)
}

// UnmarkNotDuplicate removes a previously-dismissed pair so it can resurface
// in the duplicate-candidates feed. Same validation shape as the POST.
//
// DELETE /api/v1/products/not-duplicate-pairs
func (h *ProductHandler) UnmarkNotDuplicate(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)

	req, stop := parseNotDuplicateRequest(c)
	if stop {
		return nil
	}
	if stop := validateHouseholdPair(c, h.DB, householdID, req.ProductAID, req.ProductBID); stop {
		return nil
	}

	a, b := canonicalPair(req.ProductAID, req.ProductBID)
	if _, err := h.DB.Exec(
		`DELETE FROM not_duplicate_pairs WHERE household_id = ? AND product_a_id = ? AND product_b_id = ?`,
		householdID, a, b,
	); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	return c.NoContent(http.StatusNoContent)
}

// --- helpers ---

// parseNotDuplicateRequest reads + trims the JSON body. Returns (req, stop).
// When stop is true the caller should `return nil` — the error response has
// already been written. Using a bool flag (rather than a sentinel error)
// avoids the foot-gun where `c.JSON` returns nil on success and makes the
// caller accidentally proceed past validation (which is what happened in
// the first draft of this endpoint).
func parseNotDuplicateRequest(c echo.Context) (*notDuplicateRequest, bool) {
	var req notDuplicateRequest
	if err := c.Bind(&req); err != nil {
		_ = c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return nil, true
	}
	req.ProductAID = strings.TrimSpace(req.ProductAID)
	req.ProductBID = strings.TrimSpace(req.ProductBID)
	if req.ProductAID == "" || req.ProductBID == "" {
		_ = c.JSON(http.StatusBadRequest, map[string]string{"error": "product_a_id and product_b_id are required"})
		return nil, true
	}
	if req.ProductAID == req.ProductBID {
		_ = c.JSON(http.StatusBadRequest, map[string]string{"error": "product_a_id and product_b_id must differ"})
		return nil, true
	}
	return &req, false
}

// validateHouseholdPair ensures both ids refer to products in the caller's
// household. Returns true when the caller should abort — an error response
// has already been written. We deliberately don't distinguish "missing" from
// "cross-household" in the 404 so we don't leak existence across
// households.
func validateHouseholdPair(c echo.Context, db *sql.DB, householdID, aID, bID string) bool {
	var count int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM products WHERE household_id = ? AND id IN (?, ?)`,
		householdID, aID, bID,
	).Scan(&count); err != nil {
		_ = c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		return true
	}
	if count < 2 {
		_ = c.JSON(http.StatusNotFound, map[string]string{"error": "product not found"})
		return true
	}
	return false
}

// canonicalPair returns (lower, higher) by string compare so the pair matches
// the not_duplicate_pairs CHECK (product_a_id < product_b_id) constraint.
func canonicalPair(a, b string) (string, string) {
	if a < b {
		return a, b
	}
	return b, a
}

// pairKey builds the lookup key used against the dismissed-set loaded into
// memory. Same canonicalization as canonicalPair.
func pairKey(a, b string) string {
	lo, hi := canonicalPair(a, b)
	return lo + "|" + hi
}

// loadDismissedPairs returns a set of canonical pair keys ("a_id|b_id") for
// every dismissed pair in the household. Uses QueryContext so a caller
// cancellation propagates into the SQLite driver.
func loadDismissedPairs(ctx context.Context, db *sql.DB, householdID string) (map[string]struct{}, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT product_a_id, product_b_id FROM not_duplicate_pairs WHERE household_id = ?`,
		householdID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]struct{}, 16)
	for rows.Next() {
		var a, b string
		if err := rows.Scan(&a, &b); err != nil {
			return nil, err
		}
		out[a+"|"+b] = struct{}{}
	}
	return out, rows.Err()
}

// hydrateDuplicateSide loads purchase_count / last_purchased_at / sample
// aliases for one side of a surfaced pair.
func hydrateDuplicateSide(ctx context.Context, db *sql.DB, id, name string, brand, category *string) (duplicatePairSide, error) {
	side := duplicatePairSide{
		ID:            id,
		Name:          name,
		Brand:         brand,
		Category:      category,
		SampleAliases: []string{},
	}

	// purchase_count: line items referencing this product.
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM line_items WHERE product_id = ?`, id,
	).Scan(&side.PurchaseCount); err != nil {
		return side, err
	}

	// last_purchased_at: newest receipt_date across those line items.
	var last sql.NullTime
	if err := db.QueryRowContext(ctx,
		`SELECT MAX(r.receipt_date)
		 FROM receipts r JOIN line_items li ON li.receipt_id = r.id
		 WHERE li.product_id = ?`, id,
	).Scan(&last); err != nil {
		return side, err
	}
	if last.Valid {
		t := last.Time
		side.LastPurchasedAt = &t
	}

	// sample_aliases: up to 3 aliases, alphabetical so tests are deterministic.
	aliasRows, err := db.QueryContext(ctx,
		`SELECT alias FROM product_aliases WHERE product_id = ? ORDER BY alias LIMIT 3`, id,
	)
	if err != nil {
		return side, err
	}
	defer aliasRows.Close()
	for aliasRows.Next() {
		var a string
		if err := aliasRows.Scan(&a); err != nil {
			return side, err
		}
		side.SampleAliases = append(side.SampleAliases, a)
	}
	return side, aliasRows.Err()
}

// parseIntQuery reads an integer query param, returning def if missing or
// unparseable. Negative values pass through — the caller clamps.
func parseIntQuery(c echo.Context, name string, def int) int {
	raw := strings.TrimSpace(c.QueryParam(name))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return n
}

// parseFloatQuery reads a float query param, returning def if missing or
// unparseable.
func parseFloatQuery(c echo.Context, name string, def float64) float64 {
	raw := strings.TrimSpace(c.QueryParam(name))
	if raw == "" {
		return def
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return def
	}
	return f
}

// round4 rounds a float to 4 decimal places for stable JSON output. The
// underlying score is only meaningful to ~2 decimals but the extra digits
// make test assertions cheap to write without fighting floating-point drift.
func round4(f float64) float64 {
	return math.Round(f*10000) / 10000
}
