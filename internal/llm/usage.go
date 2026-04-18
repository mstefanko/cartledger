package llm

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrBudgetExceeded is returned when a household's monthly LLM token usage
// has reached or passed its configured budget. Terminal for the caller —
// retrying without a budget raise will just hit this again.
var ErrBudgetExceeded = errors.New("llm: monthly token budget exceeded for household")

// UsageRow is the aggregated monthly usage for one household.
type UsageRow struct {
	HouseholdID  string
	YearMonth    string // "YYYY-MM" (UTC)
	InputTokens  int64
	OutputTokens int64
	CallCount    int64
}

// CurrentYearMonth returns the "YYYY-MM" key for the current UTC month. The
// budget window is keyed on UTC so multi-timezone households see a consistent
// cutover.
func CurrentYearMonth() string {
	return time.Now().UTC().Format("2006-01")
}

// GetMonthlyUsage returns the usage row for (householdID, ym). If no row
// exists yet, returns a zero-valued UsageRow — NOT an error.
func GetMonthlyUsage(db *sql.DB, householdID, ym string) (UsageRow, error) {
	row := UsageRow{HouseholdID: householdID, YearMonth: ym}
	err := db.QueryRow(
		`SELECT input_tokens, output_tokens, call_count
		 FROM llm_usage_monthly
		 WHERE household_id = ? AND year_month = ?`,
		householdID, ym,
	).Scan(&row.InputTokens, &row.OutputTokens, &row.CallCount)
	if errors.Is(err, sql.ErrNoRows) {
		return row, nil
	}
	if err != nil {
		return row, fmt.Errorf("get monthly usage: %w", err)
	}
	return row, nil
}

// RecordMonthlyUsage upserts a usage delta (one call with the given token
// counts) into llm_usage_monthly.
func RecordMonthlyUsage(db *sql.DB, householdID, ym string, inputTokens, outputTokens int64) error {
	_, err := db.Exec(
		`INSERT INTO llm_usage_monthly (household_id, year_month, input_tokens, output_tokens, call_count, updated_at)
		 VALUES (?, ?, ?, ?, 1, CURRENT_TIMESTAMP)
		 ON CONFLICT(household_id, year_month) DO UPDATE SET
		     input_tokens  = input_tokens  + excluded.input_tokens,
		     output_tokens = output_tokens + excluded.output_tokens,
		     call_count    = call_count    + 1,
		     updated_at    = CURRENT_TIMESTAMP`,
		householdID, ym, inputTokens, outputTokens,
	)
	if err != nil {
		return fmt.Errorf("record monthly usage: %w", err)
	}
	return nil
}

// CheckBudget returns ErrBudgetExceeded if (input+output) >= budget AND
// budget > 0. Budget <= 0 means "no cap" and never returns an error.
// This is a best-effort PRE-call check — there is an inherent race window
// with concurrent calls from the same household that can briefly overshoot
// the budget by ~N concurrent calls worth of tokens. Acceptable for a
// self-hosted single-instance deployment (worker is 2 goroutines).
func CheckBudget(db *sql.DB, householdID string, budget int64) error {
	if budget <= 0 {
		return nil
	}
	row, err := GetMonthlyUsage(db, householdID, CurrentYearMonth())
	if err != nil {
		return err
	}
	if row.InputTokens+row.OutputTokens >= budget {
		return ErrBudgetExceeded
	}
	return nil
}
