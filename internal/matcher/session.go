package matcher

import (
	"database/sql"

	"github.com/lithammer/fuzzysearch/fuzzy"
)

// Session caches the fuzzy-match candidate set and per-product store-history
// lookups for a single (householdID, storeID) batch — typically one receipt
// from the worker or one spreadsheet commit group. A Session is NOT safe to
// share across goroutines; construct one per in-flight batch and discard it.
//
// Semantics: the candidate set is a snapshot taken at NewSession time. Aliases
// inserted after NewSession returns are NOT visible to Match / MatchWithSuggestion
// on this Session — callers that need to see within-batch alias writes should
// scope the Session appropriately (e.g. session-per-group in the spreadsheet
// commit path).
//
// Memory: a household with ~10k products + ~20k aliases produces ~30k candidates
// (~80 bytes each → ~2.4MB per session). Two concurrent workers is the current
// steady-state upper bound (~5MB transient).
//
// The Session's Match / MatchWithSuggestion methods preserve byte-for-byte
// parity with Engine.Match / Engine.MatchWithSuggestion: same stage ordering
// (rules → alias → fuzzy [→ suggested-exact → suggested-fuzzy]), same
// confidence values, same Method strings, same fuzzy tie-break (aliases-first
// insertion order; first-seen wins on score equality).
type Session struct {
	db          *sql.DB
	householdID string
	storeID     string

	// candidates is the preloaded fuzzy candidate set. It mirrors the two
	// queries issued by matchByFuzzy at fuzzy.go:57-62 (aliases) and
	// fuzzy.go:74 (product names), in that order. Preserving the insertion
	// order is load-bearing: the fuzzy scoring loop uses first-seen-wins on
	// score ties, matching the original Engine behavior.
	candidates []fuzzyCandidate

	// storeHistory caches productHasStoreHistory(productID, s.storeID) results
	// lazily — the first stage-4/5 hit on a productID queries the DB, all
	// subsequent lookups hit memory.
	storeHistory map[string]storeHistoryState
}

// fuzzyCandidate is the in-memory form of an alias or product-name row used by
// the cached fuzzy scoring loop.
type fuzzyCandidate struct {
	productID string
	name      string // lowercased, matching the LOWER(...) projection in fuzzy.go
}

// NewSession builds a Session for (householdID, storeID). It issues the two
// fuzzy-candidate queries ONCE — store-scoped-or-global aliases, then product
// names — and returns the resulting Session.
//
// Callers should treat errors as fall-back-to-one-shot signals rather than
// fatal: the caller can still invoke Engine.Match / Engine.MatchWithSuggestion
// per item on the underlying Engine. See worker/receipt.go and
// spreadsheet/commit.go for the fallback pattern.
func (e *Engine) NewSession(householdID, storeID string) (*Session, error) {
	s := &Session{
		db:           e.db,
		householdID:  householdID,
		storeID:      storeID,
		storeHistory: map[string]storeHistoryState{},
	}

	// Aliases (store-specific OR global), scoped to household via products.
	// Mirrors matchByFuzzy at fuzzy.go:57-62.
	aliasRows, err := e.db.Query(
		`SELECT pa.product_id, LOWER(pa.alias) FROM product_aliases pa
		 JOIN products p ON pa.product_id = p.id
		 WHERE (pa.store_id = ? OR pa.store_id IS NULL) AND p.household_id = ?`,
		storeID, householdID,
	)
	if err != nil {
		return nil, err
	}
	for aliasRows.Next() {
		var c fuzzyCandidate
		if err := aliasRows.Scan(&c.productID, &c.name); err == nil {
			s.candidates = append(s.candidates, c)
		}
	}
	aliasRows.Close()

	// Product names, scoped to household. Mirrors matchByFuzzy at fuzzy.go:74.
	prodRows, err := e.db.Query(
		`SELECT id, LOWER(name) FROM products WHERE household_id = ?`,
		householdID,
	)
	if err != nil {
		return nil, err
	}
	for prodRows.Next() {
		var c fuzzyCandidate
		if err := prodRows.Scan(&c.productID, &c.name); err == nil {
			s.candidates = append(s.candidates, c)
		}
	}
	prodRows.Close()

	return s, nil
}

