package config

import (
	"os"
	"path/filepath"

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
