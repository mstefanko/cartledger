package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// defaultJWTSecret is the placeholder value rejected by Validate in production.
const defaultJWTSecret = "change-me-in-production"

// knownLLMProviders enumerates LLM_PROVIDER values accepted by Validate.
// Empty string is also allowed (auto-detect) and handled separately.
var knownLLMProviders = map[string]struct{}{
	"claude":     {},
	"claude-cli": {},
	"gemini":     {},
	"mock":       {},
}

// Config holds application configuration parsed from environment variables.
type Config struct {
	Port            string
	DataDir         string
	AnthropicAPIKey string
	GeminiAPIKey    string
	LLMProvider     string // "claude", "claude-cli", "gemini", "mock" (empty = auto-detect)
	LLMModel        string // Claude model ID (default: claude-sonnet-4-20250514)
	// LLMMonthlyTokenBudget is the per-household monthly LLM token cap
	// (input + output, combined). 0 means no cap. A reasonable self-host
	// default is 2_000_000 (~$6/mo at Sonnet pricing). See
	// internal/llm/usage.go.
	LLMMonthlyTokenBudget int64
	JWTSecret       string
	// AllowPrivateIntegrations, when true, permits integration base_urls that
	// resolve to loopback / link-local / RFC1918 / IPv6 ULA addresses. Default
	// is false to prevent SSRF probes of internal services; self-hosted users
	// on a LAN can opt in to reach e.g. http://192.168.x.x:9000.
	AllowPrivateIntegrations bool
	// LockInactivityTTL is how long a shopping-list edit lock may be idle
	// before the sweeper reclaims it. Placeholder default — see
	// tmp/ux_flows/multi-store-implementation-plan.md §6 Q5.
	LockInactivityTTL time.Duration
	// TrustedProxies is the parsed TRUST_PROXY list: reverse-proxy peer CIDRs
	// whose X-Forwarded-* headers should be honored. Empty means no proxy is
	// trusted (safer default). Populated by Load from the TRUST_PROXY env var.
	// Convenience macros: "loopback" expands to 127.0.0.0/8 + ::1/128,
	// "private" expands to RFC1918 + loopback + link-local ranges.
	TrustedProxies []netip.Prefix
	// AllowedOrigins is the parsed ALLOWED_ORIGINS list: scheme+host entries
	// (e.g. "https://cartledger.example.com") that the WebSocket upgrade
	// handler accepts as Origin. Populated from ALLOWED_ORIGINS env var; in
	// non-production mode a localhost dev default is applied when unset.
	// Production mode REQUIRES an explicit allow-list (Validate fails if unset).
	AllowedOrigins []string
	// RateLimitEnabled toggles the tiered per-route rate limiter (see
	// internal/api/ratelimit.go). Default true. Set RATE_LIMIT_ENABLED=false
	// for local testing where throttling is inconvenient. Hard-coded tier
	// values are NOT configurable — operators can't usefully tune them
	// without load-testing data.
	RateLimitEnabled bool
	// ImageRetentionDays is the retention window for original receipt image
	// uploads (files under DATA_DIR/receipts/<uuid>/ NOT prefixed
	// "processed_"). When > 0, a background janitor deletes original files
	// whose mtime is older than this many days; processed_* files are kept
	// forever (they're the only thing the review UI can display). 0 (default)
	// disables the janitor entirely. A reasonable self-host value is 90.
	// Env: IMAGE_RETENTION_DAYS.
	ImageRetentionDays int
	// ImageRetentionSweepInterval is how often the retention janitor walks
	// DATA_DIR/receipts/ to age out originals. Default 24h. Env:
	// IMAGE_RETENTION_SWEEP_INTERVAL (Go duration, e.g. "24h", "6h").
	ImageRetentionSweepInterval time.Duration
	// BackupRetainCount is the max number of status='complete' backup rows
	// (and their archive files) to keep on disk. When a new backup finishes
	// the runner prunes oldest-first down to this count. Env:
	// BACKUP_RETAIN_COUNT. Default 5; validated positive in Load.
	BackupRetainCount int
}

