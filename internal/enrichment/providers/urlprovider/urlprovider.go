package urlprovider

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/mstefanko/cartledger/internal/enrichment"
	"github.com/mstefanko/cartledger/internal/enrichment/adapters"
	"github.com/mstefanko/cartledger/internal/enrichment/providers"
	"github.com/mstefanko/cartledger/internal/enrichment/store"
	"github.com/mstefanko/cartledger/internal/httpsafe"
)

type Provider struct {
	Client *httpsafe.SafeHTTPClient
}

func New(allowPrivate bool) *Provider {
	return &Provider{Client: httpsafe.NewSafeHTTPClient(8*time.Second, 512*1024, allowPrivate)}
}

func (p *Provider) Name() string { return "url" }

func (p *Provider) Lookup(ctx context.Context, input providers.LookupInput) ([]providers.Metadata, error) {
	if input.URL == nil || strings.TrimSpace(*input.URL) == "" {
		return nil, nil
	}
	client := p.Client
	if client == nil {
		client = httpsafe.NewSafeHTTPClient(8*time.Second, 512*1024, input.AllowPrivate)
	}
	result, err := client.Fetch(ctx, *input.URL)
	if err != nil {
		return nil, err
	}
	source, externalID, _ := ClassifyProductURL(result.URL)
	visibleText := enrichment.VisibleText(result.Body)
	suggestions := suggestionsForURL(result.URL, visibleText)
	hash := store.HashBytes([]byte(visibleText))
	confidence := sourceConfidenceForSuggestions(suggestions)
	payload := enrichment.MetadataPayload{
		Version:        1,
		Source:         source,
		SourceRecordID: externalID,
		SourceURL:      &result.URL,
		ProviderMeta: map[string]string{
			"content_type": result.ContentType,
		},
		Evidence: []enrichment.EvidencePayload{{
			Field: "visible_text",
			Text:  truncateEvidence(visibleText, 2000),
			URL:   &result.URL,
		}},
	}
	return []providers.Metadata{{
		Source:         source,
		SourceRecordID: externalID,
		SourceURL:      result.URL,
		LookupKey:      input.LookupKey,
		Confidence:     confidence,
		Payload:        payload,
		Suggestions:    suggestions,
		FetchedAt:      result.FetchedAt,
		HTTPStatus:     result.StatusCode,
		ContentHash:    &hash,
	}}, nil
}

func suggestionsForURL(rawURL, visibleText string) []enrichment.Suggestion {
	if adapters.Matches(rawURL) {
		return adapters.Parse(rawURL, visibleText)
	}
	return nil
}

func ClassifyProductURL(rawURL string) (string, *string, *string) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "url", nil, nil
	}
	host := strings.ToLower(u.Hostname())
	if host == "kroger.com" || strings.HasSuffix(host, ".kroger.com") {
		var externalID *string
		for _, part := range strings.Split(strings.Trim(u.Path, "/"), "/") {
			if len(part) >= 8 && isDigits(part) {
				value := part
				externalID = &value
			}
		}
		return "kroger", externalID, stringPtr("Kroger product page")
	}
	if host != "" {
		hash := store.HashBytes([]byte(rawURL))
		return "url", &hash, stringPtr(host)
	}
	return "url", nil, nil
}

func sourceConfidenceForSuggestions(suggestions []enrichment.Suggestion) float64 {
	if len(suggestions) == 0 {
		return 0
	}
	var total float64
	for _, s := range suggestions {
		total += s.Confidence
	}
	return total / float64(len(suggestions))
}

func truncateEvidence(value string, max int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= max {
		return value
	}
	return value[:max]
}

func isDigits(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != ""
}

func stringPtr(value string) *string { return &value }
