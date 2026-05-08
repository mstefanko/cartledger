package config

import (
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
