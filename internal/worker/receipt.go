package worker

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/llm"
	"github.com/mstefanko/cartledger/internal/matcher"
	"github.com/mstefanko/cartledger/internal/units"
	"github.com/mstefanko/cartledger/internal/ws"
)

// ReceiptJob represents a receipt processing job submitted to the worker pool.
type ReceiptJob struct {
	ReceiptID   string
	HouseholdID string
	ImageDir    string // directory containing receipt images
}

// ReceiptWorker manages a pool of goroutines that process receipt images.
type ReceiptWorker struct {
	jobs        chan ReceiptJob
	llmClient   llm.Client
	matchEngine *matcher.Engine
	db          *sql.DB
	hub         *ws.Hub
	cfg         *config.Config
}

// NewReceiptWorker creates a new ReceiptWorker and starts the goroutine pool.
func NewReceiptWorker(concurrency int, llmClient llm.Client, matchEngine *matcher.Engine, db *sql.DB, hub *ws.Hub, cfg *config.Config) *ReceiptWorker {
	w := &ReceiptWorker{
		jobs:        make(chan ReceiptJob, 100),
		llmClient:   llmClient,
		matchEngine: matchEngine,
		db:          db,
		hub:         hub,
		cfg:         cfg,
	}
	for i := 0; i < concurrency; i++ {
		go w.process()
	}
	return w
}

// ErrQueueFull is returned when the worker queue cannot accept more jobs.
var ErrQueueFull = fmt.Errorf("receipt processing queue is full")

// Submit enqueues a receipt job for background processing.
// Returns ErrQueueFull if the queue is at capacity, allowing the caller to return 503.
func (w *ReceiptWorker) Submit(job ReceiptJob) error {
	select {
	case w.jobs <- job:
		return nil
	default:
		return ErrQueueFull
	}
}

// process is the main worker loop that pulls jobs from the channel and processes them.
func (w *ReceiptWorker) process() {
	for job := range w.jobs {
		if err := w.processJob(job); err != nil {
			log.Printf("worker: failed to process receipt %s: %v", job.ReceiptID, err)
			// Update receipt status to error.
			_, _ = w.db.Exec(
				"UPDATE receipts SET status = 'error' WHERE id = ?",
				job.ReceiptID,
			)
			w.hub.Broadcast(ws.Message{
				Type:      ws.EventReceiptComplete,
				Household: job.HouseholdID,
				Payload: map[string]interface{}{
					"receipt_id": job.ReceiptID,
					"status":     "error",
					"error":      err.Error(),
				},
			})
		}
	}
}

