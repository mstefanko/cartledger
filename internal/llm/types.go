package llm

// ReceiptExtraction represents the structured data extracted from a receipt image by an LLM.
type ReceiptExtraction struct {
	StoreName    string          `json:"store_name"`
	StoreAddress *string         `json:"store_address"`
	Date         string          `json:"date"`
	Items        []ExtractedItem `json:"items"`
	Subtotal     float64         `json:"subtotal"`
	Tax          float64         `json:"tax"`
	Total        float64         `json:"total"`
	Confidence   float64         `json:"confidence"`
}

// ExtractedItem represents a single line item extracted from a receipt.
type ExtractedItem struct {
	RawName           string  `json:"raw_name"`
	SuggestedName     string  `json:"suggested_name"`
	SuggestedCategory string  `json:"suggested_category"`
	Quantity          float64 `json:"quantity"`
	Unit              *string `json:"unit"`
	UnitPrice         *float64 `json:"unit_price"`
	TotalPrice        float64 `json:"total_price"`
	LineNumber        int     `json:"line_number"`
	Confidence        float64 `json:"confidence"`
}
