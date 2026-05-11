package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
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
	"github.com/mstefanko/cartledger/internal/prices"
	"github.com/mstefanko/cartledger/internal/storage"
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
	ProductID            *string `json:"product_id"`
	Quantity             *string `json:"quantity"`
	Unit                 *string `json:"unit"`
	Price                *string `json:"price"`
	TotalPrice           *string `json:"total_price"`
	PackQuantityOverride *string `json:"pack_quantity_override"`
	PackUnitOverride     *string `json:"pack_unit_override"`
	PackOverrideSource   *string `json:"pack_override_source"`
	ReviewStatus         *string `json:"review_status"`
}

type manualLineItemRequest struct {
	RawName    string  `json:"raw_name"`
	ProductID  *string `json:"product_id"` // optional: user picked from autocomplete
	Quantity   *string `json:"quantity"`   // decimal string; defaults to "1"
	Unit       *string `json:"unit"`
	UnitPrice  *string `json:"unit_price"`
	TotalPrice string  `json:"total_price"` // required
}

type createLineItemRequest struct {
	RawName           string  `json:"raw_name"`
	ProductID         *string `json:"product_id"`
	Quantity          *string `json:"quantity"`
	Unit              *string `json:"unit"`
	UnitPrice         *string `json:"unit_price"`
	TotalPrice        string  `json:"total_price"`
	LineNumber        *int    `json:"line_number"`
	CountContribution *string `json:"count_contribution"`
}

type createLineItemsRequest struct {
	Items []manualLineItemRequest `json:"items"`
}

type createLineItemsResponse struct {
	CreatedCount int    `json:"created_count"`
	Status       string `json:"status"`
}

type repairPreviewRequest struct {
	Note string `json:"note"`
}

type manualReceiptRequest struct {
	StoreID     *string                 `json:"store_id"`
	ReceiptDate string                  `json:"receipt_date"` // "2006-01-02"
	Subtotal    *string                 `json:"subtotal"`
	Tax         *string                 `json:"tax"`
	Total       *string                 `json:"total"`
	Items       []manualLineItemRequest `json:"items"`
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
	ProductPackQuantity  *float64 `json:"product_pack_quantity,omitempty"`
	ProductPackUnit      *string  `json:"product_pack_unit,omitempty"`
	Category             *string  `json:"category,omitempty"`
	RawName              string   `json:"raw_name"`
	Quantity             string   `json:"quantity"`
	Unit                 *string  `json:"unit,omitempty"`
	UnitPrice            *string  `json:"unit_price,omitempty"`
	TotalPrice           string   `json:"total_price"`
	RegularPrice         *string  `json:"regular_price,omitempty"`
	DiscountAmount       *string  `json:"discount_amount,omitempty"`
	CountContribution    string   `json:"count_contribution"`
	PackQuantityOverride *string  `json:"pack_quantity_override,omitempty"`
	PackUnitOverride     *string  `json:"pack_unit_override,omitempty"`
	PackOverrideSource   *string  `json:"pack_override_source,omitempty"`
	Matched              string   `json:"matched"`
	ReviewStatus         string   `json:"review_status"`
	Confidence           *float64 `json:"confidence,omitempty"`
	LineNumber           *int     `json:"line_number,omitempty"`
	SuggestedName        *string  `json:"suggested_name,omitempty"`
	SuggestedCategory    *string  `json:"suggested_category,omitempty"`
	SuggestedProductID   *string  `json:"suggested_product_id,omitempty"`
	SuggestedProductName *string  `json:"suggested_product_name,omitempty"`
	SuggestionType       *string  `json:"suggestion_type,omitempty"`
}

type receiptWarningResponse struct {
	Code     string  `json:"code"`
	Severity string  `json:"severity"`
	Message  string  `json:"message"`
	Expected *string `json:"expected,omitempty"`
	Actual   *string `json:"actual,omitempty"`
}

type receiptImageResponse struct {
	Kind string `json:"kind"`
	Page int    `json:"page"`
	URL  string `json:"url"`
}

type receiptDetailResponse struct {
	ID                 string                   `json:"id"`
	HouseholdID        string                   `json:"household_id"`
	StoreID            *string                  `json:"store_id,omitempty"`
	StoreName          *string                  `json:"store_name,omitempty"`
	ScannedBy          *string                  `json:"scanned_by,omitempty"`
	ReceiptDate        string                   `json:"receipt_date"`
	Subtotal           *string                  `json:"subtotal,omitempty"`
	Tax                *string                  `json:"tax,omitempty"`
	Total              *string                  `json:"total,omitempty"`
	Status             string                   `json:"status"`
	LLMProvider        *string                  `json:"llm_provider,omitempty"`
	CardType           *string                  `json:"card_type,omitempty"`
	CardLast4          *string                  `json:"card_last4,omitempty"`
	ReceiptTime        *string                  `json:"receipt_time,omitempty"`
	ItemsSoldCount     *int                     `json:"items_sold_count,omitempty"`
	AccountedItemCount string                   `json:"accounted_item_count"`
	ImagePaths         *string                  `json:"image_paths,omitempty"`
	RawLLMJSON         *string                  `json:"raw_llm_json,omitempty"`
	CreatedAt          string                   `json:"created_at"`
	ErrorMessage       *string                  `json:"error_message,omitempty"`
	Warnings           []receiptWarningResponse `json:"warnings"`
	Images             []receiptImageResponse   `json:"images"`
	CanReprocess       bool                     `json:"can_reprocess"`
	LineItems          []lineItemResponse       `json:"line_items"`
}

const (
	// uploadBodyLimit caps the request body for multipart receipt uploads.
	//
	// Per-file cap is 10 MB and maxReceiptUploadFiles caps page count. 110 MB
	// allows ten max-size image pages plus multipart framing overhead while
	// still preventing pathological uploads from OOMing the process.
	uploadBodyLimit = "110M"

	maxReceiptUploadFiles    = 10
	maxReceiptUploadFileSize = 10 << 20 // 10MB
	receiptUploadMemory      = 8 << 20  // 8MB; larger multipart file parts spill to temp files.
)

