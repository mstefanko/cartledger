package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/shopspring/decimal"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/prices"
	"github.com/mstefanko/cartledger/internal/units"
)

const (
	compareReceiptsMinCount = 2
	compareReceiptsMaxCount = 12
)

type compareReceiptsRequest struct {
	ReceiptIDs []string `json:"receipt_ids"`
	MinOverlap *int     `json:"min_overlap"`
}

type compareReceiptsResponse struct {
	Receipts         []compareReceiptResponse `json:"receipts"`
	Products         []compareProductResponse `json:"products"`
	MinOverlap       int                      `json:"min_overlap"`
	MissingUnitCount int                      `json:"missing_unit_count"`
}

type compareReceiptResponse struct {
	ID          string  `json:"id"`
	StoreID     *string `json:"store_id"`
	StoreName   *string `json:"store_name"`
	ReceiptDate string  `json:"receipt_date"`
	Total       *string `json:"total"`
	LineCount   int     `json:"line_count"`
	Status      string  `json:"status"`
}

type compareProductResponse struct {
	ComparisonKey  string                      `json:"comparison_key"`
	ProductID      string                      `json:"product_id"`
	ProductGroupID *string                     `json:"product_group_id"`
	Name           string                      `json:"name"`
	Category       *string                     `json:"category"`
	ComparableUnit *string                     `json:"comparable_unit"`
	BestAppearance *string                     `json:"best_appearance_id"`
	Appearances    []compareAppearanceResponse `json:"appearances"`
	overlapCount   int
}

type compareAppearanceResponse struct {
	LineItemID      string                      `json:"line_item_id"`
	ReceiptID       string                      `json:"receipt_id"`
	RawName         string                      `json:"raw_name"`
	Quantity        *string                     `json:"quantity,omitempty"`
	Unit            *string                     `json:"unit,omitempty"`
	TotalPrice      string                      `json:"total_price"`
	UnitPrice       *string                     `json:"unit_price,omitempty"`
	SizeKnown       bool                        `json:"size_known"`
	NormalizedPrice *string                     `json:"normalized_price,omitempty"`
	NormalizedUnit  *string                     `json:"normalized_unit,omitempty"`
	Lines           []compareLineChoiceResponse `json:"lines,omitempty"`
}

type compareLineChoiceResponse struct {
	LineItemID string  `json:"line_item_id"`
	RawName    string  `json:"raw_name"`
	Quantity   *string `json:"quantity,omitempty"`
	Unit       *string `json:"unit,omitempty"`
	TotalPrice string  `json:"total_price"`
	UnitPrice  *string `json:"unit_price,omitempty"`
}

type compareInvalidReceipt struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// compareReceipts builds a product-by-receipt matrix for 2-12 matched/reviewed
// receipts. It intentionally normalizes prices from line_items at request time
// so older/manual receipts do not depend on product_prices backfill status.
func (h *ReceiptHandler) compareReceipts(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)

	var req compareReceiptsRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid json body"})
	}

	if len(req.ReceiptIDs) < compareReceiptsMinCount || len(req.ReceiptIDs) > compareReceiptsMaxCount {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("receipt_ids must contain %d to %d receipts", compareReceiptsMinCount, compareReceiptsMaxCount),
		})
	}

	receiptIDs := dedupeTrimmedStrings(req.ReceiptIDs)
	if len(receiptIDs) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "receipt_ids is required"})
	}

	minOverlap := compareReceiptsMinCount
	if req.MinOverlap != nil {
		minOverlap = *req.MinOverlap
	}
	if minOverlap < compareReceiptsMinCount || minOverlap > len(receiptIDs) {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("min_overlap must be between %d and the number of unique receipts", compareReceiptsMinCount),
		})
	}

	receipts, err := h.loadCompareReceipts(c.Request().Context(), householdID, receiptIDs)
	if errors.Is(err, sql.ErrNoRows) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "receipt not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	receiptsByID := make(map[string]compareReceiptResponse, len(receipts))
	for _, receipt := range receipts {
		receiptsByID[receipt.ID] = receipt
	}
	if len(receiptsByID) != len(receiptIDs) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "receipt not found"})
	}

	invalid := make([]compareInvalidReceipt, 0)
	orderedReceipts := make([]compareReceiptResponse, 0, len(receiptIDs))
	for _, id := range receiptIDs {
		receipt := receiptsByID[id]
		if receipt.Status != "matched" && receipt.Status != "reviewed" {
			invalid = append(invalid, compareInvalidReceipt{ID: id, Status: receipt.Status})
		}
		orderedReceipts = append(orderedReceipts, receipt)
	}
	if len(invalid) > 0 {
		return c.JSON(http.StatusConflict, map[string][]compareInvalidReceipt{"invalid": invalid})
	}

	products, missingUnitCount, err := h.loadCompareProducts(c.Request().Context(), householdID, receiptIDs, minOverlap)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusOK, compareReceiptsResponse{
		Receipts:         orderedReceipts,
		Products:         products,
		MinOverlap:       minOverlap,
		MissingUnitCount: missingUnitCount,
	})
}

