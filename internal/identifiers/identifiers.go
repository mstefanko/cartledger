package identifiers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Kind string

const (
	KindGTIN       Kind = "gtin"
	KindPLU        Kind = "plu"
	KindExternalID Kind = "external_id"
)

type Observation struct {
	Kind            Kind
	Authority       string
	RawValue        string
	NormalizedValue string
	Source          string
	Confidence      *float64
}

type ProductIdentifier struct {
	HouseholdID       string
	ProductID         string
	Kind              Kind
	Authority         string
	Value             string
	NormalizedValue   string
	Source            string
	Confidence        *float64
	SetPrimaryProduct bool
}

var ErrIdentifierConflict = errors.New("identifier already belongs to another product")

type IdentifierConflictError struct {
	ExistingProductID string
}

func (e *IdentifierConflictError) Error() string {
	if e == nil || e.ExistingProductID == "" {
		return ErrIdentifierConflict.Error()
	}
	return fmt.Sprintf("%s: %s", ErrIdentifierConflict, e.ExistingProductID)
}

func (e *IdentifierConflictError) Is(target error) bool {
	return target == ErrIdentifierConflict
}

type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

func NormalizeGTIN(raw string) (string, error) {
	var b strings.Builder
	for _, r := range strings.TrimSpace(raw) {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_' || r == '.' || r == '\t' || r == '\n' || r == '\r':
			continue
		default:
			return "", fmt.Errorf("gtin may contain only digits and visual separators")
		}
	}
	out := b.String()
	if out == "" {
		return "", nil
	}
	switch len(out) {
	case 8, 12, 13, 14:
	default:
		return "", fmt.Errorf("gtin must contain 8, 12, 13, or 14 digits")
	}
	if !validGTINCheckDigit(out) {
		return "", fmt.Errorf("gtin check digit is invalid")
	}
	return out, nil
}

func NormalizePLU(raw string) (normalized string, authority string, err error) {
	var b strings.Builder
	for _, r := range strings.TrimSpace(raw) {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
			continue
		}
		if r == ' ' || r == '-' {
			continue
		}
		return "", "", fmt.Errorf("plu may contain only digits and separators")
	}
	out := b.String()
	if out == "" {
		return "", "ifps", nil
	}
	if len(out) == 4 || (len(out) == 5 && strings.HasPrefix(out, "9")) {
		return out, "ifps", nil
	}
	return "", "ifps", fmt.Errorf("plu must be a four-digit IFPS code or five-digit organic code starting with 9")
}

func NormalizeExternalID(authority, raw string) (string, error) {
	authority = NormalizeAuthority(authority)
	if authority == "" {
		return "", fmt.Errorf("external identifier authority is required")
	}
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	value = strings.Join(strings.Fields(strings.ToLower(value)), " ")
	return value, nil
}

func Normalize(raw string, kind Kind, authority string) (Observation, error) {
	obs := Observation{
		Kind:      kind,
		Authority: NormalizeAuthority(authority),
		RawValue:  strings.TrimSpace(raw),
		Source:    "receipt",
	}
	switch kind {
	case KindGTIN:
		value, err := NormalizeGTIN(raw)
		if err != nil {
			return obs, err
		}
		obs.Authority = ""
		obs.NormalizedValue = value
	case KindPLU:
		value, auth, err := NormalizePLU(raw)
		if err != nil {
			return obs, err
		}
		obs.Authority = auth
		obs.NormalizedValue = value
	case KindExternalID:
		value, err := NormalizeExternalID(authority, raw)
		if err != nil {
			return obs, err
		}
		obs.NormalizedValue = value
	default:
		return obs, fmt.Errorf("unsupported identifier kind %q", kind)
	}
	return obs, nil
}

func NormalizeAuthority(authority string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(authority))), "_")
}

func validGTINCheckDigit(value string) bool {
	sum := 0
	weight := 3
	for i := len(value) - 2; i >= 0; i-- {
		sum += int(value[i]-'0') * weight
		if weight == 3 {
			weight = 1
		} else {
			weight = 3
		}
	}
	check := (10 - (sum % 10)) % 10
	return check == int(value[len(value)-1]-'0')
}