// RegisterRoutes mounts receipt endpoints onto the protected group.
func (h *ReceiptHandler) RegisterRoutes(protected *echo.Group) {
	receipts := protected.Group("/receipts")
	// Cap multipart upload size before it is read into memory / disk.
	receipts.POST("/scan", h.Scan, middleware.BodyLimit(uploadBodyLimit))
	receipts.POST("/manual", h.CreateManual)
	receipts.GET("", h.List)
	receipts.POST("/compare", h.compareReceipts)
	receipts.GET("/:id/images/:kind/:page", h.ServeImage)
	receipts.GET("/:id", h.Get)
	receipts.POST("/:id/line-items/bulk", h.CreateLineItems)
	receipts.POST("/:id/line-items", h.CreateLineItem)
	receipts.PUT("/:id/line-items/:itemId", h.UpdateLineItem)
	receipts.POST("/:id/repair-preview", h.RepairPreview)
	receipts.POST("/:id/apply-repair", h.ApplyRepair)
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

	if err := c.Request().ParseMultipartForm(receiptUploadMemory); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid multipart form"})
	}
	form := c.Request().MultipartForm
	if form == nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid multipart form"})
	}
	defer form.RemoveAll()

	files := form.File["images"]
	if len(files) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "at least one image is required"})
	}
	if len(files) > maxReceiptUploadFiles {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("maximum %d pages per receipt", maxReceiptUploadFiles),
		})
	}

	allowedTypes := map[string]bool{
		"image/jpeg": true,
		"image/png":  true,
	}

	// Validate all files before saving any.
	for _, fh := range files {
		if fh.Size > maxReceiptUploadFileSize {
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
	pageSources, pageSourcesProvided, pageSourcesErr := parseReceiptPageSources(form.Value)

	// Create receipt row.
	receiptID := uuid.New().String()
	now := time.Now().UTC()
	if pageSourcesProvided {
		if pageSourcesErr != "" {
			slog.Warn("receipt scan page_sources metadata ignored", "receipt_id", receiptID, "reason", pageSourcesErr, "image_count", len(files))
		} else if !allReceiptPageSourcesPhoto(pageSources) {
			slog.Info("receipt scan page sources", "receipt_id", receiptID, "image_count", len(files), "source_count", len(pageSources), "page_sources", pageSources)
		}
	}

	localStore, err := storage.NewLocal(h.Cfg.DataDir)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "storage unavailable"})
	}

	// Save images to disk.
	type savedReceiptImage struct {
		key        string
		mimeType   string
		pageNumber int
		sizeBytes  int64
		sha256     string
	}
	savedImages := make([]savedReceiptImage, 0, len(files))
	var imagePaths []string
	for i, fh := range files {
		ext := ".jpg"
		if fh.Header.Get("Content-Type") == "image/png" {
			ext = ".png"
		}
		key, err := storage.ReceiptOriginalKey(receiptID, i+1, ext)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to prepare image storage"})
		}

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
		if err := localStore.WriteFileAtomic(key, scrubbed, 0o644); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to save image"})
		}

		imagePaths = append(imagePaths, key)
		savedImages = append(savedImages, savedReceiptImage{
			key:        key,
			mimeType:   fh.Header.Get("Content-Type"),
			pageNumber: i + 1,
			sizeBytes:  int64(len(scrubbed)),
			sha256:     storage.SHA256Hex(scrubbed),
		})
	}

	imagePathsStr := strings.Join(imagePaths, ",")

	tx, err := h.DB.BeginTx(c.Request().Context(), nil)
	if err != nil {
		_ = localStore.DeleteReceipt(receiptID)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create receipt"})
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(c.Request().Context(),
		`INSERT INTO receipts (id, household_id, scanned_by, receipt_date, image_paths, status, created_at)
		 VALUES (?, ?, ?, ?, ?, 'pending', ?)`,
		receiptID, householdID, userID, now, imagePathsStr, now,
	)
	if err != nil {
		_ = localStore.DeleteReceipt(receiptID)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create receipt"})
	}
	for _, img := range savedImages {
		if err := storage.UpsertReceiptImage(c.Request().Context(), tx, storage.ReceiptImage{
			ReceiptID:  receiptID,
			Kind:       storage.ReceiptImageKindOriginal,
			PageNumber: img.pageNumber,
			StorageKey: img.key,
			MimeType:   img.mimeType,
			SizeBytes:  img.sizeBytes,
			SHA256:     sql.NullString{String: img.sha256, Valid: true},
			CreatedAt:  now,
		}); err != nil {
			_ = localStore.DeleteReceipt(receiptID)
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to record image metadata"})
		}
	}
	if err := tx.Commit(); err != nil {
		_ = localStore.DeleteReceipt(receiptID)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create receipt"})
	}

	// Submit to worker for background processing.
	if err := h.Worker.Submit(worker.ReceiptJob{
		ReceiptID:   receiptID,
		HouseholdID: householdID,
	}); err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "server busy, please try again later"})
	}

	return c.JSON(http.StatusAccepted, map[string]string{
		"id":     receiptID,
		"status": "pending",
	})
}

func parseReceiptPageSources(values map[string][]string) ([]string, bool, string) {
	rawValues := values["page_sources"]
	if len(rawValues) == 0 {
		return nil, false, ""
	}

	if len(rawValues) == 1 {
		trimmed := strings.TrimSpace(rawValues[0])
		if strings.HasPrefix(trimmed, "[") {
			var parsed []string
			if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
				return nil, true, "invalid_json"
			}
			return normalizeReceiptPageSources(parsed), true, ""
		}
	}

	return normalizeReceiptPageSources(rawValues), true, ""
}

func normalizeReceiptPageSources(sources []string) []string {
	normalized := make([]string, 0, len(sources))
	for _, source := range sources {
		switch source {
		case "photo", "pdf_rendered":
			normalized = append(normalized, source)
		default:
			normalized = append(normalized, "unknown")
		}
	}
	return normalized
}

func allReceiptPageSourcesPhoto(sources []string) bool {
	if len(sources) == 0 {
		return false
	}
	for _, source := range sources {
		if source != "photo" {
			return false
		}
	}
	return true
}

