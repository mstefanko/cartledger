package runner

import (
	"context"
	"sync"
	"time"
)

type ProviderLimiter struct {
	mu   sync.Mutex
	next map[string]time.Time
}

func NewProviderLimiter() *ProviderLimiter {
	return &ProviderLimiter{next: map[string]time.Time{}}
}

func (l *ProviderLimiter) Wait(ctx context.Context, key string, interval time.Duration) error {
	if interval <= 0 || key == "" {
		return nil
	}
	l.mu.Lock()
	now := time.Now()
	waitUntil := l.next[key]
	if waitUntil.Before(now) {
		l.next[key] = now.Add(interval)
		l.mu.Unlock()
		return nil
	}
	l.next[key] = waitUntil.Add(interval)
	l.mu.Unlock()

	timer := time.NewTimer(time.Until(waitUntil))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
