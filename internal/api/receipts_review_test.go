package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/db"
)

func newReceiptReviewTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := db.RunMigrations(database); err != nil {
		database.Close()
		t.Fatalf("RunMigrations: %v", err)
	}

	var householdID string
	if err := database.QueryRow("INSERT INTO households (name) VALUES ('Test') RETURNING id").Scan(&householdID); err != nil {
		database.Close()
		t.Fatalf("insert household: %v", err)
	}
	return database, householdID
}

func receiptContext(method, target, body, householdID string) (echo.Context, *httptest.ResponseRecorder) {
	e := echo.New()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set(auth.ContextKeyHouseholdID, householdID)
	return c, rec
}

func TestUpdateLineItemReviewStatusRequiresMatchedProduct(t *testing.T) {
	database, householdID := newReceiptReviewTestDB(t)
	defer database.Close()

	receiptID := "receipt-review-line"
	lineItemID := "line-review-line"
	if _, err := database.Exec(
		"INSERT INTO receipts (id, household_id, receipt_date, status) VALUES (?, ?, '2026-05-10', 'matched')",
		receiptID, householdID,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	if _, err := database.Exec(
		"INSERT INTO line_items (id, receipt_id, raw_name, total_price, matched) VALUES (?, ?, 'ROMAINE', '2.99', 'unmatched')",
		lineItemID, receiptID,
	); err != nil {
		t.Fatalf("insert line item: %v", err)
	}

	h := &ReceiptHandler{DB: database}
	c, rec := receiptContext(http.MethodPut, "/receipts/"+receiptID+"/line-items/"+lineItemID, `{"review_status":"accepted"}`, householdID)
	c.SetParamNames("id", "itemId")
	c.SetParamValues(receiptID, lineItemID)

	if err := h.UpdateLineItem(c); err != nil {
		t.Fatalf("UpdateLineItem: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestUpdateLineItemRecomputesCountContribution(t *testing.T) {
	database, householdID := newReceiptReviewTestDB(t)
	defer database.Close()

	receiptID := "receipt-count-update"
	lineItemID := "line-count-update"
	if _, err := database.Exec(
		"INSERT INTO receipts (id, household_id, receipt_date, status) VALUES (?, ?, '2026-05-10', 'matched')",
		receiptID, householdID,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO line_items
		    (id, receipt_id, raw_name, quantity, unit, total_price, matched, count_contribution)
		 VALUES (?, ?, 'MILK', '1', 'each', '2.99', 'unmatched', '1')`,
		lineItemID, receiptID,
	); err != nil {
		t.Fatalf("insert line item: %v", err)
	}

	h := &ReceiptHandler{DB: database}
	c, rec := receiptContext(http.MethodPut, "/receipts/"+receiptID+"/line-items/"+lineItemID, `{"quantity":"3"}`, householdID)
	c.SetParamNames("id", "itemId")
	c.SetParamValues(receiptID, lineItemID)

	if err := h.UpdateLineItem(c); err != nil {
		t.Fatalf("UpdateLineItem: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var quantity, countContribution string
	if err := database.QueryRow(
		"SELECT quantity, count_contribution FROM line_items WHERE id = ?",
		lineItemID,
	).Scan(&quantity, &countContribution); err != nil {
		t.Fatalf("query line item: %v", err)
	}
	if quantity != "3" || countContribution != "3" {
		t.Fatalf("quantity/count = %q/%q, want 3/3", quantity, countContribution)
	}
}

func TestUpdateReceiptRequiresReviewedLineItems(t *testing.T) {
	database, householdID := newReceiptReviewTestDB(t)
	defer database.Close()

	receiptID := "receipt-review-status"
	lineItemID := "line-review-status"
	productID := "product-review-status"
	if _, err := database.Exec(
		"INSERT INTO products (id, household_id, name) VALUES (?, ?, 'Romaine Lettuce')",
		productID, householdID,
	); err != nil {
		t.Fatalf("insert product: %v", err)
	}
	if _, err := database.Exec(
		"INSERT INTO receipts (id, household_id, receipt_date, status) VALUES (?, ?, '2026-05-10', 'matched')",
		receiptID, householdID,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO line_items (id, receipt_id, product_id, raw_name, total_price, matched)
		 VALUES (?, ?, ?, 'ROMAINE', '2.99', 'auto')`,
		lineItemID, receiptID, productID,
	); err != nil {
		t.Fatalf("insert line item: %v", err)
	}

	h := &ReceiptHandler{DB: database}
	c, rec := receiptContext(http.MethodPut, "/receipts/"+receiptID, `{"status":"reviewed"}`, householdID)
	c.SetParamNames("id")
	c.SetParamValues(receiptID)

	if err := h.UpdateReceipt(c); err != nil {
		t.Fatalf("UpdateReceipt pending: %v", err)
	}
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}

	if _, err := database.Exec("UPDATE line_items SET review_status = 'accepted' WHERE id = ?", lineItemID); err != nil {
		t.Fatalf("accept line item: %v", err)
	}

	c, rec = receiptContext(http.MethodPut, "/receipts/"+receiptID, `{"status":"reviewed"}`, householdID)
	c.SetParamNames("id")
	c.SetParamValues(receiptID)
	if err := h.UpdateReceipt(c); err != nil {
		t.Fatalf("UpdateReceipt accepted: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var status string
	if err := database.QueryRow("SELECT status FROM receipts WHERE id = ?", receiptID).Scan(&status); err != nil {
		t.Fatalf("query receipt status: %v", err)
	}
	if status != "reviewed" {
		t.Fatalf("receipt status = %q, want reviewed", status)
	}
}

func TestUpdateReceiptMetadataRebuildsPriceRecords(t *testing.T) {
	database, householdID := newReceiptReviewTestDB(t)
	defer database.Close()

	storeID := "store-review-metadata"
	productID := "product-review-metadata"
	receiptID := "receipt-review-metadata"
	lineItemID := "line-review-metadata"
	if _, err := database.Exec(
		"INSERT INTO stores (id, household_id, name) VALUES (?, ?, 'Costco')",
		storeID, householdID,
	); err != nil {
		t.Fatalf("insert store: %v", err)
	}
	if _, err := database.Exec(
		"INSERT INTO products (id, household_id, name) VALUES (?, ?, 'Romaine Lettuce')",
		productID, householdID,
	); err != nil {
		t.Fatalf("insert product: %v", err)
	}
	if _, err := database.Exec(
		"INSERT INTO receipts (id, household_id, store_id, receipt_date, status) VALUES (?, ?, ?, '2026-05-10', 'matched')",
		receiptID, householdID, storeID,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO line_items (id, receipt_id, product_id, raw_name, quantity, unit, total_price, matched, review_status)
		 VALUES (?, ?, ?, 'ROMAINE', '1', 'each', '2.99', 'auto', 'accepted')`,
		lineItemID, receiptID, productID,
	); err != nil {
		t.Fatalf("insert line item: %v", err)
	}

	h := &ReceiptHandler{DB: database}
	c, rec := receiptContext(
		http.MethodPut,
		"/receipts/"+receiptID,
		`{"receipt_date":"2026-05-11","receipt_time":"21:06","total":"2.99"}`,
		householdID,
	)
	c.SetParamNames("id")
	c.SetParamValues(receiptID)

	if err := h.UpdateReceipt(c); err != nil {
		t.Fatalf("UpdateReceipt metadata: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var receiptDate, receiptTime, priceDate string
	if err := database.QueryRow(
		"SELECT date(receipt_date), receipt_time FROM receipts WHERE id = ?",
		receiptID,
	).Scan(&receiptDate, &receiptTime); err != nil {
		t.Fatalf("query receipt: %v", err)
	}
	if receiptDate != "2026-05-11" {
		t.Fatalf("receipt_date = %q, want 2026-05-11", receiptDate)
	}
	if receiptTime != "21:06" {
		t.Fatalf("receipt_time = %q, want 21:06", receiptTime)
	}
	if err := database.QueryRow(
		"SELECT date(receipt_date) FROM product_prices WHERE line_item_id = ?",
		lineItemID,
	).Scan(&priceDate); err != nil {
		t.Fatalf("query product price: %v", err)
	}
	if priceDate != "2026-05-11" {
		t.Fatalf("product price date = %q, want 2026-05-11", priceDate)
	}
}

func TestCreateLineItemsBulkRecoversErrorReceipt(t *testing.T) {
	database, householdID := newReceiptReviewTestDB(t)
	defer database.Close()

	storeID := "store-bulk-items"
	productID := "product-bulk-items"
	receiptID := "receipt-bulk-items"
	if _, err := database.Exec(
		"INSERT INTO stores (id, household_id, name) VALUES (?, ?, 'Costco')",
		storeID, householdID,
	); err != nil {
		t.Fatalf("insert store: %v", err)
	}
	if _, err := database.Exec(
		"INSERT INTO products (id, household_id, name) VALUES (?, ?, 'Whole Milk')",
		productID, householdID,
	); err != nil {
		t.Fatalf("insert product: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO receipts
		    (id, household_id, store_id, receipt_date, status, error_message, items_sold_count)
		 VALUES (?, ?, ?, '2026-05-10', 'error', 'no line items', 2)`,
		receiptID, householdID, storeID,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}

	h := &ReceiptHandler{DB: database}
	body := `{"items":[` +
		`{"raw_name":" Whole Milk ","product_id":"` + productID + `","quantity":"2","unit":"each","total_price":"5.98"},` +
		`{"raw_name":"Eggs","total_price":"3.49"}` +
		`]}`
	c, rec := receiptContext(http.MethodPost, "/receipts/"+receiptID+"/line-items/bulk", body, householdID)
	c.SetParamNames("id")
	c.SetParamValues(receiptID)

	if err := h.CreateLineItems(c); err != nil {
		t.Fatalf("CreateLineItems: %v", err)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	var status string
	var errorMessage sql.NullString
	if err := database.QueryRow(
		"SELECT status, error_message FROM receipts WHERE id = ?",
		receiptID,
	).Scan(&status, &errorMessage); err != nil {
		t.Fatalf("query receipt: %v", err)
	}
	if status != "matched" {
		t.Fatalf("receipt status = %q, want matched", status)
	}
	if errorMessage.Valid {
		t.Fatalf("error_message = %q, want NULL", errorMessage.String)
	}

	var lineCount, priceCount int
	if err := database.QueryRow(
		"SELECT COUNT(*) FROM line_items WHERE receipt_id = ?",
		receiptID,
	).Scan(&lineCount); err != nil {
		t.Fatalf("query line item count: %v", err)
	}
	if lineCount != 2 {
		t.Fatalf("line items = %d, want 2", lineCount)
	}
	if err := database.QueryRow(
		"SELECT COUNT(*) FROM product_prices WHERE receipt_id = ? AND product_id = ?",
		receiptID, productID,
	).Scan(&priceCount); err != nil {
		t.Fatalf("query price count: %v", err)
	}
	if priceCount != 1 {
		t.Fatalf("product prices = %d, want 1", priceCount)
	}

	var firstLineNumber, secondLineNumber int
	var firstRawName, firstMatched, firstCount, secondMatched string
	if err := database.QueryRow(
		`SELECT raw_name, matched, line_number, count_contribution
		   FROM line_items
		  WHERE receipt_id = ? AND product_id = ?`,
		receiptID, productID,
	).Scan(&firstRawName, &firstMatched, &firstLineNumber, &firstCount); err != nil {
		t.Fatalf("query first line: %v", err)
	}
	if firstRawName != "Whole Milk" || firstMatched != "manual" || firstLineNumber != 1 || firstCount != "2" {
		t.Fatalf("first line = (%q, %q, %d, %q), want (Whole Milk, manual, 1, 2)", firstRawName, firstMatched, firstLineNumber, firstCount)
	}
	if err := database.QueryRow(
		`SELECT matched, line_number
		   FROM line_items
		  WHERE receipt_id = ? AND raw_name = 'Eggs'`,
		receiptID,
	).Scan(&secondMatched, &secondLineNumber); err != nil {
		t.Fatalf("query second line: %v", err)
	}
	if secondMatched != "unmatched" || secondLineNumber != 2 {
		t.Fatalf("second line = (%q, %d), want (unmatched, 2)", secondMatched, secondLineNumber)
	}
}

func TestCreateLineItemsBulkParsesCostcoCodeAndMatchesSecondReceiptByCode(t *testing.T) {
	database, householdID := newReceiptReviewTestDB(t)
	defer database.Close()

	storeID := "store-costco-code"
	productID := "product-costco-milk"
	firstReceiptID := "receipt-costco-code-first"
	secondReceiptID := "receipt-costco-code-second"
	if _, err := database.Exec(
		"INSERT INTO stores (id, household_id, name) VALUES (?, ?, 'Costco Wholesale')",
		storeID, householdID,
	); err != nil {
		t.Fatalf("insert store: %v", err)
	}
	if _, err := database.Exec(
		"INSERT INTO products (id, household_id, name) VALUES (?, ?, 'Costco 2% Milk 1 Gallon')",
		productID, householdID,
	); err != nil {
		t.Fatalf("insert product: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO receipts (id, household_id, store_id, receipt_date, status) VALUES
		    (?, ?, ?, '2026-05-10', 'matched'),
		    (?, ?, ?, '2026-05-11', 'matched')`,
		firstReceiptID, householdID, storeID,
		secondReceiptID, householdID, storeID,
	); err != nil {
		t.Fatalf("insert receipts: %v", err)
	}

	h := &ReceiptHandler{DB: database}
	body := `{"items":[{"raw_name":"8 2% MILK 1GAL","product_id":"` + productID + `","quantity":"1","unit":"each","total_price":"2.92"}]}`
	c, rec := receiptContext(http.MethodPost, "/receipts/"+firstReceiptID+"/line-items/bulk", body, householdID)
	c.SetParamNames("id")
	c.SetParamValues(firstReceiptID)

	if err := h.CreateLineItems(c); err != nil {
		t.Fatalf("CreateLineItems first: %v", err)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("first status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	var code, desc, matched string
	if err := database.QueryRow(
		`SELECT store_item_code, receipt_description, matched
		   FROM line_items WHERE receipt_id = ?`,
		firstReceiptID,
	).Scan(&code, &desc, &matched); err != nil {
		t.Fatalf("query first line: %v", err)
	}
	if code != "8" || desc != "2% MILK 1GAL" || matched != "manual" {
		t.Fatalf("first line = (%q, %q, %q), want (8, 2%% MILK 1GAL, manual)", code, desc, matched)
	}

	var mappedProduct, source string
	if err := database.QueryRow(
		`SELECT product_id, source FROM store_product_codes
		  WHERE store_id = ? AND store_item_code = '8'`,
		storeID,
	).Scan(&mappedProduct, &source); err != nil {
		t.Fatalf("query store code: %v", err)
	}
	if mappedProduct != productID || source != "manual" {
		t.Fatalf("store code mapping = (%q, %q), want (%q, manual)", mappedProduct, source, productID)
	}

	body = `{"items":[{"raw_name":"8 2% MILK 1GAL","total_price":"2.99"}]}`
	c, rec = receiptContext(http.MethodPost, "/receipts/"+secondReceiptID+"/line-items/bulk", body, householdID)
	c.SetParamNames("id")
	c.SetParamValues(secondReceiptID)

	if err := h.CreateLineItems(c); err != nil {
		t.Fatalf("CreateLineItems second: %v", err)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("second status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	var secondProduct string
	if err := database.QueryRow(
		`SELECT product_id, matched, store_item_code, receipt_description
		   FROM line_items WHERE receipt_id = ?`,
		secondReceiptID,
	).Scan(&secondProduct, &matched, &code, &desc); err != nil {
		t.Fatalf("query second line: %v", err)
	}
	if secondProduct != productID || matched != "code" || code != "8" || desc != "2% MILK 1GAL" {
		t.Fatalf("second line = (%q, %q, %q, %q), want (%q, code, 8, 2%% MILK 1GAL)",
			secondProduct, matched, code, desc, productID)
	}
}

func TestCreateLineItemsBulkValidatesBeforeWriting(t *testing.T) {
	database, householdID := newReceiptReviewTestDB(t)
	defer database.Close()

	receiptID := "receipt-bulk-invalid"
	if _, err := database.Exec(
		"INSERT INTO receipts (id, household_id, receipt_date, status, error_message) VALUES (?, ?, '2026-05-10', 'error', 'no line items')",
		receiptID, householdID,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}

	h := &ReceiptHandler{DB: database}
	body := `{"items":[{"raw_name":"Milk","total_price":"5.98"},{"raw_name":"Eggs","total_price":"nope"}]}`
	c, rec := receiptContext(http.MethodPost, "/receipts/"+receiptID+"/line-items/bulk", body, householdID)
	c.SetParamNames("id")
	c.SetParamValues(receiptID)

	if err := h.CreateLineItems(c); err != nil {
		t.Fatalf("CreateLineItems: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}

	var lineCount int
	var status string
	if err := database.QueryRow(
		"SELECT COUNT(*) FROM line_items WHERE receipt_id = ?",
		receiptID,
	).Scan(&lineCount); err != nil {
		t.Fatalf("query line item count: %v", err)
	}
	if lineCount != 0 {
		t.Fatalf("line items = %d, want 0", lineCount)
	}
	if err := database.QueryRow("SELECT status FROM receipts WHERE id = ?", receiptID).Scan(&status); err != nil {
		t.Fatalf("query receipt status: %v", err)
	}
	if status != "error" {
		t.Fatalf("receipt status = %q, want error", status)
	}
}