// CreateManual handles manually-entered receipts (no image, no LLM).
// POST /api/v1/receipts/manual
func (h *ReceiptHandler) CreateManual(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	userID := auth.UserIDFrom(c)

	var req manualReceiptRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid json body"})
	}

	if strings.TrimSpace(req.ReceiptDate) == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "receipt_date is required"})
	}
	if _, err := time.Parse("2006-01-02", req.ReceiptDate); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "receipt_date must be YYYY-MM-DD"})
	}
	if len(req.Items) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "at least one item is required"})
	}
	// Validate receipt-level optional decimal fields.
	if req.Subtotal != nil && *req.Subtotal != "" {
		if _, err := decimal.NewFromString(*req.Subtotal); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "subtotal must be a decimal"})
		}
	}
	if req.Tax != nil && *req.Tax != "" {
		if _, err := decimal.NewFromString(*req.Tax); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "tax must be a decimal"})
		}
	}
	if req.Total != nil && *req.Total != "" {
		if _, err := decimal.NewFromString(*req.Total); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "total must be a decimal"})
		}
	}

	for i, it := range req.Items {
		if strings.TrimSpace(it.RawName) == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("items[%d].raw_name is required", i),
			})
		}
		if _, err := decimal.NewFromString(it.TotalPrice); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("items[%d].total_price must be a decimal", i),
			})
		}
		if it.Quantity != nil && *it.Quantity != "" {
			if _, err := decimal.NewFromString(*it.Quantity); err != nil {
				return c.JSON(http.StatusBadRequest, map[string]string{
					"error": fmt.Sprintf("items[%d].quantity must be a decimal", i),
				})
			}
		}
		if it.UnitPrice != nil && *it.UnitPrice != "" {
			if _, err := decimal.NewFromString(*it.UnitPrice); err != nil {
				return c.JSON(http.StatusBadRequest, map[string]string{
					"error": fmt.Sprintf("items[%d].unit_price must be a decimal", i),
				})
			}
		}
	}

	// Validate store belongs to household if provided.
	if req.StoreID != nil && *req.StoreID != "" {
		var exists int
		err := h.DB.QueryRow(
			`SELECT 1 FROM stores WHERE id = ? AND household_id = ?`,
			*req.StoreID, householdID,
		).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "store not found"})
		} else if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "db error validating store"})
		}
	}

	// Validate each item's product_id belongs to this household if provided.
	for i, it := range req.Items {
		if it.ProductID != nil && *it.ProductID != "" {
			var exists int
			err := h.DB.QueryRow(
				`SELECT 1 FROM products WHERE id = ? AND household_id = ?`,
				*it.ProductID, householdID,
			).Scan(&exists)
			if errors.Is(err, sql.ErrNoRows) {
				return c.JSON(http.StatusBadRequest, map[string]string{
					"error": fmt.Sprintf("items[%d].product_id not found", i),
				})
			} else if err != nil {
				return c.JSON(http.StatusInternalServerError, map[string]string{
					"error": "db error validating product"})
			}
		}
	}

	receiptID := uuid.New().String()
	now := time.Now().UTC()

	tx, err := h.DB.BeginTx(c.Request().Context(), nil)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to begin tx"})
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(c.Request().Context(), `
		INSERT INTO receipts
		    (id, household_id, store_id, scanned_by, receipt_date,
		     subtotal, tax, total, status, source, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'matched', 'manual', ?)`,
		receiptID, householdID, req.StoreID, userID, req.ReceiptDate,
		req.Subtotal, req.Tax, req.Total, now,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to insert receipt"})
	}

	engine := matcher.NewEngine(h.DB)

	for i, it := range req.Items {
		itemID := uuid.New().String()
		lineNum := i + 1

		var productID *string
		matched := "unmatched"
		var confidence *float64
		var suggestedProductID *string

		if it.ProductID != nil && *it.ProductID != "" {
			productID = it.ProductID
			matched = "manual"
		} else {
			storeIDArg := ""
			if req.StoreID != nil {
				storeIDArg = *req.StoreID
			}
			result := engine.MatchWithSuggestion(it.RawName, "", storeIDArg, householdID)
			switch result.Method {
			case "rule", "alias", "fuzzy":
				if result.ProductID != "" {
					pid := result.ProductID
					productID = &pid
					matched = result.Method
					conf := result.Confidence
					confidence = &conf
				}
			case "suggested", "cross_store_match":
				if result.ProductID != "" {
					sid := result.ProductID
					suggestedProductID = &sid
				}
			}
		}

		quantity := "1"
		if it.Quantity != nil && *it.Quantity != "" {
			quantity = *it.Quantity
		}

		_, err = tx.ExecContext(c.Request().Context(), `
			INSERT INTO line_items
			    (id, receipt_id, product_id, raw_name, quantity, unit,
			     unit_price, total_price, matched, confidence, line_number,
			     suggested_product_id, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			itemID, receiptID, productID, it.RawName, quantity, it.Unit,
			it.UnitPrice, it.TotalPrice, matched, confidence, lineNum,
			suggestedProductID, now,
		)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("failed to insert item %d: %v", i, err),
			})
		}

		if productID != nil && req.StoreID != nil && strings.TrimSpace(*req.StoreID) != "" {
			normalized := matcher.Normalize(it.RawName)
			var aliasExists int
			_ = tx.QueryRowContext(c.Request().Context(),
				"SELECT COUNT(*) FROM product_aliases WHERE product_id = ? AND alias = ?",
				*productID, normalized,
			).Scan(&aliasExists)
			if aliasExists == 0 {
				_, _ = tx.ExecContext(c.Request().Context(),
					"INSERT INTO product_aliases (id, product_id, alias, store_id, created_at) VALUES (?, ?, ?, ?, ?)",
					uuid.New().String(), *productID, normalized, *req.StoreID, now,
				)
			}
			if err := prices.RecordProductPriceFromLineItem(c.Request().Context(), tx, itemID); err != nil {
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create price record"})
			}
			if it.Unit != nil && strings.TrimSpace(*it.Unit) != "" {
				_, _ = tx.ExecContext(c.Request().Context(),
					"UPDATE products SET default_unit = ? WHERE id = ? AND default_unit IS NULL",
					*it.Unit, *productID,
				)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to commit"})
	}

	return c.JSON(http.StatusCreated, map[string]string{"id": receiptID})
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
			msg := "LLM monthly budget exhausted; raise LLM_MONTHLY_TOKEN_BUDGET or wait until next month"
			_, _ = h.DB.Exec(
				"UPDATE receipts SET status = 'error', error_message = ? WHERE id = ? AND household_id = ?",
				msg, receiptID, householdID,
			)
			return c.JSON(http.StatusServiceUnavailable, map[string]string{
				"error": msg,
			})
		}
		if h.Guard.Breaker() != nil && h.Guard.Breaker().IsOpen() {
			msg := "Receipt extraction is paused because the AI service is rate-limiting requests. Wait a few minutes, then retry extraction."
			_, _ = h.DB.Exec(
				"UPDATE receipts SET status = 'error', error_message = ? WHERE id = ? AND household_id = ?",
				msg, receiptID, householdID,
			)
			return c.JSON(http.StatusServiceUnavailable, map[string]string{
				"error": msg,
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
	res, err := h.DB.Exec(
		"UPDATE receipts SET status = 'pending', error_message = NULL WHERE id = ? AND household_id = ? AND status = ?",
		receiptID, householdID, status,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	n, err := res.RowsAffected()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if n != 1 {
		return c.JSON(http.StatusConflict, map[string]string{
			"error": "receipt status changed; refresh and try again",
		})
	}

	// 5. Re-enqueue via the worker. Resubmit re-reads image paths from disk
	// so we never require the user to re-upload.
	if err := h.Worker.Resubmit(receiptID, householdID); err != nil {
		if errors.Is(err, worker.ErrReceiptAlreadyQueued) {
			return c.JSON(http.StatusAccepted, map[string]string{
				"id":     receiptID,
				"status": "pending",
			})
		}
		// Roll back the status flip.
		msg := reprocessErrorMessage(err)
		_, _ = h.DB.Exec(
			"UPDATE receipts SET status = 'error', error_message = ? WHERE id = ? AND household_id = ?",
			msg, receiptID, householdID,
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

func reprocessErrorMessage(err error) string {
	switch {
	case errors.Is(err, worker.ErrImagesGone):
		return "receipt images are no longer on disk; please re-upload the receipt"
	case errors.Is(err, worker.ErrQueueFull), errors.Is(err, worker.ErrWorkerShuttingDown):
		return "server busy, please try again later"
	default:
		return "failed to enqueue receipt"
	}
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
		        r.card_type, r.card_last4, r.receipt_time, r.items_sold_count,
		        r.image_paths, r.raw_llm_json, r.created_at, r.error_message
		 FROM receipts r
		 LEFT JOIN stores s ON r.store_id = s.id
		 WHERE r.id = ? AND r.household_id = ?`,
		receiptID, householdID,
	).Scan(
		&resp.ID, &resp.HouseholdID, &resp.StoreID, &resp.StoreName,
		&resp.ScannedBy, &receiptDate, &subtotal, &tax, &total,
		&resp.Status, &resp.LLMProvider,
		&resp.CardType, &resp.CardLast4, &resp.ReceiptTime, &resp.ItemsSoldCount,
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
	// Do not leak raw historical image_paths values (which may be absolute
	// host paths). The compatibility field is rebuilt from receipt_images
	// below as app-relative storage keys.
	resp.ImagePaths = nil

	// Fetch line items with product info and suggestion data.
	rows, err := h.DB.Query(
		`SELECT li.id, li.receipt_id, li.product_id, p.name, p.category,
		        p.pack_quantity, p.pack_unit,
		        li.raw_name, li.quantity, li.unit, li.unit_price, li.total_price,
		        li.regular_price, li.discount_amount, li.count_contribution,
		        li.pack_quantity_override, li.pack_unit_override, li.pack_override_source,
		        li.matched, li.review_status, li.confidence, li.line_number,
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
		var quantity, totalPrice, countContribution decimal.Decimal
		var unitPrice *decimal.Decimal
		if err := rows.Scan(
			&li.ID, &li.ReceiptID, &li.ProductID, &li.ProductName, &li.Category,
			&li.ProductPackQuantity, &li.ProductPackUnit,
			&li.RawName, &quantity, &li.Unit, &unitPrice, &totalPrice,
			&li.RegularPrice, &li.DiscountAmount, &countContribution,
			&li.PackQuantityOverride, &li.PackUnitOverride, &li.PackOverrideSource,
			&li.Matched, &li.ReviewStatus, &li.Confidence, &li.LineNumber,
			&li.SuggestedName, &li.SuggestedCategory,
			&li.SuggestedProductID, &li.SuggestedProductName,
		); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		li.Quantity = quantity.String()
		li.TotalPrice = totalPrice.String()
		li.CountContribution = countContribution.String()
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

	accounted, warnings := receiptReviewWarnings(resp.ItemsSoldCount, resp.LineItems)
	resp.AccountedItemCount = accounted
	resp.Warnings = warnings
	images, canReprocess, compatImagePaths, err := h.receiptImagesForResponse(c.Request().Context(), receiptID)
	if err != nil {
		slog.Warn("receipts: load image metadata failed", "receipt_id", receiptID, "err", err)
	} else {
		resp.Images = images
		resp.CanReprocess = canReprocess
		if compatImagePaths != "" {
			resp.ImagePaths = &compatImagePaths
		} else {
			resp.ImagePaths = nil
		}
	}

	return c.JSON(http.StatusOK, resp)
}

func (h *ReceiptHandler) receiptImagesForResponse(ctx context.Context, receiptID string) ([]receiptImageResponse, bool, string, error) {
	localStore, err := storage.NewLocal(h.Cfg.DataDir)
	if err != nil {
		return nil, false, "", err
	}
	rows, err := storage.ListReceiptImages(ctx, h.DB, receiptID, "")
	if err != nil {
		return nil, false, "", err
	}
	images := make([]receiptImageResponse, 0, len(rows))
	processedKeys := make([]string, 0)
	originalKeys := make([]string, 0)
	canReprocess := false
	for _, row := range rows {
		images = append(images, receiptImageResponse{
			Kind: row.Kind,
			Page: row.PageNumber,
			URL:  receiptImageURL(receiptID, row.Kind, row.PageNumber, filepath.Ext(row.StorageKey)),
		})
		if row.Kind == storage.ReceiptImageKindProcessed {
			processedKeys = append(processedKeys, row.StorageKey)
		}
		if row.Kind == storage.ReceiptImageKindOriginal {
			originalKeys = append(originalKeys, row.StorageKey)
			if !canReprocess {
				p, err := localStore.Path(row.StorageKey)
				if err == nil {
					if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
						canReprocess = true
					}
				}
			}
		}
	}
	compat := strings.Join(processedKeys, ",")
	if compat == "" {
		compat = strings.Join(originalKeys, ",")
	}
	return images, canReprocess, compat, nil
}

func receiptImageURL(receiptID, kind string, page int, ext string) string {
	if ext == "" {
		ext = ".jpg"
	}
	return fmt.Sprintf("/api/v1/receipts/%s/images/%s/%d%s",
		url.PathEscape(receiptID), url.PathEscape(kind), page, ext)
}

// ServeImage returns a receipt-scoped image after household ownership checks.
func (h *ReceiptHandler) ServeImage(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	receiptID := c.Param("id")
	kind := c.Param("kind")
	if kind != storage.ReceiptImageKindOriginal && kind != storage.ReceiptImageKindProcessed {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
	}
	pageParam := c.Param("page")
	page, _, err := storage.ParsePageFilename(pageParam)
	if err != nil {
		page, err = strconv.Atoi(pageParam)
		if err != nil || page <= 0 {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
		}
	}

	var storageKey, mimeType string
	err = h.DB.QueryRowContext(c.Request().Context(),
		`SELECT ri.storage_key, ri.mime_type
		   FROM receipt_images ri
		   JOIN receipts r ON r.id = ri.receipt_id
		  WHERE ri.receipt_id = ?
		    AND r.household_id = ?
		    AND ri.kind = ?
		    AND ri.page_number = ?
		    AND ri.deleted_at IS NULL`,
		receiptID, householdID, kind, page,
	).Scan(&storageKey, &mimeType)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	localStore, err := storage.NewLocal(h.Cfg.DataDir)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "storage unavailable"})
	}
	path, err := localStore.Path(storageKey)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
	}
	if _, err := os.Stat(path); err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
	}
	if mimeType != "" {
		c.Response().Header().Set(echo.HeaderContentType, mimeType)
	}
	return c.File(path)
}

func receiptReviewWarnings(itemsSoldCount *int, lineItems []lineItemResponse) (string, []receiptWarningResponse) {
	accounted := decimal.Zero
	for _, item := range lineItems {
		count, err := decimal.NewFromString(item.CountContribution)
		if err != nil {
			continue
		}
		accounted = accounted.Add(count)
	}

	warnings := make([]receiptWarningResponse, 0)
	if itemsSoldCount != nil {
		expected := decimal.NewFromInt(int64(*itemsSoldCount))
		if !accounted.Equal(expected) {
			expectedStr := expected.String()
			actualStr := accounted.String()
			warnings = append(warnings, receiptWarningResponse{
				Code:     "item_count_mismatch",
				Severity: "warning",
				Message:  fmt.Sprintf("Receipt says %s items sold, but this scan accounts for %s.", expectedStr, actualStr),
				Expected: &expectedStr,
				Actual:   &actualStr,
			})
		}
	}

	return accounted.String(), warnings
}

// CreateLineItem adds a manual line item to an existing receipt.
// POST /api/v1/receipts/:id/line-items
func (h *ReceiptHandler) CreateLineItem(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	receiptID := c.Param("id")

	var req createLineItemRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	req.RawName = strings.TrimSpace(req.RawName)
	if req.RawName == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "raw_name is required"})
	}
	totalPrice, err := decimal.NewFromString(req.TotalPrice)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "total_price must be a decimal"})
	}

	quantity := decimal.NewFromInt(1)
	if req.Quantity != nil && strings.TrimSpace(*req.Quantity) != "" {
		quantity, err = decimal.NewFromString(*req.Quantity)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "quantity must be a decimal"})
		}
	}
	var unitPrice *string
	if req.UnitPrice != nil && strings.TrimSpace(*req.UnitPrice) != "" {
		if _, err := decimal.NewFromString(*req.UnitPrice); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "unit_price must be a decimal"})
		}
		unitPrice = req.UnitPrice
	}

	countContribution := deriveCountContribution(quantity, req.Unit)
	if req.CountContribution != nil && strings.TrimSpace(*req.CountContribution) != "" {
		countContribution, err = decimal.NewFromString(*req.CountContribution)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "count_contribution must be a decimal"})
		}
	}

	var storeID *string
	err = h.DB.QueryRow(
		"SELECT store_id FROM receipts WHERE id = ? AND household_id = ?",
		receiptID, householdID,
	).Scan(&storeID)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "receipt not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	var productID *string
	matched := "unmatched"
	if req.ProductID != nil && strings.TrimSpace(*req.ProductID) != "" {
		var exists int
		err := h.DB.QueryRow(
			"SELECT 1 FROM products WHERE id = ? AND household_id = ?",
			*req.ProductID, householdID,
		).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "product_id not found"})
		}
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		productID = req.ProductID
		matched = "manual"
	}

	lineNumber := 1
	if req.LineNumber != nil && *req.LineNumber > 0 {
		lineNumber = *req.LineNumber
	} else {
		_ = h.DB.QueryRow(
			"SELECT COALESCE(MAX(line_number), 0) + 1 FROM line_items WHERE receipt_id = ?",
			receiptID,
		).Scan(&lineNumber)
	}

	itemID := uuid.New().String()
	now := time.Now().UTC()

	tx, err := h.DB.Begin()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer tx.Rollback()

	_, err = tx.Exec(
		`INSERT INTO line_items
		    (id, receipt_id, product_id, raw_name, quantity, unit, unit_price,
		     total_price, matched, line_number, count_contribution, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		itemID, receiptID, productID, req.RawName, quantity.String(), req.Unit, unitPrice,
		totalPrice.String(), matched, lineNumber, countContribution.String(), now,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to insert line item"})
	}

	if productID != nil && storeID != nil {
		normalized := matcher.Normalize(req.RawName)
		var aliasExists int
		_ = tx.QueryRow(
			"SELECT COUNT(*) FROM product_aliases WHERE product_id = ? AND alias = ?",
			*productID, normalized,
		).Scan(&aliasExists)
		if aliasExists == 0 {
			_, _ = tx.Exec(
				"INSERT INTO product_aliases (id, product_id, alias, store_id, created_at) VALUES (?, ?, ?, ?, ?)",
				uuid.New().String(), *productID, normalized, *storeID, now,
			)
		}

		if err := prices.RecordProductPriceFromLineItem(c.Request().Context(), tx, itemID); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create price record"})
		}
	}

	if err := tx.Commit(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to commit"})
	}

	lineItem, err := h.lineItemResponse(receiptID, itemID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	return c.JSON(http.StatusCreated, lineItem)
}

type parsedManualLineItem struct {
	rawName           string
	productID         *string
	quantity          decimal.Decimal
	unit              *string
	unitPrice         *string
	totalPrice        decimal.Decimal
	countContribution decimal.Decimal
}

// CreateLineItems adds multiple manually-entered line items to an existing receipt.
// POST /api/v1/receipts/:id/line-items/bulk
func (h *ReceiptHandler) CreateLineItems(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	receiptID := c.Param("id")

	var req createLineItemsRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	if len(req.Items) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "at least one item is required"})
	}

	var storeID *string
	var status string
	err := h.DB.QueryRow(
		"SELECT store_id, status FROM receipts WHERE id = ? AND household_id = ?",
		receiptID, householdID,
	).Scan(&storeID, &status)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "receipt not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if status == "reviewed" {
		return c.JSON(http.StatusConflict, map[string]string{"error": "cannot add line items to a reviewed receipt"})
	}
	if status == "pending" || status == "processing" {
		return c.JSON(http.StatusConflict, map[string]string{"error": "receipt is still processing"})
	}

	parsedItems := make([]parsedManualLineItem, 0, len(req.Items))
	for i, item := range req.Items {
		parsed, err := h.parseManualLineItem(item, householdID)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("items[%d].%s", i, err.Error()),
			})
		}
		parsedItems = append(parsedItems, parsed)
	}

	tx, err := h.DB.BeginTx(c.Request().Context(), nil)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer tx.Rollback()

	var startLineNumber int
	if err := tx.QueryRowContext(
		c.Request().Context(),
		"SELECT COALESCE(MAX(line_number), 0) + 1 FROM line_items WHERE receipt_id = ?",
		receiptID,
	).Scan(&startLineNumber); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	engine := matcher.NewEngine(h.DB)
	storeIDArg := ""
	if storeID != nil {
		storeIDArg = *storeID
	}
	now := time.Now().UTC()

	for i, item := range parsedItems {
		itemID := uuid.New().String()
		productID := item.productID
		matched := "unmatched"
		var confidence *float64

		if productID != nil {
			matched = "manual"
		} else {
			result := engine.MatchWithSuggestion(item.rawName, "", storeIDArg, householdID)
			switch result.Method {
			case "rule", "alias", "fuzzy":
				if result.ProductID != "" {
					pid := result.ProductID
					productID = &pid
					matched = result.Method
					conf := result.Confidence
					confidence = &conf
				}
			}
		}

		_, err = tx.ExecContext(
			c.Request().Context(),
			`INSERT INTO line_items
			    (id, receipt_id, product_id, raw_name, quantity, unit, unit_price,
			     total_price, matched, confidence, line_number, count_contribution, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			itemID, receiptID, productID, item.rawName, item.quantity.String(), item.unit,
			item.unitPrice, item.totalPrice.String(), matched, confidence,
			startLineNumber+i, item.countContribution.String(), now,
		)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to insert line item"})
		}

		if productID != nil && storeID != nil {
			normalized := matcher.Normalize(item.rawName)
			var aliasExists int
			_ = tx.QueryRowContext(
				c.Request().Context(),
				"SELECT COUNT(*) FROM product_aliases WHERE product_id = ? AND alias = ?",
				*productID, normalized,
			).Scan(&aliasExists)
			if aliasExists == 0 {
				_, _ = tx.ExecContext(
					c.Request().Context(),
					"INSERT INTO product_aliases (id, product_id, alias, store_id, created_at) VALUES (?, ?, ?, ?, ?)",
					uuid.New().String(), *productID, normalized, *storeID, now,
				)
			}
			if err := prices.RecordProductPriceFromLineItem(c.Request().Context(), tx, itemID); err != nil {
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create price record"})
			}
			if item.unit != nil && strings.TrimSpace(*item.unit) != "" {
				_, _ = tx.ExecContext(
					c.Request().Context(),
					"UPDATE products SET default_unit = ? WHERE id = ? AND default_unit IS NULL",
					*item.unit, *productID,
				)
			}
		}
	}

	_, err = tx.ExecContext(
		c.Request().Context(),
		"UPDATE receipts SET status = 'matched', error_message = NULL WHERE id = ?",
		receiptID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to update receipt"})
	}

	if err := tx.Commit(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to commit"})
	}

	return c.JSON(http.StatusCreated, createLineItemsResponse{
		CreatedCount: len(parsedItems),
		Status:       "matched",
	})
}

func (h *ReceiptHandler) parseManualLineItem(item manualLineItemRequest, householdID string) (parsedManualLineItem, error) {
	rawName := strings.TrimSpace(item.RawName)
	if rawName == "" {
		return parsedManualLineItem{}, fmt.Errorf("raw_name is required")
	}
	totalPriceText := strings.TrimSpace(item.TotalPrice)
	totalPrice, err := decimal.NewFromString(totalPriceText)
	if err != nil {
		return parsedManualLineItem{}, fmt.Errorf("total_price must be a decimal")
	}

	quantityText := "1"
	if item.Quantity != nil && strings.TrimSpace(*item.Quantity) != "" {
		quantityText = strings.TrimSpace(*item.Quantity)
	}
	quantity, err := decimal.NewFromString(quantityText)
	if err != nil {
		return parsedManualLineItem{}, fmt.Errorf("quantity must be a decimal")
	}

	var unitPrice *string
	if item.UnitPrice != nil && strings.TrimSpace(*item.UnitPrice) != "" {
		value := strings.TrimSpace(*item.UnitPrice)
		if _, err := decimal.NewFromString(value); err != nil {
			return parsedManualLineItem{}, fmt.Errorf("unit_price must be a decimal")
		}
		unitPrice = &value
	}

	var productID *string
	if item.ProductID != nil && strings.TrimSpace(*item.ProductID) != "" {
		value := strings.TrimSpace(*item.ProductID)
		var exists int
		err := h.DB.QueryRow(
			"SELECT 1 FROM products WHERE id = ? AND household_id = ?",
			value, householdID,
		).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return parsedManualLineItem{}, fmt.Errorf("product_id not found")
		}
		if err != nil {
			return parsedManualLineItem{}, fmt.Errorf("product_id validation failed")
		}
		productID = &value
	}

	unit := trimmedStringPtr(item.Unit)
	return parsedManualLineItem{
		rawName:           rawName,
		productID:         productID,
		quantity:          quantity,
		unit:              unit,
		unitPrice:         unitPrice,
		totalPrice:        totalPrice,
		countContribution: deriveCountContribution(quantity, unit),
	}, nil
}

func trimmedStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func deriveCountContribution(quantity decimal.Decimal, unit *string) decimal.Decimal {
	unitStr := ""
	if unit != nil {
		unitStr = strings.ToLower(strings.TrimSpace(*unit))
	}
	switch unitStr {
	case "", "each", "ea", "pack", "ct", "count", "gal", "qt", "pt":
		if quantity.GreaterThan(decimal.Zero) && quantity.Equal(quantity.Round(0)) {
			return quantity
		}
	}
	return decimal.NewFromInt(1)
}

func nullableTrimmedString(value string) interface{} {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func nullableDecimalString(value string) (interface{}, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if _, err := decimal.NewFromString(value); err != nil {
		return nil, err
	}
	return value, nil
}

func nullableReceiptTime(value string) (interface{}, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "<unknown>") {
		return nil, nil
	}
	if parsed, err := time.Parse("15:04", value); err == nil {
		return parsed.Format("15:04"), nil
	}
	if parsed, err := time.Parse("15:04:05", value); err == nil {
		return parsed.Format("15:04:05"), nil
	}
	return nil, fmt.Errorf("invalid receipt time")
}

func (h *ReceiptHandler) lineItemResponse(receiptID, itemID string) (lineItemResponse, error) {
	var li lineItemResponse
	var quantity, totalPrice, countContribution decimal.Decimal
	var unitPrice *decimal.Decimal
	err := h.DB.QueryRow(
		`SELECT li.id, li.receipt_id, li.product_id, p.name, p.category,
		        p.pack_quantity, p.pack_unit,
		        li.raw_name, li.quantity, li.unit, li.unit_price, li.total_price,
		        li.regular_price, li.discount_amount, li.count_contribution,
		        li.pack_quantity_override, li.pack_unit_override, li.pack_override_source,
		        li.matched, li.review_status, li.confidence, li.line_number,
		        li.suggested_name, li.suggested_category,
		        li.suggested_product_id, sp.name
		 FROM line_items li
		 LEFT JOIN products p ON li.product_id = p.id
		 LEFT JOIN products sp ON li.suggested_product_id = sp.id
		 WHERE li.receipt_id = ? AND li.id = ?`,
		receiptID, itemID,
	).Scan(
		&li.ID, &li.ReceiptID, &li.ProductID, &li.ProductName, &li.Category,
		&li.ProductPackQuantity, &li.ProductPackUnit,
		&li.RawName, &quantity, &li.Unit, &unitPrice, &totalPrice,
		&li.RegularPrice, &li.DiscountAmount, &countContribution,
		&li.PackQuantityOverride, &li.PackUnitOverride, &li.PackOverrideSource,
		&li.Matched, &li.ReviewStatus, &li.Confidence, &li.LineNumber,
		&li.SuggestedName, &li.SuggestedCategory,
		&li.SuggestedProductID, &li.SuggestedProductName,
	)
	if err != nil {
		return li, err
	}
	li.Quantity = quantity.String()
	li.TotalPrice = totalPrice.String()
	li.CountContribution = countContribution.String()
	if unitPrice != nil {
		s := unitPrice.String()
		li.UnitPrice = &s
	}
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
	return li, nil
}

// RepairPreview asks the LLM for a contextual, non-destructive repair proposal.
// POST /api/v1/receipts/:id/repair-preview
func (h *ReceiptHandler) RepairPreview(c echo.Context) error {
	if h.Guard == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "receipt repair is not available"})
	}

	householdID := auth.HouseholdIDFrom(c)
	receiptID := c.Param("id")

	var req repairPreviewRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	req.Note = strings.TrimSpace(req.Note)
	if req.Note == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "note is required"})
	}

	var rawJSON sql.NullString
	err := h.DB.QueryRow(
		"SELECT raw_llm_json FROM receipts WHERE id = ? AND household_id = ?",
		receiptID, householdID,
	).Scan(&rawJSON)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "receipt not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	images, err := h.receiptImages(receiptID)
	if err != nil {
		return c.JSON(http.StatusGone, map[string]string{"error": "receipt images are no longer on disk"})
	}

	currentJSON := "{}"
	if rawJSON.Valid && strings.TrimSpace(rawJSON.String) != "" {
		currentJSON = rawJSON.String
	}

	extraction, err := h.Guard.RepairForHousehold(householdID, images, currentJSON, req.Note)
	if err != nil {
		switch {
		case errors.Is(err, llm.ErrBudgetExceeded):
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "LLM monthly budget exhausted"})
		case errors.Is(err, llm.ErrCircuitOpen):
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "receipt extraction is temporarily rate-limited"})
		case errors.Is(err, llm.ErrUnsupportedRepair):
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "receipt repair is not supported by this LLM provider"})
		default:
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to repair receipt"})
		}
	}

	return c.JSON(http.StatusOK, extraction)
}

// ApplyRepair accepts a repair preview and replaces the receipt's extracted
// line items with the normalized repaired set.
// POST /api/v1/receipts/:id/apply-repair
func (h *ReceiptHandler) ApplyRepair(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	receiptID := c.Param("id")

	var extraction llm.ReceiptExtraction
	if err := c.Bind(&extraction); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	if len(extraction.Items) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "repair preview has no items"})
	}

	var storeID *string
	var status string
	err := h.DB.QueryRow(
		"SELECT store_id, status FROM receipts WHERE id = ? AND household_id = ?",
		receiptID, householdID,
	).Scan(&storeID, &status)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "receipt not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if status == "reviewed" {
		return c.JSON(http.StatusConflict, map[string]string{"error": "cannot repair a reviewed receipt"})
	}

	extraction.Items = worker.NormalizeExtractedItems(extraction.Items)
	rawJSON, err := json.Marshal(extraction)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid repair preview"})
	}

	tx, err := h.DB.Begin()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer tx.Rollback()

	preserved, err := loadPreservedMatches(tx, receiptID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	if _, err = tx.Exec("DELETE FROM product_prices WHERE receipt_id = ?", receiptID); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to clear old prices"})
	}
	if _, err = tx.Exec("DELETE FROM line_items WHERE receipt_id = ?", receiptID); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to clear old line items"})
	}

	now := time.Now().UTC()
	for _, item := range extraction.Items {
		if strings.TrimSpace(item.RawName) == "" {
			continue
		}
		if err := insertRepairedLineItem(tx, receiptID, storeID, item, preserved, now); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to apply repaired items"})
		}
	}

	subtotal := decimal.NewFromFloat(extraction.Subtotal).String()
	tax := decimal.NewFromFloat(extraction.Tax).String()
	total := decimal.NewFromFloat(extraction.Total).String()
	_, err = tx.Exec(
		`UPDATE receipts
		 SET subtotal = ?, tax = ?, total = ?, items_sold_count = ?, raw_llm_json = ?, status = 'matched'
		 WHERE id = ?`,
		subtotal, tax, total, extraction.ItemsSoldCount, string(rawJSON), receiptID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to update receipt"})
	}

	if err := tx.Commit(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to commit repair"})
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "applied"})
}

type preservedLineMatch struct {
	ProductID          *string
	Matched            string
	SuggestedProductID *string
}

func loadPreservedMatches(tx *sql.Tx, receiptID string) (map[string]preservedLineMatch, error) {
	rows, err := tx.Query(
		`SELECT raw_name, product_id, matched, suggested_product_id
		 FROM line_items
		 WHERE receipt_id = ?`,
		receiptID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	matches := make(map[string]preservedLineMatch)
	for rows.Next() {
		var rawName, matched string
		var productID, suggestedProductID *string
		if err := rows.Scan(&rawName, &productID, &matched, &suggestedProductID); err != nil {
			return nil, err
		}
		key := matcher.Normalize(rawName)
		if key == "" {
			continue
		}
		if existing, ok := matches[key]; ok && existing.ProductID != nil {
			continue
		}
		matches[key] = preservedLineMatch{
			ProductID:          productID,
			Matched:            matched,
			SuggestedProductID: suggestedProductID,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return matches, nil
}

func insertRepairedLineItem(tx *sql.Tx, receiptID string, storeID *string, item llm.ExtractedItem, preserved map[string]preservedLineMatch, now time.Time) error {
	quantity := decimal.NewFromFloat(item.Quantity)
	if quantity.IsZero() {
		quantity = decimal.NewFromInt(1)
	}
	countContribution := decimal.NewFromFloat(item.CountContribution)
	if countContribution.IsZero() {
		countContribution = deriveCountContribution(quantity, item.Unit)
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

	var productID, suggestedProductID *string
	matched := "unmatched"
	if preservedMatch, ok := preserved[matcher.Normalize(item.RawName)]; ok {
		productID = preservedMatch.ProductID
		suggestedProductID = preservedMatch.SuggestedProductID
		if productID != nil {
			matched = preservedMatch.Matched
			if matched == "" || matched == "unmatched" {
				matched = "manual"
			}
		}
	}

	var confidence *float64
	if item.Confidence > 0 {
		conf := item.Confidence
		confidence = &conf
	}

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

	lineItemID := uuid.New().String()
	_, err := tx.Exec(
		`INSERT INTO line_items
		    (id, receipt_id, product_id, raw_name, quantity, unit, unit_price,
		     total_price, regular_price, discount_amount, count_contribution,
		     suggested_name, suggested_category, suggested_brand, suggested_product_id,
		     matched, confidence, line_number, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		lineItemID, receiptID, productID, item.RawName, quantity.String(), item.Unit, unitPrice,
		totalPrice.String(), regularPrice, discountAmount, countContribution.String(),
		suggestedName, suggestedCategory, suggestedBrand, suggestedProductID,
		matched, confidence, item.LineNumber, now,
	)
	if err != nil {
		return err
	}

	if productID != nil && storeID != nil {
		err = prices.RecordProductPriceFromLineItem(context.Background(), tx, lineItemID)
	}
	return err
}

func (h *ReceiptHandler) receiptImages(receiptID string) ([][]byte, error) {
	localStore, err := storage.NewLocal(h.Cfg.DataDir)
	if err == nil {
		rows, rerr := storage.ListReceiptImages(context.Background(), h.DB, receiptID, storage.ReceiptImageKindOriginal)
		if rerr == nil && len(rows) > 0 {
			images := make([][]byte, 0, len(rows))
			for _, row := range rows {
				data, err := localStore.ReadFile(row.StorageKey)
				if err != nil {
					return nil, err
				}
				images = append(images, data)
			}
			return images, nil
		}
	}

	imageDir, err := storage.LegacyReceiptDir(h.Cfg.DataDir, receiptID)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(imageDir)
	if err != nil {
		return nil, err
	}

	var images [][]byte
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), "processed_") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(imageDir, entry.Name()))
		if err != nil {
			return nil, err
		}
		images = append(images, data)
	}
	if len(images) == 0 {
		return nil, fmt.Errorf("no receipt images found")
	}
	return images, nil
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
	reviewSensitiveChange := false
	settingAccepted := false
	clearingProduct := false

	if req.ProductID != nil {
		productID := strings.TrimSpace(*req.ProductID)
		if productID == "" {
			clearingProduct = true
			setClauses = append(setClauses, "product_id = NULL", "matched = 'unmatched'", "confidence = NULL")
		} else {
			var productExists int
			err := h.DB.QueryRow(
				"SELECT 1 FROM products WHERE id = ? AND household_id = ?",
				productID, householdID,
			).Scan(&productExists)
			if errors.Is(err, sql.ErrNoRows) {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "product_id not found"})
			}
			if err != nil {
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
			}
			setClauses = append(setClauses, "product_id = ?", "matched = 'manual'")
			args = append(args, productID)
		}
		reviewSensitiveChange = true
	}
	if req.Quantity != nil {
		setClauses = append(setClauses, "quantity = ?")
		args = append(args, *req.Quantity)
		reviewSensitiveChange = true
	}
	if req.Unit != nil {
		setClauses = append(setClauses, "unit = ?")
		args = append(args, *req.Unit)
		reviewSensitiveChange = true
	}
	if req.Price != nil {
		setClauses = append(setClauses, "total_price = ?")
		args = append(args, *req.Price)
		reviewSensitiveChange = true
	}
	if req.TotalPrice != nil {
		setClauses = append(setClauses, "total_price = ?")
		args = append(args, *req.TotalPrice)
		reviewSensitiveChange = true
	}
	if req.PackQuantityOverride != nil {
		setClauses = append(setClauses, "pack_quantity_override = ?")
		args = append(args, nullableTrimmedString(*req.PackQuantityOverride))
		reviewSensitiveChange = true
	}
	if req.PackUnitOverride != nil {
		setClauses = append(setClauses, "pack_unit_override = ?")
		args = append(args, nullableTrimmedString(*req.PackUnitOverride))
		reviewSensitiveChange = true
	}
	if req.PackOverrideSource != nil {
		source := strings.TrimSpace(*req.PackOverrideSource)
		if source != "" && source != "user" && source != "import" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "pack_override_source must be user or import"})
		}
		setClauses = append(setClauses, "pack_override_source = ?")
		args = append(args, nullableTrimmedString(source))
		reviewSensitiveChange = true
	}
	if req.ReviewStatus != nil {
		status := strings.TrimSpace(*req.ReviewStatus)
		if status != "pending" && status != "accepted" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "review_status must be pending or accepted"})
		}
		if status == "accepted" {
			settingAccepted = true
		}
		if settingAccepted && clearingProduct {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "match line item before accepting it"})
		}
		setClauses = append(setClauses, "review_status = ?")
		args = append(args, status)
	} else if reviewSensitiveChange {
		setClauses = append(setClauses, "review_status = 'pending'")
	}

	if len(setClauses) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "no fields to update"})
	}

	if settingAccepted && req.ProductID == nil {
		var productID sql.NullString
		err := h.DB.QueryRow(
			"SELECT product_id FROM line_items WHERE id = ? AND receipt_id = ?",
			itemID, receiptID,
		).Scan(&productID)
		if err == sql.ErrNoRows {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "line item not found"})
		}
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		if !productID.Valid || strings.TrimSpace(productID.String) == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "match line item before accepting it"})
		}
	}

	args = append(args, itemID, receiptID)
	query := fmt.Sprintf(
		"UPDATE line_items SET %s WHERE id = ? AND receipt_id = ?",
		strings.Join(setClauses, ", "),
	)

	tx, err := h.DB.BeginTx(c.Request().Context(), nil)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(c.Request().Context(), query, args...)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "line item not found"})
	}

	if err := prices.RecordProductPriceFromLineItem(c.Request().Context(), tx, itemID); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to update price record"})
	}
	if err := tx.Commit(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to commit"})
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "updated"})
}

