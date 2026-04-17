package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mstefanko/cartledger/internal/models"
)

// IntegrationStore provides CRUD for the integrations table.
type IntegrationStore struct {
	DB *sql.DB
}

// NewIntegrationStore constructs an IntegrationStore.
func NewIntegrationStore(database *sql.DB) *IntegrationStore {
	return &IntegrationStore{DB: database}
}

// GetByHousehold returns all integrations for a household ordered by type.
func (s *IntegrationStore) GetByHousehold(ctx context.Context, householdID string) ([]models.Integration, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, household_id, type, enabled, config, created_at, updated_at
		 FROM integrations WHERE household_id = ? ORDER BY type`,
		householdID,
	)
	if err != nil {
		return nil, fmt.Errorf("query integrations: %w", err)
	}
	defer rows.Close()

	out := make([]models.Integration, 0)
	for rows.Next() {
		var it models.Integration
		var configStr string
		var enabledInt int
		if err := rows.Scan(&it.ID, &it.HouseholdID, &it.Type, &enabledInt, &configStr, &it.CreatedAt, &it.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan integration: %w", err)
		}
		it.Enabled = enabledInt != 0
		it.Config = json.RawMessage(configStr)
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iter integrations: %w", err)
	}
	return out, nil
}

// GetByType returns the integration row for (household, type) or (nil, nil) on miss.
// A miss is explicitly not an error so callers can branch on `== nil`.
func (s *IntegrationStore) GetByType(ctx context.Context, householdID, integrationType string) (*models.Integration, error) {
	var it models.Integration
	var configStr string
	var enabledInt int
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, household_id, type, enabled, config, created_at, updated_at
		 FROM integrations WHERE household_id = ? AND type = ?`,
		householdID, integrationType,
	).Scan(&it.ID, &it.HouseholdID, &it.Type, &enabledInt, &configStr, &it.CreatedAt, &it.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get integration by type: %w", err)
	}
	it.Enabled = enabledInt != 0
	it.Config = json.RawMessage(configStr)
	return &it, nil
}

// Upsert inserts a new integration row or replaces an existing one for
// (household_id, type). On insert, the caller-supplied ID is used if non-empty;
// otherwise SQLite generates one. On replace, the existing id is preserved.
func (s *IntegrationStore) Upsert(ctx context.Context, it *models.Integration) error {
	if it.HouseholdID == "" {
		return errors.New("integration: household_id required")
	}
	if it.Type == "" {
		return errors.New("integration: type required")
	}
	if len(it.Config) == 0 {
		return errors.New("integration: config required")
	}

	enabledInt := 0
	if it.Enabled {
		enabledInt = 1
	}
	now := time.Now().UTC()

	// Try UPDATE first; fall back to INSERT if no row matched.
	res, err := s.DB.ExecContext(ctx,
		`UPDATE integrations
		   SET enabled = ?, config = ?, updated_at = ?
		 WHERE household_id = ? AND type = ?`,
		enabledInt, string(it.Config), now, it.HouseholdID, it.Type,
	)
	if err != nil {
		return fmt.Errorf("update integration: %w", err)
	}
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected > 0 {
		// Hydrate the struct with the persisted row.
		return s.hydrate(ctx, it)
	}

	// Insert new row.
	if it.ID != "" {
		_, err = s.DB.ExecContext(ctx,
			`INSERT INTO integrations (id, household_id, type, enabled, config, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			it.ID, it.HouseholdID, it.Type, enabledInt, string(it.Config), now, now,
		)
	} else {
		_, err = s.DB.ExecContext(ctx,
			`INSERT INTO integrations (household_id, type, enabled, config, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			it.HouseholdID, it.Type, enabledInt, string(it.Config), now, now,
		)
	}
	if err != nil {
		return fmt.Errorf("insert integration: %w", err)
	}
	return s.hydrate(ctx, it)
}

// Delete removes the integration row for (household, type). Returns whether
// a row was actually deleted.
func (s *IntegrationStore) Delete(ctx context.Context, householdID, integrationType string) (bool, error) {
	res, err := s.DB.ExecContext(ctx,
		`DELETE FROM integrations WHERE household_id = ? AND type = ?`,
		householdID, integrationType,
	)
	if err != nil {
		return false, fmt.Errorf("delete integration: %w", err)
	}
	rowsAffected, _ := res.RowsAffected()
	return rowsAffected > 0, nil
}

// hydrate refreshes an Integration struct from the database row identified by
// (household_id, type). Used by Upsert to return the authoritative id + timestamps.
func (s *IntegrationStore) hydrate(ctx context.Context, it *models.Integration) error {
	got, err := s.GetByType(ctx, it.HouseholdID, it.Type)
	if err != nil {
		return err
	}
	if got == nil {
		return errors.New("integration: row missing after upsert")
	}
	*it = *got
	return nil
}
