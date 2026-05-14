package matcher

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mstefanko/cartledger/internal/sqliteutil"
)

type AliasSource string

const (
	AliasSourceLegacy            AliasSource = "legacy"
	AliasSourceReceiptMatch      AliasSource = "receipt_match"
	AliasSourceManualMatch       AliasSource = "manual_match"
	AliasSourceUserAlias         AliasSource = "user_alias"
	AliasSourceImport            AliasSource = "import"
	AliasSourceEnrichment        AliasSource = "enrichment"
	AliasSourceReceiptReviewScan AliasSource = "receipt_review_scan"
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
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
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

	existingID, existingProductID, err := findAliasByNormalized(ctx, db, row, normalized)
	if err != nil {
		return err
	}
	if existingID != "" && existingProductID != row.ProductID {
		return &AliasConflictError{ExistingProductID: existingProductID}
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

	if row.StoreID != nil && strings.TrimSpace(*row.StoreID) != "" {
		storeID := strings.TrimSpace(*row.StoreID)
		row.StoreID = &storeID
	} else {
		row.StoreID = nil
	}

	_, err = db.ExecContext(ctx,
		`INSERT INTO product_aliases
		    (id, product_id, household_id, alias, alias_normalized, store_id,
		     source, confidence, accepted_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.New().String(), row.ProductID, row.HouseholdID, row.Alias, normalized,
		row.StoreID, row.Source, row.Confidence, row.AcceptedAt, now, now,
	)
	if sqliteutil.IsUniqueConstraint(err) {
		return &AliasConflictError{}
	}
	return err
}

func findAliasByNormalized(ctx context.Context, db AliasDBTX, row AliasUpsert, normalized string) (string, string, error) {
	args := []interface{}{row.HouseholdID, normalized}
	storeClause := "store_id IS NULL"
	if row.StoreID != nil && strings.TrimSpace(*row.StoreID) != "" {
		storeClause = "store_id = ?"
		args = []interface{}{row.HouseholdID, strings.TrimSpace(*row.StoreID), normalized}
	}
	rows, err := db.QueryContext(ctx,
		fmt.Sprintf(`SELECT id, product_id, alias, alias_normalized
		               FROM product_aliases
		              WHERE household_id = ?
		                AND %s
		              ORDER BY CASE WHEN alias_normalized = ? THEN 0 ELSE 1 END,
		                       alias_normalized IS NULL, created_at, id`, storeClause),
		args...,
	)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()

	for rows.Next() {
		var id, productID, alias string
		var aliasNormalized sql.NullString
		if err := rows.Scan(&id, &productID, &alias, &aliasNormalized); err != nil {
			return "", "", err
		}
		if aliasNormalized.Valid && aliasNormalized.String == normalized {
			return id, productID, nil
		}
		if NormalizeProductName(alias) == normalized {
			return id, productID, nil
		}
	}
	if err := rows.Err(); err != nil {
		return "", "", err
	}
	return "", "", nil
}
