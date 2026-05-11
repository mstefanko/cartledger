package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
	"github.com/mstefanko/cartledger/internal/matcher"
	"github.com/mstefanko/cartledger/internal/storecodes"
)

type storeCodeBackfillRow struct {
	LineItemID         string
	RawName            string
	ProductID          *string
	HouseholdID        string
	StoreID            string
	StoreName          string
	StoreItemCode      *string
	ReceiptDescription *string
	ParsedCode         string
	ParsedDescription  string
}

type storeCodeBackfillSummary struct {
	ScannedRows        int
	CostcoRows         int
	ParsedRows         int
	LineItemsUpdated   int
	MappingsWritten    int
	ConflictCount      int
	ConflictSamples    []string
	ConflictReportPath string
	Conflicts          []storeCodeBackfillConflict
	SkippedNoProduct   int
	SkippedNoParse     int
	SkippedNonCostco   int
}

type storeCodeBackfillConflict struct {
	HouseholdID    string                          `json:"household_id"`
	StoreID        string                          `json:"store_id"`
	StoreName      string                          `json:"store_name"`
	StoreItemCode  string                          `json:"store_item_code"`
	ProductIDs     []string                        `json:"product_ids"`
	LineItemCount  int                             `json:"line_item_count"`
	LineItemSample []storeCodeBackfillConflictLine `json:"line_item_sample"`
}

type storeCodeBackfillConflictLine struct {
	LineItemID         string `json:"line_item_id"`
	ProductID          string `json:"product_id"`
	RawName            string `json:"raw_name"`
	ReceiptDescription string `json:"receipt_description"`
}

