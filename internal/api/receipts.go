package api

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/shopspring/decimal"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/imaging"
	"github.com/mstefanko/cartledger/internal/llm"
	"github.com/mstefanko/cartledger/internal/matcher"
	"github.com/mstefanko/cartledger/internal/worker"
)

// ReceiptHandler holds dependencies for receipt-related endpoints.
type ReceiptHandler struct {
	DB     *sql.DB
	Cfg    *config.Config
	Worker *worker.ReceiptWorker
	// Guard is optional. When non-nil, the Reprocess handler uses it to
	// pre-flight budget + circuit-breaker state so users get a fast 503
	// with a clear message instead of enqueueing a doomed job.
	Guard *llm.GuardedExtractor
}

// --- Request / Response types ---

type updateLineItemRequest struct {
	ProductID *string `json:"product_id"`
	Quantity  *string `json:"quantity"`
	Unit      *string `json:"unit"`
	Price     *string `json:"price"`
}

type receiptListItem struct {
	ID           string  `json:"id"`
	StoreName    *string `json:"store_name"`
	ReceiptDate  string  `json:"receipt_date"`
	Total        *string `json:"total"`
	Status       string  `json:"status"`
	ItemCount    int     `json:"item_count"`
	CreatedAt    string  `json:"created_at"`
	ErrorMessage *string `json:"error_message,omitempty"`
}

type lineItemResponse struct {
	ID                   string   `json:"id"`
	ReceiptID            string   `json:"receipt_id"`
	ProductID            *string  `json:"product_id,omitempty"`
	ProductName          *string  `json:"product_name,omitempty"`
	Category             *string  `json:"category,omitempty"`
	RawName              string   `json:"raw_name"`
	Quantity             string   `json:"quantity"`
	Unit                 *string  `json:"unit,omitempty"`
	UnitPrice            *string  `json:"unit_price,omitempty"`
	TotalPrice           string   `json:"total_price"`
	RegularPrice         *string  `json:"regular_price,omitempty"`
	DiscountAmount       *string  `json:"discount_amount,omitempty"`
	Matched              string   `json:"matched"`
	Confidence           *float64 `json:"confidence,omitempty"`
	LineNumber           *int     `json:"line_number,omitempty"`
	SuggestedName        *string  `json:"suggested_name,omitempty"`
	SuggestedCategory    *string  `json:"suggested_category,omitempty"`
	SuggestedProductID   *string  `json:"suggested_product_id,omitempty"`
	SuggestedProductName *string  `json:"suggested_product_name,omitempty"`
	SuggestionType       *string  `json:"suggestion_type,omitempty"`
}

type receiptDetailResponse struct {
	ID          string             `json:"id"`
	HouseholdID string            `json:"household_id"`
	StoreID     *string            `json:"store_id,omitempty"`
	StoreName   *string            `json:"store_name,omitempty"`
	ScannedBy   *string            `json:"scanned_by,omitempty"`
	ReceiptDate string             `json:"receipt_date"`
	Subtotal    *string            `json:"subtotal,omitempty"`
	Tax         *string            `json:"tax,omitempty"`
	Total       *string            `json:"total,omitempty"`
	Status      string             `json:"status"`
	LLMProvider *string            `json:"llm_provider,omitempty"`
	CardType    *string            `json:"card_type,omitempty"`
	CardLast4   *string            `json:"card_last4,omitempty"`
	ReceiptTime *string            `json:"receipt_time,omitempty"`
	ImagePaths   *string            `json:"image_paths,omitempty"`
	RawLLMJSON   *string            `json:"raw_llm_json,omitempty"`
	CreatedAt    string             `json:"created_at"`
	ErrorMessage *string            `json:"error_message,omitempty"`
	LineItems    []lineItemResponse `json:"line_items"`
}

// uploadBodyLimit caps the request body for multipart receipt uploads.
//
// Per-file cap is 10 MB (see Scan). A single scan accepts multiple images
// (front/back, multi-page receipts); 50 MB allows ~5 full-size images plus
// multipart framing overhead while still preventing pathological uploads from
// OOMing the process. Tightened later via a config knob if needed.
const uploadBodyLimit = "50M"

