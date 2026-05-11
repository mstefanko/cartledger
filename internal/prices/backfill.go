package prices

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/mstefanko/cartledger/internal/units"
)

type BackfillOptions struct {
	Apply       bool
	ProductID   string
	SampleLimit int
}

type BackfillSummary struct {
	TotalRows              int
	AlreadyNormalized      int
	ReceiptUnitNormalized  int
	LineOverrideNormalized int
	ProductPackNormalized  int
	MissingPackSkipped     int
	AmbiguousUnitSkipped   int
	InvalidSkipped         int
	LinkableRows           int
	LinkedRows             int
	AmbiguousLinkSkipped   int
	Samples                []BackfillSkippedSample
}

type BackfillSkippedSample struct {
	ProductName string
	ReceiptDate string
	RawLineText string
	Quantity    string
	Unit        string
	Price       string
	Reason      string
}

type backfillPriceRow struct {
	ID                 string
	ProductID          string
	ProductName        string
	ReceiptID          string
	ReceiptDate        string
	Quantity           string
	Unit               string
	UnitPrice          string
	NormalizedPriceSet bool
	NormalizedUnitSet  bool
	LineItemID         sql.NullString
	PackQuantity       sql.NullFloat64
	PackUnit           sql.NullString
}

type backfillLineItem struct {
	ID                   string
	RawName              string
	Quantity             string
	Unit                 sql.NullString
	TotalPrice           string
	PackQuantityOverride sql.NullString
	PackUnitOverride     sql.NullString
}

func BackfillNormalizedPrices(ctx context.Context, database *sql.DB, opts BackfillOptions) (BackfillSummary, error) {
	if opts.SampleLimit <= 0 {
		opts.SampleLimit = 10
	}

	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return BackfillSummary{}, fmt.Errorf("begin backfill: %w", err)
	}
	defer tx.Rollback()

	summary := BackfillSummary{}
	if err := tx.QueryRowContext(ctx, backfillCountQuery(opts.ProductID), backfillCountArgs(opts.ProductID)...).Scan(&summary.TotalRows, &summary.AlreadyNormalized); err != nil {
		return BackfillSummary{}, fmt.Errorf("count product prices: %w", err)
	}

	rows, err := tx.QueryContext(ctx, backfillRowsQuery(opts.ProductID), backfillRowsArgs(opts.ProductID)...)
	if err != nil {
		return BackfillSummary{}, fmt.Errorf("query product prices: %w", err)
	}
	defer rows.Close()

	linkedLineItems := make(map[string]string)
	for rows.Next() {
		var row backfillPriceRow
		if err := rows.Scan(
			&row.ID,
			&row.ProductID,
			&row.ProductName,
			&row.ReceiptID,
			&row.ReceiptDate,
			&row.Quantity,
			&row.Unit,
			&row.UnitPrice,
			&row.NormalizedPriceSet,
			&row.NormalizedUnitSet,
			&row.LineItemID,
			&row.PackQuantity,
			&row.PackUnit,
		); err != nil {
			return BackfillSummary{}, fmt.Errorf("scan product price: %w", err)
		}

		if err := processBackfillRow(ctx, tx, row, opts, &summary, linkedLineItems); err != nil {
			return BackfillSummary{}, err
		}
	}
	if err := rows.Err(); err != nil {
		return BackfillSummary{}, fmt.Errorf("iterate product prices: %w", err)
	}

	if opts.Apply {
		if err := tx.Commit(); err != nil {
			return BackfillSummary{}, fmt.Errorf("commit backfill: %w", err)
		}
	}

	return summary, nil
}

