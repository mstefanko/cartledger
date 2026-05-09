package llm

import (
	"database/sql"
	"log/slog"
)

// GuardedExtractor wraps an LLM client with two per-instance safeguards:
//
//  1. Per-household monthly token budget (llm_usage_monthly table). If
//     the household has already consumed >= budget tokens this month,
//     the call is rejected with ErrBudgetExceeded before hitting the API.
//  2. A circuit breaker that trips after repeated 429s from Anthropic.
//     While open, all calls return ErrCircuitOpen immediately (no API call).
//
// The senior-dev-review watch-out is: a cost cap alone still lets a buggy
// retry loop spend money in minutes if the API itself is throttling. The
// breaker closes that hole — once Anthropic signals overload, we stop
// calling even if budget remains.
type GuardedExtractor struct {
	client  Client   // underlying LLM client (ClaudeClient, MockClient, etc.)
	db      *sql.DB  // for usage reads/writes
	budget  int64    // per-household monthly total tokens (0 = no cap)
	breaker *Breaker // process-local circuit breaker
}

// NewGuardedExtractor constructs a GuardedExtractor. budget <= 0 disables
// the budget check. breaker must be non-nil.
func NewGuardedExtractor(client Client, db *sql.DB, budget int64, breaker *Breaker) *GuardedExtractor {
	return &GuardedExtractor{
		client:  client,
		db:      db,
		budget:  budget,
		breaker: breaker,
	}
}

// Provider proxies to the underlying client.
func (g *GuardedExtractor) Provider() string { return g.client.Provider() }

// Breaker exposes the underlying circuit breaker (read-only uses only —
// the admin usage endpoint calls IsOpen()).
func (g *GuardedExtractor) Breaker() *Breaker { return g.breaker }

// Budget returns the configured per-household monthly budget (0 = no cap).
func (g *GuardedExtractor) Budget() int64 { return g.budget }

// DB returns the database handle. Used by the admin usage endpoint to
// read llm_usage_monthly without duplicating config wiring.
func (g *GuardedExtractor) DB() *sql.DB { return g.db }

// ExtractForHousehold performs a budget pre-check, runs the call through
// the circuit breaker, and records usage on success. Errors:
//
//   - ErrBudgetExceeded: budget reached for this month. Terminal.
//   - ErrCircuitOpen:    breaker is open (recent 429 storm). Terminal for
//     this job; will clear on cooldown.
//   - any other error from the underlying client (network, parse, etc.).
//
// Token counts are only tracked for the Claude client. Other clients
// (mock) record a zero-token row so call_count still increments.
func (g *GuardedExtractor) ExtractForHousehold(householdID string, images [][]byte) (*ReceiptExtraction, error) {
	// 1. Budget pre-check (cheap single-row query). Race window: two
	// concurrent jobs from the same household can both pass this check
	// before either upserts usage. Accepted overshoot is bounded by
	// worker concurrency (default 2).
	if err := CheckBudget(g.db, householdID, g.budget); err != nil {
		return nil, err
	}

	// 2. Circuit breaker: reject outright if open; mark probe in flight
	// if half-open.
	allow, isProbe := g.breaker.Allow()
	if !allow {
		return nil, ErrCircuitOpen
	}

	// 3. Call through. Try to pull token counts if the client supports it.
	extraction, inputTokens, outputTokens, err := callWithUsage(g.client, images)
	if err != nil {
		g.classifyAndReport(err, isProbe)
		return nil, err
	}

	// Success path.
	g.breaker.OnSuccess()

	// 4. Record usage (best-effort — a failure here shouldn't fail the
	// job, which already succeeded end-to-end).
	if recErr := RecordMonthlyUsage(g.db, householdID, CurrentYearMonth(), inputTokens, outputTokens); recErr != nil {
		slog.Warn("llm: failed to record monthly usage", "household_id", householdID, "err", recErr)
	}

	return extraction, nil
}

func (g *GuardedExtractor) RepairForHousehold(householdID string, images [][]byte, currentJSON, note string) (*ReceiptExtraction, error) {
	if err := CheckBudget(g.db, householdID, g.budget); err != nil {
		return nil, err
	}

	allow, isProbe := g.breaker.Allow()
	if !allow {
		return nil, ErrCircuitOpen
	}

	extraction, inputTokens, outputTokens, err := callRepairWithUsage(g.client, images, currentJSON, note)
	if err != nil {
		g.classifyAndReport(err, isProbe)
		return nil, err
	}

	g.breaker.OnSuccess()
	if recErr := RecordMonthlyUsage(g.db, householdID, CurrentYearMonth(), inputTokens, outputTokens); recErr != nil {
		slog.Warn("llm: failed to record monthly usage", "household_id", householdID, "err", recErr)
	}
	return extraction, nil
}

// classifyAndReport inspects err and advances breaker state accordingly.
// A 429 from Anthropic (anthropic.Error with StatusCode 429) counts toward
// tripping the breaker; anything else is OnOtherError.
func (g *GuardedExtractor) classifyAndReport(err error, _ bool) {
	if IsRateLimit(err) {
		g.breaker.OnRateLimit()
		slog.Warn("llm: Anthropic rate limit (429)", "breaker_state", g.breaker.State())
		return
	}
	g.breaker.OnOtherError()
}

// tokenAwareClient is the optional interface implemented by clients that can
// report input/output token counts per call. ClaudeClient implements it;
// MockClient does not.
type tokenAwareClient interface {
	ExtractReceiptWithUsage(images [][]byte) (*ReceiptExtraction, int64, int64, error)
}

type repairAwareClient interface {
	RepairReceipt(images [][]byte, currentJSON, note string) (*ReceiptExtraction, error)
}

type repairTokenAwareClient interface {
	RepairReceiptWithUsage(images [][]byte, currentJSON, note string) (*ReceiptExtraction, int64, int64, error)
}

// callWithUsage invokes the client and returns token counts when possible.
// Falls back to zero counts for non-token-aware clients (e.g. mock).
func callWithUsage(c Client, images [][]byte) (*ReceiptExtraction, int64, int64, error) {
	if ta, ok := c.(tokenAwareClient); ok {
		return ta.ExtractReceiptWithUsage(images)
	}
	extraction, err := c.ExtractReceipt(images)
	return extraction, 0, 0, err
}

func callRepairWithUsage(c Client, images [][]byte, currentJSON, note string) (*ReceiptExtraction, int64, int64, error) {
	if ta, ok := c.(repairTokenAwareClient); ok {
		return ta.RepairReceiptWithUsage(images, currentJSON, note)
	}
	if ra, ok := c.(repairAwareClient); ok {
		extraction, err := ra.RepairReceipt(images, currentJSON, note)
		return extraction, 0, 0, err
	}
	return nil, 0, 0, ErrUnsupportedRepair
}
