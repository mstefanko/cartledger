package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
	"github.com/mstefanko/cartledger/internal/llm"
	"github.com/mstefanko/cartledger/internal/matcher"
	"github.com/mstefanko/cartledger/internal/ws"
)

// blockingLLM implements llm.Client. Each ExtractReceipt call blocks on <-start
// and returns whatever sample the mock returns.
type blockingLLM struct {
	start  chan struct{}
	inner  *llm.MockClient
	called chan struct{}
}

func (b *blockingLLM) Provider() string { return "mock-blocking" }

func (b *blockingLLM) ExtractReceipt(images [][]byte) (*llm.ReceiptExtraction, error) {
	select {
	case b.called <- struct{}{}:
	default:
	}
	<-b.start
	return b.inner.ExtractReceipt(images)
}

// TestShutdown_DrainsBufferedJobs exercises the wg-accounting invariant that
// Submit adds to wg (not process). With 1 worker and 5 submits (1 in-flight,
// 4 buffered), a 100ms Shutdown context must:
//   - return within a small multiple of the deadline (no indefinite wait)
//   - mark all 4 buffered jobs pending in DB
//   - not deadlock wg.Wait
func TestShutdown_DrainsBufferedJobs(t *testing.T) {
	// --- DB setup ---
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer database.Close()
	if err := db.RunMigrations(database); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Seed a household and 5 receipts in 'processing' state.
	var householdID string
	if err := database.QueryRow("INSERT INTO households (name) VALUES ('Test') RETURNING id").Scan(&householdID); err != nil {
		t.Fatalf("insert household: %v", err)
	}

	const nJobs = 5
	receiptIDs := make([]string, nJobs)
	receiptsDir := filepath.Join(dir, "receipts")
	if err := os.MkdirAll(receiptsDir, 0o755); err != nil {
		t.Fatalf("mkdir receipts: %v", err)
	}
	for i := 0; i < nJobs; i++ {
		var id string
		if err := database.QueryRow("SELECT lower(hex(randomblob(16)))").Scan(&id); err != nil {
			t.Fatalf("gen receipt id: %v", err)
		}
		receiptIDs[i] = id
		if _, err := database.Exec(
			"INSERT INTO receipts (id, household_id, receipt_date, total, status) VALUES (?, ?, '2024-01-01', '10.00', 'processing')",
			id, householdID,
		); err != nil {
			t.Fatalf("insert receipt: %v", err)
		}
		// Create a matching image dir so Resubmit-style paths work (Submit
		// doesn't need this, but it mirrors production layout).
		if err := os.MkdirAll(filepath.Join(receiptsDir, id), 0o755); err != nil {
			t.Fatalf("mkdir receipt dir: %v", err)
		}
		// Touch a fake image so processJob (if it ran) would find something.
		if err := os.WriteFile(filepath.Join(receiptsDir, id, "img.jpg"), []byte("fake"), 0o644); err != nil {
			t.Fatalf("write fake image: %v", err)
		}
	}

	// --- Worker setup ---
	hub := ws.NewHub()
	go hub.Run()
	cfg := &config.Config{DataDir: dir}
	mockInner := llm.NewMockClient()
	blocking := &blockingLLM{
		start:  make(chan struct{}),
		inner:  mockInner,
		called: make(chan struct{}, 1),
	}
	matchEngine := matcher.NewEngine(database)

	// concurrency=1 so exactly one job is "in flight" and the rest buffer.
	w := NewReceiptWorker(1, blocking, nil, matchEngine, database, hub, cfg)

	// Submit all 5 jobs.
	for _, id := range receiptIDs {
		err := w.Submit(ReceiptJob{
			ReceiptID:   id,
			HouseholdID: householdID,
			ImageDir:    filepath.Join(receiptsDir, id),
		})
		if err != nil {
			t.Fatalf("Submit(%s): %v", id, err)
		}
	}

	// Wait until the LLM-blocked goroutine is actually inside ExtractReceipt
	// — otherwise the "in-flight" slot might still be empty when we hit
	// shutdown and the test wouldn't exercise the race path.
	select {
	case <-blocking.called:
	case <-time.After(2 * time.Second):
		t.Fatal("worker never called LLM — setup bug")
	}

	// --- Shutdown with a tight deadline ---
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- w.Shutdown(shutdownCtx)
	}()

	// Shutdown must return near the 100ms deadline (give generous slack).
	select {
	case shutdownErr := <-done:
		// Expect context.DeadlineExceeded because in-flight goroutine is
		// still blocked on blocking.start.
		if shutdownErr != context.DeadlineExceeded {
			t.Fatalf("expected DeadlineExceeded, got %v", shutdownErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not return within 5s — wg.Wait may be hanging")
	}

	// Unblock the in-flight goroutine so the worker can exit cleanly without
	// leaking goroutines into the next test.
	close(blocking.start)

	// --- Assertions ---
	// 4 buffered jobs must be marked 'pending'. The in-flight one ends in
	// whatever state its processing finishes with — we don't assert on it
	// beyond "no panic" (the test would have failed via goroutine dump).
	var pendingCount int
	if err := database.QueryRow(
		"SELECT COUNT(*) FROM receipts WHERE status = 'pending'",
	).Scan(&pendingCount); err != nil {
		t.Fatalf("count pending: %v", err)
	}
	if pendingCount < nJobs-1 {
		t.Errorf("expected at least %d receipts marked pending, got %d", nJobs-1, pendingCount)
	}

	// Give the formerly-in-flight goroutine a brief moment to finish up its
	// DB writes so the test doesn't leak a live goroutine into the race
	// detector summary.
	time.Sleep(100 * time.Millisecond)
}