// defaultDevAllowedOrigins is the default ALLOWED_ORIGINS set used in non-
// production mode when the env var is unset. Covers Vite dev server + the Go
// server's own port, both on localhost and 127.0.0.1.
var defaultDevAllowedOrigins = []string{
	"http://localhost:5173",
	"http://localhost:8079",
	"http://127.0.0.1:5173",
	"http://127.0.0.1:8079",
}

// loopbackProxyCIDRs is the expansion of TRUST_PROXY="loopback".
var loopbackProxyCIDRs = []string{
	"127.0.0.0/8",
	"::1/128",
}

// privateProxyCIDRs is the expansion of TRUST_PROXY="private": RFC1918 +
// loopback + link-local (IPv4 169.254/16 and IPv6 fe80::/10) + IPv6 ULA fc00::/7.
var privateProxyCIDRs = []string{
	"127.0.0.0/8",
	"::1/128",
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"169.254.0.0/16",
	"fe80::/10",
	"fc00::/7",
}

// parseTrustedProxies parses the raw TRUST_PROXY env value into a slice of
// netip.Prefix. An empty string returns (nil, nil). The literal values
// "loopback" and "private" expand to predefined CIDR sets. Otherwise the
// value is split on commas, each entry trimmed, and each parsed via
// netip.ParsePrefix. Any parse failure aborts with a descriptive error.
func parseTrustedProxies(raw string) ([]netip.Prefix, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	var entries []string
	switch raw {
	case "loopback":
		entries = loopbackProxyCIDRs
	case "private":
		entries = privateProxyCIDRs
	default:
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			entries = append(entries, part)
		}
	}

	out := make([]netip.Prefix, 0, len(entries))
	for _, e := range entries {
		p, err := netip.ParsePrefix(e)
		if err != nil {
			return nil, fmt.Errorf("TRUST_PROXY: invalid CIDR %q: %w", e, err)
		}
		out = append(out, p)
	}
	return out, nil
}

