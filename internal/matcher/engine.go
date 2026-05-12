package matcher

import (
	"context"
	"database/sql"
	"errors"

	"github.com/mstefanko/cartledger/internal/identifiers"
)

// MatchResult describes how a raw receipt item name was matched to a product.
type MatchResult struct {
	ProductID  string  `json:"product_id"`
	Confidence float64 `json:"confidence"`
	Method     string  `json:"method"` // "identifier", "code", "rule", "alias", "fuzzy", "suggested", "unmatched"
	Err        error   `json:"-"`
}

// Engine implements the three-stage product matching pipeline.
type Engine struct {
	db *sql.DB
}

type Input struct {
	RawName       string
	StoreItemCode string
	SuggestedName string
	StoreID       string
	HouseholdID   string
	Identifiers   []identifiers.Observation
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
	return e.MatchInput(context.Background(), Input{
		RawName:     rawName,
		StoreID:     storeID,
		HouseholdID: householdID,
	})
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
	return e.MatchInput(context.Background(), Input{
		RawName:       rawName,
		SuggestedName: suggestedName,
		StoreID:       storeID,
		HouseholdID:   householdID,
	})
}

func (e *Engine) MatchWithCodeAndSuggestion(rawName, storeItemCode, suggestedName, storeID, householdID string) MatchResult {
	return e.MatchInput(context.Background(), Input{
		RawName:       rawName,
		StoreItemCode: storeItemCode,
		SuggestedName: suggestedName,
		StoreID:       storeID,
		HouseholdID:   householdID,
	})
}

func (e *Engine) MatchInput(ctx context.Context, input Input) MatchResult {
	normalized := Normalize(input.RawName)

	if result, err := matchByIdentifier(ctx, e.db, input.HouseholdID, input.Identifiers); err != nil {
		return MatchResult{Method: "unmatched", Err: err}
	} else if result != nil {
		return *result
	}

	if result := matchByCode(e.db, input.StoreItemCode, input.StoreID, input.HouseholdID); result != nil {
		return *result
	}

	if result := matchByRules(e.db, normalized, input.StoreID, input.HouseholdID); result != nil {
		return *result
	}

	if result := matchByAlias(e.db, normalized, input.StoreID, input.HouseholdID); result != nil {
		return *result
	}

	if result := matchByFuzzy(e.db, normalized, input.StoreID, input.HouseholdID); result != nil {
		return *result
	}

	return e.matchSuggestion(input, MatchResult{Method: "unmatched", Confidence: 0})
}

func (e *Engine) matchSuggestion(input Input, result MatchResult) MatchResult {
	if input.SuggestedName == "" {
		return result
	}

	if r, err := matchNameExact(e.db, input.SuggestedName, input.HouseholdID); err != nil {
		return MatchResult{Method: "unmatched", Err: err}
	} else if r != nil {
		if hist := productHasStoreHistory(e.db, r.ProductID, input.StoreID); hist == storeHistoryOtherStore {
			r.Confidence = 0.7
			r.Method = "cross_store_match"
		}
		return *r
	}

	normalizedSuggestion := Normalize(input.SuggestedName)
	if r := matchByFuzzy(e.db, normalizedSuggestion, input.StoreID, input.HouseholdID); r != nil {
		if hist := productHasStoreHistory(e.db, r.ProductID, input.StoreID); hist == storeHistoryOtherStore {
			r.Confidence = 0.6
			r.Method = "cross_store_match"
		} else {
			r.Method = "suggested"
		}
		return *r
	}

	return result
}

// matchNameExact does a case-insensitive exact match of suggestedName against product names.
func matchNameExact(db *sql.DB, suggestedName string, householdID string) (*MatchResult, error) {
	var productID string
	normalizedName := NormalizeProductName(suggestedName)
	err := db.QueryRow(
		`SELECT id
		   FROM products
			  WHERE household_id = ?
			    AND (name_normalized = ? OR (name_normalized IS NULL AND LOWER(name) = LOWER(?)))
			  ORDER BY updated_at DESC, id ASC
			  LIMIT 1`,
		householdID, normalizedName, suggestedName,
	).Scan(&productID)
	if err == nil {
		return &MatchResult{
			ProductID:  productID,
			Confidence: 0.92,
			Method:     "suggested",
		}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return nil, nil
}
