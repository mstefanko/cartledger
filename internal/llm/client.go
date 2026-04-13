package llm

// Client defines the interface for LLM-based receipt extraction.
type Client interface {
	// ExtractReceipt sends one or more receipt images to an LLM and returns structured extraction.
	ExtractReceipt(images [][]byte) (*ReceiptExtraction, error)

	// Provider returns the name of the LLM provider: "claude", "gemini", or "mock".
	Provider() string
}