func (h *ReceiptHandler) loadCompareReceipts(ctx context.Context, householdID string, receiptIDs []string) ([]compareReceiptResponse, error) {
	placeholders, args := compareInClauseArgs(receiptIDs)
	queryArgs := append([]any{householdID}, args...)
	rows, err := h.DB.QueryContext(ctx, fmt.Sprintf(
		`SELECT r.id, r.store_id, s.name, r.receipt_date, r.total, r.status,
		        COUNT(li.id) AS line_count
		   FROM receipts r
		   LEFT JOIN stores s ON s.id = r.store_id
		   LEFT JOIN line_items li ON li.receipt_id = r.id
		  WHERE r.household_id = ? AND r.id IN (%s)
		  GROUP BY r.id, r.store_id, s.name, r.receipt_date, r.total, r.status`,
		placeholders,
	), queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	receipts := make([]compareReceiptResponse, 0, len(receiptIDs))
	for rows.Next() {
		var receipt compareReceiptResponse
		var receiptDate time.Time
		var total sql.NullString
		if err := rows.Scan(
			&receipt.ID, &receipt.StoreID, &receipt.StoreName, &receiptDate,
			&total, &receipt.Status, &receipt.LineCount,
		); err != nil {
			return nil, err
		}
		receipt.ReceiptDate = receiptDate.Format("2006-01-02")
		if total.Valid {
			receipt.Total = &total.String
		}
		receipts = append(receipts, receipt)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return receipts, nil
}

type compareLineRow struct {
	ComparisonKey  string
	LineItemID     string
	ReceiptID      string
	RawName        string
	Quantity       decimal.Decimal
	Unit           string
	TotalPrice     decimal.Decimal
	ProductID      string
	ProductGroupID *string
	Name           string
	Category       *string
	PackQuantity   *decimal.Decimal
	PackUnit       *string
	OverrideQty    *decimal.Decimal
	OverrideUnit   *string
}

type compareCellAggregate struct {
	ComparisonKey  string
	ReceiptID      string
	LineItemID     string
	RawName        string
	ProductID      string
	ProductGroupID *string
	Name           string
	Category       *string
	PackQuantity   *decimal.Decimal
	PackUnit       *string
	OverrideQty    *decimal.Decimal
	OverrideUnit   *string
	TotalPrice     decimal.Decimal

	Quantity          decimal.Decimal
	Unit              string
	quantitySummable  bool
	primaryTotalPrice decimal.Decimal
	Lines             []compareLineChoiceResponse
}

func (h *ReceiptHandler) loadCompareProducts(ctx context.Context, householdID string, receiptIDs []string, minOverlap int) ([]compareProductResponse, int, error) {
	placeholders, args := compareInClauseArgs(receiptIDs)
	queryArgs := append([]any{householdID}, args...)
	rows, err := h.DB.QueryContext(ctx, fmt.Sprintf(
		`SELECT COALESCE(p.product_group_id, p.id) AS comparison_key,
		        li.id, li.receipt_id, li.raw_name, li.quantity, li.unit, li.total_price,
		        p.id, p.product_group_id, COALESCE(pg.name, p.name) AS display_name, p.category,
		        p.pack_quantity, p.pack_unit, li.pack_quantity_override, li.pack_unit_override
		   FROM line_items li
		   JOIN receipts r ON r.id = li.receipt_id
		   JOIN products p ON p.id = li.product_id AND p.household_id = r.household_id
		   LEFT JOIN product_groups pg ON pg.id = p.product_group_id AND pg.household_id = p.household_id
		  WHERE r.household_id = ?
		    AND li.receipt_id IN (%s)
		    AND li.product_id IS NOT NULL
		  ORDER BY display_name COLLATE NOCASE, li.receipt_id, li.line_number, li.created_at, li.id`,
		placeholders,
	), queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	groups := make(map[string]map[string]*compareCellAggregate)
	for rows.Next() {
		line, err := scanCompareLineRow(rows)
		if err != nil {
			return nil, 0, err
		}
		cells := groups[line.ComparisonKey]
		if cells == nil {
			cells = make(map[string]*compareCellAggregate)
			groups[line.ComparisonKey] = cells
		}
		cell := cells[line.ReceiptID]
		if cell == nil {
			cell = newCompareCellAggregate(line)
			cells[line.ReceiptID] = cell
			continue
		}
		cell.add(line)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	receiptOrder := make(map[string]int, len(receiptIDs))
	for i, id := range receiptIDs {
		receiptOrder[id] = i
	}

	products := make([]compareProductResponse, 0, len(groups))
	missingUnitCount := 0
	for comparisonKey, cells := range groups {
		if len(cells) < minOverlap {
			continue
		}

		cellList := make([]*compareCellAggregate, 0, len(cells))
		for _, cell := range cells {
			cellList = append(cellList, cell)
		}
		sort.SliceStable(cellList, func(i, j int) bool {
			return receiptOrder[cellList[i].ReceiptID] < receiptOrder[cellList[j].ReceiptID]
		})

		product := compareProductResponse{
			ComparisonKey: comparisonKey,
			Appearances:   make([]compareAppearanceResponse, 0, len(cellList)),
			overlapCount:  len(cellList),
		}
		for i, cell := range cellList {
			if i == 0 {
				product.ProductID = cell.ProductID
				product.ProductGroupID = cell.ProductGroupID
				product.Name = cell.Name
				product.Category = cell.Category
			} else if product.Category == nil && cell.Category != nil {
				product.Category = cell.Category
			}

			appearance := h.compareAppearanceFromAggregate(cell)
			if !appearance.SizeKnown {
				missingUnitCount++
			}
			product.Appearances = append(product.Appearances, appearance)
		}
		markBestCompareAppearance(&product)
		products = append(products, product)
	}

	sort.SliceStable(products, func(i, j int) bool {
		if products[i].overlapCount != products[j].overlapCount {
			return products[i].overlapCount > products[j].overlapCount
		}
		return strings.ToLower(products[i].Name) < strings.ToLower(products[j].Name)
	})

	return products, missingUnitCount, nil
}

func scanCompareLineRow(rows *sql.Rows) (compareLineRow, error) {
	var line compareLineRow
	var quantityText, totalPriceText string
	var unit, productGroupID, category, packUnit, overrideQtyText, overrideUnit sql.NullString
	var packQty sql.NullFloat64
	if err := rows.Scan(
		&line.ComparisonKey,
		&line.LineItemID,
		&line.ReceiptID,
		&line.RawName,
		&quantityText,
		&unit,
		&totalPriceText,
		&line.ProductID,
		&productGroupID,
		&line.Name,
		&category,
		&packQty,
		&packUnit,
		&overrideQtyText,
		&overrideUnit,
	); err != nil {
		return line, err
	}

	quantity, err := decimal.NewFromString(strings.TrimSpace(quantityText))
	if err != nil {
		return line, err
	}
	totalPrice, err := decimal.NewFromString(strings.TrimSpace(totalPriceText))
	if err != nil {
		return line, err
	}

	line.Quantity = quantity
	line.TotalPrice = totalPrice
	if unit.Valid {
		line.Unit = normalizedCompareUnit(unit.String)
	}
	if productGroupID.Valid {
		line.ProductGroupID = &productGroupID.String
	}
	if category.Valid {
		line.Category = &category.String
	}
	if packQty.Valid {
		d := decimal.NewFromFloat(packQty.Float64)
		line.PackQuantity = &d
	}
	if packUnit.Valid && strings.TrimSpace(packUnit.String) != "" {
		line.PackUnit = &packUnit.String
	}
	if overrideQtyText.Valid && strings.TrimSpace(overrideQtyText.String) != "" {
		d, err := decimal.NewFromString(strings.TrimSpace(overrideQtyText.String))
		if err != nil {
			return line, err
		}
		line.OverrideQty = &d
	}
	if overrideUnit.Valid && strings.TrimSpace(overrideUnit.String) != "" {
		line.OverrideUnit = &overrideUnit.String
	}
	return line, nil
}

func newCompareCellAggregate(line compareLineRow) *compareCellAggregate {
	return &compareCellAggregate{
		ComparisonKey:     line.ComparisonKey,
		ReceiptID:         line.ReceiptID,
		LineItemID:        line.LineItemID,
		RawName:           line.RawName,
		ProductID:         line.ProductID,
		ProductGroupID:    line.ProductGroupID,
		Name:              line.Name,
		Category:          line.Category,
		PackQuantity:      line.PackQuantity,
		PackUnit:          line.PackUnit,
		OverrideQty:       line.OverrideQty,
		OverrideUnit:      line.OverrideUnit,
		TotalPrice:        line.TotalPrice,
		Quantity:          line.Quantity,
		Unit:              line.Unit,
		quantitySummable:  true,
		primaryTotalPrice: line.TotalPrice,
		Lines:             []compareLineChoiceResponse{compareLineChoiceFromRow(line)},
	}
}

func (cell *compareCellAggregate) add(line compareLineRow) {
	cell.TotalPrice = cell.TotalPrice.Add(line.TotalPrice)
	cell.Lines = append(cell.Lines, compareLineChoiceFromRow(line))
	if cell.quantitySummable && cell.Unit == line.Unit {
		cell.Quantity = cell.Quantity.Add(line.Quantity)
	} else {
		cell.quantitySummable = false
		cell.Quantity = decimal.Zero
		cell.Unit = ""
	}

	if line.TotalPrice.GreaterThan(cell.primaryTotalPrice) ||
		(line.TotalPrice.Equal(cell.primaryTotalPrice) && line.LineItemID < cell.LineItemID) {
		cell.primaryTotalPrice = line.TotalPrice
		cell.LineItemID = line.LineItemID
		cell.RawName = line.RawName
		cell.ProductID = line.ProductID
		cell.ProductGroupID = line.ProductGroupID
		cell.Name = line.Name
		cell.Category = line.Category
		cell.PackQuantity = line.PackQuantity
		cell.PackUnit = line.PackUnit
		cell.OverrideQty = line.OverrideQty
		cell.OverrideUnit = line.OverrideUnit
	}
}

func (h *ReceiptHandler) compareAppearanceFromAggregate(cell *compareCellAggregate) compareAppearanceResponse {
	appearance := compareAppearanceResponse{
		LineItemID: cell.LineItemID,
		ReceiptID:  cell.ReceiptID,
		RawName:    cell.RawName,
		TotalPrice: cell.TotalPrice.String(),
	}
	if len(cell.Lines) > 1 {
		appearance.Lines = cell.Lines
	}

	if !cell.quantitySummable {
		return appearance
	}

	quantity := cell.Quantity.String()
	appearance.Quantity = &quantity
	if cell.Unit != "" {
		unit := cell.Unit
		appearance.Unit = &unit
	}
	if cell.Quantity.GreaterThan(decimal.Zero) {
		unitPrice := cell.TotalPrice.Div(cell.Quantity).String()
		appearance.UnitPrice = &unitPrice
	}

	var lineUnit *string
	if cell.Unit != "" {
		lineUnit = &cell.Unit
	}
	normalized, err := prices.NormalizeLineItemPrice(
		cell.TotalPrice,
		cell.Quantity,
		lineUnit,
		cell.PackQuantity,
		cell.PackUnit,
		cell.OverrideQty,
		cell.OverrideUnit,
	)
	if err != nil || normalized.NormalizedPrice == nil || normalized.NormalizedUnit == nil {
		return appearance
	}
	normalizedPriceStr := normalized.NormalizedPrice.String()
	appearance.NormalizedPrice = &normalizedPriceStr
	appearance.NormalizedUnit = normalized.NormalizedUnit
	appearance.SizeKnown = true
	return appearance
}

func compareLineChoiceFromRow(line compareLineRow) compareLineChoiceResponse {
	choice := compareLineChoiceResponse{
		LineItemID: line.LineItemID,
		RawName:    line.RawName,
		TotalPrice: line.TotalPrice.String(),
	}
	quantity := line.Quantity.String()
	choice.Quantity = &quantity
	if line.Unit != "" {
		unit := line.Unit
		choice.Unit = &unit
	}
	if line.Quantity.GreaterThan(decimal.Zero) {
		unitPrice := line.TotalPrice.Div(line.Quantity).String()
		choice.UnitPrice = &unitPrice
	}
	return choice
}

func markBestCompareAppearance(product *compareProductResponse) {
	unitCounts := make(map[string]int)
	for _, appearance := range product.Appearances {
		if appearance.SizeKnown && appearance.NormalizedUnit != nil {
			unitCounts[*appearance.NormalizedUnit]++
		}
	}

	var comparableUnit string
	comparableCount := 0
	for unit, count := range unitCounts {
		if count < 2 {
			continue
		}
		if count > comparableCount || (count == comparableCount && (comparableUnit == "" || unit < comparableUnit)) {
			comparableUnit = unit
			comparableCount = count
		}
	}
	if comparableUnit == "" {
		return
	}

	var bestID string
	var bestPrice decimal.Decimal
	for _, appearance := range product.Appearances {
		if !appearance.SizeKnown || appearance.NormalizedUnit == nil || *appearance.NormalizedUnit != comparableUnit || appearance.NormalizedPrice == nil {
			continue
		}
		price, err := decimal.NewFromString(*appearance.NormalizedPrice)
		if err != nil {
			continue
		}
		if bestID == "" || price.LessThan(bestPrice) {
			bestID = appearance.LineItemID
			bestPrice = price
		}
	}
	if bestID == "" {
		return
	}
	product.ComparableUnit = &comparableUnit
	product.BestAppearance = &bestID
}

func dedupeTrimmedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func compareInClauseArgs(ids []string) (string, []any) {
	parts := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		parts[i] = "?"
		args[i] = id
	}
	return strings.Join(parts, ","), args
}

func normalizedCompareUnit(unit string) string {
	unit = strings.TrimSpace(unit)
	if unit == "" {
		return ""
	}
	return units.NormalizeUnit(unit)
}