func (w *ReceiptWorker) processJob(job ReceiptJob) error {
	// 1. Read image files from disk.
	entries, err := os.ReadDir(job.ImageDir)
	if err != nil {
		return fmt.Errorf("read image dir: %w", err)
	}

	var images [][]byte
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(job.ImageDir, entry.Name()))
		if err != nil {
			return fmt.Errorf("read image %s: %w", entry.Name(), err)
		}
		images = append(images, data)
	}
	if len(images) == 0 {
		return fmt.Errorf("no images found in %s", job.ImageDir)
	}

	// Broadcast processing status.
	w.hub.Broadcast(ws.Message{
		Type:      ws.EventReceiptProcessing,
		Household: job.HouseholdID,
		Payload:   map[string]interface{}{"receipt_id": job.ReceiptID},
	})

	// 2. Send to LLM vision API.
	log.Printf("worker: calling LLM for receipt %s (%d images, provider=%s)", job.ReceiptID, len(images), w.llmClient.Provider())
	extraction, err := w.llmClient.ExtractReceipt(images)
	if err != nil {
		return fmt.Errorf("llm extraction: %w", err)
	}
	log.Printf("worker: LLM returned for receipt %s (store=%s, items=%d)", job.ReceiptID, extraction.StoreName, len(extraction.Items))

	// 3. Store raw_llm_json on the receipt.
	rawJSON, err := json.Marshal(extraction)
	if err != nil {
		return fmt.Errorf("marshal extraction: %w", err)
	}

	now := time.Now().UTC()

	tx, err := w.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Parse receipt date.
	receiptDate, err := time.Parse("2006-01-02", extraction.Date)
	if err != nil {
		receiptDate = now
	}

	subtotal := decimal.NewFromFloat(extraction.Subtotal)
	tax := decimal.NewFromFloat(extraction.Tax)
	total := decimal.NewFromFloat(extraction.Total)
	rawJSONStr := string(rawJSON)
	provider := w.llmClient.Provider()

	// 4. Find-or-create store.
	var storeID string
	if extraction.StoreName != "" {
		// Phase 1: Exact name match (existing behavior)
		err = tx.QueryRow(
			"SELECT id FROM stores WHERE household_id = ? AND LOWER(name) = LOWER(?)",
			job.HouseholdID, extraction.StoreName,
		).Scan(&storeID)

		// Phase 2: Store number + name prefix match
		if err == sql.ErrNoRows && extraction.StoreNumber != nil {
			if fields := strings.Fields(extraction.StoreName); len(fields) > 0 {
				err = tx.QueryRow(
					`SELECT id FROM stores WHERE household_id = ? AND store_number = ? AND LOWER(name) LIKE LOWER(? || '%')`,
					job.HouseholdID, *extraction.StoreNumber, fields[0],
				).Scan(&storeID)
			}
		}

		if err == sql.ErrNoRows {
			storeID = uuid.New().String()
			log.Printf("worker: creating new store %q (id=%s)", extraction.StoreName, storeID)
			_, err = tx.Exec(
				`INSERT INTO stores (id, household_id, name, address, city, state, zip, store_number, created_at, updated_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				storeID, job.HouseholdID, extraction.StoreName,
				nilPtrStr(extraction.StoreAddress), nilPtrStr(extraction.StoreCity),
				nilPtrStr(extraction.StoreState), nilPtrStr(extraction.StoreZip),
				nilPtrStr(extraction.StoreNumber), now, now,
			)
			if err != nil {
				return fmt.Errorf("create store: %w", err)
			}
			log.Printf("worker: store created successfully")
		} else if err != nil {
			return fmt.Errorf("lookup store: %w", err)
		} else {
			// Progressive enrichment: fill NULL fields on existing store
			_, _ = tx.Exec(`UPDATE stores SET
				address = COALESCE(address, ?),
				city = COALESCE(city, ?),
				state = COALESCE(state, ?),
				zip = COALESCE(zip, ?),
				store_number = COALESCE(store_number, ?),
				updated_at = ?
				WHERE id = ?`,
				nilPtrStr(extraction.StoreAddress), nilPtrStr(extraction.StoreCity),
				nilPtrStr(extraction.StoreState), nilPtrStr(extraction.StoreZip),
				nilPtrStr(extraction.StoreNumber), now, storeID)
		}
	}

	// Update receipt with extraction data.
	log.Printf("worker: updating receipt %s with extraction data", job.ReceiptID)
	_, err = tx.Exec(
		`UPDATE receipts SET store_id = ?, receipt_date = ?, receipt_time = ?,
		 subtotal = ?, tax = ?, total = ?,
		 card_type = ?, card_last4 = ?,
		 raw_llm_json = ?, llm_provider = ?, status = 'processing'
		 WHERE id = ?`,
		nilIfEmpty(storeID), receiptDate, extraction.Time,
		subtotal.String(), tax.String(), total.String(),
		extraction.PaymentCardType, extraction.PaymentCardLast4,
		rawJSONStr, provider, job.ReceiptID,
	)
	if err != nil {
		return fmt.Errorf("update receipt: %w", err)
	}

	// 5. Process each extracted item.
	hasUnmatched := false
	for _, item := range extraction.Items {
		lineItemID := uuid.New().String()
		quantity := decimal.NewFromFloat(item.Quantity)
		if quantity.IsZero() {
			quantity = decimal.NewFromInt(1)
		}
		totalPrice := decimal.NewFromFloat(item.TotalPrice)

		var unitPrice *string
		if item.UnitPrice != nil {
			up := decimal.NewFromFloat(*item.UnitPrice).String()
			unitPrice = &up
		}

		var regularPrice, discountAmount *string
		if item.RegularPrice != nil {
			rp := decimal.NewFromFloat(*item.RegularPrice).String()
			regularPrice = &rp
		}
		if item.DiscountAmount != nil {
			da := decimal.NewFromFloat(*item.DiscountAmount).String()
			discountAmount = &da
		}

		// Run matcher with suggested-name fallback.
		matchResult := w.matchEngine.MatchWithSuggestion(item.RawName, item.SuggestedName, storeID, job.HouseholdID)

		matched := matchResult.Method
		if matched == "unmatched" || matched == "suggested" {
			hasUnmatched = true
		}

		var productID *string
		var confidence *float64
		var suggestedProductID *string
		if matchResult.Method == "suggested" {
			// Suggestion only — don't finalize, store as suggested_product_id.
			suggestedProductID = &matchResult.ProductID
			confidence = &matchResult.Confidence
			matched = "unmatched" // remains unmatched until user accepts
		} else if matchResult.Method != "unmatched" {
			productID = &matchResult.ProductID
			confidence = &matchResult.Confidence
		}

		lineNum := item.LineNumber

		var suggestedName, suggestedCategory *string
		if item.SuggestedName != "" {
			suggestedName = &item.SuggestedName
		}
		if item.SuggestedCategory != "" {
			suggestedCategory = &item.SuggestedCategory
		}

		_, err = tx.Exec(
			`INSERT INTO line_items (id, receipt_id, product_id, raw_name, quantity, unit, unit_price, total_price, regular_price, discount_amount, suggested_name, suggested_category, suggested_product_id, matched, confidence, line_number, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			lineItemID, job.ReceiptID, productID, item.RawName,
			quantity.String(), item.Unit, unitPrice, totalPrice.String(),
			regularPrice, discountAmount,
			suggestedName, suggestedCategory, suggestedProductID,
			matched, confidence, lineNum, now,
		)
		if err != nil {
			return fmt.Errorf("insert line item: %w", err)
		}

		// If matched: create product_alias (if new) and product_prices entry.
		if productID != nil && storeID != "" {
			normalized := matcher.Normalize(item.RawName)

			// Create alias if it doesn't already exist.
			var aliasExists int
			err = tx.QueryRow(
				"SELECT COUNT(*) FROM product_aliases WHERE product_id = ? AND alias = ?",
				*productID, normalized,
			).Scan(&aliasExists)
			if err != nil {
				return fmt.Errorf("check alias: %w", err)
			}
			if aliasExists == 0 {
				_, err = tx.Exec(
					"INSERT INTO product_aliases (id, product_id, alias, store_id, created_at) VALUES (?, ?, ?, ?, ?)",
					uuid.New().String(), *productID, normalized, storeID, now,
				)
				if err != nil {
					return fmt.Errorf("insert alias: %w", err)
				}
			}

			// Insert product_prices entry.
			unit := "each"
			if item.Unit != nil {
				unit = *item.Unit
			}
			up := totalPrice.Div(quantity)

			isSale := regularPrice != nil && discountAmount != nil
			priceID := uuid.New().String()
			_, err = tx.Exec(
				`INSERT INTO product_prices (id, product_id, store_id, receipt_id, receipt_date, quantity, unit, unit_price, regular_price, discount_amount, is_sale, created_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				priceID, *productID, storeID, job.ReceiptID,
				receiptDate, quantity.String(), unit, up.String(),
				regularPrice, discountAmount, isSale, now,
			)
			if err != nil {
				return fmt.Errorf("insert product price: %w", err)
			}

			// Normalize price to standard unit (per oz, per fl_oz, per each).
			normalizedPrice, normalizedUnit, normErr := units.NormalizePrice(totalPrice, quantity, unit, *productID, tx)
			if normErr == nil {
				_, _ = tx.Exec(
					`UPDATE product_prices SET normalized_price = ?, normalized_unit = ? WHERE id = ?`,
					normalizedPrice.String(), normalizedUnit, priceID,
				)
			}

			// Update product purchase stats.
			_, err = tx.Exec(
				"UPDATE products SET last_purchased_at = ?, purchase_count = purchase_count + 1, updated_at = ? WHERE id = ?",
				receiptDate, now, *productID,
			)
			if err != nil {
				return fmt.Errorf("update product stats: %w", err)
			}
		}
	}

	// 6. Update receipt status.
	// "matched" = all items matched to products, "review" = some items need user attention.
	// Never set back to "pending" — that means LLM hasn't processed yet.
	finalStatus := "matched"
	if hasUnmatched {
		finalStatus = "matched" // extraction complete; unmatched items get suggestions in the UI
	}
	_, err = tx.Exec("UPDATE receipts SET status = ? WHERE id = ?", finalStatus, job.ReceiptID)
	if err != nil {
		return fmt.Errorf("update receipt status: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	// 7. Broadcast completion.
	w.hub.Broadcast(ws.Message{
		Type:      ws.EventReceiptComplete,
		Household: job.HouseholdID,
		Payload: map[string]interface{}{
			"receipt_id": job.ReceiptID,
			"status":     finalStatus,
		},
	})

	return nil
}

// nilIfEmpty returns nil for empty strings, otherwise a pointer to the string.
func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// nilPtrStr dereferences a *string, returning nil if the pointer is nil.
// Used to pass *string values from LLM extraction directly to SQL parameters.
func nilPtrStr(s *string) interface{} {
	if s == nil {
		return nil
	}
	return *s
}
