package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validTestConfig(t *testing.T) *Config {
	t.Helper()
	return &Config{
		Port:              "8079",
		DataDir:           t.TempDir(),
		LLMProvider:       "mock",
		JWTSecret:         "test-secret-for-config",
		BackupRetainCount: 5,
		SMTPPort:          587,
		SMTPTLSMode:       "starttls",
	}
}

func TestValidateSMTPRequiresFromAndBaseURLWhenEnabled(t *testing.T) {
	cfg := validTestConfig(t)
	cfg.SMTPHost = "smtp.example.com"

	err := cfg.Validate()
	if err == nil {
		t.Fatalf("Validate nil, want SMTP validation errors")
	}
	msg := err.Error()
	if !strings.Contains(msg, "SMTP_FROM") || !strings.Contains(msg, "APP_BASE_URL") {
		t.Fatalf("Validate err = %q, want SMTP_FROM and APP_BASE_URL errors", msg)
	}
}

func TestValidateSMTPTLSMode(t *testing.T) {
	cfg := validTestConfig(t)
	cfg.SMTPTLSMode = "sometimes"

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "SMTP_TLS_MODE") {
		t.Fatalf("Validate err = %v, want SMTP_TLS_MODE error", err)
	}
}

func TestLoadBaseDoesNotRequireServerOnlyConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DATA_DIR", filepath.Join(dir, "data"))
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("LLM_PROVIDER", "")
	t.Setenv("JWT_SECRET", "")
	t.Setenv("CARTLEDGER_ENV", "")
	t.Setenv("PROD", "false")
	t.Setenv("TRUST_PROXY", "not-a-cidr")

	cfg, err := LoadBase()
	if err != nil {
		t.Fatalf("LoadBase: %v", err)
	}
	if cfg.AnthropicAPIKey != "" {
		t.Fatalf("AnthropicAPIKey = %q, want empty", cfg.AnthropicAPIKey)
	}
	if _, err := os.Stat(cfg.BackupDir()); err != nil {
		t.Fatalf("BackupDir not created: %v", err)
	}
}

func TestLoadRequiresLLMConfigForServer(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DATA_DIR", filepath.Join(dir, "data"))
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("LLM_PROVIDER", "")
	t.Setenv("JWT_SECRET", "")
	t.Setenv("CARTLEDGER_ENV", "")
	t.Setenv("PROD", "false")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Fatalf("Load err = %v, want ANTHROPIC_API_KEY validation error", err)
	}
}

func TestValidateBaseRejectsQuotedDataDir(t *testing.T) {
	for _, raw := range []string{`"` + t.TempDir() + `"`, `'` + t.TempDir() + `'`} {
		cfg := validTestConfig(t)
		cfg.DataDir = raw
		err := cfg.ValidateBase()
		if err == nil || !strings.Contains(err.Error(), "literal quote") || !strings.Contains(err.Error(), ".env") {
			t.Fatalf("ValidateBase(%q) err = %v, want literal quote .env error", raw, err)
		}
	}
}

func TestValidateBaseRejectsWhitespaceDataDir(t *testing.T) {
	cfg := validTestConfig(t)
	cfg.DataDir = " " + t.TempDir()
	err := cfg.ValidateBase()
	if err == nil || !strings.Contains(err.Error(), "whitespace") || !strings.Contains(err.Error(), ".env") {
		t.Fatalf("ValidateBase err = %v, want whitespace .env error", err)
	}

	cfg = validTestConfig(t)
	cfg.DataDir = "   "
	err = cfg.ValidateBase()
	if err == nil || !strings.Contains(err.Error(), "whitespace-only") {
		t.Fatalf("ValidateBase err = %v, want whitespace-only error", err)
	}
}

func TestValidateBaseProductionRequiresAbsoluteDataDir(t *testing.T) {
	t.Setenv("CARTLEDGER_ENV", "production")
	t.Setenv("PROD", "")
	cfg := validTestConfig(t)
	cfg.DataDir = "./data"
	err := cfg.ValidateBase()
	if err == nil || !strings.Contains(err.Error(), "absolute path in production") {
		t.Fatalf("ValidateBase err = %v, want production absolute path error", err)
	}
}

func TestValidateBaseAllowsDevRelativeAndSpaces(t *testing.T) {
	t.Setenv("CARTLEDGER_ENV", "")
	t.Setenv("PROD", "false")

	cfg := validTestConfig(t)
	cfg.DataDir = filepath.Join(t.TempDir(), "Application Support", "cartledger")
	if err := cfg.ValidateBase(); err != nil {
		t.Fatalf("ValidateBase path with spaces: %v", err)
	}

	cfg = validTestConfig(t)
	cfg.DataDir = filepath.Join(t.TempDir(), "data")
	cwd, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs cwd: %v", err)
	}
	rel, err := filepath.Rel(cwd, cfg.DataDir)
	if err != nil {
		t.Fatalf("rel: %v", err)
	}
	cfg.DataDir = rel
	if err := cfg.ValidateBase(); err != nil {
		t.Fatalf("ValidateBase dev relative path: %v", err)
	}
}