// UpdateReceipt updates editable receipt metadata and status.
// PUT /api/v1/receipts/:id
func (h *ReceiptHandler) UpdateReceipt(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	receiptID := c.Param("id")

	var req struct {
		Status      *string `json:"status"`
		StoreID     *string `json:"store_id"`
		ReceiptDate *string `json:"receipt_date"`
		ReceiptTime *string `json:"receipt_time"`
		Subtotal    *string `json:"subtotal"`
		Tax         *string `json:"tax"`
		Total       *string `json:"total"`
		CardType    *string `json:"card_type"`
		CardLast4   *string `json:"card_last4"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	setClauses := make([]string, 0)
	args := make([]interface{}, 0)
	priceSensitiveChange := false
	responseStatus := "updated"

	if req.Status != nil {
		status := strings.TrimSpace(*req.Status)
		allowedStatuses := map[string]bool{"reviewed": true, "matched": true}
		if !allowedStatuses[status] {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid status"})
		}
		setClauses = append(setClauses, "status = ?")
		args = append(args, status)
		responseStatus = status
	}
	if req.StoreID != nil {
		storeID := strings.TrimSpace(*req.StoreID)
		if storeID != "" {
			var exists int
			err := h.DB.QueryRow(
				"SELECT 1 FROM stores WHERE id = ? AND household_id = ?",
				storeID, householdID,
			).Scan(&exists)
			if errors.Is(err, sql.ErrNoRows) {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "store_id not found"})
			}
			if err != nil {
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
			}
		}
		setClauses = append(setClauses, "store_id = ?")
		args = append(args, nullableTrimmedString(storeID))
		priceSensitiveChange = true
	}
	if req.ReceiptDate != nil {
		receiptDate := strings.TrimSpace(*req.ReceiptDate)
		if receiptDate == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "receipt_date is required"})
		}
		if _, err := time.Parse("2006-01-02", receiptDate); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "receipt_date must be YYYY-MM-DD"})
		}
		setClauses = append(setClauses, "receipt_date = ?")
		args = append(args, receiptDate)
		priceSensitiveChange = true
	}
	if req.ReceiptTime != nil {
		receiptTime, err := nullableReceiptTime(*req.ReceiptTime)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "receipt_time must be HH:MM"})
		}
		setClauses = append(setClauses, "receipt_time = ?")
		args = append(args, receiptTime)
	}
	if req.Subtotal != nil {
		value, err := nullableDecimalString(*req.Subtotal)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "subtotal must be a decimal"})
		}
		setClauses = append(setClauses, "subtotal = ?")
		args = append(args, value)
	}
	if req.Tax != nil {
		value, err := nullableDecimalString(*req.Tax)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "tax must be a decimal"})
		}
		setClauses = append(setClauses, "tax = ?")
		args = append(args, value)
	}
	if req.Total != nil {
		value, err := nullableDecimalString(*req.Total)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "total must be a decimal"})
		}
		setClauses = append(setClauses, "total = ?")
		args = append(args, value)
	}
	if req.CardType != nil {
		setClauses = append(setClauses, "card_type = ?")
		args = append(args, nullableTrimmedString(*req.CardType))
	}
	if req.CardLast4 != nil {
		last4 := strings.TrimSpace(*req.CardLast4)
		if last4 != "" && len(last4) != 4 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "card_last4 must be 4 digits"})
		}
		setClauses = append(setClauses, "card_last4 = ?")
		args = append(args, nullableTrimmedString(last4))
	}
	if len(setClauses) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "no fields to update"})
	}

	tx, err := h.DB.BeginTx(c.Request().Context(), nil)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer tx.Rollback()

	var exists int
	err = tx.QueryRowContext(
		c.Request().Context(),
		"SELECT 1 FROM receipts WHERE id = ? AND household_id = ?",
		receiptID, householdID,
	).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "receipt not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	if req.Status != nil && strings.TrimSpace(*req.Status) == "reviewed" {
		var pendingCount int
		if err := tx.QueryRowContext(
			c.Request().Context(),
			"SELECT COUNT(*) FROM line_items WHERE receipt_id = ? AND review_status <> 'accepted'",
			receiptID,
		).Scan(&pendingCount); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		if pendingCount > 0 {
			return c.JSON(http.StatusConflict, map[string]string{"error": "all line items must be reviewed before confirming receipt"})
		}
	}

	args = append(args, receiptID, householdID)
	query := fmt.Sprintf(
		"UPDATE receipts SET %s WHERE id = ? AND household_id = ?",
		strings.Join(setClauses, ", "),
	)
	result, err := tx.ExecContext(c.Request().Context(), query, args...)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "receipt not found"})
	}

	if priceSensitiveChange {
		rows, err := tx.QueryContext(
			c.Request().Context(),
			"SELECT id FROM line_items WHERE receipt_id = ?",
			receiptID,
		)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		lineItemIDs := make([]string, 0)
		for rows.Next() {
			var lineItemID string
			if err := rows.Scan(&lineItemID); err != nil {
				rows.Close()
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
			}
			lineItemIDs = append(lineItemIDs, lineItemID)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		rows.Close()
		for _, lineItemID := range lineItemIDs {
			if err := prices.RecordProductPriceFromLineItem(c.Request().Context(), tx, lineItemID); err != nil {
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to update price records"})
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to commit"})
	}

	return c.JSON(http.StatusOK, map[string]string{"status": responseStatus})
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
	CreatedCount    int            `json:"created_count"`
	MatchedCount    int            `json:"matched_count"`
	ProductsCreated []productBrief `json:"products_created"`
	ProductsMatched []productBrief `json:"products_matched"`
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
	err := h.DB.QueryRow(
		"SELECT store_id FROM receipts WHERE id = ? AND household_id = ?",
		receiptID, householdID,
	).Scan(&storeID)
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
		var unit *string
		err := tx.QueryRow(
			`SELECT li.raw_name, li.suggested_name, li.suggested_category, li.suggested_product_id,
			        li.unit, li.suggested_brand
			 FROM line_items li
			 WHERE li.id = ? AND li.receipt_id = ?`,
			itemID, receiptID,
		).Scan(&rawName, &suggestedName, &suggestedCategory, &suggestedProductID,
			&unit, &suggestedBrand)
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

			if err := prices.RecordProductPriceFromLineItem(c.Request().Context(), tx, itemID); err != nil {
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create price record"})
			}
		}

		// Set default_unit if not yet set.
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

	// Verify receipt belongs to household before deleting DB rows/files.
	var exists int
	err := h.DB.QueryRow(
		"SELECT 1 FROM receipts WHERE id = ? AND household_id = ?",
		receiptID, householdID,
	).Scan(&exists)
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

	localStore, err := storage.NewLocal(h.Cfg.DataDir)
	if err == nil {
		_ = localStore.DeleteReceipt(receiptID)
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "deleted"})
}
