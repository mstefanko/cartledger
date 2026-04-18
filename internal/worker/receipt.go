package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/imaging"
	"github.com/mstefanko/cartledger/internal/llm"
	"github.com/mstefanko/cartledger/internal/matcher"
	"github.com/mstefanko/cartledger/internal/units"
	"github.com/mstefanko/cartledger/internal/ws"
)

// packPattern detects multi-pack indicators like "12PK", "24 CT", "6 COUNT", "8 PACK".
var packPattern = regexp.MustCompile(`(?i)\d+\s*(PK|CT|COUNT|PACK)\b`)

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

	// Shutdown coordination.
	wg          sync.WaitGroup // tracks in-flight processJob calls
	mu          sync.Mutex     // guards accepting flag
	accepting   bool           // true when Submit is allowed
	shutdown    atomic.Bool    // true once Shutdown was called (idempotent guard)
	shutdownRes chan struct{}  // closed once Shutdown has fully completed
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
		accepting:   true,
		shutdownRes: make(chan struct{}),
	}
	for i := 0; i < concurrency; i++ {
		go w.process()
	}
	return w
}

// ErrQueueFull is returned when the worker queue cannot accept more jobs.
var ErrQueueFull = fmt.Errorf("receipt processing queue is full")

// ErrWorkerShuttingDown is returned by Submit when the worker has begun shutdown
// and no longer accepts new jobs.
var ErrWorkerShuttingDown = fmt.Errorf("receipt worker is shutting down")

// Submit enqueues a receipt job for background processing.
// Returns ErrQueueFull if the queue is at capacity, allowing the caller to return 503.
// Returns ErrWorkerShuttingDown if Shutdown has been initiated.
//
// We hold the mutex across the channel send so Shutdown's close(jobs) cannot
// race with an in-progress send. The select is non-blocking, so the critical
// section is cheap.
func (w *ReceiptWorker) Submit(job ReceiptJob) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.accepting {
		return ErrWorkerShuttingDown
	}
	select {
	case w.jobs <- job:
		return nil
	default:
		return ErrQueueFull
	}
}

// process is the main worker loop that pulls jobs from the channel and processes them.
// Each pulled job is counted against wg so Shutdown can wait for in-flight work.
func (w *ReceiptWorker) process() {
	for job := range w.jobs {
		w.wg.Add(1)
		w.runJob(job)
		w.wg.Done()
	}
}

// runJob executes a single job and handles errors. Split from process() so
// shutdown wg-accounting stays obvious at the call site.
func (w *ReceiptWorker) runJob(job ReceiptJob) {
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

// Shutdown stops accepting new submissions, waits for in-flight jobs to finish
// (up to ctx deadline), and marks any remaining queued jobs' receipts as
// status='pending' so they will be re-enqueued on next boot.
//
// Shutdown is idempotent: subsequent calls return nil immediately after waiting
// for the first call to complete.
func (w *ReceiptWorker) Shutdown(ctx context.Context) error {
	// Idempotency guard: only the first caller runs the shutdown sequence.
	// Later callers block until the first one has completed.
	if !w.shutdown.CompareAndSwap(false, true) {
		<-w.shutdownRes
		return nil
	}
	defer close(w.shutdownRes)

	// Stop accepting new submissions and close the jobs channel so process()
	// loops can exit once the channel drains. We hold w.mu across the close to
	// make sure no in-flight Submit is mid-send.
	w.mu.Lock()
	w.accepting = false
	close(w.jobs)
	w.mu.Unlock()

	// Wait for in-flight jobs (those currently being processed by runJob) to
	// finish, up to ctx deadline.
	inFlightDone := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(inFlightDone)
	}()

	var requeued int
	select {
	case <-inFlightDone:
		// All in-flight jobs finished and process() goroutines have exited
		// (they exit once w.jobs is closed AND drained). Any remaining items
		// in w.jobs would have been picked up before exit — in practice the
		// channel is empty here, but drain defensively.
		for job := range w.jobs {
			requeued++
			w.markPending(job.ReceiptID)
		}
		log.Printf("worker: shutdown complete — all in-flight jobs finished, %d buffered requeued as pending", requeued)
		return nil
	case <-ctx.Done():
		// Deadline hit. Drain whatever is still buffered and mark those
		// receipts pending for the next boot. process() goroutines may still
		// be running; we cannot interrupt a Claude HTTP call safely. Their
		// receipt rows will remain at whatever status the tx reached (usually
		// 'processing' pre-commit, which rolls back on process exit, or
		// 'matched'/'error' if the tx already committed).
		for {
			select {
			case job, ok := <-w.jobs:
				if !ok {
					// Channel closed and drained.
					log.Printf("worker: shutdown deadline exceeded — %d buffered requeued as pending; in-flight goroutines may still be running", requeued)
					return ctx.Err()
				}
				requeued++
				w.markPending(job.ReceiptID)
			default:
				log.Printf("worker: shutdown deadline exceeded — %d buffered requeued as pending; in-flight goroutines may still be running", requeued)
				return ctx.Err()
			}
		}
	}
}

// markPending sets a receipt's status back to 'pending' so the next boot can
// re-enqueue it. Errors are logged, not returned — we're on the shutdown path
// and cannot meaningfully recover.
func (w *ReceiptWorker) markPending(receiptID string) {
	_, err := w.db.Exec(
		"UPDATE receipts SET status = 'pending' WHERE id = ?",
		receiptID,
	)
	if err != nil {
		log.Printf("worker: failed to mark receipt %s pending on shutdown: %v", receiptID, err)
	}
}

