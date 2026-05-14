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
	enrichmentrunner "github.com/mstefanko/cartledger/internal/enrichment/runner"
	"github.com/mstefanko/cartledger/internal/identifiers"
	"github.com/mstefanko/cartledger/internal/imaging"
	"github.com/mstefanko/cartledger/internal/llm"
	"github.com/mstefanko/cartledger/internal/matcher"
	"github.com/mstefanko/cartledger/internal/prices"
	"github.com/mstefanko/cartledger/internal/receiptline"
	"github.com/mstefanko/cartledger/internal/storage"
	"github.com/mstefanko/cartledger/internal/storecodes"
	"github.com/mstefanko/cartledger/internal/upc"
	"github.com/mstefanko/cartledger/internal/ws"
)

// packPattern detects multi-pack indicators like "12PK", "24 CT", "6 COUNT", "8 PACK".
var packPattern = regexp.MustCompile(`(?i)\d+\s*(PK|CT|COUNT|PACK)\b`)

// backfillMinConfidence is the minimum item-level LLM confidence required
// before a suggested_brand / suggested_category can promote into a product's
// canonical brand/category via BackfillProductMetadata. Below this, the
// suggestion is too weak to write into user-visible data.
const backfillMinConfidence = 0.5

const (
	receiptPackageDeterministicConfidence = 0.78
	receiptPackageLLMAgreementConfidence  = 0.92
	receiptPackageLLMOnlyConfidence       = 0.55
)

func extractionErrorMessage(err error, now time.Time) (string, bool) {
	switch {
	case errors.Is(err, llm.ErrBudgetExceeded):
		return "LLM budget exceeded for this month; edit receipt manually or raise LLM_MONTHLY_TOKEN_BUDGET", true
	case errors.Is(err, llm.ErrCircuitOpen):
		return "Receipt extraction is paused because the AI service is rate-limiting requests. Wait a few minutes, then retry extraction.", true
	case llm.IsRateLimit(err):
		retryAfter := llm.RateLimitRetryAfter(err, now)
		if retryAfter > 0 {
			return fmt.Sprintf("Receipt extraction is temporarily rate-limited by the AI service. Wait %s, then retry extraction.", humanRetryAfter(retryAfter)), true
		}
		return "Receipt extraction is temporarily rate-limited by the AI service. Wait a minute, then retry extraction.", true
	default:
		return "", false
	}
}

func humanRetryAfter(d time.Duration) string {
	seconds := int(d.Round(time.Second).Seconds())
	if seconds <= 1 {
		return "1 second"
	}
	if seconds < 60 {
		return fmt.Sprintf("%d seconds", seconds)
	}

	minutes := (seconds + 59) / 60
	if minutes == 1 {
		return "about 1 minute"
	}
	return fmt.Sprintf("about %d minutes", minutes)
}

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
	enrichment  *enrichmentrunner.Service

	// Shutdown coordination.
	wg          sync.WaitGroup // tracks in-flight processJob calls
	mu          sync.Mutex     // guards accepting flag
	queued      sync.Map       // receipt_id -> struct{} for buffered/in-flight dedupe
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

