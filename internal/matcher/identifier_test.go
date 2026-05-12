package matcher

import (
	"context"
	"testing"

	"github.com/mstefanko/cartledger/internal/identifiers"
)

func TestMatchInputIdentifierWins(t *testing.T) {
	database := newSessionTestDB(t)
	sessionSeed(t, database)

	if _, err := database.Exec(
		`INSERT INTO product_identifiers
		    (household_id, product_id, kind, authority, value, normalized_value, source)
		 VALUES ('h1', 'p_broccoli', 'gtin', '', '036000291452', '036000291452', 'manual')`,
	); err != nil {
		t.Fatalf("insert product identifier: %v", err)
	}

	engine := NewEngine(database)
	obs := identifiers.Observation{
		Kind:            identifiers.KindGTIN,
		RawValue:        "036000291452",
		NormalizedValue: "036000291452",
	}

	got := engine.MatchInput(context.Background(), Input{
		RawName:       "ORG BANANAS",
		StoreItemCode: "8",
		StoreID:       "s_main",
		HouseholdID:   "h1",
		Identifiers:   []identifiers.Observation{obs},
	})
	if got.Method != "identifier" || got.ProductID != "p_broccoli" {
		t.Fatalf("MatchInput = %+v, want identifier p_broccoli", got)
	}

	session, err := engine.NewSession("h1", "s_main")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	got = session.MatchInput(context.Background(), Input{
		RawName:       "ORG BANANAS",
		StoreItemCode: "8",
		Identifiers:   []identifiers.Observation{obs},
	})
	if got.Method != "identifier" || got.ProductID != "p_broccoli" {
		t.Fatalf("Session MatchInput = %+v, want identifier p_broccoli", got)
	}
}