func (w *ReceiptWorker) processJob(job ReceiptJob) error {
	// 1. Read image files from disk.
	entries, err := os.ReadDir(job.ImageDir)
	if err != nil {
		return fmt.Errorf("read image dir: %w", err)
	}

	var images [][]byte
	var processedPaths []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		// Skip previously processed files on rescan.
		if strings.HasPrefix(entry.Name(), "processed_") {
			continue
		}
		imgPath := filepath.Join(job.ImageDir, entry.Name())
		data, err := os.ReadFile(imgPath)
		if err != nil {
			return fmt.Errorf("read image %s: %w", entry.Name(), err)
		}

		// Preprocess: resize, grayscale, contrast, sharpen, crop.
		// Falls back to raw image on any error.
		originalSize := len(data)
		processed, _ := imaging.PreprocessReceipt(data)
		log.Printf("worker: preprocessed %s (%d KB → %d KB)", entry.Name(), originalSize/1024, len(processed)/1024)

		// Save preprocessed version alongside original.
		processedName := "processed_" + entry.Name()
		processedPath := filepath.Join(job.ImageDir, processedName)
		if err := os.WriteFile(processedPath, processed, 0644); err != nil {
			log.Printf("WARN: failed to save preprocessed image: %v", err)
			// Fall back to original path for display.
			processedPaths = append(processedPaths, filepath.Join(job.ImageDir, entry.Name()))
		} else {
			processedPaths = append(processedPaths, processedPath)
		}

		images = append(images, processed)
	}
	if len(images) == 0 {
		return fmt.Errorf("no images found in %s", job.ImageDir)
	}

	// Update image_paths to point to processed versions for frontend display.
	if len(processedPaths) > 0 {
		_, err := w.db.Exec(
			"UPDATE receipts SET image_paths = ? WHERE id = ?",
			strings.Join(processedPaths, ","), job.ReceiptID,
		)
		if err != nil {
			log.Printf("WARN: failed to update image_paths: %v", err)
		}
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

	// 5. "Each" overload detection: identify items that look like multi-packs sold as quantity=1.
	// Build a map of flagged item indices and their confidence caps.
	// Applied AFTER matching (step 6) so the matcher doesn't overwrite the cap.
	noFlagCategories := map[string]bool{
		"meat": true, "produce": true, "dairy": true, "bakery": true,
	}
	priceFlagCategories := map[string]bool{
		"beverages": true, "snacks": true, "household": true,
	}
	packOverloadCaps := make(map[int]float64) // index → max confidence

	for i := range extraction.Items {
		item := &extraction.Items[i]
		cat := strings.ToLower(item.SuggestedCategory)

		// Rule 1: Raw name matches multi-pack pattern AND quantity=1.
		if packPattern.MatchString(item.RawName) && item.Quantity == 1 {
			packOverloadCaps[i] = 0.6
			log.Printf("worker: flagged pack overload (pattern) for %q", item.RawName)
		}

		// Rule 2: unit="each" AND quantity=1 AND total_price > 8.0 AND category in flaggable set.
		if !noFlagCategories[cat] && priceFlagCategories[cat] {
			unitStr := "each"
			if item.Unit != nil {
				unitStr = *item.Unit
			}
			if strings.EqualFold(unitStr, "each") && item.Quantity == 1 && item.TotalPrice > 8.0 {
				if _, already := packOverloadCaps[i]; !already {
					packOverloadCaps[i] = 0.7
				}
				log.Printf("worker: flagged pack overload (price) for %q ($%.2f)", item.RawName, item.TotalPrice)
			}
		}
	}

	// 6. Process each extracted item.
	hasUnmatched := false
	for i, item := range extraction.Items {
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
		if matched == "unmatched" || matched == "suggested" || matched == "cross_store_match" {
			hasUnmatched = true
		}

		var productID *string
		var confidence *float64
		var suggestedProductID *string
		if matchResult.Method == "suggested" || matchResult.Method == "cross_store_match" {
			// Suggestion only — don't finalize, store as suggested_product_id.
			suggestedProductID = &matchResult.ProductID
			confidence = &matchResult.Confidence
			matched = "unmatched" // remains unmatched until user accepts
		} else if matchResult.Method != "unmatched" {
			productID = &matchResult.ProductID
			confidence = &matchResult.Confidence
		}

		// Apply "each" overload confidence cap AFTER matching (so matcher doesn't overwrite it).
		if cap, flagged := packOverloadCaps[i]; flagged && confidence != nil && *confidence > cap {
			capped := cap
			confidence = &capped
		}

		lineNum := item.LineNumber

		var suggestedName, suggestedCategory, suggestedBrand *string
		if item.SuggestedName != "" {
			suggestedName = &item.SuggestedName
		}
		if item.SuggestedCategory != "" {
			suggestedCategory = &item.SuggestedCategory
		}
		if item.SuggestedBrand != "" {
			suggestedBrand = &item.SuggestedBrand
		}

		_, err = tx.Exec(
			`INSERT INTO line_items (id, receipt_id, product_id, raw_name, quantity, unit, unit_price, total_price, regular_price, discount_amount, suggested_name, suggested_category, suggested_brand, suggested_product_id, matched, confidence, line_number, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			lineItemID, job.ReceiptID, productID, item.RawName,
			quantity.String(), item.Unit, unitPrice, totalPrice.String(),
			regularPrice, discountAmount,
			suggestedName, suggestedCategory, suggestedBrand, suggestedProductID,
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

	// 7. Update receipt status.
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

	// 8. Broadcast completion.
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