func newBackfillStoreItemCodesCmd() *cobra.Command {
	var apply bool
	var sampleLimit int

	cmd := &cobra.Command{
		Use:   "backfill-store-item-codes",
		Short: "Dry-run or apply Costco store item-code backfill",
		Long: `Parse existing Costco receipt lines into store_item_code and
receipt_description. Mappings with conflicting products are reported and left
unresolved.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBackfillStoreItemCodes(apply, sampleLimit)
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "apply line-item and mapping updates instead of dry-run")
	cmd.Flags().IntVar(&sampleLimit, "sample-limit", 10, "number of conflict samples to print")
	return cmd
}

func runBackfillStoreItemCodes(apply bool, sampleLimit int) error {
	initLogger()

	cfg, err := config.LoadBase()
	if err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}
	database, err := db.Open(cfg.DBPath())
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()
	if err := db.RunMigrations(database); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	summary, err := backfillStoreItemCodes(ctx, database, apply, sampleLimit)
	if err != nil {
		return err
	}
	if len(summary.Conflicts) > 0 {
		reportPath, err := writeStoreCodeConflictReport(cfg.DataDir, summary.Conflicts, time.Now().UTC())
		if err != nil {
			return fmt.Errorf("write conflict report: %w", err)
		}
		summary.ConflictReportPath = reportPath
	}

	mode := "dry-run"
	if apply {
		mode = "apply"
	}
	fmt.Printf("store item-code backfill (%s)\n", mode)
	fmt.Printf("  scanned_rows:        %d\n", summary.ScannedRows)
	fmt.Printf("  non_costco_skipped:  %d\n", summary.SkippedNonCostco)
	fmt.Printf("  costco_rows:         %d\n", summary.CostcoRows)
	fmt.Printf("  no_parse_skipped:    %d\n", summary.SkippedNoParse)
	fmt.Printf("  parsed_rows:         %d\n", summary.ParsedRows)
	fmt.Printf("  no_product_skipped:  %d\n", summary.SkippedNoProduct)
	fmt.Printf("  conflicts:           %d\n", summary.ConflictCount)
	if summary.ConflictReportPath != "" {
		fmt.Printf("  conflict_report:     %s\n", summary.ConflictReportPath)
	}
	if apply {
		fmt.Printf("  line_items_updated:  %d\n", summary.LineItemsUpdated)
		fmt.Printf("  mappings_written:    %d\n", summary.MappingsWritten)
	}
	if len(summary.ConflictSamples) > 0 {
		fmt.Printf("  conflict_samples:\n")
		for _, sample := range summary.ConflictSamples {
			fmt.Printf("    - %s\n", sample)
		}
	}
	return nil
}

func backfillStoreItemCodes(ctx context.Context, database *sql.DB, apply bool, sampleLimit int) (storeCodeBackfillSummary, error) {
	rows, err := database.QueryContext(ctx,
		`SELECT li.id, li.raw_name, li.product_id,
		        r.household_id, r.store_id, s.name,
		        li.store_item_code, li.receipt_description
		   FROM line_items li
		   JOIN receipts r ON r.id = li.receipt_id
		   JOIN stores s ON s.id = r.store_id
		  WHERE r.store_id IS NOT NULL
		  ORDER BY s.name COLLATE NOCASE, li.id`,
	)
	if err != nil {
		return storeCodeBackfillSummary{}, fmt.Errorf("query line items: %w", err)
	}
	defer rows.Close()

	summary := storeCodeBackfillSummary{}
	parsedRows := make([]storeCodeBackfillRow, 0)
	rowsByCode := make(map[string][]storeCodeBackfillRow)

	for rows.Next() {
		var row storeCodeBackfillRow
		if err := rows.Scan(
			&row.LineItemID, &row.RawName, &row.ProductID,
			&row.HouseholdID, &row.StoreID, &row.StoreName,
			&row.StoreItemCode, &row.ReceiptDescription,
		); err != nil {
			return storeCodeBackfillSummary{}, fmt.Errorf("scan line item: %w", err)
		}
		summary.ScannedRows++
		if matcher.ClassifyStore(row.StoreName) != matcher.ChainCostco {
			summary.SkippedNonCostco++
			continue
		}
		summary.CostcoRows++
		parsed := matcher.ParseLine(row.RawName, matcher.ChainCostco)
		row.ParsedCode = storecodes.Normalize(parsed.StoreItemCode)
		row.ParsedDescription = parsed.ReceiptDescription
		if row.ParsedCode == "" {
			summary.SkippedNoParse++
			continue
		}
		summary.ParsedRows++
		parsedRows = append(parsedRows, row)
		if row.ProductID == nil || strings.TrimSpace(*row.ProductID) == "" {
			summary.SkippedNoProduct++
			continue
		}
		key := row.StoreID + "\x00" + row.ParsedCode
		rowsByCode[key] = append(rowsByCode[key], row)
	}
	if err := rows.Err(); err != nil {
		return storeCodeBackfillSummary{}, fmt.Errorf("iterate line items: %w", err)
	}
	conflicts := buildStoreCodeConflicts(rowsByCode)
	summary.Conflicts = conflicts
	summary.ConflictCount = len(conflicts)
	for _, conflict := range conflicts {
		if len(summary.ConflictSamples) >= sampleLimit {
			break
		}
		summary.ConflictSamples = append(summary.ConflictSamples,
			fmt.Sprintf("%s code %s maps to products %s", conflict.StoreName, conflict.StoreItemCode, strings.Join(conflict.ProductIDs, ", ")))
	}

	conflictKeys := make(map[string]bool, len(conflicts))
	for _, conflict := range conflicts {
		conflictKeys[conflict.StoreID+"\x00"+conflict.StoreItemCode] = true
	}

	if !apply {
		return summary, nil
	}

	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return summary, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	for _, row := range parsedRows {
		res, err := tx.ExecContext(ctx,
			`UPDATE line_items
			    SET store_item_code = CASE
			          WHEN store_item_code IS NULL OR store_item_code = '' THEN ? ELSE store_item_code END,
			        receipt_description = CASE
			          WHEN receipt_description IS NULL OR receipt_description = '' THEN ? ELSE receipt_description END
			  WHERE id = ?
			    AND ((store_item_code IS NULL OR store_item_code = '')
			      OR (receipt_description IS NULL OR receipt_description = ''))`,
			row.ParsedCode, row.ParsedDescription, row.LineItemID,
		)
		if err != nil {
			return summary, fmt.Errorf("update line item %s: %w", row.LineItemID, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			summary.LineItemsUpdated += int(n)
		}

		if row.ProductID == nil || strings.TrimSpace(*row.ProductID) == "" {
			continue
		}
		key := row.StoreID + "\x00" + row.ParsedCode
		if conflictKeys[key] {
			continue
		}
		label := row.ParsedDescription
		if label == "" {
			label = row.RawName
		}
		if err := storecodes.UpsertBackfill(ctx, tx, row.HouseholdID, row.StoreID, *row.ProductID, row.ParsedCode, &label, now); err != nil {
			return summary, fmt.Errorf("upsert store code %s: %w", row.ParsedCode, err)
		}
		summary.MappingsWritten++
	}

	if err := tx.Commit(); err != nil {
		return summary, fmt.Errorf("commit: %w", err)
	}
	return summary, nil
}

func buildStoreCodeConflicts(rowsByCode map[string][]storeCodeBackfillRow) []storeCodeBackfillConflict {
	conflicts := make([]storeCodeBackfillConflict, 0)
	for _, rows := range rowsByCode {
		productSeen := make(map[string]bool)
		productIDs := make([]string, 0)
		for _, row := range rows {
			if row.ProductID == nil || strings.TrimSpace(*row.ProductID) == "" {
				continue
			}
			productID := strings.TrimSpace(*row.ProductID)
			if !productSeen[productID] {
				productSeen[productID] = true
				productIDs = append(productIDs, productID)
			}
		}
		if len(productIDs) < 2 {
			continue
		}

		first := rows[0]
		conflict := storeCodeBackfillConflict{
			HouseholdID:   first.HouseholdID,
			StoreID:       first.StoreID,
			StoreName:     first.StoreName,
			StoreItemCode: first.ParsedCode,
			ProductIDs:    productIDs,
			LineItemCount: len(rows),
		}
		for _, row := range rows {
			if row.ProductID == nil || strings.TrimSpace(*row.ProductID) == "" {
				continue
			}
			conflict.LineItemSample = append(conflict.LineItemSample, storeCodeBackfillConflictLine{
				LineItemID:         row.LineItemID,
				ProductID:          strings.TrimSpace(*row.ProductID),
				RawName:            row.RawName,
				ReceiptDescription: row.ParsedDescription,
			})
		}
		conflicts = append(conflicts, conflict)
	}
	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].StoreName != conflicts[j].StoreName {
			return conflicts[i].StoreName < conflicts[j].StoreName
		}
		return conflicts[i].StoreItemCode < conflicts[j].StoreItemCode
	})
	return conflicts
}

func writeStoreCodeConflictReport(dataDir string, conflicts []storeCodeBackfillConflict, now time.Time) (string, error) {
	if len(conflicts) == 0 {
		return "", nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	reportDir := filepath.Join(dataDir, "backups")
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(reportDir, fmt.Sprintf("code-conflicts-%s.json", now.Format("20060102")))
	payload, err := json.MarshalIndent(conflicts, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(payload, '\n'), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
