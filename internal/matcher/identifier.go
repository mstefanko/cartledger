package matcher

import (
	"context"
	"database/sql"

	"github.com/mstefanko/cartledger/internal/identifiers"
)

func matchByIdentifier(ctx context.Context, db *sql.DB, householdID string, observations []identifiers.Observation) *MatchResult {
	if householdID == "" || len(observations) == 0 {
		return nil
	}
	for _, obs := range observations {
		if obs.NormalizedValue == "" {
			continue
		}
		var productID string
		err := db.QueryRowContext(ctx, `
            SELECT pi.product_id
              FROM product_identifiers pi
              JOIN products p ON p.id = pi.product_id
             WHERE pi.household_id = ?
               AND p.household_id = ?
               AND pi.kind = ?
               AND pi.authority = ?
               AND pi.normalized_value = ?
             LIMIT 1`,
			householdID, householdID, obs.Kind, obs.Authority, obs.NormalizedValue,
		).Scan(&productID)
		if err == nil {
			return &MatchResult{ProductID: productID, Confidence: 0.995, Method: "identifier"}
		}
	}
	return nil
}
