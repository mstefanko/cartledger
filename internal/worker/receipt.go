package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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

// backfillMinConfidence is the minimum item-level LLM confidence required
// before a suggested_brand / suggested_category can promote into a product's
// canonical brand/category via BackfillProductMetadata. Below this, the
// suggestion is too weak to write into user-visible data.
const backfillMinConfidence = 0.5

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
	guard       *llm.GuardedExtractor // wraps llmClient with budget + circuit breaker
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
// guard is optional — when non-nil, the worker calls guard.ExtractForHousehold
// (which enforces budget + circuit breaker) instead of llmClient.ExtractReceipt.
// Passing nil keeps pre-guard behavior (used by existing tests).
func NewReceiptWorker(concurrency int, llmClient llm.Client, guard *llm.GuardedExtractor, matchEngine *matcher.Engine, db *sql.DB, hub *ws.Hub, cfg *config.Config) *ReceiptWorker {
	w := &ReceiptWorker{
		jobs:        make(chan ReceiptJob, 100),
		llmClient:   llmClient,
		guard:       guard,
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

// QueueDepth returns the current number of jobs buffered in the worker
// channel. It is a best-effort snapshot safe to call concurrently — the
// value can change between read and use. Exposed for operational metrics
// (see internal/api/metrics.go cartledger_worker_queue_depth gauge).
func (w *ReceiptWorker) QueueDepth() int {
	return len(w.jobs)
}

// ErrQueueFull is returned when the worker queue cannot accept more jobs.
var ErrQueueFull = fmt.Errorf("receipt processing queue is full")

// ErrWorkerShuttingDown is returned by Submit when the worker has begun shutdown
// and no longer accepts new jobs.
var ErrWorkerShuttingDown = fmt.Errorf("receipt worker is shutting down")

// ErrImagesGone is returned by Resubmit when the receipt's on-disk image
// directory no longer exists (e.g. retention policy deleted it). The caller
// should surface this as 410 Gone rather than 500, because the situation is
// deterministic and user-actionable (re-upload the receipt).
var ErrImagesGone = fmt.Errorf("receipt images no longer on disk")

// Resubmit re-enqueues an existing receipt for background processing. Unlike
// Submit, this is for user-initiated retries: it reconstructs the ReceiptJob
// by locating the receipt's image directory under <DataDir>/receipts/<id>
// (the canonical layout established in the Scan handler).
//
// Returns:
//   - ErrImagesGone if the image directory is missing.
//   - ErrQueueFull / ErrWorkerShuttingDown propagated from Submit.
//
// The caller (API handler) is responsible for flipping status='pending' and
// clearing error_message before calling Resubmit; that ordering lets us fail
// fast (no DB mutation) if the queue is closed or the images are gone.
func (w *ReceiptWorker) Resubmit(receiptID, householdID string) error {
	imageDir := filepath.Join(w.cfg.DataDir, "receipts", receiptID)
	info, err := os.Stat(imageDir)
	if err != nil || !info.IsDir() {
		return ErrImagesGone
	}
	return w.Submit(ReceiptJob{
		ReceiptID:   receiptID,
		HouseholdID: householdID,
		ImageDir:    imageDir,
	})
}

// Submit enqueues a receipt job for background processing.
// Returns ErrQueueFull if the queue is at capacity, allowing the caller to return 503.
// Returns ErrWorkerShuttingDown if Shutdown has been initiated.
//
// We hold the mutex across the channel send so Shutdown's close(jobs) cannot
// race with an in-progress send. The select is non-blocking, so the critical
// section is cheap.
//
// wg-accounting invariant: w.wg.Add(1) happens HERE, after a successful send,
// while still holding w.mu. The process() loop only calls wg.Done. The
// consequence is that every job sitting in w.jobs (buffered or in-flight)
// carries an open wg count, so Shutdown's wg.Wait can block on buffered work
// that hasn't been picked up yet — AND the drain loop in Shutdown must call
// wg.Done for each buffered job it pulls out (since we're "handling" it
// instead of letting process() do so). This closes the race where
// wg.Wait returned 0 while a process() goroutine was mid-receive but had not
// yet called Add — making the shutdown deadline effectively zero.
func (w *ReceiptWorker) Submit(job ReceiptJob) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.accepting {
		return ErrWorkerShuttingDown
	}
	select {
	case w.jobs <- job:
		w.wg.Add(1)
		return nil
	default:
		return ErrQueueFull
	}
}

// process is the main worker loop that pulls jobs from the channel and processes them.
// The wg.Add was performed by Submit; process() only needs to call wg.Done
// when the job completes (runJob returned or panicked-then-recovered).
func (w *ReceiptWorker) process() {
	for job := range w.jobs {
		w.runJob(job)
		w.wg.Done()
	}
}

// runJob executes a single job and handles errors. Split from process() so
// shutdown wg-accounting stays obvious at the call site.
func (w *ReceiptWorker) runJob(job ReceiptJob) {
	if err := w.processJob(job); err != nil {
		slog.Error("worker: failed to process receipt", "receipt_id", job.ReceiptID, "err", err)
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
		// channel is empty here, but drain defensively. wg-accounting: every
		// Submit added 1; process() calls Done. If we reach a buffered job
		// via this drain, we must Done it ourselves.
		for job := range w.jobs {
			requeued++
			w.markPending(job.ReceiptID)
			w.wg.Done()
		}
		slog.Info("worker: shutdown complete", "in_flight_finished", true, "requeued", requeued)
		return nil
	case <-ctx.Done():
		// Deadline hit. Drain whatever is still buffered and mark those
		// receipts pending for the next boot. process() goroutines may still
		// be running; we cannot interrupt a Claude HTTP call safely. Their
		// receipt rows will remain at whatever status the tx reached (usually
		// 'processing' pre-commit, which rolls back on process exit, or
		// 'matched'/'error' if the tx already committed). wg-accounting: each
		// buffered job we drain needs a wg.Done (Submit already Add'd 1 for
		// it).
		for {
			select {
			case job, ok := <-w.jobs:
				if !ok {
					// Channel closed and drained.
					slog.Warn("worker: shutdown deadline exceeded", "requeued", requeued, "note", "in-flight goroutines may still be running")
					return ctx.Err()
				}
				requeued++
				w.markPending(job.ReceiptID)
				w.wg.Done()
			default:
				slog.Warn("worker: shutdown deadline exceeded", "requeued", requeued, "note", "in-flight goroutines may still be running")
				return ctx.Err()
			}
		}
	}
}

// RequeuePending reloads receipts left at status='pending' from a prior run
// (Shutdown marks in-flight + buffered jobs pending, but the process itself
// never re-enqueues them on boot). Returns the number of successfully
// re-submitted receipts. Errors reading the DB are returned; per-receipt
// Resubmit failures (ErrImagesGone, etc.) are logged and skipped.
//
// Caps at 1000 rows per boot to avoid a resubmit storm on massive DBs; if
// more than the cap exist, logs a warning and leaves the remainder for the
// next boot (they stay 'pending', so the next call picks up where we left
// off).
func (w *ReceiptWorker) RequeuePending(ctx context.Context) (int, error) {
	const cap = 1000
	// Query one extra row to detect overflow.
	rows, err := w.db.QueryContext(ctx,
		"SELECT id, household_id FROM receipts WHERE status = 'pending' LIMIT ?",
		cap+1,
	)
	if err != nil {
		return 0, fmt.Errorf("query pending receipts: %w", err)
	}
	defer rows.Close()

	type pendingRow struct{ id, householdID string }
	var pending []pendingRow
	for rows.Next() {
		var r pendingRow
		if err := rows.Scan(&r.id, &r.householdID); err != nil {
			return 0, fmt.Errorf("scan pending row: %w", err)
		}
		pending = append(pending, r)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate pending rows: %w", err)
	}

	overflow := len(pending) > cap
	if overflow {
		pending = pending[:cap]
	}

	var resubmitted int
	for _, r := range pending {
		if ctx.Err() != nil {
			slog.Warn("worker: requeue aborted", "reason", ctx.Err(), "resubmitted", resubmitted)
			return resubmitted, ctx.Err()
		}
		err := w.Resubmit(r.id, r.householdID)
		switch {
		case err == nil:
			resubmitted++
		case errors.Is(err, ErrImagesGone):
			slog.Warn("worker: requeue skipped — images gone", "receipt_id", r.id)
		case errors.Is(err, ErrQueueFull):
			// The queue is full already; stop rather than hammering. Remaining
			// rows stay 'pending' for the next boot.
			slog.Warn("worker: requeue stopped — queue full", "resubmitted", resubmitted, "remaining", len(pending)-resubmitted)
			return resubmitted, nil
		case errors.Is(err, ErrWorkerShuttingDown):
			// Shouldn't happen at startup, but handle defensively.
			slog.Warn("worker: requeue stopped — shutting down", "resubmitted", resubmitted)
			return resubmitted, nil
		default:
			slog.Warn("worker: requeue error — skipping", "receipt_id", r.id, "err", err)
		}
	}
	if overflow {
		slog.Warn("worker: requeue capped", "cap", cap, "note", "more pending receipts exist; they will be picked up on next boot")
	}
	return resubmitted, nil
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
		slog.Error("worker: failed to mark receipt pending on shutdown", "receipt_id", receiptID, "err", err)
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
		slog.Debug("worker: image preprocessed", "name", entry.Name(), "orig_kb", originalSize/1024, "processed_kb", len(processed)/1024)

		// Save preprocessed version alongside original.
		processedName := "processed_" + entry.Name()
		processedPath := filepath.Join(job.ImageDir, processedName)
		if err := os.WriteFile(processedPath, processed, 0644); err != nil {
			slog.Warn("worker: failed to save preprocessed image", "err", err)
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
			slog.Warn("worker: failed to update image_paths", "err", err)
		}
	}

	// Broadcast processing status.
	w.hub.Broadcast(ws.Message{
		Type:      ws.EventReceiptProcessing,
		Household: job.HouseholdID,
		Payload:   map[string]interface{}{"receipt_id": job.ReceiptID},
	})

	// 2. Send to LLM vision API.
	slog.Info("worker: calling LLM", "receipt_id", job.ReceiptID, "images", len(images), "provider", w.llmClient.Provider())
	var extraction *llm.ReceiptExtraction
	if w.guard != nil {
		extraction, err = w.guard.ExtractForHousehold(job.HouseholdID, images)
	} else {
		extraction, err = w.llmClient.ExtractReceipt(images)
	}
	if err != nil {
		// Budget + breaker errors are terminal for THIS receipt — mark the
		// row with a specific error_message so the user understands why
		// the receipt stalled. Other errors fall through to the generic
		// "status='error'" path in runJob.
		if errors.Is(err, llm.ErrBudgetExceeded) {
			_, _ = w.db.Exec(
				"UPDATE receipts SET status = 'error', error_message = ? WHERE id = ?",
				"LLM budget exceeded for this month; edit receipt manually or raise LLM_MONTHLY_TOKEN_BUDGET",
				job.ReceiptID,
			)
			return fmt.Errorf("llm extraction: %w", err)
		}
		if errors.Is(err, llm.ErrCircuitOpen) {
			_, _ = w.db.Exec(
				"UPDATE receipts SET status = 'error', error_message = ? WHERE id = ?",
				"LLM temporarily unavailable (circuit breaker open)",
				job.ReceiptID,
			)
			return fmt.Errorf("llm extraction: %w", err)
		}
		return fmt.Errorf("llm extraction: %w", err)
	}
	slog.Info("worker: LLM returned", "receipt_id", job.ReceiptID, "store", extraction.StoreName, "items", len(extraction.Items))

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
			slog.Info("worker: creating new store", "store_name", extraction.StoreName, "store_id", storeID)
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
			slog.Debug("worker: store created successfully")
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
	slog.Debug("worker: updating receipt with extraction data", "receipt_id", job.ReceiptID)
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
			slog.Debug("worker: flagged pack overload (pattern)", "raw_name", item.RawName)
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
				slog.Debug("worker: flagged pack overload (price)", "raw_name", item.RawName, "total_price", item.TotalPrice)
			}
		}
	}

	// Open a matcher session for this receipt — collapses the per-item fuzzy
	// queries (aliases JOIN products + products scan) into a single preload
	// at open time. Matcher reads use w.db (not the in-flight tx), so the
	// session's candidate set is consistent with what the per-call path would
	// see. Session is per-receipt and scoped to this goroutine — do NOT share
	// across worker goroutines.
	//
	// On NewSession error (e.g. transient DB hiccup) we fall through to the
	// per-call path below; the receipt still processes, just without the
	// batched optimization.
	sess, sessErr := w.matchEngine.NewSession(job.HouseholdID, storeID)
	if sessErr != nil {
		slog.Warn("worker: match session open failed, using per-call path",
			"receipt_id", job.ReceiptID, "err", sessErr)
		sess = nil
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

		// Run matcher with suggested-name fallback. Prefer the per-receipt
		// session when it opened cleanly; fall back to the one-shot path
		// otherwise. Both paths return byte-identical MatchResult per
		// internal/matcher/session_test.go:TestSessionEquivalence.
		var matchResult matcher.MatchResult
		if sess != nil {
			matchResult = sess.MatchWithSuggestion(item.RawName, item.SuggestedName)
		} else {
			matchResult = w.matchEngine.MatchWithSuggestion(item.RawName, item.SuggestedName, storeID, job.HouseholdID)
		}

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
			// Matcher backfill: if the assigned product has NULL brand/category
			// and the LLM supplied a suggestion, fill those NULLs in. Load-bearing
			// `AND brand IS NULL` / `AND category IS NULL` inside the helper
			// protects user-set data from being clobbered. Errors are logged but
			// never fatal — a failed backfill must not abort the line-item assign.
			// Gated on item.Confidence >= backfillMinConfidence so a weak LLM guess
			// can't permanently populate canonical product metadata.
			if item.Confidence >= backfillMinConfidence {
				if err := matcher.BackfillProductMetadata(tx, *productID, item.SuggestedBrand, item.SuggestedCategory); err != nil {
					slog.Warn("worker: matcher backfill failed", "receipt_id", job.ReceiptID, "product_id", *productID, "err", err)
				}
			}

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