// Match mirrors Engine.Match but uses the cached fuzzy candidates for stage 3.
// Stages 1 (rules) and 2 (alias-exact) continue to query the DB — they are
// already indexed lookups with negligible per-call cost.
func (s *Session) Match(rawName string) MatchResult {
	normalized := Normalize(rawName)

	// Stage 1: Rules.
	if result := matchByRules(s.db, normalized, s.storeID, s.householdID); result != nil {
		return *result
	}

	// Stage 2: Alias exact match.
	if result := matchByAlias(s.db, normalized, s.storeID, s.householdID); result != nil {
		return *result
	}

	// Stage 3: Fuzzy matching against cached candidates.
	if result := s.matchByFuzzyCached(normalized); result != nil {
		return *result
	}

	return MatchResult{Method: "unmatched", Confidence: 0}
}

// MatchWithSuggestion mirrors Engine.MatchWithSuggestion. Stages 1-3 go through
// Session.Match; stage 4 (exact suggested match) hits the DB directly (indexed
// on name, small result); stage 5 uses the cached fuzzy candidates. The
// productHasStoreHistory check is served from the per-session lazy cache.
func (s *Session) MatchWithSuggestion(rawName, suggestedName string) MatchResult {
	// Stages 1-3: standard pipeline on raw_name.
	result := s.Match(rawName)
	if result.Method != "unmatched" {
		return result
	}

	if suggestedName == "" {
		return result
	}

	// Stage 4: Exact match suggested_name against product names.
	if r := matchNameExact(s.db, suggestedName, s.householdID); r != nil {
		if hist := s.productHasStoreHistoryCached(r.ProductID); hist == storeHistoryOtherStore {
			r.Confidence = 0.7
			r.Method = "cross_store_match"
		}
		return *r
	}

	// Stage 5: Fuzzy match suggested_name against cached candidates.
	normalizedSuggestion := Normalize(suggestedName)
	if r := s.matchByFuzzyCached(normalizedSuggestion); r != nil {
		if hist := s.productHasStoreHistoryCached(r.ProductID); hist == storeHistoryOtherStore {
			r.Confidence = 0.6
			r.Method = "cross_store_match"
		} else {
			r.Method = "suggested"
		}
		return *r
	}

	return MatchResult{Method: "unmatched", Confidence: 0}
}

// matchByFuzzyCached is a byte-for-byte duplicate of the scoring loop in
// matchByFuzzy (fuzzy.go:89-127), operating on the preloaded s.candidates
// instead of issuing two DB queries per call. Same fuzzy.RankMatchNormalizedFold
// gate, same calculateSimilarity scoring, same 0.7 threshold, same
// 0.5 + score*0.4 confidence curve capped at 0.9, same first-seen-wins
// tie-break.
//
// The loop is intentionally duplicated rather than refactoring matchByFuzzy to
// take a `[]fuzzyCandidate` — the plan explicitly accepts local duplication to
// keep the diff small and the semantics obvious. See fuzzy.go for the canonical
// implementation.
func (s *Session) matchByFuzzyCached(normalized string) *MatchResult {
	if len(s.candidates) == 0 {
		return nil
	}

	var bestScore float64
	var bestProductID string

	for _, c := range s.candidates {
		// fuzzy.RankMatchNormalizedFold returns -1 for no match.
		if fuzzy.RankMatchNormalizedFold(normalized, c.name) == -1 {
			continue
		}
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

// productHasStoreHistoryCached serves productHasStoreHistory from the
// per-session lazy cache. On first call for a given productID the underlying
// query fires; subsequent calls hit memory.
func (s *Session) productHasStoreHistoryCached(productID string) storeHistoryState {
	if v, ok := s.storeHistory[productID]; ok {
		return v
	}
	v := productHasStoreHistory(s.db, productID, s.storeID)
	s.storeHistory[productID] = v
	return v
}
