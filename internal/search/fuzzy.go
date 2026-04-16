// Package search provides fuzzy-search helpers shared across the HTTP API.
//
// It implements a two-stage strategy:
//
//  1. DB-side FTS5 prefix match against products_fts (name, category, brand)
//     — fast, handles prefix queries like "tortil" → "Tortilla Wraps".
//  2. In-memory re-rank with github.com/sahilm/fuzzy — always used to improve
//     ordering, and also falls back to scanning the full household catalog
//     when stage 1 returns few hits (to catch typos like "trotilla").
//
// The in-memory fallback is gated: it only runs when stage 1 returned
// fewer than stage2HitThreshold results AND the household's product count
// is below stage2CatalogCap, so very large catalogs don't pay for a full
// table scan on every keystroke.
//
// GroupIDs does not use FTS5 — product_groups has no FTS table and the
// per-household group count is always small (tens), so a pure in-memory
// fuzzy rank is cheap.
//
// This package must not import internal/matcher — that package is for a
// different problem (receipt-line reconciliation against aliases).
package search

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/sahilm/fuzzy"
)

// stage2HitThreshold: when stage-1 FTS returns fewer than this many hits,
// and the catalog is small enough, we also run an in-memory fuzzy pass so
// typos can still match.
const stage2HitThreshold = 20

// stage2CatalogCap: above this many products per household, the stage-2
// in-memory scan is skipped (we trust the FTS5 result on its own).
const stage2CatalogCap = 5000

// minTokenLen: tokens shorter than this are dropped from the FTS MATCH
// expression. FTS5 prefix queries on 1-char tokens are very loose and tend
// to match everything.
const minTokenLen = 2

// sanitizeFTSPrefixToken converts a single raw user token into an FTS5
// prefix-match term of the form `"tok"*`.
//
// Contract:
//   - Lower-cases the input.
//   - Escapes any embedded `"` as `""` (SQLite FTS5 quoted-string rule).
//   - Returns "" for tokens shorter than minTokenLen runes (caller must
//     skip empty results).
//   - Wraps the escaped form in double quotes and appends `*` so FTS5
//     treats the token as a literal prefix, neutralising operators like
//     `NEAR(`, `^`, `*`, `'`, `\`, and non-ASCII characters.
//
// Fuzz corpus in fuzzy_test.go exercises: `"`, `*`, `^`, `NEAR(`, `'`,
// `\`, and Unicode.
func sanitizeFTSPrefixToken(tok string) string {
	tok = strings.ToLower(strings.TrimSpace(tok))
	if utf8.RuneCountInString(tok) < minTokenLen {
		return ""
	}
	escaped := strings.ReplaceAll(tok, `"`, `""`)
	return `"` + escaped + `"*`
}

// buildMatchExpression joins sanitized prefix tokens with spaces. Returns
// "" when no token survives sanitization — caller should skip the FTS
// query entirely in that case.
func buildMatchExpression(q string) string {
	parts := strings.Fields(q)
	if len(parts) == 0 {
		return ""
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		s := sanitizeFTSPrefixToken(p)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return strings.Join(out, " ")
}

// ProductIDs returns product IDs in rank order for householdID matching q.
//
// Stage 1: FTS5 prefix MATCH on products_fts (name, category, brand).
// Stage 2: if stage-1 hits are sparse AND catalog is small, also scan
// name+brand with sahilm/fuzzy and merge in any new IDs.
//
// The return slice is capped at limit (if limit > 0). When q is empty or
// contains no tokens >= minTokenLen runes, ProductIDs returns a nil slice
// with no error — callers should treat this as "no search filter".
func ProductIDs(ctx context.Context, db *sql.DB, householdID, q string, limit int) ([]string, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	match := buildMatchExpression(q)
	if match == "" {
		return nil, nil
	}

	// Stage 1: FTS5 prefix match, ordered by FTS rank.
	stage1IDs, stage1Names, err := ftsProductCandidates(ctx, db, householdID, match)
	if err != nil {
		return nil, err
	}

	// In-memory re-rank of stage-1 hits by query similarity to name.
	rerankIDs := rerankByFuzzy(q, stage1IDs, stage1Names)

	// Stage 2 gate: only fall back to full-catalog scan when stage 1 was
	// sparse AND the household's catalog is small.
	if len(rerankIDs) < stage2HitThreshold {
		count, cerr := countHouseholdProducts(ctx, db, householdID)
		if cerr != nil {
			return nil, cerr
		}
		if count < stage2CatalogCap {
			extraIDs, eerr := fullCatalogFuzzy(ctx, db, householdID, q)
			if eerr != nil {
				return nil, eerr
			}
			rerankIDs = mergeUniqueKeepOrder(rerankIDs, extraIDs)
		}
	}

	if limit > 0 && len(rerankIDs) > limit {
		rerankIDs = rerankIDs[:limit]
	}
	return rerankIDs, nil
}

// GroupIDs returns product-group IDs for householdID matching q, ranked
// in-memory with sahilm/fuzzy. No FTS5 — product_groups has no FTS table
// and per-household group counts are small (tens).
//
// When q is empty, returns nil (caller should not filter).
func GroupIDs(ctx context.Context, db *sql.DB, householdID, q string, limit int) ([]string, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}

	rows, err := db.QueryContext(
		ctx,
		`SELECT id, name FROM product_groups WHERE household_id = ?`,
		householdID,
	)
	if err != nil {
		return nil, fmt.Errorf("search.GroupIDs: load groups: %w", err)
	}
	defer rows.Close()

	var ids, names []string
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("search.GroupIDs: scan: %w", err)
		}
		ids = append(ids, id)
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search.GroupIDs: rows: %w", err)
	}

	ranked := rerankByFuzzy(q, ids, names)
	if limit > 0 && len(ranked) > limit {
		ranked = ranked[:limit]
	}
	return ranked, nil
}

