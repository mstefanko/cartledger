package llm

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

func anthropicStatusError(t *testing.T, status int, headers http.Header) error {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	return &anthropic.Error{
		StatusCode: status,
		Request:    req,
		Response: &http.Response{
			StatusCode: status,
			Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
			Header:     headers,
			Request:    req,
			Body:       http.NoBody,
		},
	}
}

func TestIsRateLimit(t *testing.T) {
	err := fmt.Errorf("claude API call failed: %w", anthropicStatusError(t, http.StatusTooManyRequests, nil))
	if !IsRateLimit(err) {
		t.Fatal("expected wrapped Anthropic 429 to be classified as rate limit")
	}

	if !IsRateLimit(fmt.Errorf("claude API call failed: 429 Too Many Requests: rate_limit_error")) {
		t.Fatal("expected string-shaped 429 to be classified as rate limit")
	}

	if IsRateLimit(anthropicStatusError(t, http.StatusInternalServerError, nil)) {
		t.Fatal("did not expect 500 to be classified as rate limit")
	}
}

func TestRateLimitRetryAfter(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		headers http.Header
		want    time.Duration
	}{
		{
			name:    "retry after milliseconds",
			headers: http.Header{"Retry-After-Ms": []string{"1500"}},
			want:    1500 * time.Millisecond,
		},
		{
			name:    "retry after seconds",
			headers: http.Header{"Retry-After": []string{"42"}},
			want:    42 * time.Second,
		},
		{
			name:    "retry after date",
			headers: http.Header{"Retry-After": []string{now.Add(2 * time.Minute).Format(http.TimeFormat)}},
			want:    2 * time.Minute,
		},
		{
			name:    "invalid retry after",
			headers: http.Header{"Retry-After": []string{"soon"}},
			want:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := fmt.Errorf("claude API call failed: %w", anthropicStatusError(t, http.StatusTooManyRequests, tt.headers))
			if got := RateLimitRetryAfter(err, now); got != tt.want {
				t.Fatalf("RateLimitRetryAfter() = %v, want %v", got, tt.want)
			}
		})
	}
}
