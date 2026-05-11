package matcher

import (
	"database/sql"
	"strings"
)

func matchByCode(db *sql.DB, storeItemCode, storeID, householdID string) *MatchResult {
	storeItemCode = strings.TrimSpace(storeItemCode)
	if storeItemCode == "" || storeID == "" || householdID == "" {
		return nil
	}

	var productID string
	err := db.QueryRow(
		`SELECT spc.product_id
		   FROM store_product_codes spc
		   JOIN products p ON p.id = spc.product_id
		  WHERE spc.store_id = ?
		    AND spc.store_item_code = ?
		    AND spc.household_id = ?
		    AND p.household_id = ?
		  LIMIT 1`,
		storeID, storeItemCode, householdID, householdID,
	).Scan(&productID)
	if err != nil {
		return nil
	}
	return &MatchResult{
		ProductID:  productID,
		Confidence: 0.99,
		Method:     "code",
	}
}
