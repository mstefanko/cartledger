package runner

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
	"github.com/mstefanko/cartledger/internal/enrichment"
	"github.com/mstefanko/cartledger/internal/enrichment/providers"
	"github.com/mstefanko/cartledger/internal/ws"
)

type fakeProvider struct {
	name string
	err  error
}

func (p fakeProvider) Name() string { return p.name }

func (p fakeProvider) Lookup(ctx context.Context, input providers.LookupInput) ([]providers.Metadata, error) {
	if p.err != nil {
		return nil, p.err
	}
	recordID := "fake-record"
	sourceURL := "https://example.com/product/fake-record"
	name := "Provider Name"
	payload := enrichment.MetadataPayload{
		Version:        1,
		Source:         p.name,
		SourceRecordID: &recordID,
		SourceURL:      &sourceURL,
		Name:           &name,
	}
	return []providers.Metadata{{
		Source:         p.name,
		SourceRecordID: &recordID,
		SourceURL:      sourceURL,
		LookupKey:      input.LookupKey,
		Confidence:     0.8,
		Payload:        payload,
		Suggestions: []enrichment.Suggestion{
			enrichment.NewSuggestion(p.name, sourceURL, "name", name, "provider fixture", 0.8),
		},
		FetchedAt:  time.Now().UTC(),
		HTTPStatus: 200,
	}}, nil
}

type recordingBroadcaster struct {
	mu       sync.Mutex
	messages []ws.Message
}

func (b *recordingBroadcaster) Broadcast(msg ws.Message) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.messages = append(b.messages, msg)
}

func (b *recordingBroadcaster) findMessage(messageType string) (ws.Message, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, msg := range b.messages {
		if msg.Type == messageType {
			return msg, true
		}
	}
	return ws.Message{}, false
}

func setupServiceTest(t *testing.T, provider providers.Provider) (*sql.DB, *Service, string, string, func()) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.RunMigrations(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var householdID string
	if err := database.QueryRow("INSERT INTO households (name) VALUES ('Test') RETURNING id").Scan(&householdID); err != nil {
		t.Fatalf("insert household: %v", err)
	}
	var productID string
	if err := database.QueryRow("INSERT INTO products (household_id, name) VALUES (?, 'Widget') RETURNING id", householdID).Scan(&productID); err != nil {
		t.Fatalf("insert product: %v", err)
	}

	broadcaster := &recordingBroadcaster{}
	service := NewServiceWithProviders(database, &config.Config{ProductEnrichmentEnabled: true, ProductEnrichmentAutoOnScan: true}, broadcaster, []providers.Provider{provider})
	cleanup := func() {
		database.Close()
		os.RemoveAll(dir)
	}
	return database, service, householdID, productID, cleanup
}

func TestProcessJobStoresSnapshotSuggestionsAndEmitsTerminalEvent(t *testing.T) {
	database, service, householdID, productID, cleanup := setupServiceTest(t, fakeProvider{name: "openfoodfacts"})
	defer cleanup()

	job, _, err := service.QueueJob(context.Background(), QueueJobRequest{
		HouseholdID: householdID,
		ProductID:   productID,
		LookupKey:   "upc:0001111008404",
	})
	if err != nil {
		t.Fatalf("queue job: %v", err)
	}
	if err := service.ProcessJob(context.Background(), job.ID); err != nil {
		t.Fatalf("process job: %v", err)
	}

	var status string
	if err := database.QueryRow("SELECT status FROM product_enrichment_jobs WHERE id = ?", job.ID).Scan(&status); err != nil {
		t.Fatalf("query job: %v", err)
	}
	if status != StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", status)
	}
	var snapshots, suggestions int
	if err := database.QueryRow("SELECT COUNT(*) FROM product_external_metadata WHERE product_id = ?", productID).Scan(&snapshots); err != nil {
		t.Fatalf("count snapshots: %v", err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM product_enrichment_suggestions WHERE product_id = ?", productID).Scan(&suggestions); err != nil {
		t.Fatalf("count suggestions: %v", err)
	}
	if snapshots != 1 || suggestions != 1 {
		t.Fatalf("snapshots=%d suggestions=%d, want 1 each", snapshots, suggestions)
	}
	broadcaster := service.Hub.(*recordingBroadcaster)
	msg, ok := broadcaster.findMessage(ws.EventProductEnrichmentJobUpdated)
	if !ok {
		t.Fatalf("terminal job event was not broadcast; messages=%+v", broadcaster.messages)
	}
	payload := msg.Payload.(map[string]interface{})
	if payload["status"] != StatusSucceeded {
		t.Fatalf("event payload = %+v", payload)
	}
	providerStatus := payload["provider_status"].(map[string]string)
	if providerStatus["openfoodfacts"] != "succeeded" {
		t.Fatalf("provider status = %+v", providerStatus)
	}
}

