package matcher

import "testing"

func TestMatchWithCodeAndSuggestionScopesStoreAndHousehold(t *testing.T) {
	database := newSessionTestDB(t)
	sessionSeed(t, database)

	engine := NewEngine(database)

	got := engine.MatchWithCodeAndSuggestion("UNKNOWN TEXT", "8", "", "s_main", "h1")
	if got.Method != "code" || got.ProductID != "p_milk_2pct" || got.Confidence != 0.99 {
		t.Fatalf("code hit = %+v, want p_milk_2pct method code confidence 0.99", got)
	}

	tests := []struct {
		name        string
		storeID     string
		householdID string
	}{
		{"wrong store", "s_other", "h1"},
		{"wrong household", "s_main", "h_missing"},
		{"blank code", "s_main", "h1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code := "8"
			if tc.name == "blank code" {
				code = ""
			}
			got := engine.MatchWithCodeAndSuggestion("QQQQQQQQQQQ", code, "", tc.storeID, tc.householdID)
			if got.Method == "code" || got.ProductID != "" {
				t.Fatalf("code miss = %+v, want unmatched/no product", got)
			}
		})
	}
}
