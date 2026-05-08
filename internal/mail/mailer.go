package mail

import (
	"bytes"
	"context"
	"embed"
	htmltemplate "html/template"
	texttemplate "text/template"

	"github.com/mstefanko/cartledger/internal/config"
)

//go:embed templates/*
var templatesFS embed.FS

var (
	htmlTemplates = htmltemplate.Must(htmltemplate.ParseFS(templatesFS, "templates/*.html"))
	textTemplates = texttemplate.Must(texttemplate.ParseFS(templatesFS, "templates/*.txt"))
)

type Mailer interface {
	Send(ctx context.Context, to, subject, htmlBody, textBody string) error
	Enabled() bool
}

type noopMailer struct{}

func (noopMailer) Send(context.Context, string, string, string, string) error { return nil }
func (noopMailer) Enabled() bool                                              { return false }

func New(cfg *config.Config) (Mailer, error) {
	if cfg == nil || cfg.SMTPHost == "" {
		return noopMailer{}, nil
	}
	return newSMTPMailer(cfg)
}

type TemplateData struct {
	AppName      string
	Household    string
	InviterName  string
	Name         string
	URL          string
	SupportEmail string
}

func Render(name string, data TemplateData) (htmlBody, textBody string, err error) {
	if data.AppName == "" {
		data.AppName = "CartLedger"
	}

	var htmlOut bytes.Buffer
	if err := htmlTemplates.ExecuteTemplate(&htmlOut, name+".html", data); err != nil {
		return "", "", err
	}
	var textOut bytes.Buffer
	if err := textTemplates.ExecuteTemplate(&textOut, name+".txt", data); err != nil {
		return "", "", err
	}
	return htmlOut.String(), textOut.String(), nil
}
