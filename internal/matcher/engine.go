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