func processBackfillRow(ctx context.Context, tx *sql.Tx, row backfillPriceRow, opts BackfillOptions, summary *BackfillSummary, linkedLineItems map[string]string) error {
	lineItemID := ""
	if row.LineItemID.Valid && strings.TrimSpace(row.LineItemID.String) != "" {
		lineItemID = row.LineItemID.String
	} else {
		totalPrice, ok := productPriceTotal(row.Quantity, row.UnitPrice)
		if ok {
			match, ambiguous, err := findUniqueLineItemMatch(ctx, tx, row, totalPrice, linkedLineItems)
			if err != nil {
				return err
			}
			if ambiguous {
				summary.AmbiguousLinkSkipped++
			}
			if match != "" {
				summary.LinkableRows++
				lineItemID = match
				linkedLineItems[match] = row.ID
			}
		}
	}

	var line *backfillLineItem
	if lineItemID != "" {
		loaded, err := loadBackfillLineItem(ctx, tx, lineItemID)
		if err != nil {
			return err
		}
		line = loaded
	}

	normalized, sample, err := normalizeBackfillRow(row, line)
	if err != nil {
		summary.InvalidSkipped++
		addBackfillSample(summary, opts.SampleLimit, sample, "invalid quantity or price")
		return nil
	}

	switch normalized.Source {
	case SourceLineUnit:
		summary.ReceiptUnitNormalized++
	case SourceLineOverride:
		summary.LineOverrideNormalized++
	case SourceProductPack:
		summary.ProductPackNormalized++
	case SourceMissingPack:
		reason := backfillMissingReason(row, line)
		if reason == "ambiguous unit" {
			summary.AmbiguousUnitSkipped++
		} else {
			summary.MissingPackSkipped++
		}
		addBackfillSample(summary, opts.SampleLimit, sample, reason)
		return nil
	}

	if !opts.Apply {
		return nil
	}

	if lineItemID != "" {
		if !row.LineItemID.Valid || row.LineItemID.String != lineItemID {
			if _, err := tx.ExecContext(ctx, `UPDATE product_prices SET line_item_id = ? WHERE id = ?`, lineItemID, row.ID); err != nil {
				return fmt.Errorf("link product price to line item: %w", err)
			}
			summary.LinkedRows++
		}
		return RecordProductPriceFromLineItem(ctx, tx, lineItemID)
	}

	var normalizedPriceValue any
	if normalized.NormalizedPrice != nil {
		normalizedPriceValue = normalized.NormalizedPrice.String()
	}
	var normalizedUnitValue any
	if normalized.NormalizedUnit != nil {
		normalizedUnitValue = *normalized.NormalizedUnit
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE product_prices
		    SET unit_price = ?, normalized_price = ?, normalized_unit = ?
		  WHERE id = ?`,
		normalized.UnitPrice.String(), normalizedPriceValue, normalizedUnitValue, row.ID,
	); err != nil {
		return fmt.Errorf("update product price normalization: %w", err)
	}
	return nil
}

func normalizeBackfillRow(row backfillPriceRow, line *backfillLineItem) (NormalizedLinePrice, BackfillSkippedSample, error) {
	sample := BackfillSkippedSample{
		ProductName: row.ProductName,
		ReceiptDate: row.ReceiptDate,
		Quantity:    row.Quantity,
		Unit:        row.Unit,
	}

	var quantityText, totalPriceText string
	var lineUnit *string
	var overrideQuantity *decimal.Decimal
	var overrideUnit *string

	if line != nil {
		quantityText = line.Quantity
		totalPriceText = line.TotalPrice
		lineUnit = stringPtrFromNullString(line.Unit)
		overrideUnit = stringPtrFromNullString(line.PackUnitOverride)
		var err error
		overrideQuantity, err = decimalPtrFromNullString(line.PackQuantityOverride)
		if err != nil {
			return NormalizedLinePrice{}, sample, err
		}
		sample.RawLineText = line.RawName
		sample.Quantity = line.Quantity
		sample.Unit = line.Unit.String
		sample.Price = line.TotalPrice
	} else {
		quantityText = row.Quantity
		totalPrice, ok := productPriceTotal(row.Quantity, row.UnitPrice)
		if !ok {
			return NormalizedLinePrice{}, sample, fmt.Errorf("invalid product price total")
		}
		totalPriceText = totalPrice.String()
		lineUnit = stringPtr(row.Unit)
		sample.Price = totalPriceText
	}

	lineQuantity, err := decimal.NewFromString(emptyDefault(quantityText, "1"))
	if err != nil {
		return NormalizedLinePrice{}, sample, err
	}
	totalPrice, err := decimal.NewFromString(emptyDefault(totalPriceText, "0"))
	if err != nil {
		return NormalizedLinePrice{}, sample, err
	}

	productPackQuantity := decimalPtrFromFloat(row.PackQuantity)
	productPackUnit := stringPtrFromNullString(row.PackUnit)
	normalized, err := NormalizeLineItemPrice(
		totalPrice,
		lineQuantity,
		lineUnit,
		productPackQuantity,
		productPackUnit,
		overrideQuantity,
		overrideUnit,
	)
	return normalized, sample, err
}

func loadBackfillLineItem(ctx context.Context, tx *sql.Tx, lineItemID string) (*backfillLineItem, error) {
	var line backfillLineItem
	err := tx.QueryRowContext(ctx,
		`SELECT id, raw_name, quantity, unit, total_price,
		        pack_quantity_override, pack_unit_override
		   FROM line_items
		  WHERE id = ?`,
		lineItemID,
	).Scan(
		&line.ID,
		&line.RawName,
		&line.Quantity,
		&line.Unit,
		&line.TotalPrice,
		&line.PackQuantityOverride,
		&line.PackUnitOverride,
	)
	if err != nil {
		return nil, fmt.Errorf("load line item for backfill: %w", err)
	}
	return &line, nil
}

func findUniqueLineItemMatch(ctx context.Context, tx *sql.Tx, row backfillPriceRow, totalPrice decimal.Decimal, linkedLineItems map[string]string) (string, bool, error) {
	totalPriceFloat, _ := totalPrice.Float64()
	rows, err := tx.QueryContext(ctx,
		`SELECT li.id
		   FROM line_items li
		  WHERE li.receipt_id = ?
		    AND li.product_id = ?
		    AND ABS(CAST(li.total_price AS REAL) - ?) < 0.0001
		    AND NOT EXISTS (
		        SELECT 1 FROM product_prices pp
		         WHERE pp.line_item_id = li.id
		           AND pp.id <> ?
		    )
		  ORDER BY li.line_number, li.rowid, li.id`,
		row.ReceiptID, row.ProductID, totalPriceFloat, row.ID,
	)
	if err != nil {
		return "", false, fmt.Errorf("find line item match: %w", err)
	}
	defer rows.Close()

	matches := make([]string, 0, 2)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", false, fmt.Errorf("scan line item match: %w", err)
		}
		if linkedBy, exists := linkedLineItems[id]; exists && linkedBy != row.ID {
			continue
		}
		matches = append(matches, id)
	}
	if err := rows.Err(); err != nil {
		return "", false, fmt.Errorf("iterate line item matches: %w", err)
	}
	if len(matches) == 1 {
		return matches[0], false, nil
	}
	if len(matches) == 0 {
		return "", false, nil
	}

	priceIDs, err := matchingBackfillPriceIDs(ctx, tx, row, totalPriceFloat)
	if err != nil {
		return "", false, err
	}
	if len(priceIDs) != len(matches) {
		return "", true, nil
	}
	for i, priceID := range priceIDs {
		if priceID == row.ID {
			return matches[i], false, nil
		}
	}
	return "", true, nil
}

func matchingBackfillPriceIDs(ctx context.Context, tx *sql.Tx, row backfillPriceRow, totalPrice float64) ([]string, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT pp.id
		   FROM product_prices pp
		  WHERE pp.receipt_id = ?
		    AND pp.product_id = ?
		    AND pp.line_item_id IS NULL
		    AND ABS((CAST(pp.quantity AS REAL) * CAST(pp.unit_price AS REAL)) - ?) < 0.0001
		  ORDER BY pp.rowid`,
		row.ReceiptID, row.ProductID, totalPrice,
	)
	if err != nil {
		return nil, fmt.Errorf("find product price line indexes: %w", err)
	}
	defer rows.Close()

	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan product price line index: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate product price line indexes: %w", err)
	}
	return ids, nil
}

