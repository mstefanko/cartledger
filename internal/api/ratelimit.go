package api

import (
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
	"golang.org/x/time/rate"

	"github.com/mstefanko/cartledger/internal/auth"
)

// Tier names. Keys in the limiter map are namespaced as "<tier>:<identifier>"
// so a household and its source IP can't collide across tiers.
const (
	TierAuth         = "auth"
	TierRead         = "read"
	TierWrite        = "write"
	TierWorkerSubmit = "worker-submit"
	TierGlobal       = "global"
)

// tierConfig describes the token-bucket parameters for one tier.
type tierConfig struct {
	// rps is steady-state requests-per-second.
	rps rate.Limit
	// burst is the bucket capacity (max instantaneous burst).
	burst int
}

// tierConfigs hard-codes the per-tier limits. These values were chosen to
// protect against brute-force (auth) and LLM cost blow-ups (worker-submit)
// while giving normal interactive UI traffic plenty of headroom (read/write).
// Operators can't usefully tune these without load-testing data, so they are
// intentionally not configurable.
var tierConfigs = map[string]tierConfig{
	TierAuth:         {rps: 5, burst: 10},
	TierRead:         {rps: 20, burst: 40},
	TierWrite:        {rps: 10, burst: 20},
	TierWorkerSubmit: {rps: 3, burst: 6},
	TierGlobal:       {rps: 50, burst: 100},
}

// limiterEntry pairs a rate.Limiter with a last-touched timestamp so stale
// entries can be reaped.
type limiterEntry struct {
	lim      *rate.Limiter
	lastSeen atomic.Value // holds time.Time; updated without a full mutex
}

// RateLimiter holds per-key token buckets keyed by "<tier>:<identifier>".
// Enabled=false turns every middleware call into a pass-through (useful for
// local dev / tests). The internal map uses sync.Map because the access
// pattern is overwhelmingly lookup-existing; creation goes through a narrow
// mu-guarded slow path to avoid double-constructing a limiter on first use.
type RateLimiter struct {
	Enabled bool

	mu       sync.Mutex    // guards create path of limiters map
	store    sync.Map      // map[string]*limiterEntry — concurrent safe
	stop     chan struct{} // closed by Close() to stop the janitor
	sampleMu sync.Mutex    // guards 429-log sampling counter
	sampleN  uint64        // how many 429s have happened (for sampled slog.Debug)
}

// NewRateLimiter constructs a RateLimiter. When enabled is true, a background
// janitor goroutine prunes entries older than staleAfter every sweepEvery.
// Callers should invoke Close() at shutdown to stop the janitor; in practice
// this process is long-lived so leaking is fine but tests use Close.
func NewRateLimiter(enabled bool) *RateLimiter {
	rl := &RateLimiter{
		Enabled: enabled,
		stop:    make(chan struct{}),
	}
	if enabled {
		go rl.janitor(5*time.Minute, 30*time.Minute)
	}
	return rl
}

// Close stops the background janitor. Safe to call multiple times.
func (r *RateLimiter) Close() {
	select {
	case <-r.stop:
		// already closed
	default:
		close(r.stop)
	}
}

// getOrCreate returns the limiter for (tier, key), creating it on first use.
// tierConfigs[tier] MUST exist — the caller controls tier names, so an
// unknown tier is a programmer error and we fall back to TierGlobal rather
// than panic in hot path.
func (r *RateLimiter) getOrCreate(tier, key string) *rate.Limiter {
	cfg, ok := tierConfigs[tier]
	if !ok {
		cfg = tierConfigs[TierGlobal]
	}
	k := tier + ":" + key

	// Fast path.
	if v, ok := r.store.Load(k); ok {
		entry := v.(*limiterEntry)
		entry.lastSeen.Store(time.Now())
		return entry.lim
	}

	// Slow path: construct under mu so we don't double-make a bucket if two
	// goroutines race for the same key on first use. sync.Map.LoadOrStore
	// would be cheaper but would allocate a *rate.Limiter even on the hit
	// side; this is fine given creation is rare.
	r.mu.Lock()
	defer r.mu.Unlock()
	if v, ok := r.store.Load(k); ok {
		entry := v.(*limiterEntry)
		entry.lastSeen.Store(time.Now())
		return entry.lim
	}
	entry := &limiterEntry{
		lim: rate.NewLimiter(cfg.rps, cfg.burst),
	}
	entry.lastSeen.Store(time.Now())
	r.store.Store(k, entry)
	return entry.lim
}

