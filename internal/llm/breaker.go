package llm

import (
	"errors"
	"sync"
	"time"
)

// ErrCircuitOpen is returned when the LLM circuit breaker is open and new
// calls are being refused. Terminal for the caller — the breaker is process-
// local and resets on restart or on a successful probe after cooldown.
var ErrCircuitOpen = errors.New("llm: circuit breaker open (too many recent 429s)")

// BreakerState is the internal state machine label.
type BreakerState int

const (
	// BreakerClosed — calls flow through. Default state.
	BreakerClosed BreakerState = iota
	// BreakerOpen — all calls rejected immediately with ErrCircuitOpen.
	BreakerOpen
	// BreakerHalfOpen — one probe call is permitted; outcome decides whether
	// to close (success) or re-open with exponential backoff (429).
	BreakerHalfOpen
)

// Breaker is a process-local circuit breaker that opens after N consecutive
// 429 responses within M seconds. Per-INSTANCE (not per-household) — a 429
// from Anthropic means our overall API quota is strained, which is a global
// concern regardless of which household triggered it.
//
// State is NOT persisted; process restart resets to closed. Acceptable.
type Breaker struct {
	// failureThreshold is N: consecutive 429s needed to trip the breaker.
	failureThreshold int
	// failureWindow is M: consecutive 429s must all fall within this window
	// from the first one. A successful call (or any non-429 error, since the
	// only signal we react to is 429) resets the counter.
	failureWindow time.Duration
	// cooldown is the initial BreakerOpen dwell time before transitioning
	// to BreakerHalfOpen for a probe.
	cooldown time.Duration
	// maxCooldown caps exponential backoff on repeated half-open probe
	// failures (prevents unbounded sleep).
	maxCooldown time.Duration
	// now is injectable for tests.
	now func() time.Time

	mu sync.Mutex

	state          BreakerState
	failureCount   int       // consecutive 429s toward threshold
	firstFailureAt time.Time // start of the current failure window
	openedAt       time.Time // when we entered BreakerOpen
	currentCD      time.Duration
	probeInFlight  bool // true while a half-open probe is outstanding
}

// NewBreaker constructs a Breaker with the given tuning parameters.
// Pass zero to use the defaults: N=5, window=60s, cooldown=120s, maxCooldown=30m.
func NewBreaker(failureThreshold int, failureWindow, cooldown, maxCooldown time.Duration) *Breaker {
	if failureThreshold <= 0 {
		failureThreshold = 5
	}
	if failureWindow <= 0 {
		failureWindow = 60 * time.Second
	}
	if cooldown <= 0 {
		cooldown = 120 * time.Second
	}
	if maxCooldown <= 0 {
		maxCooldown = 30 * time.Minute
	}
	return &Breaker{
		failureThreshold: failureThreshold,
		failureWindow:    failureWindow,
		cooldown:         cooldown,
		maxCooldown:      maxCooldown,
		now:              time.Now,
		state:            BreakerClosed,
		currentCD:        cooldown,
	}
}

// Allow reports whether a call may proceed. Returns (allow, isProbe).
// If allow is false, the caller should return ErrCircuitOpen.
// If allow is true and isProbe is true, the caller MUST invoke either
// OnSuccess or OnFailure/OnNon429 after the call so the breaker can
// transition out of half-open.
func (b *Breaker) Allow() (allow, isProbe bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()

	switch b.state {
	case BreakerClosed:
		return true, false
	case BreakerOpen:
		if now.Sub(b.openedAt) >= b.currentCD {
			// Cooldown elapsed — move to half-open and let THIS caller probe.
			b.state = BreakerHalfOpen
			b.probeInFlight = true
			return true, true
		}
		return false, false
	case BreakerHalfOpen:
		if b.probeInFlight {
			// Some other caller already holds the probe slot. Reject.
			return false, false
		}
		// Shouldn't normally happen (OnSuccess/OnFailure reset state), but
		// guard in case a caller forgot to report. Treat as still open.
		b.probeInFlight = true
		return true, true
	}
	return true, false
}

// OnSuccess reports a successful call. If the breaker was half-open (probing),
// this closes it and resets backoff. If it was closed, this resets the
// failure counter.
func (b *Breaker) OnSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.failureCount = 0
	b.firstFailureAt = time.Time{}

	switch b.state {
	case BreakerHalfOpen:
		b.state = BreakerClosed
		b.probeInFlight = false
		b.currentCD = b.cooldown // reset backoff on success
	case BreakerOpen:
		// Unexpected — a call succeeded while we thought we were open.
		// Close anyway; future 429s will re-trip.
		b.state = BreakerClosed
		b.probeInFlight = false
		b.currentCD = b.cooldown
	}
}

// OnRateLimit reports a 429 response from Anthropic. Trips the breaker if
// threshold is reached; doubles cooldown on a half-open probe failure.
func (b *Breaker) OnRateLimit() {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()

	switch b.state {
	case BreakerHalfOpen:
		// Probe failed — re-open with exponential backoff.
		b.state = BreakerOpen
		b.probeInFlight = false
		b.openedAt = now
		b.currentCD *= 2
		if b.currentCD > b.maxCooldown {
			b.currentCD = b.maxCooldown
		}
	case BreakerClosed:
		// Roll the failure window: if the first failure is too old, reset.
		if b.firstFailureAt.IsZero() || now.Sub(b.firstFailureAt) > b.failureWindow {
			b.failureCount = 1
			b.firstFailureAt = now
		} else {
			b.failureCount++
		}
		if b.failureCount >= b.failureThreshold {
			b.state = BreakerOpen
			b.openedAt = now
			b.currentCD = b.cooldown
		}
	case BreakerOpen:
		// Already open; nothing to update.
	}
}

// OnOtherError reports a non-429 error. It does NOT reset the failure counter
// (a 500 is not evidence that we're not being throttled), but it does not
// count toward tripping the breaker either.
func (b *Breaker) OnOtherError() {
	// Intentional no-op. Only 429s move the breaker toward open.
}

// State returns the current breaker state. Intended for tests and the admin
// usage endpoint.
func (b *Breaker) State() BreakerState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// IsOpen is a convenience for the admin endpoint.
func (b *Breaker) IsOpen() bool {
	return b.State() == BreakerOpen
}
