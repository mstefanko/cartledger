package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mstefanko/cartledger/internal/config"
	enrichmentrunner "github.com/mstefanko/cartledger/internal/enrichment/runner"
)

func TestApplyLineItemBarcodeMatchesExistingProduct(t *testing.T) {
	database, householdID := newReceiptReviewTestDB(t)
	defer database.Close()

	receiptID := "receipt-barcode-match"
	lineItemID := "line-barcode-match"
	productID := "product-barcode-match"
	if _, err := database.Exec(
		"INSERT INTO products (id, household_id, name, upc) VALUES (?, ?, 'Mission Tortillas', '036000291452')",
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
		"INSERT INTO line_items (id, receipt_id, raw_name, total_price, matched) VALUES (?, ?, 'MISSION TORTILLAS', '3.99', 'unmatched')",
		lineItemID, receiptID,
	); err != nil {
		t.Fatalf("insert line item: %v", err)
	}

	h := &ReceiptHandler{DB: database, Cfg: &config.Config{DataDir: t.TempDir()}}
	c, rec := receiptContext(http.MethodPost, "/receipts/"+receiptID+"/line-items/"+lineItemID+"/barcode", `{"upc":"036000291452"}`, householdID)
	c.SetParamNames("id", "itemId")
	c.SetParamValues(receiptID, lineItemID)
	if err := h.ApplyLineItemBarcode(c); err != nil {
		t.Fatalf("ApplyLineItemBarcode: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var gotProductID, matched, upcValue string
	var confidence float64
	if err := database.QueryRow(
		"SELECT product_id, matched, upc, confidence FROM line_items WHERE id = ?",
		lineItemID,
	).Scan(&gotProductID, &matched, &upcValue, &confidence); err != nil {
		t.Fatalf("query line item: %v", err)
	}
	if gotProductID != productID || matched != "identifier" || upcValue != "036000291452" || confidence != 1 {
		t.Fatalf("line item = product:%q matched:%q upc:%q confidence:%v", gotProductID, matched, upcValue, confidence)
	}
	var identifierCount, observationCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM product_identifiers WHERE product_id = ? AND source = 'receipt_review_scan'", productID).Scan(&identifierCount); err != nil {
		t.Fatalf("count identifiers: %v", err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM line_item_identifier_observations WHERE line_item_id = ? AND source = 'receipt_review_scan'", lineItemID).Scan(&observationCount); err != nil {
		t.Fatalf("count observations: %v", err)
	}
	if identifierCount != 1 || observationCount != 1 {
		t.Fatalf("identifier/observation count = %d/%d, want 1/1", identifierCount, observationCount)
	}
}

func TestGetReceiptDoesNotLeakEnrichmentJobFromAnotherReceipt(t *testing.T) {
	database, householdID := newReceiptReviewTestDB(t)
	defer database.Close()

	productID := "product-enrichment-leak"
	receiptOneID := "receipt-enrichment-one"
	receiptTwoID := "receipt-enrichment-two"
	lineOneID := "line-enrichment-one"
	if _, err := database.Exec(
		"INSERT INTO products (id, household_id, name, upc) VALUES (?, ?, 'Mission Tortillas', '036000291452')",
		productID, householdID,
	); err != nil {
		t.Fatalf("insert product: %v", err)
	}
	for _, receiptID := range []string{receiptOneID, receiptTwoID} {
		if _, err := database.Exec(
			"INSERT INTO receipts (id, household_id, receipt_date, status) VALUES (?, ?, '2026-05-10', 'matched')",
			receiptID, householdID,
		); err != nil {
			t.Fatalf("insert receipt %s: %v", receiptID, err)
		}
	}
	if _, err := database.Exec(
		`INSERT INTO line_items (id, receipt_id, product_id, raw_name, total_price, matched)
		 VALUES (?, ?, ?, 'MISSION TORTILLAS', '3.99', 'identifier')`,
		lineOneID, receiptOneID, productID,
	); err != nil {
		t.Fatalf("insert line one: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO product_enrichment_jobs
		    (id, household_id, product_id, receipt_id, trigger, lookup_key, status, queued_at, updated_at)
		 VALUES ('job-other-receipt', ?, ?, ?, 'receipt_review_scan', 'upc:036000291452', 'succeeded', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		householdID, productID, receiptTwoID,
	); err != nil {
		t.Fatalf("insert enrichment job: %v", err)
	}

	h := &ReceiptHandler{DB: database, Cfg: &config.Config{DataDir: t.TempDir()}}
	c, rec := receiptContext(http.MethodGet, "/receipts/"+receiptOneID, "", householdID)
	c.SetParamNames("id")
	c.SetParamValues(receiptOneID)
	if err := h.Get(c); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body receiptDetailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.LineItems) != 1 {
		t.Fatalf("line item count = %d, want 1", len(body.LineItems))
	}
	if body.LineItems[0].EnrichmentJobID != nil || body.LineItems[0].EnrichmentJobStatus != nil {
		t.Fatalf("leaked enrichment job id/status = %v/%v", body.LineItems[0].EnrichmentJobID, body.LineItems[0].EnrichmentJobStatus)
	}
}

func TestApplyLineItemBarcodeIdempotentReapplyRestoresIdentifierAndAlias(t *testing.T) {
	database, householdID := newReceiptReviewTestDB(t)
	defer database.Close()

	receiptID := "receipt-barcode-idempotent"
	lineItemID := "line-barcode-idempotent"
	productID := "product-barcode-idempotent"
	if _, err := database.Exec(
		"INSERT INTO products (id, household_id, name, upc) VALUES (?, ?, 'Mission Tortillas', '036000291452')",
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
		 VALUES (?, ?, ?, 'MISSION TORTILLAS', '3.99', 'identifier')`,
		lineItemID, receiptID, productID,
	); err != nil {
		t.Fatalf("insert line item: %v", err)
	}

	h := &ReceiptHandler{DB: database}
	c, rec := receiptContext(http.MethodPost, "/receipts/"+receiptID+"/line-items/"+lineItemID+"/barcode", `{"upc":"036000291452"}`, householdID)
	c.SetParamNames("id", "itemId")
	c.SetParamValues(receiptID, lineItemID)
	if err := h.ApplyLineItemBarcode(c); err != nil {
		t.Fatalf("ApplyLineItemBarcode first: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if _, err := database.Exec("DELETE FROM product_identifiers WHERE product_id = ?", productID); err != nil {
		t.Fatalf("delete identifier: %v", err)
	}
	if _, err := database.Exec("DELETE FROM product_aliases WHERE product_id = ?", productID); err != nil {
		t.Fatalf("delete alias: %v", err)
	}

	c, rec = receiptContext(http.MethodPost, "/receipts/"+receiptID+"/line-items/"+lineItemID+"/barcode", `{"upc":"036000291452"}`, householdID)
	c.SetParamNames("id", "itemId")
	c.SetParamValues(receiptID, lineItemID)
	if err := h.ApplyLineItemBarcode(c); err != nil {
		t.Fatalf("ApplyLineItemBarcode second: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var identifierCount, aliasCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM product_identifiers WHERE product_id = ? AND source = 'receipt_review_scan'", productID).Scan(&identifierCount); err != nil {
		t.Fatalf("count identifiers: %v", err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM product_aliases WHERE product_id = ? AND source = 'receipt_review_scan'", productID).Scan(&aliasCount); err != nil {
		t.Fatalf("count aliases: %v", err)
	}
	if identifierCount != 1 || aliasCount != 1 {
		t.Fatalf("identifier/alias count = %d/%d, want 1/1", identifierCount, aliasCount)
	}
}

func TestPreviewLineItemBarcodeReportsExistingProductConflict(t *testing.T) {
	database, householdID := newReceiptReviewTestDB(t)
	defer database.Close()

	receiptID := "receipt-barcode-conflict"
	lineItemID := "line-barcode-conflict"
	productID := "product-barcode-conflict"
	if _, err := database.Exec(
		"INSERT INTO products (id, household_id, name, upc) VALUES (?, ?, 'Mission Tortillas', '036000291452')",
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
		"INSERT INTO line_items (id, receipt_id, raw_name, total_price, matched) VALUES (?, ?, 'MISSION TORTILLAS', '3.99', 'unmatched')",
		lineItemID, receiptID,
	); err != nil {
		t.Fatalf("insert line item: %v", err)
	}

	h := &ReceiptHandler{DB: database}
	c, rec := receiptContext(http.MethodPost, "/receipts/"+receiptID+"/line-items/"+lineItemID+"/barcode/preview", `{"upc":"036000291452","create_product":true}`, householdID)
	c.SetParamNames("id", "itemId")
	c.SetParamValues(receiptID, lineItemID)
	if err := h.PreviewLineItemBarcode(c); err != nil {
		t.Fatalf("PreviewLineItemBarcode: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body lineItemBarcodePreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Conflict == nil || body.Conflict.ExistingProductID != productID {
		t.Fatalf("conflict = %#v, want existing product %s", body.Conflict, productID)
	}
}

func TestApplyLineItemBarcodeCreatesProductAndSkipsLookupWhenEnvDisabled(t *testing.T) {
	database, householdID := newReceiptReviewTestDB(t)
	defer database.Close()

	receiptID := "receipt-barcode-create"
	lineItemID := "line-barcode-create"
	if _, err := database.Exec(
		"INSERT INTO receipts (id, household_id, receipt_date, status) VALUES (?, ?, '2026-05-10', 'matched')",
		receiptID, householdID,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	if _, err := database.Exec(
		"INSERT INTO line_items (id, receipt_id, raw_name, suggested_name, total_price, matched) VALUES (?, ?, 'KIRKLAND COFFEE', 'Kirkland Coffee', '12.99', 'unmatched')",
		lineItemID, receiptID,
	); err != nil {
		t.Fatalf("insert line item: %v", err)
	}

	h := &ReceiptHandler{
		DB:         database,
		Cfg:        &config.Config{ProductEnrichmentEnabled: false},
		Enrichment: enrichmentrunner.NewService(database, &config.Config{ProductEnrichmentEnabled: false}, nil),
	}
	c, rec := receiptContext(http.MethodPost, "/receipts/"+receiptID+"/line-items/"+lineItemID+"/barcode", `{"upc":"036000291452","create_product":true}`, householdID)
	c.SetParamNames("id", "itemId")
	c.SetParamValues(receiptID, lineItemID)
	if err := h.ApplyLineItemBarcode(c); err != nil {
		t.Fatalf("ApplyLineItemBarcode: %v", err)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var body lineItemBarcodeApplyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.LookupSkippedReason == nil || *body.LookupSkippedReason != "env_disabled" {
		t.Fatalf("lookup_skipped_reason = %v, want env_disabled", body.LookupSkippedReason)
	}
	var jobs int
	if err := database.QueryRow("SELECT COUNT(*) FROM product_enrichment_jobs").Scan(&jobs); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if jobs != 0 {
		t.Fatalf("jobs = %d, want 0", jobs)
	}
}

func TestApplyLineItemBarcodeCreatesProductAndSkipsLookupWhenHouseholdDisabled(t *testing.T) {
	database, householdID := newReceiptReviewTestDB(t)
	defer database.Close()

	receiptID := "receipt-barcode-household-disabled"
	lineItemID := "line-barcode-household-disabled"
	if _, err := database.Exec(
		`INSERT INTO product_enrichment_settings (household_id, manual_lookup_enabled)
		 VALUES (?, 0)
		 ON CONFLICT(household_id) DO UPDATE SET manual_lookup_enabled = 0`,
		householdID,
	); err != nil {
		t.Fatalf("disable manual lookup: %v", err)
	}
	if _, err := database.Exec(
		"INSERT INTO receipts (id, household_id, receipt_date, status) VALUES (?, ?, '2026-05-10', 'matched')",
		receiptID, householdID,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	if _, err := database.Exec(
		"INSERT INTO line_items (id, receipt_id, raw_name, suggested_name, total_price, matched) VALUES (?, ?, 'KIRKLAND COFFEE', 'Kirkland Coffee', '12.99', 'unmatched')",
		lineItemID, receiptID,
	); err != nil {
		t.Fatalf("insert line item: %v", err)
	}

	h := &ReceiptHandler{
		DB:         database,
		Cfg:        &config.Config{ProductEnrichmentEnabled: true},
		Enrichment: enrichmentrunner.NewService(database, &config.Config{ProductEnrichmentEnabled: true}, nil),
	}
	c, rec := receiptContext(http.MethodPost, "/receipts/"+receiptID+"/line-items/"+lineItemID+"/barcode", `{"upc":"036000291452","create_product":true}`, householdID)
	c.SetParamNames("id", "itemId")
	c.SetParamValues(receiptID, lineItemID)
	if err := h.ApplyLineItemBarcode(c); err != nil {
		t.Fatalf("ApplyLineItemBarcode: %v", err)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var body lineItemBarcodeApplyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.LookupSkippedReason == nil || *body.LookupSkippedReason != "household_manual_lookup_disabled" {
		t.Fatalf("lookup_skipped_reason = %v, want household_manual_lookup_disabled", body.LookupSkippedReason)
	}
}

func TestApplyLineItemBarcodeRejectsIneligibleLineItem(t *testing.T) {
	database, householdID := newReceiptReviewTestDB(t)
	defer database.Close()

	receiptID := "receipt-barcode-ineligible"
	lineItemID := "line-barcode-ineligible"
	if _, err := database.Exec(
		"INSERT INTO receipts (id, household_id, receipt_date, status) VALUES (?, ?, '2026-05-10', 'matched')",
		receiptID, householdID,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO line_items (id, receipt_id, raw_name, total_price, matched, review_status)
		 VALUES (?, ?, 'KIRKLAND COFFEE', '12.99', 'unmatched', 'accepted')`,
		lineItemID, receiptID,
	); err != nil {
		t.Fatalf("insert line item: %v", err)
	}

	h := &ReceiptHandler{DB: database}
	c, rec := receiptContext(http.MethodPost, "/receipts/"+receiptID+"/line-items/"+lineItemID+"/barcode", `{"upc":"036000291452","create_product":true}`, householdID)
	c.SetParamNames("id", "itemId")
	c.SetParamValues(receiptID, lineItemID)
	if err := h.ApplyLineItemBarcode(c); err != nil {
		t.Fatalf("ApplyLineItemBarcode: %v", err)
	}
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
}

func TestApplyLineItemBarcodeRollsBackWhenCreateProductFails(t *testing.T) {
	database, householdID := newReceiptReviewTestDB(t)
	defer database.Close()

	receiptID := "receipt-barcode-create-fail"
	lineItemID := "line-barcode-create-fail"
	if _, err := database.Exec(
		"INSERT INTO products (id, household_id, name) VALUES ('product-existing-name', ?, 'Kirkland Coffee')",
		householdID,
	); err != nil {
		t.Fatalf("insert existing product: %v", err)
	}
	if _, err := database.Exec(
		"INSERT INTO receipts (id, household_id, receipt_date, status) VALUES (?, ?, '2026-05-10', 'matched')",
		receiptID, householdID,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	if _, err := database.Exec(
		"INSERT INTO line_items (id, receipt_id, raw_name, suggested_name, total_price, matched) VALUES (?, ?, 'KIRKLAND COFFEE', 'Kirkland Coffee', '12.99', 'unmatched')",
		lineItemID, receiptID,
	); err != nil {
		t.Fatalf("insert line item: %v", err)
	}

	h := &ReceiptHandler{DB: database}
	c, rec := receiptContext(http.MethodPost, "/receipts/"+receiptID+"/line-items/"+lineItemID+"/barcode", `{"upc":"036000291452","create_product":true}`, householdID)
	c.SetParamNames("id", "itemId")
	c.SetParamValues(receiptID, lineItemID)
	if err := h.ApplyLineItemBarcode(c); err != nil {
		t.Fatalf("ApplyLineItemBarcode: %v", err)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", rec.Code, rec.Body.String())
	}
	var productID sql.NullString
	if err := database.QueryRow("SELECT product_id FROM line_items WHERE id = ?", lineItemID).Scan(&productID); err != nil {
		t.Fatalf("query line item: %v", err)
	}
	if productID.Valid {
		t.Fatalf("line product_id = %v, want nil after rollback", productID.String)
	}
	var identifiers int
	if err := database.QueryRow("SELECT COUNT(*) FROM product_identifiers WHERE normalized_value = '036000291452'").Scan(&identifiers); err != nil {
		t.Fatalf("count identifiers: %v", err)
	}
	if identifiers != 0 {
		t.Fatalf("identifiers = %d, want 0 after rollback", identifiers)
	}
}

func TestApplyLineItemBarcodeCreateReturnsSuccessWhenPostCommitQueueFails(t *testing.T) {
	database, householdID := newReceiptReviewTestDB(t)
	defer database.Close()

	otherDB, _ := newReceiptReviewTestDB(t)
	defer otherDB.Close()

	receiptID := "receipt-barcode-queue-fail"
	lineItemID := "line-barcode-queue-fail"
	if _, err := database.Exec(
		"INSERT INTO receipts (id, household_id, receipt_date, status) VALUES (?, ?, '2026-05-10', 'matched')",
		receiptID, householdID,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	if _, err := database.Exec(
		"INSERT INTO line_items (id, receipt_id, raw_name, suggested_name, total_price, matched) VALUES (?, ?, 'KIRKLAND COFFEE', 'Kirkland Coffee', '12.99', 'unmatched')",
		lineItemID, receiptID,
	); err != nil {
		t.Fatalf("insert line item: %v", err)
	}

	cfg := &config.Config{ProductEnrichmentEnabled: true}
	h := &ReceiptHandler{
		DB:         database,
		Cfg:        cfg,
		Enrichment: enrichmentrunner.NewService(otherDB, cfg, nil),
	}
	c, rec := receiptContext(http.MethodPost, "/receipts/"+receiptID+"/line-items/"+lineItemID+"/barcode", `{"upc":"036000291452","create_product":true}`, householdID)
	c.SetParamNames("id", "itemId")
	c.SetParamValues(receiptID, lineItemID)
	if err := h.ApplyLineItemBarcode(c); err != nil {
		t.Fatalf("ApplyLineItemBarcode: %v", err)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var body lineItemBarcodeApplyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.LookupSkippedReason == nil || *body.LookupSkippedReason != "queue_failed" {
		t.Fatalf("lookup_skipped_reason = %v, want queue_failed", body.LookupSkippedReason)
	}
	var gotProductID string
	if err := database.QueryRow("SELECT product_id FROM line_items WHERE id = ?", lineItemID).Scan(&gotProductID); err != nil {
		t.Fatalf("query line item: %v", err)
	}
	if gotProductID == "" || gotProductID != body.ProductID {
		t.Fatalf("line product_id = %q, response product_id = %q", gotProductID, body.ProductID)
	}
}
