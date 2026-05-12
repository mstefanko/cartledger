package prices

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/mstefanko/cartledger/internal/units"
)

type ProductPriceContext struct {
	HouseholdID      string
	ProductID        string
	ProductGroupID   *string
	ProductPackQty   *decimal.Decimal
	ProductPackUnit  *string
	GroupCompareUnit *string
}

func RecordProductPriceFromLineItem(ctx context.Context, tx *sql.Tx, lineItemID string) error {
	existingProductID, err := existingPriceProductID(ctx, tx, lineItemID)
	if err != nil {
		return err
	}

	var productID, receiptID, quantityText, totalPriceText sql.NullString
	var unit, regularPrice, discountAmount sql.NullString
	var overrideQuantityText, overrideUnit sql.NullString
	var storeID sql.NullString
	var receiptDate time.Time

	err = tx.QueryRowContext(ctx,
		`SELECT li.product_id, li.receipt_id, li.quantity, li.unit, li.total_price,
		        li.regular_price, li.discount_amount,
		        li.pack_quantity_override, li.pack_unit_override,
		        r.store_id, r.receipt_date
		   FROM line_items li
		   JOIN receipts r ON r.id = li.receipt_id
		  WHERE li.id = ?`,
		lineItemID,
	).Scan(
		&productID, &receiptID, &quantityText, &unit, &totalPriceText,
		&regularPrice, &discountAmount,
		&overrideQuantityText, &overrideUnit,
		&storeID, &receiptDate,
	)
	if err == sql.ErrNoRows {
		if existingProductID != nil {
			return deletePriceAndRefresh(ctx, tx, lineItemID, *existingProductID)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("load line item: %w", err)
	}

	if !productID.Valid || strings.TrimSpace(productID.String) == "" || !storeID.Valid || strings.TrimSpace(storeID.String) == "" {
		if existingProductID != nil {
			return deletePriceAndRefresh(ctx, tx, lineItemID, *existingProductID)
		}
		return nil
	}

	lineQuantity, err := decimal.NewFromString(nullStringOr(quantityText, "1"))
	if err != nil {
		return fmt.Errorf("parse line quantity: %w", err)
	}
	totalPrice, err := decimal.NewFromString(nullStringOr(totalPriceText, "0"))
	if err != nil {
		return fmt.Errorf("parse total price: %w", err)
	}

	priceContext, err := loadProductPriceContext(ctx, tx, productID.String)
	if err != nil {
		return err
	}
	lineOverrideQuantity, err := decimalPtrFromNullString(overrideQuantityText)
	if err != nil {
		return fmt.Errorf("parse line package override: %w", err)
	}
	lineOverrideUnit := stringPtrFromNullString(overrideUnit)
	lineUnit := stringPtrFromNullString(unit)

	scope := units.ConversionScope{
		HouseholdID: priceContext.HouseholdID,
		ProductID:   priceContext.ProductID,
	}
	if priceContext.ProductGroupID != nil {
		scope.ProductGroupID = *priceContext.ProductGroupID
	}

	normalized, err := NormalizeLineItemPriceWithScope(
		ctx,
		tx,
		totalPrice,
		lineQuantity,
		lineUnit,
		priceContext.ProductPackQty,
		priceContext.ProductPackUnit,
		lineOverrideQuantity,
		lineOverrideUnit,
		priceContext.GroupCompareUnit,
		scope,
	)
	if err != nil {
		return fmt.Errorf("normalize line price: %w", err)
	}

	unitForPrice := "each"
	if lineUnit != nil && strings.TrimSpace(*lineUnit) != "" {
		unitForPrice = *lineUnit
	}

	var normalizedPriceValue any
	if normalized.NormalizedPrice != nil {
		normalizedPriceValue = normalized.NormalizedPrice.String()
	}
	var normalizedUnitValue any
	if normalized.NormalizedUnit != nil {
		normalizedUnitValue = *normalized.NormalizedUnit
	}

	var regularPriceValue, discountAmountValue any
	if regularPrice.Valid {
		regularPriceValue = regularPrice.String
	}
	if discountAmount.Valid {
		discountAmountValue = discountAmount.String
	}
	isSale := regularPrice.Valid && discountAmount.Valid
	now := time.Now().UTC()

	_, err = tx.ExecContext(ctx,
		`INSERT INTO product_prices
		    (id, product_id, store_id, receipt_id, receipt_date, quantity, unit,
		     unit_price, normalized_price, normalized_unit,
		     regular_price, discount_amount, is_sale, line_item_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(line_item_id) WHERE line_item_id IS NOT NULL DO UPDATE SET
		     product_id = excluded.product_id,
		     store_id = excluded.store_id,
		     receipt_id = excluded.receipt_id,
		     receipt_date = excluded.receipt_date,
		     quantity = excluded.quantity,
		     unit = excluded.unit,
		     unit_price = excluded.unit_price,
		     normalized_price = excluded.normalized_price,
		     normalized_unit = excluded.normalized_unit,
		     regular_price = excluded.regular_price,
		     discount_amount = excluded.discount_amount,
		     is_sale = excluded.is_sale`,
		uuid.New().String(), productID.String, storeID.String, receiptID.String, receiptDate.Format("2006-01-02"),
		lineQuantity.String(), unitForPrice, normalized.UnitPrice.String(),
		normalizedPriceValue, normalizedUnitValue,
		regularPriceValue, discountAmountValue, isSale, lineItemID, now,
	)
	if err != nil {
		return fmt.Errorf("upsert product price: %w", err)
	}

	if existingProductID != nil && *existingProductID != productID.String {
		if err := RefreshProductPurchaseStats(ctx, tx, *existingProductID); err != nil {
			return err
		}
	}
	return RefreshProductPurchaseStats(ctx, tx, productID.String)
}

func RefreshProductPurchaseStats(ctx context.Context, tx *sql.Tx, productID string) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE products
		    SET last_purchased_at = (SELECT MAX(receipt_date) FROM product_prices WHERE product_id = ?),
		        purchase_count = (SELECT COUNT(*) FROM product_prices WHERE product_id = ?),
		        updated_at = ?
		  WHERE id = ?`,
		productID, productID, time.Now().UTC(), productID,
	)
	if err != nil {
		return fmt.Errorf("refresh product purchase stats: %w", err)
	}
	return nil
}

func existingPriceProductID(ctx context.Context, tx *sql.Tx, lineItemID string) (*string, error) {
	var productID string
	err := tx.QueryRowContext(ctx,
		`SELECT product_id FROM product_prices WHERE line_item_id = ?`,
		lineItemID,
	).Scan(&productID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load existing product price: %w", err)
	}
	return &productID, nil
}

func deletePriceAndRefresh(ctx context.Context, tx *sql.Tx, lineItemID string, productID string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM product_prices WHERE line_item_id = ?`, lineItemID); err != nil {
		return fmt.Errorf("delete product price: %w", err)
	}
	return RefreshProductPurchaseStats(ctx, tx, productID)
}

