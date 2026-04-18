package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
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
		JWTSecret:                os.Getenv("JWT_SECRET"), // no default — policy applied below
		AllowPrivateIntegrations: getEnvBool("ALLOW_PRIVATE_INTEGRATIONS", false),
		LockInactivityTTL:        getEnvDuration("LOCK_INACTIVITY_TTL", 60*time.Second),
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
		log.Printf("WARNING: JWT_SECRET unset or default; generated ephemeral secret. Sessions will invalidate on restart. Set JWT_SECRET or CARTLEDGER_ENV=production for persistent sessions.")
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
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

	return errors.Join(errs...)
}

// DBPath returns the full path to the SQLite database file.
func (c *Config) DBPath() string {
	return filepath.Join(c.DataDir, "cartledger.db")
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
	log.Printf("config: invalid %s=%q; using default %v", key, v, fallback)
	return fallback
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
		log.Printf("config: invalid %s=%q (%v); using default %s", key, v, err, fallback)
		return fallback
	}
	return d
}
