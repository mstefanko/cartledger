package llm

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

var ErrUnsupportedRepair = errors.New("receipt repair is not supported by this LLM provider")

// IsRateLimit reports whether err wraps an Anthropic 429 response.
func IsRateLimit(err error) bool {
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusTooManyRequests {
		return true
	}
	if err == nil {
		return false
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "429") ||
		strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "rate_limit") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "rate-limit") ||
		strings.Contains(msg, "rate limited") ||
		strings.Contains(msg, "rate-limited")
}

// RateLimitRetryAfter returns the provider's retry hint when a 429 response
// includes one. It understands Anthropic SDK responses with Retry-After-Ms or
// Retry-After headers; a zero duration means no usable hint was present.
func RateLimitRetryAfter(err error, now time.Time) time.Duration {
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusTooManyRequests {
		return 0
	}
	if apiErr.Response == nil {
		return 0
	}

	if raw := apiErr.Response.Header.Get("Retry-After-Ms"); raw != "" {
		ms, parseErr := strconv.ParseFloat(raw, 64)
		if parseErr == nil && ms > 0 {
			return time.Duration(ms * float64(time.Millisecond))
		}
	}

	raw := apiErr.Response.Header.Get("Retry-After")
	if raw == "" {
		return 0
	}
	if seconds, parseErr := strconv.ParseFloat(raw, 64); parseErr == nil && seconds > 0 {
		return time.Duration(seconds * float64(time.Second))
	}
	if retryAt, parseErr := http.ParseTime(raw); parseErr == nil && retryAt.After(now) {
		return retryAt.Sub(now)
	}
	return 0
}
