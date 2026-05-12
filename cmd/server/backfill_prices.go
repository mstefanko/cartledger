package main

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
	"github.com/mstefanko/cartledger/internal/prices"
)

func newBackfillNormalizedPricesCmd() *cobra.Command {
	var apply bool
	var productID string
	var productGroupID string
	var sampleLimit int

	cmd := &cobra.Command{
		Use:   "backfill-normalized-prices",
		Short: "Dry-run or apply deterministic normalized price backfill",
		Long: `Backfill normalized price fields from receipt line facts and product
package sizes. The command defaults to dry-run and never uses LLM guesses.

Use --apply to write deterministic normalized prices and unique line-item links.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBackfillNormalizedPrices(apply, productID, productGroupID, sampleLimit)
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "apply deterministic updates instead of dry-run")
	cmd.Flags().StringVar(&productID, "product-id", "", "limit backfill to one product id")
	cmd.Flags().StringVar(&productGroupID, "product-group-id", "", "limit backfill to one product group id")
	cmd.Flags().IntVar(&sampleLimit, "sample-limit", 10, "number of skipped row samples to print")
	return cmd
}

func runBackfillNormalizedPrices(apply bool, productID string, productGroupID string, sampleLimit int) error {
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

	summary, err := prices.BackfillNormalizedPrices(ctx, database, prices.BackfillOptions{
		Apply:          apply,
		ProductID:      productID,
		ProductGroupID: productGroupID,
		SampleLimit:    sampleLimit,
	})
	if err != nil {
		return fmt.Errorf("backfill normalized prices: %w", err)
	}

	mode := "dry-run"
	if apply {
		mode = "apply"
	}
	fmt.Printf("normalized price backfill (%s)\n", mode)
	if productID != "" {
		fmt.Printf("  product_id:                     %s\n", productID)
	}
	if productGroupID != "" {
		fmt.Printf("  product_group_id:               %s\n", productGroupID)
	}
	fmt.Printf("  total_product_prices:           %d\n", summary.TotalRows)
	fmt.Printf("  already_normalized:             %d\n", summary.AlreadyNormalized)
	fmt.Printf("  normalized_from_receipt_unit:   %d\n", summary.ReceiptUnitNormalized)
	fmt.Printf("  normalized_from_line_override:  %d\n", summary.LineOverrideNormalized)
	fmt.Printf("  normalized_from_product_pack:   %d\n", summary.ProductPackNormalized)
	fmt.Printf("  normalized_to_group_unit:       %d\n", summary.GroupComparisonUnitNormalized)
	fmt.Printf("  skipped_missing_pack:           %d\n", summary.MissingPackSkipped)
	fmt.Printf("  skipped_ambiguous_unit:         %d\n", summary.AmbiguousUnitSkipped)
	fmt.Printf("  skipped_invalid:                %d\n", summary.InvalidSkipped)
	fmt.Printf("  line_item_links_unique:         %d\n", summary.LinkableRows)
	if apply {
		fmt.Printf("  line_item_links_written:        %d\n", summary.LinkedRows)
	}
	fmt.Printf("  line_item_links_ambiguous:      %d\n", summary.AmbiguousLinkSkipped)
	if len(summary.Samples) > 0 {
		fmt.Printf("  skipped_samples:\n")
		for _, sample := range summary.Samples {
			fmt.Printf("    - %s | %s | %s | qty=%s unit=%s price=%s | %s\n",
				sample.ProductName,
				sample.ReceiptDate,
				sample.RawLineText,
				sample.Quantity,
				sample.Unit,
				sample.Price,
				sample.Reason,
			)
		}
	}
	return nil
}
