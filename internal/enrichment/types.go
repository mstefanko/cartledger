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
