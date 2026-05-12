package matcher

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type AliasSource string

const (
	AliasSourceLegacy       AliasSource = "legacy"
	AliasSourceReceiptMatch AliasSource = "receipt_match"
	AliasSourceManualMatch  AliasSource = "manual_match"
	AliasSourceUserAlias    AliasSource = "user_alias"
	AliasSourceImport       AliasSource = "import"
	AliasSourceEnrichment   AliasSource = "enrichment"
)

var ErrAliasConflict = errors.New("alias already belongs to another product")

type AliasConflictError struct {
	ExistingProductID string
}

func (e *AliasConflictError) Error() string {
	if e == nil || e.ExistingProductID == "" {
		return ErrAliasConflict.Error()
	}
	return fmt.Sprintf("%s: %s", ErrAliasConflict, e.ExistingProductID)
}

func (e *AliasConflictError) Is(target error) bool {
	return target == ErrAliasConflict
}

type AliasDBTX interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

type AliasUpsert struct {
	HouseholdID string
	ProductID   string
	Alias       string
	StoreID     *string
	Source      AliasSource
	Confidence  *float64
	AcceptedAt  *time.Time
	CreatedAt   time.Time
}

func NormalizeProductName(s string) string {
	return Normalize(strings.ReplaceAll(strings.TrimSpace(s), "&", " and "))
}

func UpsertAlias(ctx context.Context, db AliasDBTX, row AliasUpsert) error {
	row.HouseholdID = strings.TrimSpace(row.HouseholdID)
	row.ProductID = strings.TrimSpace(row.ProductID)
	row.Alias = strings.TrimSpace(row.Alias)
	if row.HouseholdID == "" || row.ProductID == "" || row.Alias == "" {
		return nil
	}
	normalized := NormalizeProductName(row.Alias)
	if normalized == "" {
		return nil
	}
	if row.Source == "" {
		row.Source = AliasSourceLegacy
	}
	now := row.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}

	var existingID, existingProductID string
	var err error
	if row.StoreID != nil && strings.TrimSpace(*row.StoreID) != "" {
		storeID := strings.TrimSpace(*row.StoreID)
		err = db.QueryRowContext(ctx,
			`SELECT id, product_id
			   FROM product_aliases
			  WHERE household_id = ?
			    AND store_id = ?
			    AND alias_normalized = ?
			  LIMIT 1`,
			row.HouseholdID, storeID, normalized,
		).Scan(&existingID, &existingProductID)
		row.StoreID = &storeID
	} else {
		row.StoreID = nil
		err = db.QueryRowContext(ctx,
			`SELECT id, product_id
			   FROM product_aliases
			  WHERE household_id = ?
			    AND store_id IS NULL
			    AND alias_normalized = ?
			  LIMIT 1`,
			row.HouseholdID, normalized,
		).Scan(&existingID, &existingProductID)
	}
	if err == nil && existingProductID != row.ProductID {
		return &AliasConflictError{ExistingProductID: existingProductID}
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if existingID != "" {
		_, err = db.ExecContext(ctx,
			`UPDATE product_aliases
			    SET alias = ?, source = ?, confidence = COALESCE(?, confidence),
			        accepted_at = COALESCE(?, accepted_at),
			        updated_at = ?
			  WHERE id = ?`,
			row.Alias, row.Source, row.Confidence, row.AcceptedAt, now, existingID,
		)
		return err
	}

	_, err = db.ExecContext(ctx,
		`INSERT INTO product_aliases
		    (id, product_id, household_id, alias, alias_normalized, store_id,
		     source, confidence, accepted_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.New().String(), row.ProductID, row.HouseholdID, row.Alias, normalized,
		row.StoreID, row.Source, row.Confidence, row.AcceptedAt, now, now,
	)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
		return &AliasConflictError{}
	}
	return err
}
