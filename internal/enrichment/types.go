package enrichment

type Suggestion struct {
	Field      string
	Value      string
	Evidence   string
	Confidence float64
	Source     string
	SourceURL  string
}

func NewSuggestion(source, sourceURL, field, value, evidence string, confidence float64) Suggestion {
	return Suggestion{
		Field:      field,
		Value:      value,
		Evidence:   evidence,
		Confidence: confidence,
		Source:     source,
		SourceURL:  sourceURL,
	}
}

type MetadataPayload struct {
	Version        int                 `json:"version"`
	Source         string              `json:"source"`
	SourceRecordID *string             `json:"source_record_id,omitempty"`
	SourceURL      *string             `json:"source_url,omitempty"`
	Identifiers    []PayloadIdentifier `json:"identifiers,omitempty"`
	Name           *string             `json:"name,omitempty"`
	Brand          *string             `json:"brand,omitempty"`
	Category       *string             `json:"category,omitempty"`
	Tags           []string            `json:"tags,omitempty"`
	Package        *PackagePayload     `json:"package,omitempty"`
	Serving        *ServingPayload     `json:"serving,omitempty"`
	Nutrients      *NutrientPayload    `json:"nutrients,omitempty"`
	Ingredients    *string             `json:"ingredients,omitempty"`
	Allergens      []string            `json:"allergens,omitempty"`
	ImageURLs      map[string]string   `json:"image_urls,omitempty"`
	ProviderMeta   map[string]string   `json:"provider_meta,omitempty"`
	Evidence       []EvidencePayload   `json:"evidence,omitempty"`
}

type PayloadIdentifier struct {
	Type      string  `json:"type"`
	Authority *string `json:"authority,omitempty"`
	Value     string  `json:"value"`
}

type PackagePayload struct {
	Label    *string  `json:"label,omitempty"`
	Quantity *float64 `json:"quantity,omitempty"`
	Unit     *string  `json:"unit,omitempty"`
}

type ServingPayload struct {
	Label                *string  `json:"label,omitempty"`
	Quantity             *float64 `json:"quantity,omitempty"`
	Unit                 *string  `json:"unit,omitempty"`
	ServingsPerContainer *float64 `json:"servings_per_container,omitempty"`
}

type NutrientPayload struct {
	Calories           *float64 `json:"calories,omitempty"`
	TotalFatG          *float64 `json:"total_fat_g,omitempty"`
	SaturatedFatG      *float64 `json:"saturated_fat_g,omitempty"`
	TransFatG          *float64 `json:"trans_fat_g,omitempty"`
	CholesterolMG      *float64 `json:"cholesterol_mg,omitempty"`
	SodiumMG           *float64 `json:"sodium_mg,omitempty"`
	TotalCarbohydrateG *float64 `json:"total_carbohydrate_g,omitempty"`
	DietaryFiberG      *float64 `json:"dietary_fiber_g,omitempty"`
	TotalSugarsG       *float64 `json:"total_sugars_g,omitempty"`
	AddedSugarsG       *float64 `json:"added_sugars_g,omitempty"`
	ProteinG           *float64 `json:"protein_g,omitempty"`
}

type EvidencePayload struct {
	Field string  `json:"field"`
	Text  string  `json:"text"`
	URL   *string `json:"url,omitempty"`
}
