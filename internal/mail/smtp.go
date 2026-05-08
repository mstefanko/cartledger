package mail

import (
	"context"
	"fmt"
	"time"

	gomail "github.com/wneessen/go-mail"

	"github.com/mstefanko/cartledger/internal/config"
)

type smtpMailer struct {
	from   string
	client *gomail.Client
}

func newSMTPMailer(cfg *config.Config) (Mailer, error) {
	opts := []gomail.Option{
		gomail.WithPort(cfg.SMTPPort),
		gomail.WithTimeout(15 * time.Second),
	}

	switch cfg.SMTPTLSMode {
	case "none":
		opts = append(opts, gomail.WithTLSPolicy(gomail.NoTLS))
	case "tls":
		opts = append(opts, gomail.WithSSL())
	default:
		opts = append(opts, gomail.WithTLSPolicy(gomail.TLSMandatory))
	}

	if cfg.SMTPUser != "" {
		opts = append(opts,
			gomail.WithUsername(cfg.SMTPUser),
			gomail.WithPassword(cfg.SMTPPass),
			gomail.WithSMTPAuth(gomail.SMTPAuthPlain),
		)
	}

	client, err := gomail.NewClient(cfg.SMTPHost, opts...)
	if err != nil {
		return nil, fmt.Errorf("create smtp client: %w", err)
	}
	return &smtpMailer{from: cfg.SMTPFrom, client: client}, nil
}

func (m *smtpMailer) Enabled() bool { return true }

func (m *smtpMailer) Send(ctx context.Context, to, subject, htmlBody, textBody string) error {
	msg := gomail.NewMsg()
	if err := msg.From(m.from); err != nil {
		return fmt.Errorf("set from: %w", err)
	}
	if err := msg.To(to); err != nil {
		return fmt.Errorf("set to: %w", err)
	}
	msg.Subject(subject)
	msg.SetBodyString(gomail.TypeTextPlain, textBody)
	if htmlBody != "" {
		msg.AddAlternativeString(gomail.TypeTextHTML, htmlBody)
	}
	if err := m.client.DialAndSendWithContext(ctx, msg); err != nil {
		return fmt.Errorf("send mail: %w", err)
	}
	return nil
}