// RegisterRoutes mounts receipt endpoints onto the protected group.
func (h *ReceiptHandler) RegisterRoutes(protected *echo.Group) {
	receipts := protected.Group("/receipts")
	// Cap multipart upload size before it is read into memory / disk.
	receipts.POST("/scan", h.Scan, middleware.BodyLimit(uploadBodyLimit))
	receipts.GET("", h.List)
	receipts.GET("/:id", h.Get)
	receipts.PUT("/:id/line-items/:itemId", h.UpdateLineItem)
	receipts.POST("/:id/accept-suggestions", h.AcceptSuggestions)
	receipts.POST("/:id/reprocess", h.Reprocess)
	receipts.PUT("/:id", h.UpdateReceipt)
	receipts.DELETE("/:id", h.Delete)
}

// Scan handles multipart receipt image upload and submits for background processing.
// POST /api/v1/receipts/scan
func (h *ReceiptHandler) Scan(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	userID := auth.UserIDFrom(c)

	form, err := c.MultipartForm()
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid multipart form"})
	}

	files := form.File["images"]
	if len(files) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "at least one image is required"})
	}

	const maxFileSize = 10 << 20 // 10MB
	allowedTypes := map[string]bool{
		"image/jpeg": true,
		"image/png":  true,
	}

	// Validate all files before saving any.
	for _, fh := range files {
		if fh.Size > maxFileSize {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("file %s exceeds 10MB limit", fh.Filename),
			})
		}
		ct := fh.Header.Get("Content-Type")
		if !allowedTypes[ct] {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("file %s has unsupported type %s; only JPEG and PNG are allowed", fh.Filename, ct),
			})
		}
	}

	// Create receipt row.
	receiptID := uuid.New().String()
	now := time.Now().UTC()

	imageDir := filepath.Join(h.Cfg.DataDir, "receipts", receiptID)
	if err := os.MkdirAll(imageDir, 0o755); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create image directory"})
	}

	// Save images to disk.
	var imagePaths []string
	for i, fh := range files {
		ext := ".jpg"
		if fh.Header.Get("Content-Type") == "image/png" {
			ext = ".png"
		}
		filename := fmt.Sprintf("%d%s", i+1, ext)
		destPath := filepath.Join(imageDir, filename)

		src, err := fh.Open()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to read uploaded file"})
		}

		raw, err := io.ReadAll(src)
		src.Close()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to read uploaded file"})
		}

		// Strip EXIF/GPS and other metadata by re-encoding the image before
		// saving the "original" copy to disk. Phone uploads commonly embed
		// GPS coordinates; without this, any household member who can view
		// the receipt image could recover the uploader's location.
		// Quality 95 is near-lossless — this is our archival copy for
		// debug/rescan, not the LLM input (which is further compressed by
		// the preprocess step in the worker).
		scrubbed, err := imaging.StripMetadata(raw, 95)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("file %s could not be decoded as an image: %v", fh.Filename, err),
			})
		}

		// PNG input → re-encoded as PNG; JPEG → re-encoded as JPEG. Both
		// preserve the on-disk extension we already chose from Content-Type.
		if err := os.WriteFile(destPath, scrubbed, 0o644); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to save image"})
		}

		imagePaths = append(imagePaths, destPath)
	}

	imagePathsStr := strings.Join(imagePaths, ",")

	_, err = h.DB.Exec(
		`INSERT INTO receipts (id, household_id, scanned_by, receipt_date, image_paths, status, created_at)
		 VALUES (?, ?, ?, ?, ?, 'pending', ?)`,
		receiptID, householdID, userID, now, imagePathsStr, now,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create receipt"})
	}

	// Submit to worker for background processing.
	if err := h.Worker.Submit(worker.ReceiptJob{
		ReceiptID:   receiptID,
		HouseholdID: householdID,
		ImageDir:    imageDir,
	}); err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "server busy, please try again later"})
	}

	return c.JSON(http.StatusAccepted, map[string]string{
		"id":     receiptID,
		"status": "pending",
	})
}

