// Package locks implements an in-memory per-list optimistic lock store used
// to serialize shopping-list edit sessions across household members.
//
// Single-node only. All state is held in a process-local map guarded by a
// RWMutex; restarting the server drops every outstanding lock, and a second
// process instance would not see the first's holders. If this service is ever
// run with more than one replica, this store must be replaced with a shared
// backend (Redis SET NX + TTL, or a DB row with a lease column).
//
// See tmp/ux_flows/multi-store-implementation-plan.md §5 "Risks" (item 5)
// for context on when this becomes a problem.
package locks

import (
	"sync"
	"time"
)

// Holder describes the user currently holding a list lock.
type Holder struct {
	UserID      string    `json:"user_id"`
	UserName    string    `json:"user_name"`
	HouseholdID string    `json:"household_id"`
	LastTouched time.Time `json:"last_touched"`
}

// Store is a concurrency-safe, in-memory map from listID to its current
// Holder. Entries are pruned in the background by sweepLoop when
// `LastTouched` is older than inactivityTTL.
type Store struct {
	mu            sync.RWMutex
	locks         map[string]*Holder
	inactivityTTL time.Duration
	now           func() time.Time // injectable clock for tests
	done          chan struct{}
}

// NewStore returns a Store with a background sweeper goroutine already
// running. The sweeper wakes every 10 seconds (or the TTL, whichever is
// smaller) and drops any holder whose LastTouched exceeds inactivityTTL.
func NewStore(inactivityTTL time.Duration) *Store {
	s := &Store{
		locks:         map[string]*Holder{},
		inactivityTTL: inactivityTTL,
		now:           time.Now,
		done:          make(chan struct{}),
	}
	go s.sweepLoop()
	return s
}

// Close stops the background sweeper goroutine. Tests should call this to
// avoid leaking goroutines between cases.
func (s *Store) Close() {
	select {
	case <-s.done:
		// already closed
	default:
		close(s.done)
	}
}

// Acquire tries to take the lock for listID on behalf of (userID, userName).
//
// Returns (true, holder) if:
//   - the lock was free (or expired), or
//   - the caller already holds it (in which case LastTouched is refreshed).
//
// Returns (false, holder) if a different user currently holds the lock.
func (s *Store) Acquire(listID, userID, userName, householdID string) (bool, *Holder) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	existing, ok := s.locks[listID]
	if ok && !s.expiredLocked(existing, now) {
		if existing.UserID == userID {
			existing.LastTouched = now
			return true, copyHolder(existing)
		}
		return false, copyHolder(existing)
	}
	// Free or expired — take it.
	h := &Holder{
		UserID:      userID,
		UserName:    userName,
		HouseholdID: householdID,
		LastTouched: now,
	}
	s.locks[listID] = h
	return true, copyHolder(h)
}

// Touch refreshes LastTouched on an existing hold. Returns true if userID is
// the current holder (and the lock is still live); otherwise false without
// mutating state.
func (s *Store) Touch(listID, userID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	existing, ok := s.locks[listID]
	if !ok || s.expiredLocked(existing, now) {
		return false
	}
	if existing.UserID != userID {
		return false
	}
	existing.LastTouched = now
	return true
}

// Release drops the lock for listID if userID is the current holder. No-ops
// silently otherwise — stale releases from old sessions must not clobber a
// fresh holder.
func (s *Store) Release(listID, userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.locks[listID]
	if !ok {
		return
	}
	if existing.UserID != userID {
		return
	}
	delete(s.locks, listID)
}

// TakeOver force-transfers the lock to (userID, userName) regardless of the
// current holder. Returns the prior holder (nil if the lock was free or had
// expired). The returned Holder is a snapshot copy; callers may keep it
// beyond the critical section.
func (s *Store) TakeOver(listID, userID, userName, householdID string) *Holder {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	var prior *Holder
	if existing, ok := s.locks[listID]; ok {
		if !s.expiredLocked(existing, now) && existing.UserID != userID {
			prior = copyHolder(existing)
		}
	}
	s.locks[listID] = &Holder{
		UserID:      userID,
		UserName:    userName,
		HouseholdID: householdID,
		LastTouched: now,
	}
	return prior
}

// Current returns a snapshot of the current holder, or nil if the lock is
// free or has expired. Expired entries are NOT pruned here (sweep does that)
// so concurrent readers see a consistent view.
func (s *Store) Current(listID string) *Holder {
	s.mu.RLock()
	defer s.mu.RUnlock()
	existing, ok := s.locks[listID]
	if !ok {
		return nil
	}
	if s.expiredLocked(existing, s.now()) {
		return nil
	}
	return copyHolder(existing)
}

// sweepLoop is the background goroutine started by NewStore. It calls
// sweep() every 10 seconds until Close() is called.
func (s *Store) sweepLoop() {
	interval := 10 * time.Second
	if s.inactivityTTL > 0 && s.inactivityTTL < interval {
		interval = s.inactivityTTL
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
			s.sweep()
		}
	}
}

// sweep removes every holder whose LastTouched is older than inactivityTTL.
// Exposed (unexported) so tests can trigger a deterministic sweep without
// waiting on the ticker.
func (s *Store) sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for listID, h := range s.locks {
		if s.expiredLocked(h, now) {
			delete(s.locks, listID)
		}
	}
}

// expiredLocked reports whether h should be treated as expired. Caller must
// hold s.mu (read or write).
func (s *Store) expiredLocked(h *Holder, now time.Time) bool {
	if s.inactivityTTL <= 0 {
		return false
	}
	return now.Sub(h.LastTouched) > s.inactivityTTL
}

// copyHolder returns a defensive copy so internal pointers never escape the
// mutex.
func copyHolder(h *Holder) *Holder {
	if h == nil {
		return nil
	}
	c := *h
	return &c
}
