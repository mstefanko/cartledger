package storecodes

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

type Execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func Normalize(code string) string {
	code = strings.TrimSpace(code)
	if len(code) <= 64 {
		return code
	}
	return code[:64]
}

func UpsertReceipt(ctx context.Context, execer Execer, householdID, storeID, productID, code string, label *string, now time.Time) error {
	return upsert(ctx, execer, householdID, storeID, productID, code, label, "receipt", nil, now, false)
}

func UpsertManual(ctx context.Context, execer Execer, householdID, storeID, productID, code string, label *string, now time.Time) error {
	return upsert(ctx, execer, householdID, storeID, productID, code, label, "manual", nil, now, true)
}

func UpsertBackfill(ctx context.Context, execer Execer, householdID, storeID, productID, code string, label *string, now time.Time) error {
	return upsert(ctx, execer, householdID, storeID, productID, code, label, "backfill", nil, now, false)
}

func upsert(ctx context.Context, execer Execer, householdID, storeID, productID, code string, label *string, source string, confidence *float64, now time.Time, overwrite bool) error {
	code = Normalize(code)
	if householdID == "" || storeID == "" || productID == "" || code == "" {
		return nil
	}
	if label != nil {
		trimmed := strings.TrimSpace(*label)
		if trimmed == "" {
			label = nil
		} else {
			label = &trimmed
		}
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	if overwrite {
		_, err := execer.ExecContext(ctx,
			`INSERT INTO store_product_codes
			    (household_id, store_id, product_id, store_item_code, label, source, confidence,
			     first_seen_at, last_seen_at, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(store_id, store_item_code) DO UPDATE SET
			    product_id = excluded.product_id,
			    label = excluded.label,
			    source = excluded.source,
			    confidence = excluded.confidence,
			    last_seen_at = excluded.last_seen_at,
			    updated_at = excluded.updated_at`,
			householdID, storeID, productID, code, label, source, confidence, now, now, now, now,
		)
		return err
	}

	_, err := execer.ExecContext(ctx,
		`INSERT INTO store_product_codes
		    (household_id, store_id, product_id, store_item_code, label, source, confidence,
		     first_seen_at, last_seen_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(store_id, store_item_code) DO UPDATE SET
		    last_seen_at = excluded.last_seen_at,
		    updated_at = excluded.updated_at`,
		householdID, storeID, productID, code, label, source, confidence, now, now, now, now,
	)
	return err
}