// janitor sweeps the store every `every`, removing entries whose lastSeen is
// older than `maxIdle` AND whose limiter currently has its full burst budget
// (i.e. the caller is not actively rate-limited). This preserves any limiter
// that is actively throttling a caller — we never want to "free" a limiter
// out from under an ongoing attack.
func (r *RateLimiter) janitor(every, maxIdle time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-maxIdle)
			r.store.Range(func(k, v any) bool {
				entry := v.(*limiterEntry)
				last, _ := entry.lastSeen.Load().(time.Time)
				if last.Before(cutoff) && entry.lim.Tokens() >= float64(entry.lim.Burst()) {
					r.store.Delete(k)
				}
				return true
			})
		}
	}
}

// Middleware returns an Echo middleware that enforces the given tier. The
// identifier is chosen per tier:
//   - TierAuth (unauthenticated routes): c.RealIP() — JWT claims aren't
//     populated yet.
//   - All other tiers: the JWT household_id set by auth.JWTMiddleware. If
//     that's missing (middleware ordering bug), fall back to c.RealIP() so
//     we never silently skip rate limiting.
//
// On deny, responds 429 with Retry-After + JSON {"error":"rate_limited",
// "retry_after":N}. Logging is SAMPLED (1-in-100) at slog.Debug so the log
// is not drowned during attack floods.
func (r *RateLimiter) Middleware(tier string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if !r.Enabled {
				return next(c)
			}
			key := r.identifier(c, tier)
			lim := r.getOrCreate(tier, key)
			if !lim.Allow() {
				return r.deny(c, tier, key, lim)
			}
			return next(c)
		}
	}
}

// ProtectedMethodMiddleware picks TierRead for GETs and TierWrite for
// non-GETs. This is the default enforcement applied to the protected group
// so every authenticated endpoint is covered without per-handler plumbing.
//
// `overrides` is a path -> tier map: when the request matches one of those
// routed paths (c.Path(), e.g. "/api/v1/receipts/scan"), the override tier
// is used instead. The override ensures /receipts/scan draws from the tight
// worker-submit bucket rather than the roomier write bucket, and doesn't
// double-charge both buckets.
func (r *RateLimiter) ProtectedMethodMiddleware(overrides map[string]string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if !r.Enabled {
				return next(c)
			}
			tier := TierWrite
			if c.Request().Method == http.MethodGet {
				tier = TierRead
			}
			if overrides != nil {
				if t, ok := overrides[c.Path()]; ok {
					tier = t
				}
			}
			key := r.identifier(c, tier)
			lim := r.getOrCreate(tier, key)
			if !lim.Allow() {
				return r.deny(c, tier, key, lim)
			}
			return next(c)
		}
	}
}

// identifier returns the key used to bucket requests for the given tier.
// Only TierAuth keys on IP; everything else prefers household_id with an
// IP fallback for safety.
func (r *RateLimiter) identifier(c echo.Context, tier string) string {
	if tier == TierAuth {
		return c.RealIP()
	}
	if h := auth.HouseholdIDFrom(c); h != "" {
		return "h:" + h
	}
	return "ip:" + c.RealIP()
}

// deny writes a 429 response with a Retry-After hint. The wait estimate is
// derived from (1 token / rps); at high contention a client may get a
// slightly smaller wait than the true next-available-token moment, but that
// is acceptable and bounded by the burst size. We ceil so clients never
// retry earlier than the bucket allows.
func (r *RateLimiter) deny(c echo.Context, tier, key string, lim *rate.Limiter) error {
	// Seconds until 1 token is refilled. rate.Limit is tokens/sec, so wait
	// is 1/rate. Guard against tier misconfiguration with a 1s floor.
	var retryAfter int
	if rps := float64(lim.Limit()); rps > 0 {
		retryAfter = int(math.Ceil(1.0 / rps))
	}
	if retryAfter < 1 {
		retryAfter = 1
	}
	c.Response().Header().Set("Retry-After", strconv.Itoa(retryAfter))

	// Sample 1-in-100 at Debug to avoid log flood.
	r.sampleMu.Lock()
	r.sampleN++
	sample := r.sampleN%100 == 1
	r.sampleMu.Unlock()
	if sample {
		slog.Debug("rate_limited",
			"tier", tier,
			"key_prefix", keyPrefix(key),
			"retry_after", retryAfter,
			"path", c.Request().URL.Path,
		)
	}

	return c.JSON(http.StatusTooManyRequests, map[string]any{
		"error":       "rate_limited",
		"retry_after": retryAfter,
	})
}

// keyPrefix returns a privacy-safe prefix of the bucket key for logging.
// We deliberately avoid logging full IPs / household IDs on every 429.
func keyPrefix(k string) string {
	if len(k) <= 6 {
		return k
	}
	return k[:6] + "..."
}