// Load reads configuration from environment variables with sensible defaults,
// then calls Validate. On any validation error, Load returns (nil, err) — it
// never returns a partially populated Config.
//
// For JWT_SECRET, Load applies a dev/prod policy: when CARTLEDGER_ENV is
// "production" (or PROD=true), an explicit non-default JWT_SECRET is required.
// Otherwise, if JWT_SECRET is unset/default, Load generates an ephemeral
// random secret and logs a warning that sessions will invalidate on restart.
func Load() (*Config, error) {
	_ = godotenv.Load() // ignore error if .env doesn't exist

	cfg := &Config{
		Port:                     getEnv("PORT", "8079"),
		DataDir:                  getEnv("DATA_DIR", "./data"),
		AnthropicAPIKey:          getEnv("ANTHROPIC_API_KEY", ""),
		GeminiAPIKey:             getEnv("GEMINI_API_KEY", ""),
		LLMProvider:              getEnv("LLM_PROVIDER", ""),
		LLMModel:                 getEnv("LLM_MODEL", "claude-sonnet-4-20250514"),
		LLMMonthlyTokenBudget:    getEnvInt64("LLM_MONTHLY_TOKEN_BUDGET", 0),
		JWTSecret:                os.Getenv("JWT_SECRET"), // no default — policy applied below
		AllowPrivateIntegrations: getEnvBool("ALLOW_PRIVATE_INTEGRATIONS", false),
		LockInactivityTTL:        getEnvDuration("LOCK_INACTIVITY_TTL", 60*time.Second),
		RateLimitEnabled:         getEnvBool("RATE_LIMIT_ENABLED", true),
		ImageRetentionDays:          int(getEnvInt64("IMAGE_RETENTION_DAYS", 0)),
		ImageRetentionSweepInterval: getEnvDuration("IMAGE_RETENTION_SWEEP_INTERVAL", 24*time.Hour),
		BackupRetainCount:           int(getEnvInt64("BACKUP_RETAIN_COUNT", 5)),
	}

	// Parse TRUST_PROXY into netip.Prefix slice at load time. An unset / empty
	// value leaves TrustedProxies nil — no proxies trusted.
	trustedProxies, err := parseTrustedProxies(os.Getenv("TRUST_PROXY"))
	if err != nil {
		return nil, err
	}
	cfg.TrustedProxies = trustedProxies

	// Parse ALLOWED_ORIGINS: comma-separated scheme+host entries. In dev mode,
	// default to a localhost set when unset. In prod, leave empty and let
	// Validate fail with a clear error.
	cfg.AllowedOrigins = parseAllowedOrigins(os.Getenv("ALLOWED_ORIGINS"))
	if len(cfg.AllowedOrigins) == 0 && !isProduction() {
		cfg.AllowedOrigins = append([]string(nil), defaultDevAllowedOrigins...)
	}

	// JWT_SECRET dev/prod policy. In non-production mode, synthesize an
	// ephemeral secret so `make dev` and tests Just Work. In production,
	// Validate will reject unset / default values.
	if !isProduction() && (cfg.JWTSecret == "" || cfg.JWTSecret == defaultJWTSecret) {
		secret, err := randomHex(32)
		if err != nil {
			return nil, fmt.Errorf("generate ephemeral JWT_SECRET: %w", err)
		}
		cfg.JWTSecret = secret
		slog.Warn("JWT_SECRET unset or default; generated ephemeral secret — sessions will invalidate on restart. Set JWT_SECRET or CARTLEDGER_ENV=production for persistent sessions.")
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	// Materialize the BackupDir eagerly so the HTTP / CLI / runner surfaces
	// can rely on its presence without each one calling MkdirAll. Mirrors the
	// implicit "DATA_DIR exists and is writable" invariant set by Validate's
	// ensureWritableDir above.
	if err := os.MkdirAll(cfg.BackupDir(), 0o755); err != nil {
		return nil, fmt.Errorf("create backup dir: %w", err)
	}
	return cfg, nil
}

// Validate returns an aggregated error describing every invalid field, or
// nil when the config is usable. Errors are joined via errors.Join so callers
// can print them all at once.
func (c *Config) Validate() error {
	var errs []error

	// JWT_SECRET must be set and non-default in production.
	if isProduction() {
		switch {
		case c.JWTSecret == "":
			errs = append(errs, errors.New("JWT_SECRET: must be set in production (CARTLEDGER_ENV=production)"))
		case c.JWTSecret == defaultJWTSecret:
			errs = append(errs, errors.New("JWT_SECRET: must not be the default value in production"))
		case len(c.JWTSecret) < 16:
			errs = append(errs, errors.New("JWT_SECRET: must be at least 16 characters"))
		}
	}

	// ANTHROPIC_API_KEY is required unless the mock provider is selected.
	// (LLMProvider="" means auto-detect: auto-detect still needs a key to
	// succeed, so require it here.)
	if c.LLMProvider != "mock" && c.AnthropicAPIKey == "" {
		errs = append(errs, errors.New("ANTHROPIC_API_KEY: required unless LLM_PROVIDER=mock"))
	}

	// LLM_PROVIDER, if set, must be a known value.
	if c.LLMProvider != "" {
		if _, ok := knownLLMProviders[c.LLMProvider]; !ok {
			errs = append(errs, fmt.Errorf("LLM_PROVIDER: unknown value %q (valid: claude, claude-cli, gemini, mock)", c.LLMProvider))
		}
	}

	// DATA_DIR must exist (or be creatable) and writable.
	if c.DataDir == "" {
		errs = append(errs, errors.New("DATA_DIR: must not be empty"))
	} else if err := ensureWritableDir(c.DataDir); err != nil {
		errs = append(errs, fmt.Errorf("DATA_DIR: %w", err))
	}

	// PORT must be non-empty (numeric check is loose — net/http will fail
	// fast on bind with a clearer error than anything we invent here).
	if strings.TrimSpace(c.Port) == "" {
		errs = append(errs, errors.New("PORT: must not be empty"))
	}

	// BACKUP_RETAIN_COUNT must be positive; 0 would mean "prune every backup
	// on completion", which is never a useful configuration.
	if c.BackupRetainCount <= 0 {
		errs = append(errs, errors.New("BACKUP_RETAIN_COUNT: must be positive"))
	}

	// In production, ALLOWED_ORIGINS must be explicitly set. The dev default
	// (localhost) is never applied in prod, so an empty list here means the
	// operator forgot to set it — fail loudly rather than silently accept
	// cross-site WebSocket upgrades.
	if isProduction() && len(c.AllowedOrigins) == 0 {
		errs = append(errs, errors.New("ALLOWED_ORIGINS: must be set in production (comma-separated scheme+host list, e.g. https://cartledger.example.com)"))
	}

	return errors.Join(errs...)
}

// DBPath returns the full path to the SQLite database file.
func (c *Config) DBPath() string {
	return filepath.Join(c.DataDir, "cartledger.db")
}

// BackupDir returns the directory where backup archives live. Created by
// Load() after validation so callers can assume it exists.
func (c *Config) BackupDir() string {
	return filepath.Join(c.DataDir, "backups")
}

// RestorePendingDir returns the directory where a staged restore archive
// lives between HTTP upload and the next server boot. Not created eagerly —
// only the restore flow writes here, and it mkdirs on demand.
func (c *Config) RestorePendingDir() string {
	return filepath.Join(c.DataDir, "restore-pending")
}

// isProduction reports whether the process is running in production mode.
// Accepts either CARTLEDGER_ENV=production or PROD=true (common Docker/K8s idiom).
func isProduction() bool {
	if strings.EqualFold(os.Getenv("CARTLEDGER_ENV"), "production") {
		return true
	}
	return getEnvBool("PROD", false)
}

// ensureWritableDir verifies dir exists (creating it if missing) and that the
// current process can write a file inside it.
func ensureWritableDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	probe := filepath.Join(dir, ".writable-check")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("not writable (%s): %w", dir, err)
	}
	if _, err := f.WriteString("ok"); err != nil {
		_ = f.Close()
		_ = os.Remove(probe)
		return fmt.Errorf("not writable (%s): %w", dir, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(probe)
		return fmt.Errorf("close probe in %s: %w", dir, err)
	}
	if err := os.Remove(probe); err != nil {
		return fmt.Errorf("cleanup probe in %s: %w", dir, err)
	}
	return nil
}