// ftsProductCandidates runs the stage-1 FTS5 prefix MATCH and returns
// parallel id/name slices in FTS rank order. Empty result + nil error
// means "no match", not an error.
func ftsProductCandidates(ctx context.Context, db *sql.DB, householdID, match string) ([]string, []string, error) {
	rows, err := db.QueryContext(
		ctx,
		`SELECT p.id, p.name
		 FROM products p
		 JOIN products_fts f ON p.rowid = f.rowid
		 WHERE products_fts MATCH ? AND p.household_id = ?
		 ORDER BY rank`,
		match, householdID,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("search.ProductIDs: stage1 FTS: %w", err)
	}
	defer rows.Close()

	var ids, names []string
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, nil, fmt.Errorf("search.ProductIDs: stage1 scan: %w", err)
		}
		ids = append(ids, id)
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("search.ProductIDs: stage1 rows: %w", err)
	}
	return ids, names, nil
}

// countHouseholdProducts returns the total number of products for the
// given household. Used to gate the stage-2 full-catalog fuzzy scan.
func countHouseholdProducts(ctx context.Context, db *sql.DB, householdID string) (int, error) {
	var count int
	err := db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM products WHERE household_id = ?`,
		householdID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("search.ProductIDs: count: %w", err)
	}
	return count, nil
}

// fullCatalogFuzzy loads every product name + brand for the household and
// ranks them with sahilm/fuzzy. This is the typo-tolerance path.
func fullCatalogFuzzy(ctx context.Context, db *sql.DB, householdID, q string) ([]string, error) {
	rows, err := db.QueryContext(
		ctx,
		`SELECT id, name, COALESCE(brand, '') FROM products WHERE household_id = ?`,
		householdID,
	)
	if err != nil {
		return nil, fmt.Errorf("search.ProductIDs: stage2 load: %w", err)
	}
	defer rows.Close()

	var ids, haystack []string
	for rows.Next() {
		var id, name, brand string
		if err := rows.Scan(&id, &name, &brand); err != nil {
			return nil, fmt.Errorf("search.ProductIDs: stage2 scan: %w", err)
		}
		ids = append(ids, id)
		// Fuzzy-match against concatenated name + brand so brand hits count.
		hay := name
		if brand != "" {
			hay = name + " " + brand
		}
		haystack = append(haystack, hay)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search.ProductIDs: stage2 rows: %w", err)
	}

	return rerankByFuzzy(q, ids, haystack), nil
}

// rerankByFuzzy runs sahilm/fuzzy.Find over haystack (lower-cased) and
// returns the corresponding ids slice in fuzzy-rank order. Ids without a
// match are dropped. ids and haystack MUST be parallel (same length).
func rerankByFuzzy(q string, ids, haystack []string) []string {
	if len(ids) == 0 || len(ids) != len(haystack) {
		return nil
	}
	lowered := make([]string, len(haystack))
	for i, h := range haystack {
		lowered[i] = strings.ToLower(h)
	}
	matches := fuzzy.Find(strings.ToLower(q), lowered)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if m.Index < 0 || m.Index >= len(ids) {
			continue
		}
		out = append(out, ids[m.Index])
	}
	return out
}

// mergeUniqueKeepOrder returns primary followed by any IDs in extra that
// are not already in primary, preserving the order of each input.
func mergeUniqueKeepOrder(primary, extra []string) []string {
	if len(extra) == 0 {
		return primary
	}
	seen := make(map[string]struct{}, len(primary)+len(extra))
	out := make([]string, 0, len(primary)+len(extra))
	for _, id := range primary {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for _, id := range extra {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
