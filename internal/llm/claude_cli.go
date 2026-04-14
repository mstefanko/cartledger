package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// CLIClient implements the Client interface by spawning the Claude Code CLI.
// This uses the user's CLI subscription billing instead of direct API calls.
type CLIClient struct {
	claudePath string
	model      string
	timeout    time.Duration
}

// NewCLIClient creates a new CLI-based Claude client.
func NewCLIClient() (*CLIClient, error) {
	path, err := exec.LookPath("claude")
	if err != nil {
		return nil, fmt.Errorf("claude CLI not found on PATH: %w", err)
	}
	return &CLIClient{
		claudePath: path,
		model:      "sonnet",
		timeout:    180 * time.Second,
	}, nil
}

// Provider returns "claude-cli".
func (c *CLIClient) Provider() string {
	return "claude-cli"
}

// ExtractReceipt base64-encodes images into the prompt and sends a single
// API call through the Claude CLI. No tools needed — eliminates the Read
// tool round-trip that was causing 3-minute processing times.
func (c *CLIClient) ExtractReceipt(images [][]byte) (*ReceiptExtraction, error) {
	if len(images) == 0 {
		return nil, fmt.Errorf("at least one image is required")
	}

	// Build prompt with inline base64 images.
	var prompt strings.Builder
	for i, img := range images {
		mediaType := detectMediaType(img)
		b64 := base64.StdEncoding.EncodeToString(img)
		fmt.Fprintf(&prompt, "[Receipt image %d (%s, %d bytes original)]\ndata:%s;base64,%s\n\n",
			i+1, mediaType, len(img), mediaType, b64)
	}
	fmt.Fprintf(&prompt, "%s\n\nReturn ONLY the JSON object, no markdown fences or explanation.", receiptExtractionPrompt)

	// Spawn claude CLI: no tools needed (base64 image is in the prompt),
	// low effort for structured extraction, stdin for large prompts.
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.claudePath,
		"--print",
		"--model", c.model,
		"--output-format", "text",
		"--effort", "low",
		"--no-session-persistence",
		"--settings", `{"hooks":{}}`,
		"-",
	)

	cmd.Stdin = strings.NewReader(prompt.String())

	// Strip CLAUDECODE to avoid nesting error, ANTHROPIC_API_KEY for subscription billing.
	cmd.Env = filterEnv(os.Environ(), "ANTHROPIC_API_KEY", "CLAUDECODE")

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("claude CLI timed out after %s (stderr: %s)", c.timeout, stderr.String())
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("claude CLI failed (exit %d): %s", exitErr.ExitCode(), string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("claude CLI failed: %w (stderr: %s)", err, stderr.String())
	}
	if stderr.Len() > 0 {
		fmt.Fprintf(os.Stderr, "claude CLI stderr: %s\n", stderr.String())
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
