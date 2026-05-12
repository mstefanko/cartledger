package matcher

import (
	"context"
	"errors"
	"testing"
)

func TestUpsertAliasFindsLegacyNullNormalizedCollision(t *testing.T) {
	database := newBackfillTestDB(t)
	insertProduct(t, database, "p1", "Alpha", nil, nil)
	insertProduct(t, database, "p2", "Beta", nil, nil)

	if _, err := database.Exec(
		`INSERT INTO product_aliases
		    (id, product_id, household_id, alias, alias_normalized, created_at)
		 VALUES ('a1', 'p1', 'h1', 'A & B', NULL, CURRENT_TIMESTAMP)`,
	); err != nil {
		t.Fatalf("insert legacy alias: %v", err)
	}

	if err := UpsertAlias(context.Background(), database, AliasUpsert{
		HouseholdID: "h1",
		ProductID:   "p1",
		Alias:       "A and B",
		Source:      AliasSourceUserAlias,
	}); err != nil {
		t.Fatalf("UpsertAlias same product: %v", err)
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM product_aliases`).Scan(&count); err != nil {
		t.Fatalf("count aliases: %v", err)
	}
	if count != 1 {
		t.Fatalf("aliases = %d, want 1", count)
	}

	err := UpsertAlias(context.Background(), database, AliasUpsert{
		HouseholdID: "h1",
		ProductID:   "p2",
		Alias:       "A and B",
		Source:      AliasSourceUserAlias,
	})
	if !errors.Is(err, ErrAliasConflict) {
		t.Fatalf("UpsertAlias other product err = %v, want ErrAliasConflict", err)
	}
}
