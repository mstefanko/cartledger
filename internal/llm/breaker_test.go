package llm

import (
	"sync"
	"testing"
	"time"
)

// TestBreakerClosedToOpen verifies that the breaker trips after failureThreshold
// consecutive 429s within the failure window.
func TestBreakerClosedToOpen(t *testing.T) {
	b := NewBreaker(3, 10*time.Second, 120*time.Second, 30*time.Minute)
	if b.State() != BreakerClosed {
		t.Fatalf("initial state: want closed, got %v", b.State())
	}

	b.OnRateLimit()
	b.OnRateLimit()
	if b.State() != BreakerClosed {
		t.Fatalf("after 2 failures: want still closed, got %v", b.State())
	}
	b.OnRateLimit()
	if b.State() != BreakerOpen {
		t.Fatalf("after 3 failures: want open, got %v", b.State())
	}
}

// TestBreakerFailureWindow verifies that failures outside the window do not
// accumulate toward tripping.
func TestBreakerFailureWindow(t *testing.T) {
	b := NewBreaker(3, 10*time.Millisecond, 120*time.Second, 30*time.Minute)
	b.OnRateLimit()
	b.OnRateLimit()
	time.Sleep(20 * time.Millisecond)
	// First failure is stale — this one should reset the counter to 1.
	b.OnRateLimit()
	b.OnRateLimit()
	if b.State() != BreakerClosed {
		t.Fatalf("stale failures shouldn't trip breaker: got %v", b.State())
	}
}

// TestBreakerOpenRejects verifies that Allow returns false while open and
// cooldown has not elapsed.
func TestBreakerOpenRejects(t *testing.T) {
	b := NewBreaker(1, 10*time.Second, 1*time.Hour, 30*time.Minute)
	b.OnRateLimit() // trips with threshold=1
	if b.State() != BreakerOpen {
		t.Fatalf("want open, got %v", b.State())
	}
	allow, isProbe := b.Allow()
	if allow || isProbe {
		t.Fatalf("open breaker should reject: allow=%v probe=%v", allow, isProbe)
	}
}

// TestBreakerHalfOpenProbe verifies the open->half-open->closed happy path
// after cooldown elapses.
func TestBreakerHalfOpenProbe(t *testing.T) {
	b := NewBreaker(1, 10*time.Second, 10*time.Millisecond, 30*time.Minute)
	b.OnRateLimit()
	if b.State() != BreakerOpen {
		t.Fatalf("want open, got %v", b.State())
	}
	time.Sleep(15 * time.Millisecond) // exceed cooldown
	allow, isProbe := b.Allow()
	if !allow || !isProbe {
		t.Fatalf("after cooldown: want allow+probe, got allow=%v probe=%v", allow, isProbe)
	}
	// While probe is in flight, another caller must be rejected.
	allow2, _ := b.Allow()
	if allow2 {
		t.Fatalf("second caller during probe should be rejected")
	}
	b.OnSuccess()
	if b.State() != BreakerClosed {
		t.Fatalf("after successful probe: want closed, got %v", b.State())
	}
}

// TestBreakerHalfOpenFailureBackoff verifies exponential backoff on probe 429.
func TestBreakerHalfOpenFailureBackoff(t *testing.T) {
	b := NewBreaker(1, 10*time.Second, 10*time.Millisecond, 1*time.Second)
	b.OnRateLimit()
	time.Sleep(15 * time.Millisecond)
	allow, isProbe := b.Allow()
	if !allow || !isProbe {
		t.Fatalf("want probe allowed")
	}
	b.OnRateLimit() // probe failed
	if b.State() != BreakerOpen {
		t.Fatalf("want re-opened, got %v", b.State())
	}
	// Cooldown should have doubled (from 10ms to 20ms). The probe should NOT
	// be allowed yet after only 10ms.
	time.Sleep(10 * time.Millisecond)
	allow, _ = b.Allow()
	if allow {
		t.Fatalf("want backoff-extended reject; got allow")
	}
}

// TestBreakerConcurrent verifies no data race under concurrent Allow/OnRateLimit.
// Run with -race.
func TestBreakerConcurrent(t *testing.T) {
	b := NewBreaker(50, time.Second, time.Second, time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_, _ = b.Allow()
				b.OnRateLimit()
				b.OnSuccess()
			}
		}()
	}
	wg.Wait()
}