func loadProductPriceContext(ctx context.Context, tx *sql.Tx, productID string) (ProductPriceContext, error) {
	out := ProductPriceContext{ProductID: productID}
	var packQuantity sql.NullFloat64
	var packUnit, productGroupID, comparisonUnit sql.NullString
	err := tx.QueryRowContext(ctx,
		`SELECT p.household_id, p.product_group_id, p.pack_quantity, p.pack_unit, pg.comparison_unit
		   FROM products p
		   LEFT JOIN product_groups pg ON pg.id = p.product_group_id
		  WHERE p.id = ?`,
		productID,
	).Scan(&out.HouseholdID, &productGroupID, &packQuantity, &packUnit, &comparisonUnit)
	if err != nil {
		return out, fmt.Errorf("load product price context: %w", err)
	}
	if packQuantity.Valid {
		d := decimal.NewFromFloat(packQuantity.Float64)
		out.ProductPackQty = &d
	}
	out.ProductPackUnit = stringPtrFromNullString(packUnit)
	out.ProductGroupID = stringPtrFromNullString(productGroupID)
	out.GroupCompareUnit = stringPtrFromNullString(comparisonUnit)
	return out, nil
}

func decimalPtrFromNullString(value sql.NullString) (*decimal.Decimal, error) {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return nil, nil
	}
	d, err := decimal.NewFromString(value.String)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func stringPtrFromNullString(value sql.NullString) *string {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return nil
	}
	s := value.String
	return &s
}

func nullStringOr(value sql.NullString, fallback string) string {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return fallback
	}
	return value.String
}
