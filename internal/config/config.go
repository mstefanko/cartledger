package config

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/joho/godotenv"
)

// Config holds application configuration parsed from environment variables.
type Config struct {
	Port           string
	DataDir        string
	AnthropicAPIKey string
	GeminiAPIKey   string
	LLMProvider    string // "claude", "claude-cli", "gemini", "mock" (empty = auto-detect)
	LLMModel       string // Claude model ID (default: claude-sonnet-4-20250514)
	JWTSecret      string
	MealieURL      string
	MealieToken    string
	// LockInactivityTTL is how long a shopping-list edit lock may be idle
	// before the sweeper reclaims it. Placeholder default — see
	// tmp/ux_flows/multi-store-implementation-plan.md §6 Q5.
	LockInactivityTTL time.Duration
}

// Load reads configuration from environment variables with sensible defaults.
// It loads .env if present (does not override existing env vars).
func Load() *Config {
	_ = godotenv.Load() // ignore error if .env doesn't exist
	return &Config{
		Port:           getEnv("PORT", "8079"),
		DataDir:        getEnv("DATA_DIR", "./data"),
		AnthropicAPIKey: getEnv("ANTHROPIC_API_KEY", ""),
		GeminiAPIKey:   getEnv("GEMINI_API_KEY", ""),
		LLMProvider:    getEnv("LLM_PROVIDER", ""),
		LLMModel:       getEnv("LLM_MODEL", "claude-sonnet-4-20250514"),
		JWTSecret:      getEnv("JWT_SECRET", "change-me-in-production"),
		MealieURL:      getEnv("MEALIE_URL", ""),
		MealieToken:    getEnv("MEALIE_TOKEN", ""),
		LockInactivityTTL: getEnvDuration("LOCK_INACTIVITY_TTL", 60*time.Second),
	}
}

// DBPath returns the full path to the SQLite database file.
func (c *Config) DBPath() string {
	return filepath.Join(c.DataDir, "cartledger.db")
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
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
