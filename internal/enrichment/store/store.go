package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/mstefanko/cartledger/internal/enrichment"
)

type Repository struct {
	DB *sql.DB
}

type ProductLinkInput struct {
	ProductID        string
	Source           string
	ExternalID       *string
	URL              string
	Label            *string
	FetchedAt        time.Time
	HTTPStatus       int
	ContentHash      string
	LastError        *string
	SourceConfidence *float64
}

type MetadataInput struct {
	HouseholdID    string
	ProductID      string
	ProductLinkID  *string
	Source         string
	SourceRecordID *string
	SourceURL      *string
	LookupKey      *string
	Payload        enrichment.MetadataPayload
	ContentHash    *string
	FetchedAt      time.Time
	ExpiresAt      *time.Time
	HTTPStatus     int
	LastError      *string
	Confidence     *float64
}

func (r Repository) UpsertProductLink(ctx context.Context, input ProductLinkInput) (string, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	var id string
	err = tx.QueryRowContext(ctx,
		"SELECT id FROM product_links WHERE product_id = ? AND url = ? ORDER BY created_at LIMIT 1",
		input.ProductID, input.URL,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx,
			`INSERT INTO product_links
			    (id, product_id, source, external_id, url, label, created_at, fetched_at, http_status, content_hash, last_error, source_confidence)
			 VALUES (lower(hex(randomblob(16))), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 RETURNING id`,
			input.ProductID, input.Source, input.ExternalID, input.URL, input.Label, now, nullableTime(input.FetchedAt), nullableInt(input.HTTPStatus), nullableTrimmedString(input.ContentHash), input.LastError, input.SourceConfidence,
		).Scan(&id)
	}
	if err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx,
		`UPDATE product_links
		    SET source = ?, external_id = ?, label = ?, fetched_at = ?, http_status = ?,
		        content_hash = ?, last_error = ?, source_confidence = ?
		  WHERE id = ? AND product_id = ?`,
		input.Source, input.ExternalID, input.Label, nullableTime(input.FetchedAt), nullableInt(input.HTTPStatus), nullableTrimmedString(input.ContentHash), input.LastError, input.SourceConfidence, id, input.ProductID,
	); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return id, nil
}

func (r Repository) UpsertMetadata(ctx context.Context, input MetadataInput) (string, error) {
	if input.Payload.Version == 0 {
		input.Payload.Version = 1
	}
	if strings.TrimSpace(input.Payload.Source) == "" {
		input.Payload.Source = input.Source
	}
	payload, err := json.Marshal(input.Payload)
	if err != nil {
		return "", err
	}
	contentHash := input.ContentHash
	if contentHash == nil || strings.TrimSpace(*contentHash) == "" {
		hash := HashBytes(payload)
		contentHash = &hash
	}

	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var id string
	sourceRecordID := trimmedPtr(input.SourceRecordID)
	if sourceRecordID != nil {
		err = tx.QueryRowContext(ctx,
			`SELECT id
			   FROM product_external_metadata
			  WHERE product_id = ? AND source = ? AND source_record_id = ?
			  LIMIT 1`,
			input.ProductID, input.Source, *sourceRecordID,
		).Scan(&id)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}

	if id == "" {
		err = tx.QueryRowContext(ctx,
			`INSERT INTO product_external_metadata
			    (id, household_id, product_id, product_link_id, source, source_record_id, source_url,
			     lookup_key, payload_json, payload_version, content_hash, fetched_at, expires_at,
			     http_status, last_error, confidence, created_at, updated_at)
			 VALUES (lower(hex(randomblob(16))), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
			 RETURNING id`,
			input.HouseholdID, input.ProductID, input.ProductLinkID, input.Source, sourceRecordID, input.SourceURL,
			input.LookupKey, string(payload), input.Payload.Version, contentHash, nullableTime(input.FetchedAt), input.ExpiresAt,
			nullableInt(input.HTTPStatus), input.LastError, input.Confidence,
		).Scan(&id)
	} else {
		_, err = tx.ExecContext(ctx,
			`UPDATE product_external_metadata
			    SET household_id = ?, product_link_id = ?, source_url = ?, lookup_key = ?,
			        payload_json = ?, payload_version = ?, content_hash = ?, fetched_at = ?,
			        expires_at = ?, http_status = ?, last_error = ?, confidence = ?,
			        updated_at = CURRENT_TIMESTAMP
			  WHERE id = ? AND product_id = ?`,
			input.HouseholdID, input.ProductLinkID, input.SourceURL, input.LookupKey,
			string(payload), input.Payload.Version, contentHash, nullableTime(input.FetchedAt),
			input.ExpiresAt, nullableInt(input.HTTPStatus), input.LastError, input.Confidence,
			id, input.ProductID,
		)
	}
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return id, nil
}

