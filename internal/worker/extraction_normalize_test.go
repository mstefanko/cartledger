package worker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mstefanko/cartledger/internal/db"
	"github.com/mstefanko/cartledger/internal/llm"
	"github.com/mstefanko/cartledger/internal/matcher"
	"github.com/mstefanko/cartledger/internal/ws"
)

func TestNormalizeExtractedItems_AttachesCostcoCouponToReferencedItem(t *testing.T) {
	items := []llm.ExtractedItem{
		{RawName: "1005641 CHOBANI YGRT", SuggestedName: "Chobani Yogurt", Quantity: 1, Unit: strPtr("each"), TotalPrice: 14.99, LineNumber: 3, Confidence: 0.95},
		{RawName: "000343232/1005641", SuggestedName: "Discount/Coupon", Quantity: 1, Unit: strPtr("each"), TotalPrice: -5, LineNumber: 4, Confidence: 0.90},
	}

	got := NormalizeExtractedItems(items)
	if len(got) != 1 {
		t.Fatalf("len(normalized) = %d, want 1", len(got))
	}
	if got[0].RegularPrice == nil || *got[0].RegularPrice != 14.99 {
		t.Fatalf("regular_price = %v, want 14.99", got[0].RegularPrice)
	}
	if got[0].DiscountAmount == nil || *got[0].DiscountAmount != 5 {
		t.Fatalf("discount_amount = %v, want 5", got[0].DiscountAmount)
	}
	if !nearlyEqual(got[0].TotalPrice, 9.99) {
		t.Fatalf("total_price = %v, want 9.99", got[0].TotalPrice)
	}
}

func TestNormalizeExtractedItems_MergesAdjacentCountableDuplicates(t *testing.T) {
	items := []llm.ExtractedItem{
		{RawName: "47826 GREEN GRAPES", SuggestedName: "Green Grapes", Quantity: 1, Unit: strPtr("each"), TotalPrice: 6.39, LineNumber: 10, Confidence: 0.95},
		{RawName: "47826 GREEN GRAPES", SuggestedName: "Green Grapes", Quantity: 1, Unit: strPtr("each"), TotalPrice: 6.39, LineNumber: 11, Confidence: 0.95},
	}

	got := NormalizeExtractedItems(items)
	if len(got) != 1 {
		t.Fatalf("len(normalized) = %d, want 1", len(got))
	}
	if got[0].Quantity != 2 {
		t.Fatalf("quantity = %v, want 2", got[0].Quantity)
	}
	if !nearlyEqual(got[0].TotalPrice, 12.78) {
		t.Fatalf("total_price = %v, want 12.78", got[0].TotalPrice)
	}
	if got[0].CountContribution != 2 {
		t.Fatalf("count_contribution = %v, want 2", got[0].CountContribution)
	}
}

func TestNormalizeExtractedItems_DoesNotMergeWeightedRows(t *testing.T) {
	items := []llm.ExtractedItem{
		{RawName: "22967 ORGNC BS THG", Quantity: 1.24, Unit: strPtr("lb"), TotalPrice: 12.40, LineNumber: 1, Confidence: 0.95},
		{RawName: "22967 ORGNC BS THG", Quantity: 1.31, Unit: strPtr("lb"), TotalPrice: 13.10, LineNumber: 2, Confidence: 0.95},
	}

	got := NormalizeExtractedItems(items)
	if len(got) != 2 {
		t.Fatalf("len(normalized) = %d, want 2", len(got))
	}
}

func TestNormalizeExtractedItemsForStore_CostcoCodeAndQuantityGuard(t *testing.T) {
	items := []llm.ExtractedItem{
		{RawName: "8 2% MILK 1GAL", Quantity: 8, Unit: strPtr("each"), TotalPrice: 2.92, CountContribution: 8},
	}

	got := NormalizeExtractedItemsForStore(items, matcher.ChainCostco)
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].StoreItemCode == nil || *got[0].StoreItemCode != "8" {
		t.Fatalf("StoreItemCode = %v, want 8", got[0].StoreItemCode)
	}
	if got[0].ReceiptDescription == nil || *got[0].ReceiptDescription != "2% MILK 1GAL" {
		t.Fatalf("ReceiptDescription = %v, want 2%% MILK 1GAL", got[0].ReceiptDescription)
	}
	if got[0].Quantity != 1 {
		t.Fatalf("Quantity = %v, want 1", got[0].Quantity)
	}
	if got[0].CountContribution != 1 {
		t.Fatalf("CountContribution = %v, want 1", got[0].CountContribution)
	}
}

func TestNormalizeExtractedItemsForStore_PreservesExplicitCostcoQuantity(t *testing.T) {
	unitPrice := 2.92
	tests := []llm.ExtractedItem{
		{RawName: "8 2% MILK 1GAL", Quantity: 2, UnitPrice: &unitPrice, TotalPrice: 5.84},
		{RawName: "123456 2 X MUFFINS", Quantity: 2, TotalPrice: 12.99},
		{RawName: "123456 KS WATER 2x8", Quantity: 5, TotalPrice: 19.95},
		{RawName: "123456 BAGELS @ 5.99", Quantity: 2, TotalPrice: 11.98},
	}
	for _, item := range tests {
		got := NormalizeExtractedItemsForStore([]llm.ExtractedItem{item}, matcher.ChainCostco)
		if got[0].Quantity != item.Quantity {
			t.Fatalf("Quantity for %q = %v, want %v", item.RawName, got[0].Quantity, item.Quantity)
		}
	}
}

