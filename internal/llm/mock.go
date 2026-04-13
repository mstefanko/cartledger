package llm

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed testdata/sample-receipt.json
var sampleReceiptJSON []byte

// MockClient implements the Client interface with canned responses for development and testing.
type MockClient struct{}

// NewMockClient creates a new mock LLM client.
func NewMockClient() *MockClient {
	return &MockClient{}
}

// Provider returns "mock".
func (m *MockClient) Provider() string {
	return "mock"
}

// ExtractReceipt returns canned receipt extraction data loaded from testdata/sample-receipt.json.
func (m *MockClient) ExtractReceipt(images [][]byte) (*ReceiptExtraction, error) {
	if len(images) == 0 {
		return nil, fmt.Errorf("at least one image is required")
	}

	var extraction ReceiptExtraction
	if err := json.Unmarshal(sampleReceiptJSON, &extraction); err != nil {
		return nil, fmt.Errorf("failed to parse sample receipt JSON: %w", err)
	}

	return &extraction, nil
}