func productPriceTotal(quantityText, unitPriceText string) (decimal.Decimal, bool) {
	quantity, err := decimal.NewFromString(emptyDefault(quantityText, "1"))
	if err != nil {
		return decimal.Zero, false
	}
	unitPrice, err := decimal.NewFromString(emptyDefault(unitPriceText, "0"))
	if err != nil {
		return decimal.Zero, false
	}
	return quantity.Mul(unitPrice), true
}

func backfillMissingReason(row backfillPriceRow, line *backfillLineItem) string {
	lineUnit := row.Unit
	if line != nil && line.Unit.Valid {
		lineUnit = line.Unit.String
	}
	normalizedLineUnit := units.NormalizeUnit(lineUnit)
	if strings.TrimSpace(normalizedLineUnit) != "" && units.Classify(normalizedLineUnit) == units.CategoryUnknown {
		return "ambiguous unit"
	}
	if row.PackUnit.Valid {
		normalizedPackUnit := units.NormalizeUnit(row.PackUnit.String)
		if strings.TrimSpace(normalizedPackUnit) != "" && units.Classify(normalizedPackUnit) == units.CategoryUnknown {
			return "ambiguous unit"
		}
	}
	return "missing package size"
}

func addBackfillSample(summary *BackfillSummary, limit int, sample BackfillSkippedSample, reason string) {
	if len(summary.Samples) >= limit {
		return
	}
	sample.Reason = reason
	summary.Samples = append(summary.Samples, sample)
}

func backfillCountQuery(productID string) string {
	query := `SELECT COUNT(*),
	                COUNT(CASE WHEN pp.normalized_price IS NOT NULL
	                          AND pp.normalized_unit IS NOT NULL
	                          AND TRIM(pp.normalized_unit) <> '' THEN 1 END)
	            FROM product_prices pp`
	if strings.TrimSpace(productID) != "" {
		query += ` WHERE pp.product_id = ?`
	}
	return query
}

func backfillCountArgs(productID string) []any {
	if strings.TrimSpace(productID) == "" {
		return nil
	}
	return []any{productID}
}

func backfillRowsQuery(productID string) string {
	query := `SELECT pp.id, pp.product_id, p.name, pp.receipt_id, pp.receipt_date,
	                 pp.quantity, pp.unit, pp.unit_price,
	                 pp.normalized_price IS NOT NULL,
	                 pp.normalized_unit IS NOT NULL AND TRIM(pp.normalized_unit) <> '',
	                 pp.line_item_id, p.pack_quantity, p.pack_unit
	            FROM product_prices pp
	            JOIN products p ON p.id = pp.product_id`
	if strings.TrimSpace(productID) != "" {
		query += ` WHERE pp.product_id = ?`
	}
	query += ` ORDER BY pp.receipt_date, pp.id`
	return query
}

func backfillRowsArgs(productID string) []any {
	if strings.TrimSpace(productID) == "" {
		return nil
	}
	return []any{productID}
}

func decimalPtrFromFloat(value sql.NullFloat64) *decimal.Decimal {
	if !value.Valid {
		return nil
	}
	d := decimal.NewFromFloat(value.Float64)
	return &d
}

func stringPtr(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func emptyDefault(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