func TestNormalizeExtractedItemsForStore_DoesNotParseNonCostcoLeadingDigits(t *testing.T) {
	items := []llm.ExtractedItem{
		{RawName: "3 LBS BANANAS", Quantity: 3, Unit: strPtr("each"), TotalPrice: 4.50, CountContribution: 3},
	}

	got := NormalizeExtractedItemsForStore(items, matcher.ChainOther)
	if got[0].StoreItemCode != nil || got[0].ReceiptDescription != nil {
		t.Fatalf("store fields = %v/%v, want nil/nil", got[0].StoreItemCode, got[0].ReceiptDescription)
	}
	if got[0].Quantity != 3 || got[0].CountContribution != 3 {
		t.Fatalf("quantity/count = %v/%v, want 3/3", got[0].Quantity, got[0].CountContribution)
	}
}

func TestNormalizeExtractedItemsForStore_DoesNotOverwriteLLMFields(t *testing.T) {
	items := []llm.ExtractedItem{
		{
			RawName:            "8 2% MILK 1GAL",
			StoreItemCode:      strPtr("0008"),
			ReceiptDescription: strPtr("MILK"),
			Quantity:           1,
			TotalPrice:         2.92,
		},
	}

	got := NormalizeExtractedItemsForStore(items, matcher.ChainCostco)
	if got[0].StoreItemCode == nil || *got[0].StoreItemCode != "0008" {
		t.Fatalf("StoreItemCode = %v, want 0008", got[0].StoreItemCode)
	}
	if got[0].ReceiptDescription == nil || *got[0].ReceiptDescription != "MILK" {
		t.Fatalf("ReceiptDescription = %v, want MILK", got[0].ReceiptDescription)
	}
}

func TestPackageContentForItemConfidenceSources(t *testing.T) {
	got, confidence, source, ok := packageContentForItem(llm.ExtractedItem{
		RawName:         "2% MILK 1GAL",
		PackageLabel:    strPtr("1GAL"),
		PackageQuantity: strPtr("1"),
		PackageUnit:     strPtr("gal"),
	})
	if !ok || got.Quantity.String() != "1" || got.Unit != "gal" || source != "receipt_explicit" || confidence != receiptPackageLLMAgreementConfidence {
		t.Fatalf("agreement package = %+v confidence=%v source=%q ok=%v", got, confidence, source, ok)
	}

	_, confidence, source, ok = packageContentForItem(llm.ExtractedItem{
		RawName:         "MILK",
		PackageQuantity: strPtr("1"),
		PackageUnit:     strPtr("gal"),
	})
	if !ok || source != "receipt_llm" || confidence != receiptPackageLLMOnlyConfidence {
		t.Fatalf("llm-only package confidence=%v source=%q ok=%v", confidence, source, ok)
	}

	_, confidence, source, ok = packageContentForItem(llm.ExtractedItem{
		RawName: "TORTILLAS 12 OZ",
	})
	if !ok || source != "receipt_explicit" || confidence != receiptPackageDeterministicConfidence {
		t.Fatalf("deterministic package confidence=%v source=%q ok=%v", confidence, source, ok)
	}
}

