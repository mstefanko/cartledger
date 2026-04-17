package db

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/mstefanko/cartledger/internal/models"
)

// newIntegrationTestStore opens a fresh SQLite DB, runs all migrations, inserts
// a household, and returns (store, householdID, cleanup).
func newIntegrationTestStore(t *testing.T) (*IntegrationStore, string, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := RunMigrations(database); err != nil {
		database.Close()
		t.Fatalf("RunMigrations: %v", err)
	}

	var householdID string
	if err := database.QueryRow(
		`INSERT INTO households (name) VALUES ('Test') RETURNING id`,
	).Scan(&householdID); err != nil {
		database.Close()
		t.Fatalf("insert household: %v", err)
	}

	store := NewIntegrationStore(database)
	cleanup := func() { database.Close() }
	return store, householdID, cleanup
}

func mealieConfigJSON(t *testing.T, baseURL, token string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(models.MealieConfig{Version: 1, BaseURL: baseURL, Token: token})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return raw
}

func TestIntegrationStore_UpsertInsertThenReplace(t *testing.T) {
	store, householdID, cleanup := newIntegrationTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Insert.
	it := &models.Integration{
		HouseholdID: householdID,
		Type:        models.IntegrationTypeMealie,
		Enabled:     true,
		Config:      mealieConfigJSON(t, "https://mealie.example.com", "tok-a"),
	}
	if err := store.Upsert(ctx, it); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if it.ID == "" {
		t.Fatalf("expected ID to be populated after insert")
	}
	firstID := it.ID
	firstCreatedAt := it.CreatedAt

	// Verify round-trip.
	got, err := store.GetByType(ctx, householdID, models.IntegrationTypeMealie)
	if err != nil {
		t.Fatalf("get after insert: %v", err)
	}
	if got == nil {
		t.Fatalf("expected integration after insert, got nil")
	}
	var gotCfg models.MealieConfig
	if err := json.Unmarshal(got.Config, &gotCfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if gotCfg.BaseURL != "https://mealie.example.com" || gotCfg.Token != "tok-a" {
		t.Errorf("expected base_url/token round-trip, got %+v", gotCfg)
	}

	// Replace (different base_url, different token, disabled).
	replacement := &models.Integration{
		HouseholdID: householdID,
		Type:        models.IntegrationTypeMealie,
		Enabled:     false,
		Config:      mealieConfigJSON(t, "https://mealie-2.example.com", "tok-b"),
	}
	if err := store.Upsert(ctx, replacement); err != nil {
		t.Fatalf("replace upsert: %v", err)
	}
	if replacement.ID != firstID {
		t.Errorf("expected ID preserved on replace: got %q, want %q", replacement.ID, firstID)
	}
	if !replacement.CreatedAt.Equal(firstCreatedAt) {
		t.Errorf("expected created_at preserved on replace: got %v, want %v", replacement.CreatedAt, firstCreatedAt)
	}
	if replacement.Enabled {
		t.Errorf("expected enabled=false after replace")
	}

	// Row count stays at 1 (UNIQUE(household_id, type) respected).
	var count int
	if err := store.DB.QueryRow(
		"SELECT COUNT(*) FROM integrations WHERE household_id = ?", householdID,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 integration row after replace, got %d", count)
	}

	// New values stuck.
	got, err = store.GetByType(ctx, householdID, models.IntegrationTypeMealie)
	if err != nil {
		t.Fatalf("get after replace: %v", err)
	}
	if got == nil {
		t.Fatalf("expected integration after replace, got nil")
	}
	if err := json.Unmarshal(got.Config, &gotCfg); err != nil {
		t.Fatalf("unmarshal after replace: %v", err)
	}
	if gotCfg.BaseURL != "https://mealie-2.example.com" || gotCfg.Token != "tok-b" {
		t.Errorf("expected replaced base_url/token, got %+v", gotCfg)
	}
}

func TestIntegrationStore_GetByType_NilOnMiss(t *testing.T) {
	store, householdID, cleanup := newIntegrationTestStore(t)
	defer cleanup()
	ctx := context.Background()

	got, err := store.GetByType(ctx, householdID, models.IntegrationTypeMealie)
	if err != nil {
		t.Fatalf("GetByType: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil on miss, got %+v", got)
	}
}

func TestIntegrationStore_GetByType_Hit(t *testing.T) {
	store, householdID, cleanup := newIntegrationTestStore(t)
	defer cleanup()
	ctx := context.Background()

	it := &models.Integration{
		HouseholdID: householdID,
		Type:        models.IntegrationTypeMealie,
		Enabled:     true,
		Config:      mealieConfigJSON(t, "https://x", "t"),
	}
	if err := store.Upsert(ctx, it); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := store.GetByType(ctx, householdID, models.IntegrationTypeMealie)
	if err != nil {
		t.Fatalf("GetByType hit: %v", err)
	}
	if got == nil || got.Type != models.IntegrationTypeMealie {
		t.Errorf("expected hit, got %+v", got)
	}
}

func TestIntegrationStore_Delete(t *testing.T) {
	store, householdID, cleanup := newIntegrationTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Delete-on-missing returns (false, nil).
	deleted, err := store.Delete(ctx, householdID, models.IntegrationTypeMealie)
	if err != nil {
		t.Fatalf("delete missing: %v", err)
	}
	if deleted {
		t.Errorf("expected deleted=false on missing row")
	}

	// Insert and then delete.
	it := &models.Integration{
		HouseholdID: householdID,
		Type:        models.IntegrationTypeMealie,
		Enabled:     true,
		Config:      mealieConfigJSON(t, "https://x", "t"),
	}
	if err := store.Upsert(ctx, it); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	deleted, err = store.Delete(ctx, householdID, models.IntegrationTypeMealie)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !deleted {
		t.Errorf("expected deleted=true after insert")
	}

	got, err := store.GetByType(ctx, householdID, models.IntegrationTypeMealie)
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil after delete, got %+v", got)
	}
}
