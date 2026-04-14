package llm

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CLIClient implements the Client interface by spawning the Claude Code CLI.
// This uses the user's CLI subscription billing instead of direct API calls.
type CLIClient struct {
	claudePath string
	model      string
}

// NewCLIClient creates a new CLI-based Claude client.
// If claudePath is empty, it looks for "claude" on PATH.
func NewCLIClient() (*CLIClient, error) {
	path, err := exec.LookPath("claude")
	if err != nil {
		return nil, fmt.Errorf("claude CLI not found on PATH: %w", err)
	}
	return &CLIClient{
		claudePath: path,
		model:      "sonnet",
	}, nil
}

// Provider returns "claude-cli".
func (c *CLIClient) Provider() string {
	return "claude-cli"
}

// ExtractReceipt writes images to temp files, invokes the Claude CLI with the
// receipt extraction prompt, and parses the JSON response.
func (c *CLIClient) ExtractReceipt(images [][]byte) (*ReceiptExtraction, error) {
	if len(images) == 0 {
		return nil, fmt.Errorf("at least one image is required")
	}

	// Write images to temp files so the CLI can read them.
	tmpDir, err := os.MkdirTemp("", "cartledger-receipt-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	var imagePaths []string
	for i, img := range images {
		ext := detectExtension(img)
		path := filepath.Join(tmpDir, fmt.Sprintf("receipt-%d%s", i, ext))
		if err := os.WriteFile(path, img, 0600); err != nil {
			return nil, fmt.Errorf("write temp image: %w", err)
		}
		imagePaths = append(imagePaths, path)
	}

	// Build the prompt: tell Claude to read each image file, then extract.
	var prompt strings.Builder
	for _, p := range imagePaths {
		fmt.Fprintf(&prompt, "Read the image file at %s\n", p)
	}
	fmt.Fprintf(&prompt, "\n%s\n\nReturn ONLY the JSON object, no markdown fences or explanation.", receiptExtractionPrompt)

	// Spawn claude CLI in print mode with only the Read tool available.
	cmd := exec.Command(c.claudePath,
		"--print",
		"--model", c.model,
		"--output-format", "text",
		"--tools", "Read",
		"--dangerously-skip-permissions",
		prompt.String(),
	)

	// Strip ANTHROPIC_API_KEY so the CLI uses subscription billing.
	cmd.Env = filterEnv(os.Environ(), "ANTHROPIC_API_KEY")

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("claude CLI failed: %s\n%s", err, string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("claude CLI failed: %w", err)
	}

	// Parse JSON from the response.
	text := strings.TrimSpace(string(output))
	jsonStr := extractJSON(text)

	var extraction ReceiptExtraction
	if err := json.Unmarshal([]byte(jsonStr), &extraction); err != nil {
		return nil, fmt.Errorf("failed to parse extraction JSON: %w (response: %s)", err, text)
	}

	return &extraction, nil
}

// filterEnv returns env vars with the specified keys removed.
func filterEnv(env []string, removeKeys ...string) []string {
	blocked := make(map[string]bool, len(removeKeys))
	for _, k := range removeKeys {
		blocked[k] = true
	}
	var filtered []string
	for _, e := range env {
		key, _, _ := strings.Cut(e, "=")
		if !blocked[key] {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// detectExtension returns a file extension based on image magic bytes.
func detectExtension(data []byte) string {
	if len(data) < 4 {
		return ".jpg"
	}
	switch {
	case data[0] == 0x89 && data[1] == 0x50: // PNG
		return ".png"
	case data[0] == 0xFF && data[1] == 0xD8: // JPEG
		return ".jpg"
	case data[0] == 0x47 && data[1] == 0x49: // GIF
		return ".gif"
	case data[0] == 0x52 && data[1] == 0x49: // RIFF (WebP)
		return ".webp"
	default:
		return ".jpg"
	}
}
