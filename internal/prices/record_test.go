package prices

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/mstefanko/cartledger/internal/db"
)

func TestRecordProductPriceFromLineItem(t *testing.T) {
	database := openTestDB(t)
	defer database.Close()
	ctx := context.Background()

	householdID, _, productID, receiptID := seedPriceFixture(t, database, "32", "oz")
	lineID := uuid.New().String()
	if _, err := database.Exec(
		`INSERT INTO line_items
		    (id, receipt_id, product_id, raw_name, quantity, unit, total_price, regular_price, discount_amount, matched)
		 VALUES (?, ?, ?, 'GM CHEERIOS', '1', 'each', '8.99', '10.99', '2.00', 'manual')`,
		lineID, receiptID, productID,
	); err != nil {
		t.Fatalf("insert line item: %v", err)
	}

	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := RecordProductPriceFromLineItem(ctx, tx, lineID); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var count int
	var lineItemID, normalizedPrice, normalizedUnit, regularPrice, discountAmount string
	var isSale bool
	if err := database.QueryRow(
		`SELECT COUNT(*), line_item_id, normalized_price, normalized_unit, regular_price, discount_amount, is_sale
		   FROM product_prices
		  WHERE product_id = ?`,
		productID,
	).Scan(&count, &lineItemID, &normalizedPrice, &normalizedUnit, &regularPrice, &discountAmount, &isSale); err != nil {
		t.Fatalf("query product price: %v", err)
	}
	if count != 1 || lineItemID != lineID {
		t.Fatalf("count/line_item_id = %d/%q, want 1/%q", count, lineItemID, lineID)
	}
	if normalizedPrice != "0.2809375" || normalizedUnit != "oz" {
		t.Fatalf("normalized = %s/%s, want 0.2809375/oz", normalizedPrice, normalizedUnit)
	}
	if regularPrice != "10.99" || discountAmount != "2.00" || !isSale {
		t.Fatalf("sale fields = %s/%s/%v, want 10.99/2.00/true", regularPrice, discountAmount, isSale)
	}

	if _, err := database.Exec(`UPDATE line_items SET total_price = '7.99' WHERE id = ?`, lineID); err != nil {
		t.Fatalf("update line: %v", err)
	}
	tx, err = database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin update: %v", err)
	}
	if err := RecordProductPriceFromLineItem(ctx, tx, lineID); err != nil {
		t.Fatalf("record update: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit update: %v", err)
	}
	if err := database.QueryRow(`SELECT COUNT(*), normalized_price FROM product_prices WHERE line_item_id = ?`, lineID).Scan(&count, &normalizedPrice); err != nil {
		t.Fatalf("query updated product price: %v", err)
	}
	if count != 1 || normalizedPrice != "0.2496875" {
		t.Fatalf("updated count/normalized = %d/%s, want 1/0.2496875", count, normalizedPrice)
	}

	var purchaseCount int
	if err := database.QueryRow(`SELECT purchase_count FROM products WHERE id = ? AND household_id = ?`, productID, householdID).Scan(&purchaseCount); err != nil {
		t.Fatalf("query purchase count: %v", err)
	}
	if purchaseCount != 1 {
		t.Fatalf("purchase_count = %d, want 1", purchaseCount)
	}
}

func TestRecordProductPriceFromLineItemDeletesWhenProductCleared(t *testing.T) {
	database := openTestDB(t)
	defer database.Close()
	ctx := context.Background()

	_, _, productID, receiptID := seedPriceFixture(t, database, "12", "oz")
	lineID := uuid.New().String()
	if _, err := database.Exec(
		`INSERT INTO line_items (id, receipt_id, product_id, raw_name, quantity, unit, total_price, matched)
		 VALUES (?, ?, ?, 'CHEERIOS', '1', 'each', '4.99', 'manual')`,
		lineID, receiptID, productID,
	); err != nil {
		t.Fatalf("insert line item: %v", err)
	}
	tx, _ := database.BeginTx(ctx, nil)
	if err := RecordProductPriceFromLineItem(ctx, tx, lineID); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if _, err := database.Exec(`UPDATE line_items SET product_id = NULL WHERE id = ?`, lineID); err != nil {
		t.Fatalf("clear product: %v", err)
	}
	tx, _ = database.BeginTx(ctx, nil)
	if err := RecordProductPriceFromLineItem(ctx, tx, lineID); err != nil {
		t.Fatalf("record clear: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit clear: %v", err)
	}

	var count, purchaseCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM product_prices WHERE line_item_id = ?`, lineID).Scan(&count); err != nil {
		t.Fatalf("count product_prices: %v", err)
	}
	if err := database.QueryRow(`SELECT purchase_count FROM products WHERE id = ?`, productID).Scan(&purchaseCount); err != nil {
		t.Fatalf("count product purchases: %v", err)
	}
	if count != 0 || purchaseCount != 0 {
		t.Fatalf("counts = product_prices:%d purchase_count:%d, want 0/0", count, purchaseCount)
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := db.RunMigrations(database); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	return database
}

func seedPriceFixture(t *testing.T, database *sql.DB, packQuantity, packUnit string) (householdID, storeID, productID, receiptID string) {
	t.Helper()
	if err := database.QueryRow(`INSERT INTO households (name) VALUES ('Price HH') RETURNING id`).Scan(&householdID); err != nil {
		t.Fatalf("insert household: %v", err)
	}
	if err := database.QueryRow(`INSERT INTO stores (household_id, name) VALUES (?, 'Costco') RETURNING id`, householdID).Scan(&storeID); err != nil {
		t.Fatalf("insert store: %v", err)
	}
	if err := database.QueryRow(
		`INSERT INTO products (household_id, name, pack_quantity, pack_unit) VALUES (?, 'Cheerios', ?, ?) RETURNING id`,
		householdID, packQuantity, packUnit,
	).Scan(&productID); err != nil {
		t.Fatalf("insert product: %v", err)
	}
	receiptID = uuid.New().String()
	if _, err := database.Exec(
		`INSERT INTO receipts (id, household_id, store_id, receipt_date, total, status)
		 VALUES (?, ?, ?, '2026-05-08', '20.00', 'matched')`,
		receiptID, householdID, storeID,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	return householdID, storeID, productID, receiptID
}