func TestProcessJobReportsRequestedDisabledProviderAsSkipped(t *testing.T) {
	database, service, householdID, productID, cleanup := setupServiceTest(t, fakeProvider{name: "openfoodfacts"})
	defer cleanup()

	job, _, err := service.QueueJob(context.Background(), QueueJobRequest{
		HouseholdID:      householdID,
		ProductID:        productID,
		LookupKey:        "upc:0001111008404",
		RequestedSources: []string{"openfoodfacts", "usda"},
	})
	if err != nil {
		t.Fatalf("queue job: %v", err)
	}
	if err := service.ProcessJob(context.Background(), job.ID); err != nil {
		t.Fatalf("process job: %v", err)
	}

	var status string
	var lastError sql.NullString
	if err := database.QueryRow("SELECT status, last_error FROM product_enrichment_jobs WHERE id = ?", job.ID).Scan(&status, &lastError); err != nil {
		t.Fatalf("query job: %v", err)
	}
	if status != StatusPartial {
		t.Fatalf("status = %q, want partial", status)
	}
	if !lastError.Valid || !strings.Contains(lastError.String, "usda_fdc: provider disabled") {
		t.Fatalf("last_error = %+v, want USDA skipped reason", lastError)
	}
	broadcaster := service.Hub.(*recordingBroadcaster)
	msg, ok := broadcaster.findMessage(ws.EventProductEnrichmentJobUpdated)
	if !ok {
		t.Fatalf("terminal job event was not broadcast")
	}
	providerStatus := msg.Payload.(map[string]interface{})["provider_status"].(map[string]string)
	if providerStatus["usda_fdc"] != "skipped" {
		t.Fatalf("provider status = %+v, want usda_fdc skipped", providerStatus)
	}
}

func TestProcessJobRejectsReceiptScanWhenAutoLookupDisabled(t *testing.T) {
	database, service, householdID, productID, cleanup := setupServiceTest(t, fakeProvider{name: "openfoodfacts"})
	defer cleanup()

	job, _, err := service.QueueJob(context.Background(), QueueJobRequest{
		HouseholdID: householdID,
		ProductID:   productID,
		Trigger:     TriggerReceiptScan,
		LookupKey:   "upc:0001111008404",
	})
	if err != nil {
		t.Fatalf("queue job: %v", err)
	}
	if err := service.ProcessJob(context.Background(), job.ID); err == nil {
		t.Fatalf("process job err = nil, want disabled auto-scan error")
	}

	var status string
	var lastError sql.NullString
	if err := database.QueryRow("SELECT status, last_error FROM product_enrichment_jobs WHERE id = ?", job.ID).Scan(&status, &lastError); err != nil {
		t.Fatalf("query job: %v", err)
	}
	if status != StatusFailed {
		t.Fatalf("status = %q, want failed", status)
	}
	if !lastError.Valid || !strings.Contains(lastError.String, "automatic enrichment lookup on scan is disabled") {
		t.Fatalf("last_error = %+v, want auto-scan disabled reason", lastError)
	}
}

func TestRateLimitRequeueDoesNotConsumeAttemptBudget(t *testing.T) {
	database, service, householdID, productID, cleanup := setupServiceTest(t, fakeProvider{name: "openfoodfacts", err: errors.New("429 rate limited")})
	defer cleanup()

	job, _, err := service.QueueJob(context.Background(), QueueJobRequest{
		HouseholdID: householdID,
		ProductID:   productID,
		LookupKey:   "upc:0001111008404",
	})
	if err != nil {
		t.Fatalf("queue job: %v", err)
	}
	if err := service.ProcessJob(context.Background(), job.ID); err != nil {
		t.Fatalf("process job: %v", err)
	}

	var status string
	var attempts int
	var nextAttempt sql.NullTime
	if err := database.QueryRow("SELECT status, attempt_count, next_attempt_at FROM product_enrichment_jobs WHERE id = ?", job.ID).Scan(&status, &attempts, &nextAttempt); err != nil {
		t.Fatalf("query job: %v", err)
	}
	if status != StatusQueued {
		t.Fatalf("status = %q, want queued", status)
	}
	if attempts != 0 {
		t.Fatalf("attempt_count = %d, want 0 after rate-limit requeue", attempts)
	}
	if !nextAttempt.Valid {
		t.Fatalf("next_attempt_at was not set")
	}
}

func TestRecoverStaleRunningRequeuesAndCapsAttempts(t *testing.T) {
	database, service, householdID, productID, cleanup := setupServiceTest(t, fakeProvider{name: "openfoodfacts"})
	defer cleanup()

	if _, err := database.Exec(
		`INSERT INTO product_enrichment_jobs
		    (id, household_id, product_id, trigger, lookup_key, status, attempt_count, updated_at)
		 VALUES
		    ('stale-retry', ?, ?, 'manual_lookup', 'upc:1', 'running', 1, '2026-01-01 00:00:00'),
		    ('stale-fail', ?, ?, 'manual_lookup', 'upc:2', 'running', 3, '2026-01-01 00:00:00')`,
		householdID, productID, householdID, productID,
	); err != nil {
		t.Fatalf("insert jobs: %v", err)
	}
	worker := &Worker{service: service}
	recovered, err := worker.RecoverStaleRunning(context.Background(), time.Second, 3)
	if err != nil {
		t.Fatalf("recover stale: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered = %d, want 1", recovered)
	}
	statuses := map[string]string{}
	rows, err := database.Query("SELECT id, status FROM product_enrichment_jobs WHERE id IN ('stale-retry', 'stale-fail')")
	if err != nil {
		t.Fatalf("query statuses: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, status string
		if err := rows.Scan(&id, &status); err != nil {
			t.Fatalf("scan status: %v", err)
		}
		statuses[id] = status
	}
	if statuses["stale-retry"] != StatusQueued || statuses["stale-fail"] != StatusFailed {
		t.Fatalf("statuses = %+v", statuses)
	}
}