func InsertLineItemObservation(ctx context.Context, db DBTX, lineItemID string, obs Observation) error {
	if strings.TrimSpace(lineItemID) == "" || obs.NormalizedValue == "" {
		return nil
	}
	source := strings.TrimSpace(obs.Source)
	if source == "" {
		source = "receipt"
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO line_item_identifier_observations
		    (line_item_id, kind, authority, raw_value, normalized_value, source, confidence, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		lineItemID, obs.Kind, NormalizeAuthority(obs.Authority), obs.RawValue, obs.NormalizedValue, source, obs.Confidence, time.Now().UTC(),
	)
	return err
}

func UpsertProductIdentifier(ctx context.Context, db DBTX, row ProductIdentifier) error {
	row.HouseholdID = strings.TrimSpace(row.HouseholdID)
	row.ProductID = strings.TrimSpace(row.ProductID)
	row.Authority = NormalizeAuthority(row.Authority)
	row.Value = strings.TrimSpace(row.Value)
	row.NormalizedValue = strings.TrimSpace(row.NormalizedValue)
	if row.Source == "" {
		row.Source = "manual"
	}
	if row.HouseholdID == "" || row.ProductID == "" || row.Kind == "" || row.NormalizedValue == "" {
		return nil
	}

	var existingID, existingProductID string
	err := db.QueryRowContext(ctx,
		`SELECT id, product_id
		   FROM product_identifiers
		  WHERE household_id = ?
		    AND kind = ?
		    AND authority = ?
		    AND normalized_value = ?
		  LIMIT 1`,
		row.HouseholdID, row.Kind, row.Authority, row.NormalizedValue,
	).Scan(&existingID, &existingProductID)
	if err == nil && existingProductID != row.ProductID {
		return &IdentifierConflictError{ExistingProductID: existingProductID}
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	now := time.Now().UTC()
	if existingID != "" {
		_, err = db.ExecContext(ctx,
			`UPDATE product_identifiers
			    SET value = ?, source = ?, confidence = COALESCE(?, confidence),
			        last_seen_at = ?, updated_at = ?
			  WHERE id = ?`,
			row.Value, row.Source, row.Confidence, now, now, existingID,
		)
	} else {
		_, err = db.ExecContext(ctx,
			`INSERT INTO product_identifiers
			    (household_id, product_id, kind, authority, value, normalized_value,
			     source, confidence, first_seen_at, last_seen_at, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			row.HouseholdID, row.ProductID, row.Kind, row.Authority, row.Value, row.NormalizedValue,
			row.Source, row.Confidence, now, now, now, now,
		)
	}
	if err != nil {
		if isUniqueConstraint(err) {
			return &IdentifierConflictError{}
		}
		return err
	}

	if row.SetPrimaryProduct && row.Kind == KindGTIN && row.Authority == "" {
		_, err = db.ExecContext(ctx,
			`UPDATE products
			    SET upc = CASE
			            WHEN upc IS NULL OR TRIM(upc) = '' OR upc = ? THEN ?
			            ELSE upc
			        END,
			        updated_at = ?
			  WHERE id = ? AND household_id = ?`,
			row.NormalizedValue, row.NormalizedValue, now, row.ProductID, row.HouseholdID,
		)
	}
	return err
}

func SetProductPrimaryGTIN(ctx context.Context, db DBTX, householdID, productID, rawValue, source string, confidence *float64) (*string, error) {
	householdID = strings.TrimSpace(householdID)
	productID = strings.TrimSpace(productID)
	rawValue = strings.TrimSpace(rawValue)
	if source == "" {
		source = "manual"
	}
	now := time.Now().UTC()
	if rawValue == "" {
		if _, err := db.ExecContext(ctx,
			`DELETE FROM product_identifiers
			  WHERE household_id = ? AND product_id = ? AND kind = 'gtin' AND authority = ''`,
			householdID, productID,
		); err != nil {
			return nil, err
		}
		_, err := db.ExecContext(ctx,
			`UPDATE products SET upc = NULL, updated_at = ? WHERE id = ? AND household_id = ?`,
			now, productID, householdID,
		)
		return nil, err
	}

	normalized, err := NormalizeGTIN(rawValue)
	if err != nil {
		return nil, err
	}
	if normalized == "" {
		return nil, nil
	}
	if err := UpsertProductIdentifier(ctx, db, ProductIdentifier{
		HouseholdID:       householdID,
		ProductID:         productID,
		Kind:              KindGTIN,
		Authority:         "",
		Value:             rawValue,
		NormalizedValue:   normalized,
		Source:            source,
		Confidence:        confidence,
		SetPrimaryProduct: true,
	}); err != nil {
		return nil, err
	}
	_, err = db.ExecContext(ctx,
		`DELETE FROM product_identifiers
		  WHERE household_id = ? AND product_id = ? AND kind = 'gtin' AND authority = '' AND normalized_value <> ?`,
		householdID, productID, normalized,
	)
	if err != nil {
		return nil, err
	}
	return &normalized, nil
}

func isUniqueConstraint(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}
