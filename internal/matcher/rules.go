package matcher

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"time"
)

const maxRegexPatternLen = 200

// matchByRules queries matching_rules by priority DESC and evaluates each rule's condition
// against the normalized item name. Returns nil if no rule matches.
func matchByRules(db *sql.DB, normalized string, storeID string, householdID string) *MatchResult {
	query := `
		SELECT product_id, condition_op, condition_val
		FROM matching_rules
		WHERE household_id = ? AND (store_id IS NULL OR store_id = ?)
		ORDER BY priority DESC
	`

	rows, err := db.Query(query, householdID, storeID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	for rows.Next() {
		var productID, conditionOp, conditionVal string
		if err := rows.Scan(&productID, &conditionOp, &conditionVal); err != nil {
			continue
		}

		normalizedVal := Normalize(conditionVal)

		if evaluateCondition(normalized, normalizedVal, conditionOp) {
			return &MatchResult{
				ProductID:  productID,
				Confidence: 1.0,
				Method:     "rule",
			}
		}
	}

	return nil
}

// evaluateCondition checks whether the normalized input matches the condition value
// according to the given operation.
func evaluateCondition(normalized, conditionVal, op string) bool {
	switch op {
	case "exact":
		return normalized == conditionVal
	case "contains":
		return strings.Contains(normalized, conditionVal)
	case "starts_with":
		return strings.HasPrefix(normalized, conditionVal)
	case "matches":
		if len(conditionVal) > maxRegexPatternLen {
			return false
		}
		re, err := regexp.Compile(conditionVal)
		if err != nil {
			return false
		}
		// Run the match in a goroutine with a 100ms timeout to prevent ReDoS.
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		resultCh := make(chan bool, 1)
		go func() {
			resultCh <- re.MatchString(normalized)
		}()
		select {
		case result := <-resultCh:
			return result
		case <-ctx.Done():
			return false
		}
	default:
		return false
	}
}