// Reprocess re-enqueues a failed (or still-pending) receipt so the worker
// can take another pass. User-initiated only — there is no automatic
// retry loop; composes with the error_message surfaced in Get so the user
// can see *why* the last attempt failed.
//
// POST /api/v1/receipts/:id/reprocess
//
// Status map:
//
//	200/202 — accepted, job enqueued
//	401     — no/invalid JWT (middleware)
//	404     — receipt not found (also covers cross-household lookups)
//	409     — receipt is in a state that disallows reprocess (processing/matched/reviewed)
//	410     — receipt images no longer on disk (retention policy deleted them)
//	503     — LLM budget exhausted, breaker open, or worker queue full
//
// The endpoint is mounted under the worker-submit rate-limit tier (see
// router.go) because it triggers an LLM call, same cost surface as /scan.
func (h *ReceiptHandler) Reprocess(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	receiptID := c.Param("id")

	// 1. Verify receipt exists + belongs to this household, and read status.
	var status string
	err := h.DB.QueryRow(
		"SELECT status FROM receipts WHERE id = ? AND household_id = ?",
		receiptID, householdID,
	).Scan(&status)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "receipt not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// 2. Only allow reprocess from recoverable states. 'processing' means
	// the worker is actively handling it; 'matched'/'reviewed' are successful
	// terminal states where a silent re-extract would overwrite line-item
	// edits and product matches the user already curated.
	if status != "error" && status != "pending" {
		return c.JSON(http.StatusConflict, map[string]string{
			"error":  fmt.Sprintf("cannot reprocess receipt in status %q", status),
			"status": status,
		})
	}

	// 3. Pre-flight the LLM guard if wired. No point enqueueing a job
	// that will fail on the very first guard check inside the worker —
	// tell the user immediately so the UI doesn't flicker to "processing"
	// only to flip back to "error" a moment later.
	if h.Guard != nil {
		if err := llm.CheckBudget(h.Guard.DB(), householdID, h.Guard.Budget()); err != nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{
				"error": "LLM monthly budget exhausted; raise LLM_MONTHLY_TOKEN_BUDGET or wait until next month",
			})
		}
		if h.Guard.Breaker() != nil && h.Guard.Breaker().IsOpen() {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{
				"error": "LLM temporarily unavailable (circuit breaker open); try again in a minute",
			})
		}
	}

	// 4. Flip status back to pending and clear the old error_message so
	// the UI reflects the retry immediately. Do this BEFORE Resubmit so
	// even if enqueue returns 503 below, the row is consistent with the
	// user's intent (retryable), not stuck at 'error'.
	//
	// If Resubmit fails, we roll the row back to 'error' so List/Get
	// accurately reflect the failed retry.
	_, err = h.DB.Exec(
		"UPDATE receipts SET status = 'pending', error_message = NULL WHERE id = ? AND household_id = ?",
		receiptID, householdID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// 5. Re-enqueue via the worker. Resubmit re-reads image paths from disk
	// so we never require the user to re-upload.
	if err := h.Worker.Resubmit(receiptID, householdID); err != nil {
		// Roll back the status flip.
		_, _ = h.DB.Exec(
			"UPDATE receipts SET status = 'error', error_message = ? WHERE id = ?",
			err.Error(), receiptID,
		)
		switch {
		case errors.Is(err, worker.ErrImagesGone):
			return c.JSON(http.StatusGone, map[string]string{
				"error": "receipt images are no longer on disk; please re-upload the receipt",
			})
		case errors.Is(err, worker.ErrQueueFull), errors.Is(err, worker.ErrWorkerShuttingDown):
			return c.JSON(http.StatusServiceUnavailable, map[string]string{
				"error": "server busy, please try again later",
			})
		default:
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to enqueue receipt"})
		}
	}

	// 6. Return 202 with the minimal receipt shape that the frontend needs
	// to flip its local view optimistically. Full detail is available via
	// GET /receipts/:id, and the ws 'receipt.complete' event will prompt
	// the client to refetch when processing finishes.
	return c.JSON(http.StatusAccepted, map[string]string{
		"id":     receiptID,
		"status": "pending",
	})
}

