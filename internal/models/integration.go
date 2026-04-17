package models

import (
	"encoding/json"
	"time"
)

// Integration represents a per-household external-service integration
// (e.g. Mealie, Tandoor, Grocy). Config holds secret credentials and must
// never be serialized to a response — json:"-" guards against accidental
// leaks if a handler returns the raw struct. API responses go through
// toIntegrationResponse in internal/api/integrations.go.
type Integration struct {
	ID          string          `json:"id"`
	HouseholdID string          `json:"household_id"`
	Type        string          `json:"type"`
	Enabled     bool            `json:"enabled"`
	Config      json.RawMessage `json:"-"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// MealieConfig is the decoded JSON stored in integrations.config when
// integrations.type == "mealie". The Version field is carried from day one so
// future shape changes (e.g. verify_tls) are migratable.
type MealieConfig struct {
	Version int    `json:"version"`
	BaseURL string `json:"base_url"`
	Token   string `json:"token"`
}

// Integration type constants.
const (
	IntegrationTypeMealie = "mealie"
)