func TestProductFieldEditBlocksReceiptPackageSuggestion(t *testing.T) {
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer database.Close()
	if err := db.RunMigrations(database); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	var householdID string
	if err := database.QueryRow("INSERT INTO households (name) VALUES ('Test') RETURNING id").Scan(&householdID); err != nil {
		t.Fatalf("insert household: %v", err)
	}
	storeID := "store-field-edit"
	productID := "product-field-edit"
	if _, err := database.Exec("INSERT INTO stores (id, household_id, name) VALUES (?, ?, 'Costco Wholesale')", storeID, householdID); err != nil {
		t.Fatalf("insert store: %v", err)
	}
	if _, err := database.Exec("INSERT INTO products (id, household_id, name) VALUES (?, ?, 'Milk')", productID, householdID); err != nil {
		t.Fatalf("insert product: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO store_product_codes
		    (id, household_id, store_id, product_id, store_item_code, label, source)
		 VALUES ('spc-field-edit', ?, ?, ?, '8', '2% MILK 1GAL', 'manual')`,
		householdID, storeID, productID,
	); err != nil {
		t.Fatalf("insert store code: %v", err)
	}
	receiptID := "receipt-field-edit"
	if _, err := database.Exec(
		"INSERT INTO receipts (id, household_id, receipt_date, total, status) VALUES (?, ?, '2026-05-08', '2.92', 'processing')",
		receiptID, householdID,
	); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO product_field_edits (product_id, field, edit_source, edited_at)
		 VALUES (?, 'pack_quantity', 'manual', '2026-05-09 12:00:00'),
		        (?, 'pack_unit', 'manual', '2026-05-09 12:00:00')`,
		productID, productID,
	); err != nil {
		t.Fatalf("insert field edits: %v", err)
	}

	imageDir := filepath.Join(dir, "receipts", receiptID)
	if err := os.MkdirAll(imageDir, 0o755); err != nil {
		t.Fatalf("mkdir image dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(imageDir, "1.jpg"), []byte("fake image"), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}

	w := &ReceiptWorker{
		llmClient: staticLLM{extraction: &llm.ReceiptExtraction{
			StoreName:  "Costco Wholesale",
			Date:       "2026-05-08",
			Items:      []llm.ExtractedItem{{RawName: "8 2% MILK 1GAL", StoreItemCode: strPtr("8"), SuggestedName: "2% Milk", SuggestedCategory: "Dairy", Quantity: 1, Unit: strPtr("each"), TotalPrice: 2.92, LineNumber: 1, Confidence: 0.95}},
			Subtotal:   2.92,
			Total:      2.92,
			Confidence: 0.95,
		}},
		matchEngine: matcher.NewEngine(database),
		db:          database,
		hub:         ws.NewHub(),
	}
	if err := w.processJob(ReceiptJob{ReceiptID: receiptID, HouseholdID: householdID, ImageDir: imageDir}); err != nil {
		t.Fatalf("processJob: %v", err)
	}

	var suggestionCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM product_enrichment_suggestions WHERE product_id = ?", productID).Scan(&suggestionCount); err != nil {
		t.Fatalf("count suggestions: %v", err)
	}
	if suggestionCount != 0 {
		t.Fatalf("suggestionCount = %d, want 0 because later manual field edit blocks receipt evidence", suggestionCount)
	}

	var overrideSource string
	if err := database.QueryRow("SELECT pack_override_source FROM line_items WHERE receipt_id = ?", receiptID).Scan(&overrideSource); err != nil {
		t.Fatalf("query line override: %v", err)
	}
	if overrideSource != "receipt_explicit" {
		t.Fatalf("overrideSource = %q, want receipt_explicit", overrideSource)
	}
}

func TestNormalizeExtractedPayment_UsesMaskedLast4NearCardBrand(t *testing.T) {
	extraction := &llm.ReceiptExtraction{
		PaymentCardType:  strPtr("Visa"),
		PaymentCardLast4: strPtr("0618"),
		PaymentCardRaw: strPtr(`XXXXXXXXXXXX0388       H
AID: A0000000031010
Seq# 7378     App#: 08257D
Visa    Resp: APPROVED
Tran ID#: 507000007378`),
	}

	NormalizeExtractedPayment(extraction)

	if extraction.PaymentCardType == nil || *extraction.PaymentCardType != "Visa" {
		t.Fatalf("card type = %v, want Visa", extraction.PaymentCardType)
	}
	if extraction.PaymentCardLast4 == nil || *extraction.PaymentCardLast4 != "0388" {
		t.Fatalf("card last4 = %v, want 0388", extraction.PaymentCardLast4)
	}
}

func TestNormalizeExtractedPayment_ClearsUnverifiedLast4ButKeepsBrand(t *testing.T) {
	extraction := &llm.ReceiptExtraction{
		PaymentCardType:  strPtr("Visa"),
		PaymentCardLast4: strPtr("0618"),
		PaymentCardRaw: strPtr(`AID: A0000000031010
Seq# 7378     App#: 08257D
Visa    Resp: APPROVED
Tran ID#: 507000007378`),
	}

	NormalizeExtractedPayment(extraction)

	if extraction.PaymentCardType == nil || *extraction.PaymentCardType != "Visa" {
		t.Fatalf("card type = %v, want Visa", extraction.PaymentCardType)
	}
	if extraction.PaymentCardLast4 != nil {
		t.Fatalf("card last4 = %v, want nil", extraction.PaymentCardLast4)
	}
}

func TestNormalizeExtractedPayment_ClearsCashLast4(t *testing.T) {
	extraction := &llm.ReceiptExtraction{
		PaymentCardType:  strPtr("cash"),
		PaymentCardLast4: strPtr("1234"),
	}

	NormalizeExtractedPayment(extraction)

	if extraction.PaymentCardType == nil || *extraction.PaymentCardType != "Cash" {
		t.Fatalf("card type = %v, want Cash", extraction.PaymentCardType)
	}
	if extraction.PaymentCardLast4 != nil {
		t.Fatalf("card last4 = %v, want nil", extraction.PaymentCardLast4)
	}
}

func TestNormalizeExtractedPayment_PrefersNetworkVisibleInRawOverGenericTender(t *testing.T) {
	extraction := &llm.ReceiptExtraction{
		PaymentCardType: strPtr("Debit"),
		PaymentCardRaw:  strPtr("Visa Resp: APPROVED"),
	}

	NormalizeExtractedPayment(extraction)

	if extraction.PaymentCardType == nil || *extraction.PaymentCardType != "Visa" {
		t.Fatalf("card type = %v, want Visa", extraction.PaymentCardType)
	}
}

func strPtr(s string) *string {
	return &s
}