// List returns all receipts for the authenticated household.
// GET /api/v1/receipts
func (h *ReceiptHandler) List(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)

	rows, err := h.DB.Query(
		`SELECT r.id, s.name, r.receipt_date, r.total, r.status, r.created_at,
		        r.error_message,
		        (SELECT COUNT(*) FROM line_items WHERE receipt_id = r.id) as item_count
		 FROM receipts r
		 LEFT JOIN stores s ON r.store_id = s.id
		 WHERE r.household_id = ?
		 ORDER BY r.receipt_date DESC, r.created_at DESC`,
		householdID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer rows.Close()

	receipts := make([]receiptListItem, 0)
	for rows.Next() {
		var r receiptListItem
		var receiptDate time.Time
		var total *decimal.Decimal
		var createdAt time.Time
		if err := rows.Scan(&r.ID, &r.StoreName, &receiptDate, &total, &r.Status, &createdAt, &r.ErrorMessage, &r.ItemCount); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		r.ReceiptDate = receiptDate.Format("2006-01-02")
		r.CreatedAt = createdAt.Format(time.RFC3339)
		if total != nil {
			s := total.String()
			r.Total = &s
		}
		receipts = append(receipts, r)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusOK, receipts)
}

// Get returns a receipt with all its line items.
// GET /api/v1/receipts/:id
func (h *ReceiptHandler) Get(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	receiptID := c.Param("id")

	var resp receiptDetailResponse
	var receiptDate time.Time
	var subtotal, tax, total *decimal.Decimal
	var createdAt time.Time

	err := h.DB.QueryRow(
		`SELECT r.id, r.household_id, r.store_id, s.name, r.scanned_by, r.receipt_date,
		        r.subtotal, r.tax, r.total, r.status, r.llm_provider,
		        r.card_type, r.card_last4, r.receipt_time,
		        r.image_paths, r.raw_llm_json, r.created_at, r.error_message
		 FROM receipts r
		 LEFT JOIN stores s ON r.store_id = s.id
		 WHERE r.id = ? AND r.household_id = ?`,
		receiptID, householdID,
	).Scan(
		&resp.ID, &resp.HouseholdID, &resp.StoreID, &resp.StoreName,
		&resp.ScannedBy, &receiptDate, &subtotal, &tax, &total,
		&resp.Status, &resp.LLMProvider,
		&resp.CardType, &resp.CardLast4, &resp.ReceiptTime,
		&resp.ImagePaths, &resp.RawLLMJSON, &createdAt, &resp.ErrorMessage,
	)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "receipt not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	resp.ReceiptDate = receiptDate.Format("2006-01-02")
	resp.CreatedAt = createdAt.Format(time.RFC3339)
	if subtotal != nil {
		s := subtotal.String()
		resp.Subtotal = &s
	}
	if tax != nil {
		s := tax.String()
		resp.Tax = &s
	}
	if total != nil {
		s := total.String()
		resp.Total = &s
	}

	// Fetch line items with product info and suggestion data.
	rows, err := h.DB.Query(
		`SELECT li.id, li.receipt_id, li.product_id, p.name, p.category,
		        li.raw_name, li.quantity, li.unit, li.unit_price, li.total_price,
		        li.regular_price, li.discount_amount,
		        li.matched, li.confidence, li.line_number,
		        li.suggested_name, li.suggested_category,
		        li.suggested_product_id, sp.name
		 FROM line_items li
		 LEFT JOIN products p ON li.product_id = p.id
		 LEFT JOIN products sp ON li.suggested_product_id = sp.id
		 WHERE li.receipt_id = ?
		 ORDER BY li.line_number, li.created_at`,
		receiptID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer rows.Close()

	resp.LineItems = make([]lineItemResponse, 0)
	for rows.Next() {
		var li lineItemResponse
		var quantity, totalPrice decimal.Decimal
		var unitPrice *decimal.Decimal
		if err := rows.Scan(
			&li.ID, &li.ReceiptID, &li.ProductID, &li.ProductName, &li.Category,
			&li.RawName, &quantity, &li.Unit, &unitPrice, &totalPrice,
			&li.RegularPrice, &li.DiscountAmount,
			&li.Matched, &li.Confidence, &li.LineNumber,
			&li.SuggestedName, &li.SuggestedCategory,
			&li.SuggestedProductID, &li.SuggestedProductName,
		); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		li.Quantity = quantity.String()
		li.TotalPrice = totalPrice.String()
		if unitPrice != nil {
			s := unitPrice.String()
			li.UnitPrice = &s
		}
		// Compute suggestion_type for unmatched items with suggestions.
		if li.Matched == "unmatched" && li.SuggestedName != nil {
			if li.SuggestedProductID != nil {
				st := "existing_match"
				li.SuggestionType = &st
			} else {
				st := "new_product"
				li.SuggestionType = &st
			}
		} else if li.Matched == "cross_store_match" && li.SuggestedProductID != nil {
			st := "cross_store_match"
			li.SuggestionType = &st
		}
		resp.LineItems = append(resp.LineItems, li)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusOK, resp)
}

// UpdateLineItem updates a line item on a receipt.
// PUT /api/v1/receipts/:id/line-items/:itemId
func (h *ReceiptHandler) UpdateLineItem(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	receiptID := c.Param("id")
	itemID := c.Param("itemId")

	var req updateLineItemRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	// Verify receipt belongs to household.
	var exists int
	err := h.DB.QueryRow(
		"SELECT COUNT(*) FROM receipts WHERE id = ? AND household_id = ?",
		receiptID, householdID,
	).Scan(&exists)
	if err != nil || exists == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "receipt not found"})
	}

	// Build dynamic update.
	setClauses := make([]string, 0)
	args := make([]interface{}, 0)

	if req.ProductID != nil {
		setClauses = append(setClauses, "product_id = ?")
		args = append(args, *req.ProductID)
	}
	if req.Quantity != nil {
		setClauses = append(setClauses, "quantity = ?")
		args = append(args, *req.Quantity)
	}
	if req.Unit != nil {
		setClauses = append(setClauses, "unit = ?")
		args = append(args, *req.Unit)
	}
	if req.Price != nil {
		setClauses = append(setClauses, "total_price = ?")
		args = append(args, *req.Price)
	}

	if len(setClauses) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "no fields to update"})
	}

	args = append(args, itemID, receiptID)
	query := fmt.Sprintf(
		"UPDATE line_items SET %s WHERE id = ? AND receipt_id = ?",
		strings.Join(setClauses, ", "),
	)

	result, err := h.DB.Exec(query, args...)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "line item not found"})
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "updated"})
}

