package matcher

// Golden-file regression tests for the product matcher.
//
// These tests lock in the current behavior of the fuzzy-scoring stage that
// Engine.Match uses internally. They do NOT hit the database — instead they
// replay the same two-step selection (fuzzy.RankMatchNormalizedFold filter,
// then Levenshtein-based calculateSimilarity score, then 0.7 threshold, then
// scale to 0.5-0.9) against in-memory fixture products. This way the tests
// survive schema migrations: the fixture is pure Go structs.
//
// If any of these tests fails after a matcher change, review the change —
// a ranking regression is almost always a user-visible bug. If the change
// is intentional, update the expected band here.

import (
	"testing"

	"github.com/lithammer/fuzzysearch/fuzzy"
)

// goldenProduct is a lightweight stand-in for models.Product that captures
// only the fields the matcher actually consumes: ID and a name/alias that
// gets normalized and compared.
type goldenProduct struct {
	ID      string
	Name    string   // the canonical product name (stored normalized in the DB's LOWER(name))
	Aliases []string // zero or more aliases the matcher would scan
}

// goldenProducts is the shared fixture. It covers the product families
// exercised by the test cases: milk variants, produce, brand-prefixed
// items, multi-word items, unit-disambiguated items, and decoys.
var goldenProducts = []goldenProduct{
	{ID: "p_milk_2pct_gal", Name: "Milk 2% 1 Gallon", Aliases: []string{"2% milk gal", "2 pct milk"}},
	{ID: "p_milk_skim_gal", Name: "Skim Milk 1 Gallon", Aliases: []string{"skim milk gal"}},
	{ID: "p_milk_whole_gal", Name: "Whole Milk 1 Gallon", Aliases: []string{"whole milk gal"}},
	{ID: "p_broccoli", Name: "Broccoli Crowns", Aliases: []string{"brc", "broc crowns"}},
	{ID: "p_banana", Name: "Bananas", Aliases: []string{"banana"}},
	{ID: "p_banana_bread_mix", Name: "Banana Bread Mix", Aliases: nil},
	{ID: "p_spinach_5oz", Name: "Organic Baby Spinach 5 oz", Aliases: []string{"org baby spinach 5oz"}},
	{ID: "p_spinach_16oz", Name: "Organic Baby Spinach 16 oz", Aliases: []string{"org baby spinach 16oz"}},
	{ID: "p_eggs_dozen", Name: "Large Eggs Dozen", Aliases: []string{"lrg eggs dz"}},
	{ID: "p_bread_wheat", Name: "Whole Wheat Bread", Aliases: []string{"ww bread"}},
	{ID: "p_cheese_cheddar", Name: "Cheddar Cheese Block 8 oz", Aliases: []string{"chdr cheese 8oz"}},
	{ID: "p_apples_gala", Name: "Gala Apples", Aliases: []string{"gala apple"}},
	{ID: "p_apples_fuji", Name: "Fuji Apples", Aliases: nil},
	{ID: "p_coffee", Name: "Ground Coffee 12 oz", Aliases: []string{"gnd coffee 12oz"}},
	{ID: "p_chicken_bnls", Name: "Boneless Chicken Breast", Aliases: []string{"bnls chkn brst"}},
}

// scoreBand is a coarse confidence bucket. Using bands instead of exact
// floats avoids brittle tests — small scoring tweaks shouldn't break us
// unless they actually change the ranking decision.
type scoreBand int

const (
	bandNone   scoreBand = iota // no match (below threshold)
	bandLow                     // 0.5-0.6, borderline
	bandMedium                  // 0.6-0.8
	bandHigh                    // >= 0.8
)

func bandOf(score float64) scoreBand {
	switch {
	case score < 0.5:
		return bandNone
	case score < 0.6:
		return bandLow
	case score < 0.8:
		return bandMedium
	default:
		return bandHigh
	}
}

