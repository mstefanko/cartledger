package providers

import (
	"context"
	"time"

	"github.com/mstefanko/cartledger/internal/enrichment"
)

type LookupInput struct {
	HouseholdID  string
	ProductID    string
	ProductName  string
	Brand        *string
	UPC          *string
	URL          *string
	LookupKey    string
	USDAAPIKey   string
	AllowPrivate bool
}

type Metadata struct {
	Source         string
	SourceRecordID *string
	SourceURL      string
	LookupKey      string
	Confidence     float64
	Payload        enrichment.MetadataPayload
	Suggestions    []enrichment.Suggestion
	FetchedAt      time.Time
	ExpiresAt      *time.Time
	HTTPStatus     int
	LastError      *string
	ContentHash    *string
}

type Provider interface {
	Name() string
	Lookup(ctx context.Context, input LookupInput) ([]Metadata, error)
}

type RateLimitedProvider interface {
	Provider
	RateLimitKey() string
	MinInterval() time.Duration
}