func (w *ReceiptWorker) SetEnrichmentService(service *enrichmentrunner.Service) {
	w.enrichment = service
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

// ErrReceiptAlreadyQueued is returned when a receipt is already buffered or
// in-flight. Treating duplicate submits as an idempotent success at the API
// edge prevents double LLM spend from concurrent retry clicks.
var ErrReceiptAlreadyQueued = fmt.Errorf("receipt already queued")

// Resubmit re-enqueues an existing receipt for background processing. Unlike
// Submit, this is for user-initiated retries: it reconstructs the ReceiptJob
// by locating the receipt's image directory under <DataDir>/receipts/<id>
// (the canonical layout established in the Scan handler).
//
// Returns:
//   - ErrImagesGone if the image directory is missing.
//   - ErrReceiptAlreadyQueued / ErrQueueFull / ErrWorkerShuttingDown propagated from Submit.
//
// The caller (API handler) is responsible for flipping status='pending' and
// clearing error_message before calling Resubmit; that ordering lets us fail
// fast (no DB mutation) if the queue is closed or the images are gone.
func (w *ReceiptWorker) Resubmit(receiptID, householdID string) error {
	localStore, err := storage.NewLocal(w.cfg.DataDir)
	if err == nil {
		originals, qerr := storage.ListExistingReceiptOriginals(context.Background(), w.db, localStore, receiptID)
		if qerr == nil && len(originals) > 0 {
			return w.Submit(ReceiptJob{
				ReceiptID:   receiptID,
				HouseholdID: householdID,
			})
		}
	}

	imageDir, err := storage.LegacyReceiptDir(w.cfg.DataDir, receiptID)
	if err != nil {
		return ErrImagesGone
	}
	if !legacyReceiptDirHasOriginals(imageDir) {
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
	if _, loaded := w.queued.LoadOrStore(job.ReceiptID, struct{}{}); loaded {
		return ErrReceiptAlreadyQueued
	}
	select {
	case w.jobs <- job:
		w.wg.Add(1)
		return nil
	default:
		w.queued.Delete(job.ReceiptID)
		return ErrQueueFull
	}
}

// process is the main worker loop that pulls jobs from the channel and processes them.
// The wg.Add was performed by Submit; process() only needs to call wg.Done
// when the job completes (runJob returned or panicked-then-recovered).
func (w *ReceiptWorker) process() {
	for job := range w.jobs {
		w.runJob(job)
		w.queued.Delete(job.ReceiptID)
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
			`UPDATE receipts
			 SET status = 'error',
			     error_message = COALESCE(NULLIF(error_message, ''), ?)
			 WHERE id = ?`,
			receiptErrorMessage(err), job.ReceiptID,
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

func receiptErrorMessage(err error) string {
	if err == nil {
		return "receipt processing failed"
	}
	msg := strings.Join(strings.Fields(err.Error()), " ")
	if msg == "" {
		return "receipt processing failed"
	}
	const maxRunes = 700
	runes := []rune(msg)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "..."
	}
	return msg
}

func emptyLineItemsMessage(extraction *llm.ReceiptExtraction) string {
	if extraction != nil && extraction.ItemsSoldCount != nil && *extraction.ItemsSoldCount > 0 {
		return fmt.Sprintf("AI read %d items sold but could not extract any line items. The photo may be too blurry, angled, or partially cut off for reliable item OCR. Try Repair Scan, add rows manually, or upload a clearer photo.", *extraction.ItemsSoldCount)
	}
	return "AI could not extract any line items from this receipt. The photo may be too blurry, angled, or partially cut off for reliable item OCR. Try Repair Scan, add rows manually, or upload a clearer photo."
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
			w.queued.Delete(job.ReceiptID)
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
				w.queued.Delete(job.ReceiptID)
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
		case errors.Is(err, ErrReceiptAlreadyQueued):
			slog.Debug("worker: requeue skipped — already queued", "receipt_id", r.id)
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

func (w *ReceiptWorker) loadOriginalImages(ctx context.Context, localStore *storage.Local, dataDir string, job ReceiptJob) ([]sourceImage, error) {
	rows, err := storage.ListReceiptImages(ctx, w.db, job.ReceiptID, storage.ReceiptImageKindOriginal)
	if err == nil && len(rows) > 0 {
		out := make([]sourceImage, 0, len(rows))
		for _, row := range rows {
			data, err := localStore.ReadFile(row.StorageKey)
			if err != nil {
				return nil, fmt.Errorf("read receipt image %s: %w", row.StorageKey, err)
			}
			out = append(out, sourceImage{
				pageNumber: row.PageNumber,
				ext:        filepath.Ext(row.StorageKey),
				key:        row.StorageKey,
				data:       data,
			})
		}
		return out, nil
	}

	imageDir := job.ImageDir
	if imageDir == "" {
		imageDir, err = storage.LegacyReceiptDir(dataDir, job.ReceiptID)
		if err != nil {
			return nil, err
		}
	}
	entries, err := os.ReadDir(imageDir)
	if err != nil {
		return nil, fmt.Errorf("read image dir: %w", err)
	}
	out := make([]sourceImage, 0)
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), "processed_") {
			continue
		}
		page, ext, err := storage.ParsePageFilename(entry.Name())
		if err != nil {
			ext = strings.ToLower(filepath.Ext(entry.Name()))
			if ext != ".jpg" && ext != ".jpeg" && ext != ".png" {
				continue
			}
			page = len(out) + 1
		}
		if page <= 0 {
			continue
		}
		imgPath := filepath.Join(imageDir, entry.Name())
		data, err := os.ReadFile(imgPath)
		if err != nil {
			return nil, fmt.Errorf("read image %s: %w", entry.Name(), err)
		}
		key, err := storage.ReceiptOriginalKey(job.ReceiptID, page, ext)
		if err != nil {
			return nil, err
		}
		if err := localStore.WriteFileAtomic(key, data, 0o644); err == nil {
			_ = storage.UpsertReceiptImage(ctx, w.db, storage.ReceiptImage{
				ReceiptID:  job.ReceiptID,
				Kind:       storage.ReceiptImageKindOriginal,
				PageNumber: page,
				StorageKey: key,
				MimeType:   storage.MimeTypeFromKey(key),
				SizeBytes:  int64(len(data)),
				SHA256:     sql.NullString{String: storage.SHA256Hex(data), Valid: true},
				CreatedAt:  time.Now().UTC(),
			})
		}
		out = append(out, sourceImage{
			pageNumber: page,
			ext:        ext,
			key:        key,
			data:       data,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no receipt images found")
	}
	return out, nil
}

type sourceImage struct {
	pageNumber int
	ext        string
	key        string
	data       []byte
}

func legacyReceiptDirHasOriginals(imageDir string) bool {
	entries, err := os.ReadDir(imageDir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), "processed_") {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext == ".jpg" || ext == ".jpeg" || ext == ".png" {
			return true
		}
	}
	return false
}

func (w *ReceiptWorker) processJob(job ReceiptJob) error {
	dataDir := ""
	if w.cfg != nil {
		dataDir = w.cfg.DataDir
	}
	if dataDir == "" && job.ImageDir != "" {
		dataDir = filepath.Dir(filepath.Dir(job.ImageDir))
	}
	localStore, err := storage.NewLocal(dataDir)
	if err != nil {
		return fmt.Errorf("open local storage: %w", err)
	}

	// 1. Read original image files from storage.
	sources, err := w.loadOriginalImages(context.Background(), localStore, dataDir, job)
	if err != nil {
		return err
	}

	var images [][]byte
	var processedPaths []string
	for _, src := range sources {
		// Preprocess: resize, grayscale, contrast, sharpen, crop.
		// Falls back to raw image on any error.
		originalSize := len(src.data)
		processed, _ := imaging.PreprocessReceipt(src.data)
		slog.Debug("worker: image preprocessed", "key", src.key, "orig_kb", originalSize/1024, "processed_kb", len(processed)/1024)

		processedKey, err := storage.ReceiptProcessedKey(job.ReceiptID, src.pageNumber, src.ext)
		if err != nil {
			return fmt.Errorf("build processed image key: %w", err)
		}
		if err := localStore.WriteFileAtomic(processedKey, processed, 0o644); err != nil {
			slog.Warn("worker: failed to save preprocessed image", "err", err)
			// Fall back to original path for display.
			processedPaths = append(processedPaths, src.key)
		} else {
			processedPaths = append(processedPaths, processedKey)
			if err := storage.UpsertReceiptImage(context.Background(), w.db, storage.ReceiptImage{
				ReceiptID:  job.ReceiptID,
				Kind:       storage.ReceiptImageKindProcessed,
				PageNumber: src.pageNumber,
				StorageKey: processedKey,
				MimeType:   storage.MimeTypeFromKey(processedKey),
				SizeBytes:  int64(len(processed)),
				SHA256:     sql.NullString{String: storage.SHA256Hex(processed), Valid: true},
				CreatedAt:  time.Now().UTC(),
			}); err != nil {
				slog.Warn("worker: failed to upsert processed image metadata", "err", err)
			}
		}

		images = append(images, processed)
	}
	if len(images) == 0 {
		return fmt.Errorf("no images found for receipt %s", job.ReceiptID)
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
		// the receipt stalled. Provider rate limits get the same treatment
		// so the UI can distinguish them from unknown processing failures.
		// Other errors fall through to the generic
		// "status='error'" path in runJob.
		if msg, ok := extractionErrorMessage(err, time.Now()); ok {
			_, _ = w.db.Exec(
				"UPDATE receipts SET status = 'error', error_message = ? WHERE id = ?",
				msg,
				job.ReceiptID,
			)
			return fmt.Errorf("llm extraction: %w", err)
		}
		return fmt.Errorf("llm extraction: %w", err)
	}
	slog.Info("worker: LLM returned", "receipt_id", job.ReceiptID, "store", extraction.StoreName, "items", len(extraction.Items))

	NormalizeExtractedPayment(extraction)

	// 3. Store raw_llm_json on the receipt.
	rawJSON, err := json.Marshal(extraction)
	if err != nil {
		return fmt.Errorf("marshal extraction: %w", err)
	}
	extraction.Items = NormalizeExtractedItems(extraction.Items)
	storeChain := matcher.ClassifyStore(extraction.StoreName)
	extraction.Items = NormalizeExtractedItemsForStore(extraction.Items, storeChain)

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
		 card_type = ?, card_last4 = ?, items_sold_count = ?,
		 raw_llm_json = ?, llm_provider = ?, status = 'processing'
		 WHERE id = ?`,
		nilIfEmpty(storeID), receiptDate, extraction.Time,
		subtotal.String(), tax.String(), total.String(),
		extraction.PaymentCardType, extraction.PaymentCardLast4, extraction.ItemsSoldCount,
		rawJSONStr, provider, job.ReceiptID,
	)
	if err != nil {
		return fmt.Errorf("update receipt: %w", err)
	}
	if len(extraction.Items) == 0 {
		msg := emptyLineItemsMessage(extraction)
		_, err = tx.Exec(
			"UPDATE receipts SET status = 'error', error_message = ? WHERE id = ?",
			msg, job.ReceiptID,
		)
		if err != nil {
			return fmt.Errorf("mark empty extraction error: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit empty extraction error: %w", err)
		}
		return fmt.Errorf("llm extraction returned no line items: %s", msg)
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
		countContribution := decimal.NewFromFloat(item.CountContribution)
		if countContribution.IsZero() {
			countContribution = decimal.NewFromInt(1)
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

		packageContent, packageConfidence, packageSource, hasPackageContent := packageContentForItem(item)
		var packQuantityOverride, packUnitOverride, packOverrideSource *string
		if hasPackageContent && packageSource == "receipt_explicit" {
			qty := packageContent.Quantity.String()
			unit := packageContent.Unit
			source := "receipt_explicit"
			packQuantityOverride = &qty
			packUnitOverride = &unit
			packOverrideSource = &source
		}

		itemUPC := upc.NormalizePointer(item.UPC)
		var itemIdentifier *identifiers.Observation
		var identifierObservations []identifiers.Observation
		if item.UPC != nil {
			if obs, err := identifiers.Normalize(*item.UPC, identifiers.KindGTIN, ""); err == nil && obs.NormalizedValue != "" {
				obs.Source = "receipt"
				if item.Confidence > 0 {
					conf := item.Confidence
					obs.Confidence = &conf
				}
				itemIdentifier = &obs
				identifierObservations = append(identifierObservations, obs)
				itemUPC = &obs.NormalizedValue
			}
		}

		// Run matcher with suggested-name fallback. Prefer the per-receipt
		// session when it opened cleanly; fall back to the one-shot path
		// otherwise. Both paths return byte-identical MatchResult per
		// internal/matcher/session_test.go:TestSessionEquivalence.
		var matchResult matcher.MatchResult
		storeItemCode := ptrStringValue(item.StoreItemCode)
		if sess != nil {
			matchResult = sess.MatchInput(context.Background(), matcher.Input{
				RawName:       item.RawName,
				StoreItemCode: storeItemCode,
				SuggestedName: item.SuggestedName,
				Identifiers:   identifierObservations,
			})
		} else {
			matchResult = w.matchEngine.MatchInput(context.Background(), matcher.Input{
				RawName:       item.RawName,
				StoreItemCode: storeItemCode,
				SuggestedName: item.SuggestedName,
				StoreID:       storeID,
				HouseholdID:   job.HouseholdID,
				Identifiers:   identifierObservations,
			})
		}
		if matchResult.Err != nil {
			return fmt.Errorf("match line item %q: %w", item.RawName, matchResult.Err)
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
			`INSERT INTO line_items (id, receipt_id, product_id, raw_name, store_item_code, receipt_description, upc, quantity, unit, unit_price, total_price, regular_price, discount_amount, count_contribution, suggested_name, suggested_category, suggested_brand, suggested_product_id, matched, confidence, line_number, pack_quantity_override, pack_unit_override, pack_override_source, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			lineItemID, job.ReceiptID, productID, item.RawName,
			item.StoreItemCode, item.ReceiptDescription, itemUPC,
			quantity.String(), item.Unit, unitPrice, totalPrice.String(),
			regularPrice, discountAmount, countContribution.String(),
			suggestedName, suggestedCategory, suggestedBrand, suggestedProductID,
			matched, confidence, lineNum, packQuantityOverride, packUnitOverride, packOverrideSource, now,
		)
		if err != nil {
			return fmt.Errorf("insert line item: %w", err)
		}
		if itemIdentifier != nil {
			if err := identifiers.InsertLineItemObservation(context.Background(), tx, lineItemID, *itemIdentifier); err != nil {
				return fmt.Errorf("insert identifier observation: %w", err)
			}
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

			if err := matcher.UpsertAlias(context.Background(), tx, matcher.AliasUpsert{
				HouseholdID: job.HouseholdID,
				ProductID:   *productID,
				Alias:       item.RawName,
				StoreID:     &storeID,
				Source:      matcher.AliasSourceReceiptMatch,
				Confidence:  confidence,
				CreatedAt:   now,
			}); err != nil {
				slog.Warn("worker: alias upsert skipped", "receipt_id", job.ReceiptID, "product_id", *productID, "err", err)
			}

			if itemIdentifier != nil {
				conf := 0.9
				if confidence != nil {
					conf = *confidence
				}
				if err := identifiers.UpsertProductIdentifier(context.Background(), tx, identifiers.ProductIdentifier{
					HouseholdID:       job.HouseholdID,
					ProductID:         *productID,
					Kind:              itemIdentifier.Kind,
					Authority:         itemIdentifier.Authority,
					Value:             itemIdentifier.RawValue,
					NormalizedValue:   itemIdentifier.NormalizedValue,
					Source:            "line_item",
					Confidence:        &conf,
					SetPrimaryProduct: true,
				}); err != nil {
					slog.Warn("worker: identifier upsert skipped", "receipt_id", job.ReceiptID, "product_id", *productID, "identifier", itemIdentifier.NormalizedValue, "err", err)
				}
			}

			if storeItemCode != "" {
				label := item.ReceiptDescription
				if label == nil {
					label = &item.RawName
				}
				if err := storecodes.UpsertReceipt(context.Background(), tx, job.HouseholdID, storeID, *productID, storeItemCode, label, now); err != nil {
					return fmt.Errorf("upsert store product code: %w", err)
				}
			}

			if hasPackageContent {
				if err := insertPackageSuggestionsFromLine(context.Background(), tx, *productID, lineItemID, job.ReceiptID, item, packageContent, packageConfidence, packageSource); err != nil {
					return fmt.Errorf("insert package suggestion: %w", err)
				}
			}

			if err := prices.RecordProductPriceFromLineItem(context.Background(), tx, lineItemID); err != nil {
				return fmt.Errorf("record product price: %w", err)
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

	w.queueReceiptScanEnrichment(job.HouseholdID, job.ReceiptID)

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

func (w *ReceiptWorker) queueReceiptScanEnrichment(householdID, receiptID string) {
	if w.enrichment == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	enabled, err := w.enrichment.AutoOnScanEnabled(ctx, householdID)
	if err != nil {
		slog.Warn("worker: enrichment auto-scan setting check failed", "receipt_id", receiptID, "err", err)
		return
	}
	if !enabled {
		return
	}

	const lookupExpr = `COALESCE(NULLIF(TRIM(p.upc), ''), NULLIF(TRIM(li.upc), ''))`
	rows, err := w.db.QueryContext(ctx,
		`SELECT DISTINCT li.product_id, `+lookupExpr+` AS lookup_upc
		   FROM line_items li
		   JOIN products p ON p.id = li.product_id
		  WHERE li.receipt_id = ?
		    AND p.household_id = ?
		    AND `+lookupExpr+` IS NOT NULL`,
		receiptID, householdID,
	)
	if err != nil {
		slog.Warn("worker: enrichment auto-scan product query failed", "receipt_id", receiptID, "err", err)
		return
	}
	defer rows.Close()

	queued := 0
	skippedExisting := 0
	for rows.Next() {
		var productID, rawUPC string
		if err := rows.Scan(&productID, &rawUPC); err != nil {
			slog.Warn("worker: enrichment auto-scan row scan failed", "receipt_id", receiptID, "err", err)
			continue
		}
		normalizedUPC, err := upc.Normalize(rawUPC)
		if err != nil || normalizedUPC == "" {
			if err != nil {
				slog.Debug("worker: enrichment auto-scan skipped invalid UPC", "receipt_id", receiptID, "product_id", productID, "upc", rawUPC, "err", err)
			}
			continue
		}
		lookupKey := "upc:" + normalizedUPC
		if w.productHasSuccessfulExternalMetadata(ctx, productID, lookupKey) {
			skippedExisting++
			continue
		}
		_, created, err := w.enrichment.QueueJob(ctx, enrichmentrunner.QueueJobRequest{
			HouseholdID: householdID,
			ProductID:   productID,
			Trigger:     enrichmentrunner.TriggerReceiptScan,
			LookupKey:   lookupKey,
		})
		if err != nil {
			slog.Warn("worker: enrichment auto-scan queue failed", "receipt_id", receiptID, "product_id", productID, "err", err)
			continue
		}
		if created {
			queued++
		}
	}
	if err := rows.Err(); err != nil {
		slog.Warn("worker: enrichment auto-scan rows failed", "receipt_id", receiptID, "err", err)
	}
	if queued > 0 || skippedExisting > 0 {
		slog.Info("worker: enrichment auto-scan checked products", "receipt_id", receiptID, "queued", queued, "skipped_existing", skippedExisting)
	}
}

func (w *ReceiptWorker) productHasSuccessfulExternalMetadata(ctx context.Context, productID, lookupKey string) bool {
	var exists int
	err := w.db.QueryRowContext(ctx,
		`SELECT 1
		   FROM product_external_metadata
		  WHERE product_id = ?
		    AND lookup_key = ?
		    AND source IN ('openfoodfacts', 'usda_fdc')
		    AND last_error IS NULL
		  LIMIT 1`,
		productID, lookupKey,
	).Scan(&exists)
	return err == nil
}

func packageContentForItem(item llm.ExtractedItem) (receiptline.PackageContent, float64, string, bool) {
	deterministic, deterministicOK := receiptline.ParsePackageContent(item.RawName, ptrStringValue(item.ReceiptDescription))
	modelPackage, modelOK := llmPackageContent(item)
	if deterministicOK {
		confidence := receiptPackageDeterministicConfidence
		if modelOK && samePackageContent(deterministic, modelPackage) {
			confidence = receiptPackageLLMAgreementConfidence
		}
		return deterministic, confidence, "receipt_explicit", true
	}
	if modelOK {
		return modelPackage, receiptPackageLLMOnlyConfidence, "receipt_llm", true
	}
	return receiptline.PackageContent{}, 0, "", false
}

func llmPackageContent(item llm.ExtractedItem) (receiptline.PackageContent, bool) {
	if item.PackageQuantity != nil && item.PackageUnit != nil {
		parsed, ok := receiptline.ParsePackageContent(strings.TrimSpace(*item.PackageQuantity) + " " + strings.TrimSpace(*item.PackageUnit))
		if ok {
			if item.PackageLabel != nil && strings.TrimSpace(*item.PackageLabel) != "" {
				parsed.Label = strings.TrimSpace(*item.PackageLabel)
			}
			return parsed, true
		}
	}
	if item.PackageLabel != nil {
		return receiptline.ParsePackageContent(*item.PackageLabel)
	}
	return receiptline.PackageContent{}, false
}

func samePackageContent(a, b receiptline.PackageContent) bool {
	return a.Unit == b.Unit && a.Quantity.Equal(b.Quantity)
}

func insertPackageSuggestionsFromLine(ctx context.Context, tx *sql.Tx, productID, lineItemID, receiptID string, item llm.ExtractedItem, pkg receiptline.PackageContent, confidence float64, source string) error {
	var currentQuantity sql.NullFloat64
	var currentUnit sql.NullString
	if err := tx.QueryRowContext(ctx,
		"SELECT pack_quantity, pack_unit FROM products WHERE id = ?",
		productID,
	).Scan(&currentQuantity, &currentUnit); err != nil {
		return err
	}

	sourceURL := "receipt://" + receiptID + "/line-items/" + lineItemID
	evidence := packageEvidence(item, pkg)
	if !currentQuantity.Valid || currentQuantity.Float64 <= 0 {
		if err := insertPackageSuggestion(ctx, tx, productID, receiptID, source, sourceURL, "pack_quantity", pkg.Quantity.String(), evidence, confidence); err != nil {
			return err
		}
	}
	if !currentUnit.Valid || strings.TrimSpace(currentUnit.String) == "" {
		if err := insertPackageSuggestion(ctx, tx, productID, receiptID, source, sourceURL, "pack_unit", pkg.Unit, evidence, confidence); err != nil {
			return err
		}
	}
	return nil
}

func insertPackageSuggestion(ctx context.Context, tx *sql.Tx, productID, receiptID, source, sourceURL, field, value, evidence string, confidence float64) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	blocked, err := productFieldEditBlocksReceiptSuggestion(ctx, tx, productID, receiptID, field)
	if err != nil || blocked {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO product_enrichment_suggestions
		    (id, product_id, product_link_id, source, source_url, field, value, evidence, confidence, status, created_at, updated_at)
		 SELECT lower(hex(randomblob(16))), ?, NULL, ?, ?, ?, ?, ?, ?, 'pending', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		  WHERE NOT EXISTS (
			SELECT 1
			  FROM product_enrichment_suggestions
			 WHERE product_id = ?
			   AND source = ?
			   AND field = ?
			   AND value = ?
		  )`,
		productID, source, sourceURL, field, value, evidence, confidence,
		productID, source, field, value,
	)
	return err
}

func productFieldEditBlocksReceiptSuggestion(ctx context.Context, tx *sql.Tx, productID, receiptID, field string) (bool, error) {
	var exists int
	err := tx.QueryRowContext(ctx,
		`SELECT 1
		   FROM product_field_edits pfe
		   JOIN receipts r ON r.id = ?
		  WHERE pfe.product_id = ?
		    AND pfe.field = ?
		    AND datetime(pfe.edited_at) > datetime(substr(r.receipt_date, 1, 19))
		  LIMIT 1`,
		receiptID, productID, field,
	).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func packageEvidence(item llm.ExtractedItem, pkg receiptline.PackageContent) string {
	if item.ReceiptDescription != nil && strings.TrimSpace(*item.ReceiptDescription) != "" {
		return fmt.Sprintf("Receipt line package content %q from %q", pkg.Label, strings.TrimSpace(*item.ReceiptDescription))
	}
	return fmt.Sprintf("Receipt line package content %q from %q", pkg.Label, strings.TrimSpace(item.RawName))
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

func ptrStringValue(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}