func (b scoreBand) String() string {
	switch b {
	case bandNone:
		return "none"
	case bandLow:
		return "low"
	case bandMedium:
		return "medium"
	case bandHigh:
		return "high"
	}
	return "?"
}

// goldenMatch replicates the selection logic of matchByFuzzy against an
// in-memory candidate slice. It mirrors fuzzy.go:48-127 exactly:
//
//  1. Normalize the raw name.
//  2. For each candidate (product name + each alias), filter via
//     fuzzy.RankMatchNormalizedFold; -1 means the query is not a
//     character-subsequence of the target (it is skipped).
//  3. Score survivors with calculateSimilarity (1 - levenshtein/maxLen).
//  4. Keep the global best.
//  5. Below 0.7 raw score -> no match.
//  6. Otherwise scale to 0.5 + 0.4*score, capped at 0.9.
//
// This is intentionally a copy of the SUT flow, not a call into it — the
// SUT requires *sql.DB. Any drift between this helper and matchByFuzzy
// should be treated as a bug in the helper; update the helper to track.
func goldenMatch(rawName string, products []goldenProduct) (productID string, confidence float64) {
	normalized := Normalize(rawName)
	if normalized == "" {
		return "", 0
	}

	type cand struct {
		productID string
		target    string
	}
	var candidates []cand
	for _, p := range products {
		candidates = append(candidates, cand{productID: p.ID, target: Normalize(p.Name)})
		for _, a := range p.Aliases {
			candidates = append(candidates, cand{productID: p.ID, target: Normalize(a)})
		}
	}

	var bestScore float64
	var bestProductID string
	for _, c := range candidates {
		if fuzzy.RankMatchNormalizedFold(normalized, c.target) == -1 {
			continue
		}
		s := calculateSimilarity(normalized, c.target)
		if s > bestScore {
			bestScore = s
			bestProductID = c.productID
		}
	}

	if bestScore < 0.7 {
		return "", 0
	}
	conf := 0.5 + (bestScore * 0.4)
	if conf > 0.9 {
		conf = 0.9
	}
	return bestProductID, conf
}

func TestMatcherGolden(t *testing.T) {
	tests := []struct {
		name          string
		rawName       string
		wantProductID string // "" means no match expected
		wantBand      scoreBand
	}{
		{
			name:          "exact name match",
			rawName:       "Bananas",
			wantProductID: "p_banana",
			wantBand:      bandHigh,
		},
		{
			name:          "case insensitive exact",
			rawName:       "BANANAS",
			wantProductID: "p_banana",
			wantBand:      bandHigh,
		},
		{
			name:          "alias exact after normalize",
			rawName:       "BNLS CHKN BRST",
			wantProductID: "p_chicken_bnls",
			wantBand:      bandHigh,
		},
		{
			name:          "fuzzy abbreviation via alias",
			rawName:       "BRC",
			wantProductID: "p_broccoli",
			wantBand:      bandHigh,
		},
		{
			name:          "normalized brand-like prefix via alias",
			rawName:       "2% MILK GAL",
			wantProductID: "p_milk_2pct_gal",
			wantBand:      bandHigh,
		},
		{
			name:          "unit disambiguation small",
			rawName:       "ORG BABY SPINACH 5OZ",
			wantProductID: "p_spinach_5oz",
			wantBand:      bandHigh,
		},
		{
			name:          "unit disambiguation large",
			rawName:       "ORG BABY SPINACH 16OZ",
			wantProductID: "p_spinach_16oz",
			wantBand:      bandHigh,
		},
		{
			name:          "nutritional variant must not cross match",
			rawName:       "2% MILK GAL",
			wantProductID: "p_milk_2pct_gal", // NOT skim, NOT whole
			wantBand:      bandHigh,
		},
		{
			name:          "multi word product via abbreviated alias",
			rawName:       "GND COFFEE 12OZ",
			wantProductID: "p_coffee",
			wantBand:      bandHigh,
		},
		{
			name:          "ranking picks closer variant",
			rawName:       "GALA APPLE",
			wantProductID: "p_apples_gala", // must beat p_apples_fuji
			wantBand:      bandHigh,
		},
		{
			name:          "negative: bananas does not match banana bread mix",
			rawName:       "BANANAS",
			wantProductID: "p_banana", // must not slide to p_banana_bread_mix
			wantBand:      bandHigh,
		},
		{
			name:          "no match below threshold",
			rawName:       "COCA COLA 12PK",
			wantProductID: "",
			wantBand:      bandNone,
		},
		{
			name:          "empty input yields no match",
			rawName:       "",
			wantProductID: "",
			wantBand:      bandNone,
		},
		{
			name:          "whitespace only yields no match",
			rawName:       "   ",
			wantProductID: "",
			wantBand:      bandNone,
		},
		{
			name:          "fuzzy typo on alias",
			rawName:       "LRG EGS DZ", // LRG EGGS DZ with a typo
			wantProductID: "p_eggs_dozen",
			wantBand:      bandHigh,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotID, gotConf := goldenMatch(tt.rawName, goldenProducts)

			if gotID != tt.wantProductID {
				t.Errorf("goldenMatch(%q): got product_id=%q (conf=%.3f), want %q",
					tt.rawName, gotID, gotConf, tt.wantProductID)
			}

			gotBand := bandOf(gotConf)
			if gotBand != tt.wantBand {
				t.Errorf("goldenMatch(%q): got band=%s (conf=%.3f), want band=%s",
					tt.rawName, gotBand, gotConf, tt.wantBand)
			}
		})
	}
}

