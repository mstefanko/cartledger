package llm

import "testing"

func TestMockClient_Provider(t *testing.T) {
	c := NewMockClient()
	if got := c.Provider(); got != "mock" {
		t.Errorf("Provider() = %q, want %q", got, "mock")
	}
}

func TestMockClient_ExtractReceipt(t *testing.T) {
	c := NewMockClient()

	// Should fail with no images.
	_, err := c.ExtractReceipt(nil)
	if err == nil {
		t.Error("expected error for nil images, got nil")
	}

	// Should succeed with at least one image.
	result, err := c.ExtractReceipt([][]byte{{0xFF, 0xD8}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.StoreName != "ShopRite" {
		t.Errorf("StoreName = %q, want %q", result.StoreName, "ShopRite")
	}
	if len(result.Items) != 6 {
		t.Errorf("len(Items) = %d, want 6", len(result.Items))
	}
	if result.Total != 41.13 {
		t.Errorf("Total = %f, want 41.13", result.Total)
	}
}

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			"plain json",
			`{"key": "value"}`,
			`{"key": "value"}`,
		},
		{
			"json in code fence",
			"```json\n{\"key\": \"value\"}\n```",
			`{"key": "value"}`,
		},
		{
			"json in plain fence",
			"```\n{\"key\": \"value\"}\n```",
			`{"key": "value"}`,
		},
		{
			"json with surrounding text",
			"Here is the result:\n{\"key\": \"value\"}\nDone.",
			`{"key": "value"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractJSON(tt.input)
			if got != tt.want {
				t.Errorf("extractJSON() = %q, want %q", got, tt.want)
			}
		})
	}
}