func (r Repository) StoreSuggestions(ctx context.Context, productID string, linkID, metadataID *string, suggestions []enrichment.Suggestion, bypassFieldEdits bool) ([]string, error) {
	if trimmedPtr(linkID) == nil && trimmedPtr(metadataID) == nil {
		return nil, errors.New("enrichment suggestions require product link or metadata evidence")
	}

	evidenceAt, hasEvidenceTime, err := r.suggestionEvidenceTime(ctx, productID, linkID, metadataID)
	if err != nil {
		return nil, err
	}
	blockedFields := map[string]struct{}{}
	if !bypassFieldEdits {
		blockedFields, err = r.productFieldEditBlockedFields(ctx, productID, evidenceAt, hasEvidenceTime)
		if err != nil {
			return nil, err
		}
	}

	out := make([]string, 0, len(suggestions))
	for _, s := range suggestions {
		if strings.TrimSpace(s.Value) == "" {
			continue
		}
		if _, blocked := blockedFields[s.Field]; blocked {
			continue
		}
		var id string
		if trimmedPtr(metadataID) != nil {
			err = r.DB.QueryRowContext(ctx,
				`INSERT INTO product_enrichment_suggestions
				    (id, product_id, product_link_id, external_metadata_id, source, source_url, field, value, evidence, confidence, status, created_at, updated_at)
				 VALUES (lower(hex(randomblob(16))), ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
				 ON CONFLICT(product_id, external_metadata_id, field, value) WHERE external_metadata_id IS NOT NULL DO UPDATE SET
				    product_link_id = COALESCE(excluded.product_link_id, product_enrichment_suggestions.product_link_id),
				    source = excluded.source,
				    source_url = excluded.source_url,
				    evidence = excluded.evidence,
				    confidence = excluded.confidence,
				    status = CASE WHEN product_enrichment_suggestions.status = 'rejected' THEN 'pending' ELSE product_enrichment_suggestions.status END,
				    updated_at = CURRENT_TIMESTAMP
				 RETURNING id`,
				productID, linkID, metadataID, s.Source, s.SourceURL, s.Field, s.Value, nullableString(s.Evidence), nullableFloat(s.Confidence),
			).Scan(&id)
		} else {
			err = r.DB.QueryRowContext(ctx,
				`INSERT INTO product_enrichment_suggestions
				    (id, product_id, product_link_id, source, source_url, field, value, evidence, confidence, status, created_at, updated_at)
				 VALUES (lower(hex(randomblob(16))), ?, ?, ?, ?, ?, ?, ?, ?, 'pending', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
				 ON CONFLICT(product_id, product_link_id, field, value) DO UPDATE SET
				    source = excluded.source,
				    source_url = excluded.source_url,
				    evidence = excluded.evidence,
				    confidence = excluded.confidence,
				    status = CASE WHEN product_enrichment_suggestions.status = 'rejected' THEN 'pending' ELSE product_enrichment_suggestions.status END,
				    updated_at = CURRENT_TIMESTAMP
				 RETURNING id`,
				productID, linkID, s.Source, s.SourceURL, s.Field, s.Value, nullableString(s.Evidence), nullableFloat(s.Confidence),
			).Scan(&id)
		}
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

func (r Repository) suggestionEvidenceTime(ctx context.Context, productID string, linkID, metadataID *string) (time.Time, bool, error) {
	if trimmedPtr(metadataID) != nil {
		var fetchedAt, createdAt sql.NullTime
		if err := r.DB.QueryRowContext(ctx,
			"SELECT fetched_at, created_at FROM product_external_metadata WHERE id = ? AND product_id = ?",
			*metadataID, productID,
		).Scan(&fetchedAt, &createdAt); err != nil {
			return time.Time{}, false, err
		}
		if fetchedAt.Valid {
			return fetchedAt.Time, true, nil
		}
		if createdAt.Valid {
			return createdAt.Time, true, nil
		}
		return time.Time{}, false, nil
	}
	if trimmedPtr(linkID) == nil {
		return time.Time{}, false, nil
	}
	var fetchedAt, createdAt sql.NullTime
	if err := r.DB.QueryRowContext(ctx,
		"SELECT fetched_at, created_at FROM product_links WHERE id = ? AND product_id = ?",
		*linkID, productID,
	).Scan(&fetchedAt, &createdAt); err != nil {
		return time.Time{}, false, err
	}
	if fetchedAt.Valid {
		return fetchedAt.Time, true, nil
	}
	if createdAt.Valid {
		return createdAt.Time, true, nil
	}
	return time.Time{}, false, nil
}

func (r Repository) productFieldEditBlockedFields(ctx context.Context, productID string, evidenceAt time.Time, hasEvidenceTime bool) (map[string]struct{}, error) {
	blocked := map[string]struct{}{}
	if !hasEvidenceTime {
		return blocked, nil
	}
	rows, err := r.DB.QueryContext(ctx,
		`SELECT field
		   FROM product_field_edits
		  WHERE product_id = ?
		    AND datetime(edited_at) > datetime(?)`,
		productID, evidenceAt.Format("2006-01-02 15:04:05"),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var field string
		if err := rows.Scan(&field); err != nil {
			return nil, err
		}
		blocked[field] = struct{}{}
	}
	return blocked, rows.Err()
}

func HashBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func nullableString(value string) interface{} {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func nullableFloat(value float64) interface{} {
	if value == 0 {
		return nil
	}
	return value
}

func nullableTime(value time.Time) interface{} {
	if value.IsZero() {
		return nil
	}
	return value
}

func nullableInt(value int) interface{} {
	if value == 0 {
		return nil
	}
	return value
}

func nullableTrimmedString(value string) interface{} {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func trimmedPtr(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
