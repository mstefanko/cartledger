package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// ClaudeClient implements the Client interface using Anthropic's Claude API.
type ClaudeClient struct {
	client *anthropic.Client
	model  string
}

// NewClaudeClient creates a new Claude LLM client.
func NewClaudeClient(apiKey string) *ClaudeClient {
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &ClaudeClient{
		client: &client,
		model:  "claude-sonnet-4-20250514",
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
	})
	if err != nil {
		return nil, fmt.Errorf("claude API call failed: %w", err)
	}

	// Extract text from response.
	var text string
	for _, block := range resp.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}

	if text == "" {
		return nil, fmt.Errorf("empty response from Claude")
	}

	// Parse JSON from the response text (may be wrapped in markdown code fences).
	jsonStr := extractJSON(text)

	var extraction ReceiptExtraction
	if err := json.Unmarshal([]byte(jsonStr), &extraction); err != nil {
		return nil, fmt.Errorf("failed to parse extraction JSON: %w (response: %s)", err, text)
	}

	return &extraction, nil
}

// extractJSON attempts to extract a JSON object from text that may contain
// markdown code fences or other surrounding content.
func extractJSON(text string) string {
	// Try to find JSON within code fences first.
	if start := strings.Index(text, "```json"); start != -1 {
		content := text[start+7:]
		if end := strings.Index(content, "```"); end != -1 {
			return strings.TrimSpace(content[:end])
		}
	}
	if start := strings.Index(text, "```"); start != -1 {
		content := text[start+3:]
		if end := strings.Index(content, "```"); end != -1 {
			return strings.TrimSpace(content[:end])
		}
	}

	// Try to find a raw JSON object.
	if start := strings.Index(text, "{"); start != -1 {
		if end := strings.LastIndex(text, "}"); end != -1 {
			return text[start : end+1]
		}
	}

	return text
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
