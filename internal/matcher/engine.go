package matcher

import (
	"database/sql"
)

// MatchResult describes how a raw receipt item name was matched to a product.
type MatchResult struct {
	ProductID  string  `json:"product_id"`
	Confidence float64 `json:"confidence"`
	Method     string  `json:"method"` // "rule", "alias", "fuzzy", "unmatched"
}

// Engine implements the three-stage product matching pipeline.
type Engine struct {
	db *sql.DB
}

// NewEngine creates a new matching engine backed by the given database.
func NewEngine(db *sql.DB) *Engine {
	return &Engine{db: db}
}

// Match runs the three-stage matching pipeline against a raw receipt item name.
//
// Stage 1: Rules — explicit matching rules by priority (confidence 1.0).
// Stage 2: Alias — exact alias match, store-specific first then global (confidence 0.95).
// Stage 3: Fuzzy — trigram similarity via fuzzy search (confidence 0.5-0.9).
// Default: unmatched (confidence 0).
func (e *Engine) Match(rawName string, storeID string, householdID string) MatchResult {
	normalized := Normalize(rawName)

	// Stage 1: Rules.
	if result := matchByRules(e.db, normalized, storeID, householdID); result != nil {
		return *result
	}

	// Stage 2: Alias exact match.
	if result := matchByAlias(e.db, normalized, storeID, householdID); result != nil {
		return *result
	}

	// Stage 3: Fuzzy matching.
	if result := matchByFuzzy(e.db, normalized, storeID, householdID); result != nil {
		return *result
	}

	return MatchResult{Method: "unmatched", Confidence: 0}
}

// MatchWithSuggestion extends the standard pipeline with suggested-name matching.
// After the 3-stage pipeline fails on raw_name, it runs additional stages using
// the LLM's suggested_name against existing product names and aliases.
//
// Stage 4: Exact match suggested_name against product names (case-insensitive).
// Stage 5: Fuzzy match suggested_name against product names + aliases.
//
// Matches from stages 4-5 are returned with Method="suggested" — they are
// proposals awaiting user confirmation, not finalized matches.
func (e *Engine) MatchWithSuggestion(rawName, suggestedName, storeID, householdID string) MatchResult {
	// Stages 1-3: standard pipeline on raw_name.
	result := e.Match(rawName, storeID, householdID)
	if result.Method != "unmatched" {
		return result
	}

	if suggestedName == "" {
		return result
	}

	// Stage 4: Exact match suggested_name against product names.
	if r := matchNameExact(e.db, suggestedName, householdID); r != nil {
		// Check store history: reduce confidence for cross-store matches.
		if hist := productHasStoreHistory(e.db, r.ProductID, storeID); hist == storeHistoryOtherStore {
			r.Confidence = 0.7
			r.Method = "cross_store_match"
		}
		return *r
	}

	// Stage 5: Fuzzy match suggested_name against product names + aliases.
	normalizedSuggestion := Normalize(suggestedName)
	if r := matchByFuzzy(e.db, normalizedSuggestion, storeID, householdID); r != nil {
		// Check store history: reduce confidence for cross-store matches.
		if hist := productHasStoreHistory(e.db, r.ProductID, storeID); hist == storeHistoryOtherStore {
			r.Confidence = 0.6
			r.Method = "cross_store_match"
		} else {
			r.Method = "suggested"
		}
		return *r
	}

	return MatchResult{Method: "unmatched", Confidence: 0}
}

// matchNameExact does a case-insensitive exact match of suggestedName against product names.
func matchNameExact(db *sql.DB, suggestedName string, householdID string) *MatchResult {
	var productID string
	err := db.QueryRow(
		`SELECT id FROM products WHERE household_id = ? AND LOWER(name) = LOWER(?) LIMIT 1`,
		householdID, suggestedName,
	).Scan(&productID)
	if err == nil {
		return &MatchResult{
			ProductID:  productID,
			Confidence: 0.92,
			Method:     "suggested",
		}
	}
	return nil
}
