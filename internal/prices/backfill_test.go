package prices

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestBackfillNormalizedPricesLinksAndAppliesProductPack(t *testing.T) {
	database := openTestDB(t)
	defer database.Close()
	ctx := context.Background()

	_, storeID, productID, receiptID := seedPriceFixture(t, database, "32", "oz")
	lineID := uuid.New().String()
	if _, err := database.Exec(
		`INSERT INTO line_items
		    (id, receipt_id, product_id, raw_name, quantity, unit, total_price, matched, line_number)
		 VALUES (?, ?, ?, 'GM CHEERIOS', '1', 'each', '8.99', 'manual', 1)`,
		lineID, receiptID, productID,
	); err != nil {
		t.Fatalf("insert line item: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO product_prices
		    (product_id, store_id, receipt_id, receipt_date, quantity, unit, unit_price)
		 VALUES (?, ?, ?, '2026-05-08', '1', 'each', '8.99')`,
		productID, storeID, receiptID,
	); err != nil {
		t.Fatalf("insert product price: %v", err)
	}

	dryRun, err := BackfillNormalizedPrices(ctx, database, BackfillOptions{})
	if err != nil {
		t.Fatalf("dry-run backfill: %v", err)
	}
	if dryRun.TotalRows != 1 || dryRun.ProductPackNormalized != 1 || dryRun.LinkableRows != 1 || dryRun.LinkedRows != 0 {
		t.Fatalf("dry-run summary = %+v, want total=1 product_pack=1 linkable=1 linked=0", dryRun)
	}

	applied, err := BackfillNormalizedPrices(ctx, database, BackfillOptions{Apply: true})
	if err != nil {
		t.Fatalf("apply backfill: %v", err)
	}
	if applied.LinkedRows != 1 || applied.ProductPackNormalized != 1 {
		t.Fatalf("apply summary = %+v, want linked=1 product_pack=1", applied)
	}

	var linkedID, normalizedPrice, normalizedUnit string
	if err := database.QueryRow(
		`SELECT line_item_id, normalized_price, normalized_unit
		   FROM product_prices
		  WHERE product_id = ?`,
		productID,
	).Scan(&linkedID, &normalizedPrice, &normalizedUnit); err != nil {
		t.Fatalf("query product price: %v", err)
	}
	if linkedID != lineID || normalizedPrice != "0.2809375" || normalizedUnit != "oz" {
		t.Fatalf("backfilled row = %s %s/%s, want %s 0.2809375/oz", linkedID, normalizedPrice, normalizedUnit, lineID)
	}
}

func TestBackfillNormalizedPricesReportsMissingPack(t *testing.T) {
	database := openTestDB(t)
	defer database.Close()
	ctx := context.Background()

	_, storeID, productID, receiptID := seedPriceFixture(t, database, "32", "oz")
	if _, err := database.Exec(`UPDATE products SET pack_quantity = NULL, pack_unit = NULL WHERE id = ?`, productID); err != nil {
		t.Fatalf("clear product pack: %v", err)
	}
	lineID := uuid.New().String()
	if _, err := database.Exec(
		`INSERT INTO line_items
		    (id, receipt_id, product_id, raw_name, quantity, unit, total_price, matched, line_number)
		 VALUES (?, ?, ?, 'UNKNOWN ITEM', '1', 'each', '4.99', 'manual', 1)`,
		lineID, receiptID, productID,
	); err != nil {
		t.Fatalf("insert line item: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO product_prices
		    (product_id, store_id, receipt_id, receipt_date, quantity, unit, unit_price, line_item_id)
		 VALUES (?, ?, ?, '2026-05-08', '1', 'each', '4.99', ?)`,
		productID, storeID, receiptID, lineID,
	); err != nil {
		t.Fatalf("insert product price: %v", err)
	}

	summary, err := BackfillNormalizedPrices(ctx, database, BackfillOptions{})
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if summary.MissingPackSkipped != 1 || len(summary.Samples) != 1 {
		t.Fatalf("summary = %+v, want one missing-pack sample", summary)
	}
	if summary.Samples[0].RawLineText != "UNKNOWN ITEM" || summary.Samples[0].Reason != "missing package size" {
		t.Fatalf("sample = %+v, want raw line and missing package reason", summary.Samples[0])
	}
}

func TestBackfillNormalizedPricesUsesLineIndexForDuplicateTotals(t *testing.T) {
	database := openTestDB(t)
	defer database.Close()
	ctx := context.Background()

	_, storeID, productID, receiptID := seedPriceFixture(t, database, "16", "oz")
	line1 := uuid.New().String()
	line2 := uuid.New().String()
	if _, err := database.Exec(
		`INSERT INTO line_items
		    (id, receipt_id, product_id, raw_name, quantity, unit, total_price, matched, line_number)
		 VALUES
		    (?, ?, ?, 'YOGURT A', '1', 'each', '2.00', 'manual', 1),
		    (?, ?, ?, 'YOGURT B', '1', 'each', '2.00', 'manual', 2)`,
		line1, receiptID, productID,
		line2, receiptID, productID,
	); err != nil {
		t.Fatalf("insert duplicate lines: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO product_prices
		    (product_id, store_id, receipt_id, receipt_date, quantity, unit, unit_price)
		 VALUES
		    (?, ?, ?, '2026-05-08', '1', 'each', '2.00'),
		    (?, ?, ?, '2026-05-08', '1', 'each', '2.00')`,
		productID, storeID, receiptID,
		productID, storeID, receiptID,
	); err != nil {
		t.Fatalf("insert duplicate product prices: %v", err)
	}

	summary, err := BackfillNormalizedPrices(ctx, database, BackfillOptions{Apply: true})
	if err != nil {
		t.Fatalf("apply backfill: %v", err)
	}
	if summary.LinkableRows != 2 || summary.LinkedRows != 2 || summary.AmbiguousLinkSkipped != 0 {
		t.Fatalf("summary = %+v, want two line-index links and no ambiguity", summary)
	}

	rows, err := database.Query(`SELECT line_item_id FROM product_prices WHERE product_id = ? ORDER BY rowid`, productID)
	if err != nil {
		t.Fatalf("query links: %v", err)
	}
	defer rows.Close()

	var linked []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan link: %v", err)
		}
		linked = append(linked, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate links: %v", err)
	}
	if len(linked) != 2 || linked[0] != line1 || linked[1] != line2 {
		t.Fatalf("linked line items = %v, want [%s %s]", linked, line1, line2)
	}
}
