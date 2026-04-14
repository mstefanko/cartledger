package llm

// ReceiptExtraction represents the structured data extracted from a receipt image by an LLM.
type ReceiptExtraction struct {
	StoreName    string          `json:"store_name"`
	StoreAddress     *string         `json:"store_address"`
	StoreCity        *string         `json:"store_city"`
	StoreState       *string         `json:"store_state"`
	StoreZip         *string         `json:"store_zip"`
	StoreNumber      *string         `json:"store_number"`
	Date             string          `json:"date"`
	PaymentCardType  *string         `json:"payment_card_type"`
	PaymentCardLast4 *string         `json:"payment_card_last4"`
	Time             *string         `json:"time"`
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
	SuggestedBrand    string  `json:"suggested_brand"`
	SuggestedTags     string  `json:"suggested_tags"`
	Quantity          float64 `json:"quantity"`
	Unit              *string `json:"unit"`
	UnitPrice         *float64 `json:"unit_price"`
	TotalPrice        float64  `json:"total_price"`
	RegularPrice      *float64 `json:"regular_price"`
	DiscountAmount    *float64 `json:"discount_amount"`
	LineNumber        int     `json:"line_number"`
	Confidence        float64 `json:"confidence"`
}
