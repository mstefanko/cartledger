package locks

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newTestStore returns a Store whose background sweeper is stopped
// immediately — tests drive sweep() directly.
func newTestStore(ttl time.Duration) *Store {
	s := NewStore(ttl)
	s.Close()
	return s
}

func TestAcquire_Free(t *testing.T) {
	s := newTestStore(time.Minute)
	ok, holder := s.Acquire("list-1", "user-a", "Alice", "hh-1")
	if !ok {
		t.Fatalf("expected acquire to succeed on free lock")
	}
	if holder == nil || holder.UserID != "user-a" || holder.UserName != "Alice" {
		t.Fatalf("unexpected holder: %+v", holder)
	}
}

func TestAcquire_SelfRefreshes(t *testing.T) {
	s := newTestStore(time.Minute)
	_, first := s.Acquire("list-1", "user-a", "Alice", "hh-1")
	time.Sleep(2 * time.Millisecond)
	ok, second := s.Acquire("list-1", "user-a", "Alice", "hh-1")
	if !ok {
		t.Fatalf("expected self-acquire to refresh")
	}
	if !second.LastTouched.After(first.LastTouched) {
		t.Fatalf("expected LastTouched to advance: first=%v second=%v",
			first.LastTouched, second.LastTouched)
	}
}

func TestAcquire_OtherHolds(t *testing.T) {
	s := newTestStore(time.Minute)
	s.Acquire("list-1", "user-a", "Alice", "hh-1")
	ok, holder := s.Acquire("list-1", "user-b", "Bob", "hh-1")
	if ok {
		t.Fatalf("expected acquire to fail when another user holds the lock")
	}
	if holder == nil || holder.UserID != "user-a" {
		t.Fatalf("expected current holder returned, got %+v", holder)
	}
}

func TestTouch_Holder(t *testing.T) {
	s := newTestStore(time.Minute)
	s.Acquire("list-1", "user-a", "Alice", "hh-1")
	if !s.Touch("list-1", "user-a") {
		t.Fatalf("expected Touch by holder to succeed")
	}
}

func TestTouch_NonHolder(t *testing.T) {
	s := newTestStore(time.Minute)
	s.Acquire("list-1", "user-a", "Alice", "hh-1")
	if s.Touch("list-1", "user-b") {
		t.Fatalf("expected Touch by non-holder to fail")
	}
}

func TestRelease_Holder(t *testing.T) {
	s := newTestStore(time.Minute)
	s.Acquire("list-1", "user-a", "Alice", "hh-1")
	s.Release("list-1", "user-a")
	if s.Current("list-1") != nil {
		t.Fatalf("expected lock free after Release by holder")
	}
}

func TestRelease_NonHolderIsNoop(t *testing.T) {
	s := newTestStore(time.Minute)
	s.Acquire("list-1", "user-a", "Alice", "hh-1")
	s.Release("list-1", "user-b")
	cur := s.Current("list-1")
	if cur == nil || cur.UserID != "user-a" {
		t.Fatalf("expected lock preserved, got %+v", cur)
	}
}

func TestTakeOver(t *testing.T) {
	s := newTestStore(time.Minute)
	s.Acquire("list-1", "user-a", "Alice", "hh-1")
	prior := s.TakeOver("list-1", "user-b", "Bob", "hh-1")
	if prior == nil || prior.UserID != "user-a" {
		t.Fatalf("expected prior=user-a, got %+v", prior)
	}
	cur := s.Current("list-1")
	if cur == nil || cur.UserID != "user-b" {
		t.Fatalf("expected current=user-b, got %+v", cur)
	}
}

func TestTakeOver_FreeReturnsNilPrior(t *testing.T) {
	s := newTestStore(time.Minute)
	prior := s.TakeOver("list-1", "user-a", "Alice", "hh-1")
	if prior != nil {
		t.Fatalf("expected nil prior for free lock, got %+v", prior)
	}
	cur := s.Current("list-1")
	if cur == nil || cur.UserID != "user-a" {
		t.Fatalf("expected lock taken, got %+v", cur)
	}
}

func TestExpiry_SweepRemovesStaleHolder(t *testing.T) {
	s := newTestStore(50 * time.Millisecond)
	s.Acquire("list-1", "user-a", "Alice", "hh-1")
	time.Sleep(100 * time.Millisecond)
	s.sweep()
	if got := s.Current("list-1"); got != nil {
		t.Fatalf("expected lock swept after TTL, got %+v", got)
	}
}

func TestExpiry_CurrentHidesStaleHolder(t *testing.T) {
	s := newTestStore(25 * time.Millisecond)
	s.Acquire("list-1", "user-a", "Alice", "hh-1")
	time.Sleep(60 * time.Millisecond)
	if got := s.Current("list-1"); got != nil {
		t.Fatalf("expected Current to return nil for expired holder, got %+v", got)
	}
}

func TestConcurrentAcquire_ExactlyOneWinner(t *testing.T) {
	s := newTestStore(time.Minute)
	const n = 10
	var wg sync.WaitGroup
	var wins int64
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			ok, _ := s.Acquire("list-1", uid(i), "User", "hh-1")
			if ok {
				atomic.AddInt64(&wins, 1)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if wins != 1 {
		t.Fatalf("expected exactly 1 winner, got %d", wins)
	}
}

func uid(i int) string {
	return "user-" + string(rune('a'+i))
}