// randomHex returns 2*n hex characters of cryptographically random bytes.
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// getEnvBool parses a boolean env var. Accepts "1", "true", "yes", "on"
// (case-insensitive) as true; anything else (including empty) returns fallback.
func getEnvBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	switch v {
	case "1", "true", "TRUE", "True", "yes", "YES", "Yes", "on", "ON", "On":
		return true
	case "0", "false", "FALSE", "False", "no", "NO", "No", "off", "OFF", "Off":
		return false
	}
	slog.Warn("config: invalid env var; using default", "key", key, "value", v, "default", fallback)
	return fallback
}

// parseAllowedOrigins parses a comma-separated ALLOWED_ORIGINS value into a
// normalized list of scheme+host entries. Each entry has surrounding whitespace
// and trailing slashes stripped; empty entries are dropped. An empty or all-
// whitespace input returns nil (the caller applies dev-mode defaults).
func parseAllowedOrigins(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.TrimRight(p, "/")
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// getEnvInt64 parses a base-10 int64 env var. On missing/malformed input
// the default is returned (with a warning on malformed).
func getEnvInt64(key string, fallback int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	var n int64
	_, err := fmt.Sscanf(v, "%d", &n)
	if err != nil {
		slog.Warn("config: invalid int64; using default", "key", key, "value", v, "err", err, "default", fallback)
		return fallback
	}
	return n
}

// getEnvDuration parses a time.Duration env var (e.g. "60s", "5m") and falls
// back to the provided default on missing/malformed values.
func getEnvDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		slog.Warn("config: invalid duration; using default", "key", key, "value", v, "err", err, "default", fallback)
		return fallback
	}
	return d
}