// UpdateReceipt updates receipt status (e.g., mark as reviewed).
// PUT /api/v1/receipts/:id
func (h *ReceiptHandler) UpdateReceipt(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	receiptID := c.Param("id")

	var req struct {
		Status string `json:"status"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	allowedStatuses := map[string]bool{"reviewed": true, "matched": true}
	if !allowedStatuses[req.Status] {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid status"})
	}

	result, err := h.DB.Exec(
		"UPDATE receipts SET status = ? WHERE id = ? AND household_id = ?",
		req.Status, receiptID, householdID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "receipt not found"})
	}

	return c.JSON(http.StatusOK, map[string]string{"status": req.Status})
}

// --- Accept Suggestions types ---

type acceptSuggestionsRequest struct {
	LineItemIDs []string                       `json:"line_item_ids"`
	Edits       map[string]suggestionEditInput `json:"edits,omitempty"`
}

type suggestionEditInput struct {
	Name     *string `json:"name,omitempty"`
	Category *string `json:"category,omitempty"`
}

type acceptSuggestionsResponse struct {
	CreatedCount    int              `json:"created_count"`
	MatchedCount    int              `json:"matched_count"`
	ProductsCreated []productBrief   `json:"products_created"`
	ProductsMatched []productBrief   `json:"products_matched"`
}

type productBrief struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// AcceptSuggestions batch-accepts suggested matches and creates new products.
// POST /api/v1/receipts/:id/accept-suggestions
func (h *ReceiptHandler) AcceptSuggestions(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	receiptID := c.Param("id")

	var req acceptSuggestionsRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	if len(req.LineItemIDs) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "line_item_ids is required"})
	}

	// Verify receipt belongs to household.
	var storeID *string
	var receiptDate time.Time
	err := h.DB.QueryRow(
		"SELECT store_id, receipt_date FROM receipts WHERE id = ? AND household_id = ?",
		receiptID, householdID,
	).Scan(&storeID, &receiptDate)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "receipt not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	tx, err := h.DB.Begin()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	resp := acceptSuggestionsResponse{
		ProductsCreated: make([]productBrief, 0),
		ProductsMatched: make([]productBrief, 0),
	}

	// Track created products by name to deduplicate within batch.
	createdByName := make(map[string]string) // suggested_name -> product_id

	for _, itemID := range req.LineItemIDs {
		// Fetch line item with suggestion data.
		var rawName string
		var suggestedName, suggestedCategory, suggestedProductID, suggestedBrand *string
		var quantity decimal.Decimal
		var unit *string
		var totalPrice decimal.Decimal
		var regularPrice, discountAmount sql.NullString
		err := tx.QueryRow(
			`SELECT li.raw_name, li.suggested_name, li.suggested_category, li.suggested_product_id,
			        li.quantity, li.unit, li.total_price, li.regular_price, li.discount_amount,
			        li.suggested_brand
			 FROM line_items li
			 WHERE li.id = ? AND li.receipt_id = ?`,
			itemID, receiptID,
		).Scan(&rawName, &suggestedName, &suggestedCategory, &suggestedProductID,
			&quantity, &unit, &totalPrice, &regularPrice, &discountAmount,
			&suggestedBrand)
		if err == sql.ErrNoRows {
			continue // skip invalid IDs
		}
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}

		// Apply per-item edits if provided.
		edit := req.Edits[itemID]

		var productID string
		var productName string

		if suggestedProductID != nil {
			// Case 1: Match to existing product.
			productID = *suggestedProductID
			// Get product name for response.
			_ = tx.QueryRow("SELECT name FROM products WHERE id = ?", productID).Scan(&productName)
			resp.MatchedCount++
			resp.ProductsMatched = append(resp.ProductsMatched, productBrief{ID: productID, Name: productName})
		} else {
			// Case 2: Create new product from suggestion.
			name := ""
			if edit.Name != nil {
				name = *edit.Name
			} else if suggestedName != nil {
				name = *suggestedName
			} else {
				name = rawName // fallback
			}

			category := ""
			if edit.Category != nil {
				category = *edit.Category
			} else if suggestedCategory != nil {
				category = *suggestedCategory
			}

			// Deduplicate: check if we already created this product in this batch.
			if existingID, ok := createdByName[strings.ToLower(name)]; ok {
				productID = existingID
				productName = name
			} else {
				// Check if product already exists in household.
				err = tx.QueryRow(
					"SELECT id FROM products WHERE household_id = ? AND LOWER(name) = LOWER(?)",
					householdID, name,
				).Scan(&productID)
				if err == sql.ErrNoRows {
					// Create new product.
					productID = uuid.New().String()
					var catPtr *string
					if category != "" {
						catPtr = &category
					}
					// Normalize brand from suggested_brand if available.
					var brandPtr *string
					if suggestedBrand != nil && *suggestedBrand != "" {
						normalized := matcher.NormalizeBrand(*suggestedBrand)
						brandPtr = &normalized
					}
					_, err = tx.Exec(
						`INSERT INTO products (id, household_id, name, category, brand, purchase_count, created_at, updated_at)
						 VALUES (?, ?, ?, ?, ?, 0, ?, ?)`,
						productID, householdID, name, catPtr, brandPtr, now, now,
					)
					if err != nil {
						return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create product"})
					}
					resp.CreatedCount++
					resp.ProductsCreated = append(resp.ProductsCreated, productBrief{ID: productID, Name: name})
				} else if err != nil {
					return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
				} else {
					// Already exists — treat as match.
					resp.MatchedCount++
					resp.ProductsMatched = append(resp.ProductsMatched, productBrief{ID: productID, Name: name})
				}
				createdByName[strings.ToLower(name)] = productID
			}
			productName = name
		}

		// Finalize: set product_id, matched = 'auto', clear suggestion.
		_, err = tx.Exec(
			"UPDATE line_items SET product_id = ?, matched = 'auto', suggested_product_id = NULL WHERE id = ?",
			productID, itemID,
		)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to update line item"})
		}

		// Create alias from raw_name -> product.
		if storeID != nil {
			normalized := matcher.Normalize(rawName)
			var aliasExists int
			_ = tx.QueryRow(
				"SELECT COUNT(*) FROM product_aliases WHERE product_id = ? AND alias = ?",
				productID, normalized,
			).Scan(&aliasExists)
			if aliasExists == 0 {
				_, _ = tx.Exec(
					"INSERT INTO product_aliases (id, product_id, alias, store_id, created_at) VALUES (?, ?, ?, ?, ?)",
					uuid.New().String(), productID, normalized, *storeID, now,
				)
			}

			// Insert product_prices record.
			unitStr := "each"
			if unit != nil {
				unitStr = *unit
			}
			if quantity.IsZero() {
				quantity = decimal.NewFromInt(1)
			}
			unitPrice := totalPrice.Div(quantity)
			isSale := regularPrice.Valid && discountAmount.Valid
			var regPriceVal, discountVal interface{}
			if regularPrice.Valid {
				regPriceVal = regularPrice.String
			}
			if discountAmount.Valid {
				discountVal = discountAmount.String
			}
			_, _ = tx.Exec(
				`INSERT INTO product_prices (id, product_id, store_id, receipt_id, receipt_date, quantity, unit, unit_price, regular_price, discount_amount, is_sale, created_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				uuid.New().String(), productID, *storeID, receiptID,
				receiptDate, quantity.String(), unitStr, unitPrice.String(),
				regPriceVal, discountVal, isSale, now,
			)
		}

		// Update product purchase stats and set default_unit if not yet set.
		_, _ = tx.Exec(
			"UPDATE products SET last_purchased_at = ?, purchase_count = purchase_count + 1, updated_at = ? WHERE id = ?",
			receiptDate, now, productID,
		)
		if unit != nil && *unit != "" {
			_, _ = tx.Exec(
				"UPDATE products SET default_unit = ? WHERE id = ? AND default_unit IS NULL",
				*unit, productID,
			)
		}
	}

	// Check if all line items are now matched; update receipt status if so.
	var unmatchedCount int
	_ = tx.QueryRow(
		"SELECT COUNT(*) FROM line_items WHERE receipt_id = ? AND matched = 'unmatched'",
		receiptID,
	).Scan(&unmatchedCount)
	if unmatchedCount == 0 {
		_, _ = tx.Exec("UPDATE receipts SET status = 'matched' WHERE id = ?", receiptID)
	}

	if err := tx.Commit(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to commit"})
	}

	return c.JSON(http.StatusOK, resp)
}

// Delete removes a receipt and its associated data.
// DELETE /api/v1/receipts/:id
func (h *ReceiptHandler) Delete(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	receiptID := c.Param("id")

	// Verify receipt belongs to household and get image_paths for cleanup.
	var imagePaths *string
	err := h.DB.QueryRow(
		"SELECT image_paths FROM receipts WHERE id = ? AND household_id = ?",
		receiptID, householdID,
	).Scan(&imagePaths)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "receipt not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	tx, err := h.DB.Begin()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer tx.Rollback()

	_, _ = tx.Exec("DELETE FROM product_prices WHERE receipt_id = ?", receiptID)
	_, _ = tx.Exec("DELETE FROM line_items WHERE receipt_id = ?", receiptID)
	_, err = tx.Exec("DELETE FROM receipts WHERE id = ?", receiptID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	if err := tx.Commit(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to commit"})
	}

	// Clean up image files.
	if imagePaths != nil {
		for _, p := range strings.Split(*imagePaths, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				_ = os.RemoveAll(filepath.Dir(p))
				break // all images in same dir
			}
		}
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "deleted"})
}