// TestMatcherGoldenRanking asserts that among the full fixture, the intended
// product out-scores a specifically named decoy. This catches regressions
// where a future scoring tweak accidentally elevates the wrong candidate.
func TestMatcherGoldenRanking(t *testing.T) {
	tests := []struct {
		name      string
		rawName   string
		winnerID  string
		loserID   string
	}{
		{"2% milk beats whole milk", "2% MILK GAL", "p_milk_2pct_gal", "p_milk_whole_gal"},
		{"2% milk beats skim milk", "2% MILK GAL", "p_milk_2pct_gal", "p_milk_skim_gal"},
		{"gala beats fuji", "GALA APPLES", "p_apples_gala", "p_apples_fuji"},
		{"bananas beats banana bread mix", "BANANAS", "p_banana", "p_banana_bread_mix"},
		{"spinach 5oz beats spinach 16oz", "ORG BABY SPINACH 5OZ", "p_spinach_5oz", "p_spinach_16oz"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			winnerScore := bestScoreFor(tt.rawName, tt.winnerID)
			loserScore := bestScoreFor(tt.rawName, tt.loserID)
			if !(winnerScore > loserScore) {
				t.Errorf("%q: expected %s (score=%.3f) to outscore %s (score=%.3f)",
					tt.rawName, tt.winnerID, winnerScore, tt.loserID, loserScore)
			}
		})
	}
}

// bestScoreFor returns the best raw similarity (pre-threshold, pre-scaling)
// across all name/alias targets belonging to the given product ID. It is
// used only by the ranking tests to compare two candidates head-to-head.
func bestScoreFor(rawName, productID string) float64 {
	normalized := Normalize(rawName)
	var best float64
	for _, p := range goldenProducts {
		if p.ID != productID {
			continue
		}
		targets := append([]string{Normalize(p.Name)}, func() []string {
			out := make([]string, 0, len(p.Aliases))
			for _, a := range p.Aliases {
				out = append(out, Normalize(a))
			}
			return out
		}()...)
		for _, t := range targets {
			if fuzzy.RankMatchNormalizedFold(normalized, t) == -1 {
				continue
			}
			s := calculateSimilarity(normalized, t)
			if s > best {
				best = s
			}
		}
	}
	return best
}
