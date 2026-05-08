package mail

import (
	"context"
	"testing"

	"github.com/mstefanko/cartledger/internal/config"
)

func TestNewNoopMailerWhenHostEmpty(t *testing.T) {
	mailer, err := New(&config.Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if mailer.Enabled() {
		t.Fatalf("Enabled = true, want false")
	}
	if err := mailer.Send(context.Background(), "to@example.com", "subject", "<p>hi</p>", "hi"); err != nil {
		t.Fatalf("noop Send: %v", err)
	}
}

func TestRenderTemplates(t *testing.T) {
	htmlBody, textBody, err := Render("password_reset", TemplateData{URL: "https://example.com/reset"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if htmlBody == "" || textBody == "" {
		t.Fatalf("Render returned empty bodies")
	}
}
