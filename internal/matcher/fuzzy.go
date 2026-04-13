package matcher

import (
	"database/sql"

	"github.com/lithammer/fuzzysearch/fuzzy"
)

// matchByAlias checks for an exact alias match. Store-specific aliases are checked
// first, then global aliases (store_id IS NULL).
func matchByAlias(db *sql.DB, normalized string, storeID string, householdID string) *MatchResult {
	// Try store-specific alias first (scoped to household via products table).
	var productID string
	err := db.QueryRow(
		`SELECT pa.product_id FROM product_aliases pa
		 JOIN products p ON pa.product_id = p.id
		 WHERE LOWER(pa.alias) = ? AND pa.store_id = ? AND p.household_id = ? LIMIT 1`,
		normalized, storeID, householdID,
	).Scan(&productID)
	if err == nil {
		return &MatchResult{
			ProductID:  productID,
			Confidence: 0.95,
			Method:     "alias",
		}
	}

	// Try global alias (scoped to household via products table).
	err = db.QueryRow(
		`SELECT pa.product_id FROM product_aliases pa
		 JOIN products p ON pa.product_id = p.id
		 WHERE LOWER(pa.alias) = ? AND pa.store_id IS NULL AND p.household_id = ? LIMIT 1`,
		normalized, householdID,
	).Scan(&productID)
	if err == nil {
		return &MatchResult{
			ProductID:  productID,
			Confidence: 0.95,
			Method:     "alias",
		}
	}

	return nil
}

// matchByFuzzy performs fuzzy matching against all product aliases and product names.
// Uses trigram similarity with a threshold of 0.7. Returns nil if no match meets the threshold.
func matchByFuzzy(db *sql.DB, normalized string, storeID string, householdID string) *MatchResult {
	type candidate struct {
		productID string
		name      string
	}

	var candidates []candidate

	// Gather aliases (store-specific first, then global), scoped to household via products.
	aliasRows, err := db.Query(
		`SELECT pa.product_id, LOWER(pa.alias) FROM product_aliases pa
		 JOIN products p ON pa.product_id = p.id
		 WHERE (pa.store_id = ? OR pa.store_id IS NULL) AND p.household_id = ?`,
		storeID, householdID,
	)
	if err == nil {
		defer aliasRows.Close()
		for aliasRows.Next() {
			var c candidate
			if err := aliasRows.Scan(&c.productID, &c.name); err == nil {
				candidates = append(candidates, c)
			}
		}
	}

	// Gather product names, scoped to household.
	prodRows, err := db.Query(`SELECT id, LOWER(name) FROM products WHERE household_id = ?`, householdID)
	if err == nil {
		defer prodRows.Close()
		for prodRows.Next() {
			var c candidate
			if err := prodRows.Scan(&c.productID, &c.name); err == nil {
				candidates = append(candidates, c)
			}
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	// Build target list and find best fuzzy match.
	var bestScore float64
	var bestProductID string

	for _, c := range candidates {
		// fuzzy.RankMatch returns -1 for no match, higher (closer to 0) is better.
		// We use fuzzy.RankMatchNormalizedFold for case-insensitive comparison.
		rank := fuzzy.RankMatchNormalizedFold(normalized, c.name)
		if rank == -1 {
			continue
		}

		// Convert rank to a 0-1 confidence score.
		// RankMatch returns lower values for better matches (0 = exact).
		// We use the ratio of matching characters to produce a confidence.
		score := calculateSimilarity(normalized, c.name)
		if score > bestScore {
			bestScore = score
			bestProductID = c.productID
		}
	}

	// Threshold: 0.7 minimum confidence.
	if bestScore < 0.7 {
		return nil
	}

	// Scale confidence to 0.5-0.9 range.
	confidence := 0.5 + (bestScore * 0.4)
	if confidence > 0.9 {
		confidence = 0.9
	}

	return &MatchResult{
		ProductID:  bestProductID,
		Confidence: confidence,
		Method:     "fuzzy",
	}
}

// calculateSimilarity computes a similarity score between 0 and 1 based on
// the Levenshtein distance relative to the longer string's length.
func calculateSimilarity(a, b string) float64 {
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	if maxLen == 0 {
		return 0
	}
	if a == b {
		return 1.0
	}

	dist := levenshtein(a, b)
	return 1.0 - float64(dist)/float64(maxLen)
}

// levenshtein computes the Levenshtein edit distance between two strings.
func levenshtein(a, b string) int {
	la := len(a)
	lb := len(b)

	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	// Use a single row for space efficiency.
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)

	for j := 0; j <= lb; j++ {
		prev[j] = j
	}

	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(
				prev[j]+1,      // deletion
				curr[j-1]+1,    // insertion
				prev[j-1]+cost, // substitution
			)
		}
		prev, curr = curr, prev
	}

	return prev[lb]
}

func min(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}
