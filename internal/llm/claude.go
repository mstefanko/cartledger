package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
)

// ClaudeClient implements the Client interface using Anthropic's Claude API.
type ClaudeClient struct {
	client *anthropic.Client
	model  string
}

// receiptTool defines the tool schema for structured receipt extraction.
var receiptTool = anthropic.ToolParam{
	Name:        "extract_receipt",
	Description: param.NewOpt("Extract structured data from a grocery receipt image"),
	InputSchema: anthropic.ToolInputSchemaParam{
		Properties: map[string]any{
			"store_name":         map[string]any{"type": "string"},
			"store_address":      map[string]any{"type": []any{"string", "null"}},
			"store_city":         map[string]any{"type": []any{"string", "null"}},
			"store_state":        map[string]any{"type": []any{"string", "null"}, "description": "two-letter state code"},
			"store_zip":          map[string]any{"type": []any{"string", "null"}},
			"store_number":       map[string]any{"type": []any{"string", "null"}, "description": "digits only, no '#' prefix"},
			"date":               map[string]any{"type": "string", "description": "YYYY-MM-DD"},
			"payment_card_type":  map[string]any{"type": []any{"string", "null"}, "enum": []any{"Visa", "Mastercard", "Amex", "Discover", "Debit", "EBT", "Cash", "Check", nil}},
			"payment_card_last4": map[string]any{"type": []any{"string", "null"}},
			"time":               map[string]any{"type": []any{"string", "null"}, "description": "HH:MM 24-hour"},
			"items": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"raw_name":           map[string]any{"type": "string"},
						"suggested_name":     map[string]any{"type": "string"},
						"suggested_brand":    map[string]any{"type": "string"},
						"suggested_tags":     map[string]any{"type": "string"},
						"suggested_category": map[string]any{"type": "string", "enum": []any{"Meat", "Produce", "Dairy", "Bakery", "Frozen", "Pantry", "Snacks", "Beverages", "Household", "Health", "Other"}},
						"quantity":           map[string]any{"type": "number"},
						"unit":               map[string]any{"type": []any{"string", "null"}},
						"unit_price":         map[string]any{"type": []any{"number", "null"}},
						"total_price":        map[string]any{"type": "number"},
						"regular_price":      map[string]any{"type": []any{"number", "null"}},
						"discount_amount":    map[string]any{"type": []any{"number", "null"}},
						"line_number":        map[string]any{"type": "integer"},
						"confidence":         map[string]any{"type": "number"},
					},
					"required": []string{"raw_name", "suggested_name", "suggested_brand", "suggested_tags", "suggested_category", "quantity", "unit", "unit_price", "total_price", "regular_price", "discount_amount", "line_number", "confidence"},
				},
			},
			"subtotal":   map[string]any{"type": "number"},
			"tax":        map[string]any{"type": "number"},
			"total":      map[string]any{"type": "number"},
			"confidence": map[string]any{"type": "number"},
		},
		Required: []string{"store_name", "store_address", "store_city", "store_state", "store_zip", "store_number", "date", "payment_card_type", "payment_card_last4", "time", "items", "subtotal", "tax", "total", "confidence"},
	},
}

// NewClaudeClient creates a new Claude LLM client.
// If apiKey is empty, the SDK falls back to the ANTHROPIC_API_KEY environment variable.
// If model is empty, it defaults to claude-sonnet-4-20250514.
func NewClaudeClient(apiKey string, model string) *ClaudeClient {
	var opts []option.RequestOption
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}
	client := anthropic.NewClient(opts...)
	return &ClaudeClient{
		client: &client,
		model:  model,
	}
}

// Provider returns "claude".
func (c *ClaudeClient) Provider() string {
	return "claude"
}

// ExtractReceipt sends receipt images to Claude and returns structured extraction data.
func (c *ClaudeClient) ExtractReceipt(images [][]byte) (*ReceiptExtraction, error) {
	if len(images) == 0 {
		return nil, fmt.Errorf("at least one image is required")
	}

	// Build content blocks: images first, then text prompt.
	var contentBlocks []anthropic.ContentBlockParamUnion

	for _, img := range images {
		mediaType := detectMediaType(img)
		b64 := base64.StdEncoding.EncodeToString(img)
		contentBlocks = append(contentBlocks, anthropic.NewImageBlockBase64(mediaType, b64))
	}

	contentBlocks = append(contentBlocks, anthropic.NewTextBlock(receiptExtractionPrompt))

	resp, err := c.client.Messages.New(context.Background(), anthropic.MessageNewParams{
		Model:     c.model,
		MaxTokens: 4096,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(contentBlocks...),
		},
		Tools:      []anthropic.ToolUnionParam{{OfTool: &receiptTool}},
		ToolChoice: anthropic.ToolChoiceParamOfTool("extract_receipt"),
	})
	if err != nil {
		return nil, fmt.Errorf("claude API call failed: %w", err)
	}

	slog.Info("claude: token usage",
		"model", c.model,
		"input", resp.Usage.InputTokens,
		"output", resp.Usage.OutputTokens,
		"cache_create", resp.Usage.CacheCreationInputTokens,
		"cache_read", resp.Usage.CacheReadInputTokens,
	)

	// Find the tool_use block in the response.
	for _, block := range resp.Content {
		if block.Type == "tool_use" && block.Name == "extract_receipt" {
			var extraction ReceiptExtraction
			if err := json.Unmarshal(block.Input, &extraction); err != nil {
				return nil, fmt.Errorf("failed to parse tool_use input: %w", err)
			}
			return &extraction, nil
		}
	}

	return nil, fmt.Errorf("no extract_receipt tool_use block in response")
}

// detectMediaType inspects the first bytes of image data to determine its MIME type.
func detectMediaType(data []byte) string {
	ct := http.DetectContentType(data)
	switch {
	case strings.HasPrefix(ct, "image/jpeg"):
		return "image/jpeg"
	case strings.HasPrefix(ct, "image/png"):
		return "image/png"
	case strings.HasPrefix(ct, "image/gif"):
		return "image/gif"
	case strings.HasPrefix(ct, "image/webp"):
		return "image/webp"
	default:
		return "image/jpeg" // Default to JPEG.
	}
}
