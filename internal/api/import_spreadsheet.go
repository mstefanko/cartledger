package api

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"golang.org/x/time/rate"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/matcher"
	"github.com/mstefanko/cartledger/internal/spreadsheet"
)

// ImportSpreadsheetHandler serves the CSV/XLSX spreadsheet import flow.
// See PLAN-spreadsheet-import.md §Backend → Endpoints.
//
// State is held in two places:
//   - On disk: $DATA_DIR/import-staging/{import_id}/{raw.ext, staging.json}
//   - In memory: a sync.Map cache of *spreadsheet.Staging keyed by import_id,
//     plus a single-entry preview response cache per import_id (plan §Risks
//     — "short-circuit identical preview requests").
//
// A background janitor (started in NewImportSpreadsheetHandler) walks the
// staging directory every 30 minutes and deletes directories idle for >24h.
// Phase 12 formalizes this; for now the goroutine runs inside the handler.
type ImportSpreadsheetHandler struct {
	DB          *sql.DB
	Cfg         *config.Config
	MatchEngine spreadsheet.MatchEngine

	// staging is an in-memory cache of *spreadsheet.Staging, keyed by
	// import_id. Writes go through stagingMu so concurrent uploads don't
	// trample each other; reads use sync.Map's concurrent Load.
	staging sync.Map // map[string]*spreadsheet.Staging

	// previewCache stores one cached response per import_id, invalidated on
	// every transform (import_revision bump) or when a different config key
	// arrives. Single-entry keeps the memory footprint bounded — the common
	// case is debounce-spam with identical inputs.
	previewCache sync.Map // map[string]previewCacheEntry

	// uploadLimiter enforces upload 1/min/household (plan §Endpoints).
	// Preview has a looser limiter; commit uses the router's standard write
	// tier — these are the two buckets we keep locally because neither the
	// global rate limiter nor its middleware path exposes a hook to tighten
	// a single route post-registration.
	uploadLimiter  *householdLimiter
	previewLimiter *householdLimiter

	// stopJanitor is closed on handler shutdown to stop the background
	// cleanup goroutine. Kept for tests that want a clean teardown.
	stopJanitor chan struct{}

	// now is injected for deterministic tests. Production uses time.Now.
	now func() time.Time
}

// NewImportSpreadsheetHandler constructs the handler and starts the
// background staging janitor.
func NewImportSpreadsheetHandler(db *sql.DB, cfg *config.Config, matchEngine spreadsheet.MatchEngine) *ImportSpreadsheetHandler {
	h := &ImportSpreadsheetHandler{
		DB:             db,
		Cfg:            cfg,
		MatchEngine:    matchEngine,
		uploadLimiter:  newHouseholdLimiter(rate.Every(time.Minute), 1),
		previewLimiter: newHouseholdLimiter(rate.Limit(0.5), 30), // 30/min burst
		stopJanitor:    make(chan struct{}),
		now:            func() time.Time { return time.Now().UTC() },
	}
	go h.runJanitor()
	return h
}

// Close stops the janitor goroutine. Safe to call multiple times.
func (h *ImportSpreadsheetHandler) Close() {
	select {
	case <-h.stopJanitor:
	default:
		close(h.stopJanitor)
	}
}

// RegisterRoutes mounts all six spreadsheet-import endpoints on protected.
// The upload route additionally applies middleware.BodyLimit("10M") — Echo
// returns 413 when the Content-Length exceeds the cap before the handler
// reads a byte (plan §Endpoints file-size cap).
func (h *ImportSpreadsheetHandler) RegisterRoutes(protected *echo.Group) {
	g := protected.Group("/import/spreadsheet")
	g.POST("/upload",
		h.Upload,
		middleware.BodyLimit("10M"),
		h.uploadRateLimit(),
	)
	g.GET("/:import_id/sheet/:sheet_name", h.GetSheet)
	g.POST("/:import_id/transform", h.Transform)
	g.POST("/:import_id/preview", h.Preview, h.previewRateLimit())
	g.POST("/:import_id/commit", h.Commit)
	g.DELETE("/:import_id", h.Delete)
}

// ---------------------------------------------------------------------------
// Per-household rate limiter (local, minimal).
//
// The global RateLimiter in ratelimit.go only exposes tier-level knobs
// (read/write/worker-submit). Upload needs a tighter 1/min cap than any tier,
// and preview needs a roomier 30/min cap that's still tighter than `read`.
// Rather than introduce new tiers (which the limiter comments say are
// intentionally not extensible without load-test data), we run two small
// rate.Limiter maps here, keyed on household_id.
// ---------------------------------------------------------------------------

type householdLimiter struct {
	r     rate.Limit
	b     int
	store sync.Map // map[string]*limiterHH
}

type limiterHH struct {
	lim      *rate.Limiter
	lastSeen time.Time
	mu       sync.Mutex
}

func newHouseholdLimiter(r rate.Limit, b int) *householdLimiter {
	return &householdLimiter{r: r, b: b}
}

func (hl *householdLimiter) allow(householdID string) bool {
	if householdID == "" {
		return true // no household -> defer to outer auth layer to reject
	}
	v, ok := hl.store.Load(householdID)
	if !ok {
		fresh := &limiterHH{lim: rate.NewLimiter(hl.r, hl.b), lastSeen: time.Now()}
		v, _ = hl.store.LoadOrStore(householdID, fresh)
	}
	e := v.(*limiterHH)
	e.mu.Lock()
	e.lastSeen = time.Now()
	e.mu.Unlock()
	return e.lim.Allow()
}

func (h *ImportSpreadsheetHandler) uploadRateLimit() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			hid := auth.HouseholdIDFrom(c)
			if !h.uploadLimiter.allow(hid) {
				c.Response().Header().Set("Retry-After", "60")
				return c.JSON(http.StatusTooManyRequests, map[string]any{
					"error":       "rate_limited",
					"retry_after": 60,
				})
			}
			return next(c)
		}
	}
}

func (h *ImportSpreadsheetHandler) previewRateLimit() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			hid := auth.HouseholdIDFrom(c)
			if !h.previewLimiter.allow(hid) {
				c.Response().Header().Set("Retry-After", "2")
				return c.JSON(http.StatusTooManyRequests, map[string]any{
					"error":       "rate_limited",
					"retry_after": 2,
				})
			}
			return next(c)
		}
	}
}

// ---------------------------------------------------------------------------
// Staging cache helpers.
// ---------------------------------------------------------------------------

// stagingDir returns the on-disk directory for an import_id under DataDir.
func (h *ImportSpreadsheetHandler) stagingDir() string {
	return filepath.Join(h.Cfg.DataDir, "import-staging")
}

// loadStaging resolves an import_id to its *spreadsheet.Staging, enforcing
// household ownership. Returns (nil, false) when the staging is missing OR
// does not belong to the caller — 404 in both cases (plan §Auth — do not
// leak existence across households).
func (h *ImportSpreadsheetHandler) loadStaging(importID, householdID string) (*spreadsheet.Staging, bool) {
	v, ok := h.staging.Load(importID)
	if !ok {
		return nil, false
	}
	s := v.(*spreadsheet.Staging)
	if s.HouseholdID != householdID {
		return nil, false
	}
	return s, true
}

// persistStaging writes staging.json to disk and touches LastActiveAt. All
// calls go through this so the janitor's "idle > 24h" check sees a fresh
// mtime after every preview/transform.
func (h *ImportSpreadsheetHandler) persistStaging(s *spreadsheet.Staging) error {
	s.LastActiveAt = h.now()
	return spreadsheet.SaveStaging(h.stagingDir(), s)
}

// ---------------------------------------------------------------------------
// Upload.
// ---------------------------------------------------------------------------

// uploadSheetResponse is the per-sheet entry in the upload response. Field
// names are snake_case to match the plan's documented shape.
type uploadSheetResponse struct {
	Name          string                                `json:"name"`
	RowCount      int                                   `json:"row_count"`
	Headers       []string                              `json:"headers"`
	ColumnSamples map[string][]string                   `json:"column_samples"`
	TypeCoverage  map[string]spreadsheet.TypeCoverage   `json:"type_coverage"`
}

type suggestedConfig struct {
	Sheet       string                 `json:"sheet"`
	Mapping     map[string]int         `json:"mapping"`
	DateFormat  spreadsheet.DateFormat `json:"date_format"`
	CSVOptions  spreadsheet.CSVOptions `json:"csv_options"`
	UnitOptions spreadsheet.UnitOptions `json:"unit_options"`
	Grouping    spreadsheet.Grouping   `json:"grouping"`
	Confidence  float64                `json:"confidence"`
}

type savedMappingResponse struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	LastUsedAt string `json:"last_used_at,omitempty"`
}

type uploadResponse struct {
	ImportID             string                 `json:"import_id"`
	Sheets               []uploadSheetResponse  `json:"sheets"`
	Suggested            suggestedConfig        `json:"suggested"`
	SavedMappings        []savedMappingResponse `json:"saved_mappings"`
	Fingerprint          string                 `json:"fingerprint"`
	AutoAppliedMappingID *string                `json:"auto_applied_mapping_id"`
}

// Upload handles POST /api/v1/import/spreadsheet/upload.
//
// Parses the uploaded CSV/XLSX once, caches the ParsedSheet set in memory +
// on disk, computes a header fingerprint, and returns a suggested mapping
// for the first non-empty sheet. Errors follow a consistent JSON shape:
// 400 for malformed form/parse errors, 415 for disallowed file types, 413
// for oversize uploads (emitted by middleware.BodyLimit).
func (h *ImportSpreadsheetHandler) Upload(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	if householdID == "" {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "missing file"})
	}

	sourceType, ext, ok := classifyUpload(fileHeader)
	if !ok {
		return c.JSON(http.StatusUnsupportedMediaType, map[string]string{
			"error": "unsupported file type (accepted: .csv, .tsv, .xlsx)",
		})
	}

	src, err := fileHeader.Open()
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "cannot read upload"})
	}
	defer src.Close()

	importID := uuid.New().String()
	dir := filepath.Join(h.stagingDir(), importID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Error("spreadsheet upload: mkdir failed", "err", err, "dir", dir)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "storage unavailable"})
	}
	rawPath := filepath.Join(dir, "raw."+ext)
	if err := saveMultipart(src, rawPath); err != nil {
		_ = os.RemoveAll(dir)
		slog.Error("spreadsheet upload: write raw failed", "err", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "write failed"})
	}

	// Re-open the persisted file for parsing (so the parser sees exactly
	// what's on disk — and we avoid holding the multipart reader open).
	rf, err := os.Open(rawPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "read failed"})
	}
	defer rf.Close()

	parsed := map[string]*spreadsheet.ParsedSheet{}
	var sheetOrder []string
	switch sourceType {
	case "csv":
		ps, err := spreadsheet.ParseCSV(rf, spreadsheet.DefaultCSVOptions())
		if err != nil {
			_ = os.RemoveAll(dir)
			return c.JSON(http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("parse csv: %v", err)})
		}
		ps.Name = "Sheet1"
		parsed[ps.Name] = ps
		sheetOrder = []string{ps.Name}
	case "xlsx":
		names, psMap, err := spreadsheet.ParseXLSX(rf)
		if err != nil {
			_ = os.RemoveAll(dir)
			return c.JSON(http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("parse xlsx: %v", err)})
		}
		for _, n := range names {
			ps := psMap[n]
			if ps == nil || len(ps.Rows) == 0 {
				continue
			}
			parsed[n] = ps
			sheetOrder = append(sheetOrder, n)
		}
		if len(sheetOrder) == 0 {
			_ = os.RemoveAll(dir)
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "xlsx has no non-empty sheets"})
		}
	}

	// Build suggested config from the first non-empty sheet.
	firstName := sheetOrder[0]
	first := parsed[firstName]
	suggested := h.buildSuggested(firstName, first, sourceType)

	// Fingerprint: sha256(sheet_name || normalized_headers || column_count).
	fingerprint := computeFingerprint(firstName, first.Headers)

	// Saved-mapping auto-apply: look up household's mappings.
	savedMappings, autoApplyID := h.lookupSavedMappings(c.Request().Context(), householdID, fingerprint)

	now := h.now()
	s := &spreadsheet.Staging{
		ImportID:       importID,
		HouseholdID:    householdID,
		RawPath:        rawPath,
		SourceFilename: fileHeader.Filename,
		SourceType:     sourceType,
		Fingerprint:    fingerprint,
		Parsed:         parsed,
		Chain:          spreadsheet.TransformChain{Revision: 0},
		CreatedAt:      now,
		LastActiveAt:   now,
	}
	h.staging.Store(importID, s)
	if err := spreadsheet.SaveStaging(h.stagingDir(), s); err != nil {
		h.staging.Delete(importID)
		_ = os.RemoveAll(dir)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "persist failed"})
	}

	resp := uploadResponse{
		ImportID:             importID,
		Sheets:               buildSheetsResponse(parsed, sheetOrder),
		Suggested:            suggested,
		SavedMappings:        savedMappings,
		Fingerprint:          fingerprint,
		AutoAppliedMappingID: autoApplyID,
	}
	return c.JSON(http.StatusOK, resp)
}

// saveMultipart streams a multipart file part to dst, capped at the Echo
// BodyLimit (already enforced upstream). We don't re-enforce the cap here
// because Echo rejects oversized bodies before we see them.
func saveMultipart(src multipart.File, dst string) error {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, src)
	return err
}

// classifyUpload inspects the uploaded file's extension + MIME and returns
// ("csv" | "xlsx", ext-without-dot, true) when the type is allowed. Mime
// alone lies (browser sometimes sends application/octet-stream); extension
// alone lies (renamed .xlsx). We accept if EITHER check says "allowed".
// Rejected: .xls, anything else.
func classifyUpload(fh *multipart.FileHeader) (string, string, bool) {
	lower := strings.ToLower(fh.Filename)
	ext := ""
	if i := strings.LastIndex(lower, "."); i >= 0 {
		ext = lower[i+1:]
	}
	ct := strings.ToLower(fh.Header.Get("Content-Type"))

	// Outright reject legacy .xls by extension or MIME — the parser can't
	// handle them and silently failing would be worse than a loud 415.
	if ext == "xls" || strings.Contains(ct, "vnd.ms-excel") && !strings.Contains(ct, "openxmlformats") {
		return "", "", false
	}

	switch ext {
	case "csv":
		return "csv", "csv", true
	case "tsv":
		return "csv", "tsv", true // parser reads both; default delimiter swap handled downstream
	case "xlsx":
		return "xlsx", "xlsx", true
	}
	// Fall back to Content-Type only when extension is missing/unknown.
	if strings.Contains(ct, "csv") {
		return "csv", "csv", true
	}
	if strings.Contains(ct, "tab-separated-values") {
		return "csv", "tsv", true
	}
	if strings.Contains(ct, "openxmlformats-officedocument.spreadsheetml") {
		return "xlsx", "xlsx", true
	}
	return "", "", false
}

// buildSheetsResponse renders the parsed map into the JSON shape the
// frontend expects. Column indices become stringified keys because JSON
// objects don't support integer keys and the frontend already keys-as-string.
func buildSheetsResponse(parsed map[string]*spreadsheet.ParsedSheet, order []string) []uploadSheetResponse {
	out := make([]uploadSheetResponse, 0, len(order))
	for _, name := range order {
		ps := parsed[name]
		if ps == nil {
			continue
		}
		samples := map[string][]string{}
		for k, v := range ps.ColumnSamples {
			samples[fmt.Sprintf("%d", k)] = v
		}
		cov := map[string]spreadsheet.TypeCoverage{}
		for k, v := range ps.TypeCoverage {
			cov[fmt.Sprintf("%d", k)] = v
		}
		out = append(out, uploadSheetResponse{
			Name:          name,
			RowCount:      len(ps.Rows),
			Headers:       ps.Headers,
			ColumnSamples: samples,
			TypeCoverage:  cov,
		})
	}
	return out
}

// buildSuggested composes a suggestedConfig from a parsed sheet — used by
// both Upload and GetSheet (the latter when the user switches sheets mid-
// session). Confidence is a rough heuristic: mapped role count / 9 (total
// roles except Ignore), capped at 1.0.
func (h *ImportSpreadsheetHandler) buildSuggested(name string, ps *spreadsheet.ParsedSheet, sourceType string) suggestedConfig {
	mapping := spreadsheet.SuggestMapping(ps)

	// Infer date format from the mapped Date column's samples.
	var dateSamples []string
	if c, ok := mapping[spreadsheet.RoleDate]; ok {
		dateSamples = append(dateSamples, ps.ColumnSamples[c]...)
		for _, row := range ps.Rows {
			if c >= 0 && c < len(row.Cells) {
				dateSamples = append(dateSamples, row.Cells[c])
			}
			if len(dateSamples) > 40 {
				break
			}
		}
	}
	df := spreadsheet.DetectDateFormat(dateSamples)

	csvOpts := spreadsheet.DefaultCSVOptions()
	if sourceType == "csv" && strings.HasSuffix(strings.ToLower(ps.Name), ".tsv") {
		csvOpts.Delimiter = '\t'
	}

	out := suggestedConfig{
		Sheet:       name,
		Mapping:     roleMapToJSON(mapping),
		DateFormat:  df,
		CSVOptions:  csvOpts,
		UnitOptions: spreadsheet.DefaultUnitOptions(),
		Grouping:    spreadsheet.Grouping{Strategy: spreadsheet.GroupDateStore},
		Confidence:  float64(len(mapping)) / 9.0,
	}
	if out.Confidence > 1.0 {
		out.Confidence = 1.0
	}
	return out
}

// roleMapToJSON converts Mapping's typed-key map into the JSON-friendly
// shape (string keys). The JSON side of the contract (PLAN Endpoints shape
// "mapping": { "date": 0, ... }) uses snake_case role names verbatim, which
// already matches internal/spreadsheet/types.go Role constants.
func roleMapToJSON(m spreadsheet.Mapping) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[string(k)] = v
	}
	return out
}

// jsonToRoleMap is the inverse of roleMapToJSON. Unknown roles are skipped
// silently — the client may send a forward-compatible key we don't yet
// recognize; returning an error would force clients to lockstep-update.
func jsonToRoleMap(in map[string]int) spreadsheet.Mapping {
	out := spreadsheet.Mapping{}
	for k, v := range in {
		out[spreadsheet.Role(k)] = v
	}
	return out
}

// computeFingerprint returns sha256(sheet_name || normalized_headers ||
// column_count), hex-encoded. "Normalized" is lowercase + trim per column.
// Used to auto-apply a saved mapping when the user re-uploads a structurally-
// identical sheet (plan §Endpoints upload response).
func computeFingerprint(sheetName string, headers []string) string {
	var parts []string
	parts = append(parts, strings.ToLower(strings.TrimSpace(sheetName)))
	for _, h := range headers {
		parts = append(parts, strings.ToLower(strings.TrimSpace(h)))
	}
	parts = append(parts, fmt.Sprintf("cols=%d", len(headers)))
	raw := strings.Join(parts, "\x00")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// lookupSavedMappings returns the household's saved mappings (in last-used
// order) plus the mapping id of an auto-applied fingerprint match (or nil).
// Errors here are non-fatal — log and return empty.
func (h *ImportSpreadsheetHandler) lookupSavedMappings(ctx interface{}, householdID, fingerprint string) ([]savedMappingResponse, *string) {
	rows, err := h.DB.Query(
		`SELECT id, name, source_fingerprint, last_used_at
		 FROM import_mappings
		 WHERE household_id = ?
		 ORDER BY COALESCE(last_used_at, created_at) DESC
		 LIMIT 25`,
		householdID,
	)
	if err != nil {
		slog.Warn("spreadsheet upload: list mappings failed", "err", err)
		return nil, nil
	}
	defer rows.Close()

	var out []savedMappingResponse
	var autoApply *string
	for rows.Next() {
		var (
			id, name string
			fp       sql.NullString
			lu       sql.NullTime
		)
		if err := rows.Scan(&id, &name, &fp, &lu); err != nil {
			continue
		}
		// Skip the implicit per-household "last used" mapping from the
		// user-facing list — it's restored on re-upload via
		// LIMIT 25 + last_used_at order, but the UI should not render it
		// as a named profile.
		if name == lastUsedMappingName {
			continue
		}
		m := savedMappingResponse{ID: id, Name: name}
		if lu.Valid {
			m.LastUsedAt = lu.Time.UTC().Format(time.RFC3339)
		}
		out = append(out, m)
		if autoApply == nil && fp.Valid && fp.String == fingerprint {
			idCopy := id
			autoApply = &idCopy
		}
	}
	return out, autoApply
}

// lastUsedMappingName is the sentinel name used for the per-household
// implicit "last used" mapping row. Kept separate from user-named mappings
// so the frontend chip list doesn't show a confusing duplicate.
const lastUsedMappingName = "__last_used__"

// ---------------------------------------------------------------------------
// GetSheet.
// ---------------------------------------------------------------------------

type getSheetResponse struct {
	Sheet     uploadSheetResponse `json:"sheet"`
	Suggested suggestedConfig     `json:"suggested"`
}

// GetSheet handles GET /:import_id/sheet/:sheet_name.
func (h *ImportSpreadsheetHandler) GetSheet(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	importID := c.Param("import_id")
	sheetName := c.Param("sheet_name")

	s, ok := h.loadStaging(importID, householdID)
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
	}
	ps, ok := s.Parsed[sheetName]
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "sheet not found"})
	}

	resp := getSheetResponse{
		Sheet:     buildSheetsResponse(s.Parsed, []string{sheetName})[0],
		Suggested: h.buildSuggested(sheetName, ps, s.SourceType),
	}
	return c.JSON(http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
// Transform.
// ---------------------------------------------------------------------------

type transformRequest struct {
	Kind       string `json:"kind"`
	RowIndex   int    `json:"row_index"`
	ColIndex   int    `json:"col_index"`
	NewValue   string `json:"new_value"`
	RowIndices []int  `json:"row_indices"`
}

type transformResponse struct {
	TransformID    string `json:"transform_id"`
	ImportRevision int    `json:"import_revision"`
}

// Transform handles POST /:import_id/transform.
//
// v1 scope: only "override_cell" and "skip_row" kinds are accepted. The
// other two (ai_normalize / split_row) return 400 with a "coming soon" body
// per plan §Rollout.
func (h *ImportSpreadsheetHandler) Transform(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	importID := c.Param("import_id")

	s, ok := h.loadStaging(importID, householdID)
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
	}

	var req transformRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
	}

	var payload []byte
	switch req.Kind {
	case spreadsheet.KindOverrideCell:
		p := spreadsheet.OverrideCellPayload{
			RowIndex: req.RowIndex,
			ColIndex: req.ColIndex,
			NewValue: req.NewValue,
		}
		payload, _ = json.Marshal(p)
	case spreadsheet.KindSkipRow:
		p := spreadsheet.SkipRowPayload{RowIndices: req.RowIndices}
		if len(p.RowIndices) == 0 && req.RowIndex >= 0 {
			p.RowIndices = []int{req.RowIndex}
		}
		payload, _ = json.Marshal(p)
	case spreadsheet.KindAINormalize, spreadsheet.KindSplitRow:
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("transform kind %q coming in phase 10", req.Kind),
		})
	default:
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "unknown transform kind"})
	}

	t := spreadsheet.Transform{
		ID:       "t" + fmt.Sprintf("%d", len(s.Chain.Transforms)+1),
		Kind:     req.Kind,
		RowIndex: req.RowIndex,
		Payload:  payload,
	}
	s.Chain.Transforms = append(s.Chain.Transforms, t)
	s.Chain.Revision++
	h.staging.Store(importID, s)
	h.previewCache.Delete(importID) // invalidate any cached preview

	if err := h.persistStaging(s); err != nil {
		slog.Warn("spreadsheet transform: persist failed", "err", err, "import_id", importID)
	}
	return c.JSON(http.StatusOK, transformResponse{TransformID: t.ID, ImportRevision: s.Chain.Revision})
}

// ---------------------------------------------------------------------------
// Preview.
// ---------------------------------------------------------------------------

type previewRequest struct {
	Sheet           string                  `json:"sheet"`
	Mapping         map[string]int          `json:"mapping"`
	DateFormat      spreadsheet.DateFormat  `json:"date_format"`
	CSVOptions      spreadsheet.CSVOptions  `json:"csv_options"`
	UnitOptions     spreadsheet.UnitOptions `json:"unit_options"`
	Grouping        spreadsheet.Grouping    `json:"grouping"`
	SinceDate       string                  `json:"since_date"`
	SkipRowIndices  []int                   `json:"skip_row_indices"`
	ImportRevision  int                     `json:"import_revision"`
}

type previewRow struct {
	RowIndex             int                    `json:"row_index"`
	Raw                  []string               `json:"raw"`
	Parsed               spreadsheet.ParsedValue `json:"parsed"`
	CellErrors           map[string]string      `json:"cell_errors,omitempty"`
	DuplicateOfReceiptID *string                `json:"duplicate_of_receipt_id"`
	WouldCreateStore     bool                   `json:"would_create_store"`
	ReceiptGroupID       string                 `json:"receipt_group_id"`
	TransformOrigin      *string                `json:"transform_origin"`
}

type previewGroup struct {
	GroupID              string  `json:"group_id"`
	Store                string  `json:"store"`
	Date                 string  `json:"date"`
	RowIndices           []int   `json:"row_indices"`
	TotalCents           int64   `json:"total_cents"`
	DuplicateOfReceiptID *string `json:"duplicate_of_receipt_id"`
	SplitSuggested       bool    `json:"split_suggested"`
}

type previewSummary struct {
	Receipts              int      `json:"receipts"`
	Items                 int      `json:"items"`
	Duplicates            int      `json:"duplicates"`
	NewStores             []string `json:"new_stores"`
	RowsWithErrors        int      `json:"rows_with_errors"`
	RowsAfterSinceFilter  int      `json:"rows_after_since_filter"`
}

type previewResponse struct {
	Rows           []previewRow   `json:"rows"`
	Groups         []previewGroup `json:"groups"`
	Summary        previewSummary `json:"summary"`
	ImportRevision int            `json:"import_revision"`
}

type previewCacheEntry struct {
	key      string
	response previewResponse
}

// Preview handles POST /:import_id/preview.
func (h *ImportSpreadsheetHandler) Preview(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	importID := c.Param("import_id")
	s, ok := h.loadStaging(importID, householdID)
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
	}

	var req previewRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
	}
	if req.Sheet == "" {
		// default to the first sheet if client didn't name one
		for k := range s.Parsed {
			req.Sheet = k
			break
		}
	}
	ps, ok := s.Parsed[req.Sheet]
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "sheet not found"})
	}

	cacheKey := previewCacheKey(req, s.Chain.Revision)
	if v, ok := h.previewCache.Load(importID); ok {
		entry := v.(previewCacheEntry)
		if entry.key == cacheKey {
			return c.JSON(http.StatusOK, entry.response)
		}
	}

	resp := h.buildPreview(c, s, ps, req)
	h.previewCache.Store(importID, previewCacheEntry{key: cacheKey, response: resp})

	// Touch LastActiveAt on preview so the janitor sees active sessions.
	_ = h.persistStaging(s)

	return c.JSON(http.StatusOK, resp)
}

// previewCacheKey builds the canonical-JSON-based key described in plan
// §Risks. We sort the mapping keys to make serialization deterministic;
// SkipRowIndices is sorted too. Only the config fields the spec names are
// part of the key — the request's `sheet` is in there implicitly via the
// normalized payload.
func previewCacheKey(req previewRequest, revision int) string {
	// Sort mapping keys.
	keys := make([]string, 0, len(req.Mapping))
	for k := range req.Mapping {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	mapping := make([][2]any, 0, len(keys))
	for _, k := range keys {
		mapping = append(mapping, [2]any{k, req.Mapping[k]})
	}
	skips := make([]int, len(req.SkipRowIndices))
	copy(skips, req.SkipRowIndices)
	sort.Ints(skips)

	payload := map[string]any{
		"sheet":          req.Sheet,
		"mapping":        mapping,
		"date_format":    req.DateFormat,
		"csv_options":    req.CSVOptions,
		"unit_options":   req.UnitOptions,
		"grouping":       req.Grouping,
		"skip_rows":      skips,
		"since_date":     req.SinceDate,
		"revision":       revision,
	}
	b, _ := json.Marshal(payload)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// buildPreview runs normalization + grouping + duplicate-check on the given
// sheet and builds the preview response. Kept pure (no HTTP side-effects)
// so tests can exercise it directly if needed.
func (h *ImportSpreadsheetHandler) buildPreview(c echo.Context, s *spreadsheet.Staging, ps *spreadsheet.ParsedSheet, req previewRequest) previewResponse {
	mapping := jsonToRoleMap(req.Mapping)

	// Apply the persistent transform chain first, then the one-shot
	// skip_row_indices the client supplied in this preview request. The
	// chain-skips are carried on Staging; ad-hoc skips are NOT persisted
	// (plan distinguishes saved transforms from per-preview skip toggles).
	transformed := spreadsheet.ApplyTransforms(ps, s.Chain)
	if len(req.SkipRowIndices) > 0 {
		skipSet := make(map[int]bool, len(req.SkipRowIndices))
		for _, i := range req.SkipRowIndices {
			skipSet[i] = true
		}
		filtered := transformed.Rows[:0:0]
		for _, r := range transformed.Rows {
			if !skipSet[r.Index] {
				filtered = append(filtered, r)
			}
		}
		transformed = &spreadsheet.ParsedSheet{
			Name:          transformed.Name,
			Headers:       transformed.Headers,
			Rows:          filtered,
			ColumnSamples: transformed.ColumnSamples,
			TypeCoverage:  transformed.TypeCoverage,
		}
	}

	// Since-date filter: drop rows whose parsed date is before since_date.
	// We apply this AFTER NormalizeRow so we filter on the parsed date, not
	// a raw string that may not match the selected date format.
	parsed := make([]spreadsheet.ParsedValue, 0, len(transformed.Rows))
	for _, raw := range transformed.Rows {
		pv := spreadsheet.NormalizeRow(raw, mapping, req.UnitOptions, req.Grouping, req.DateFormat)
		if req.SinceDate != "" && pv.Date != "" && pv.Date < req.SinceDate {
			continue
		}
		parsed = append(parsed, pv)
	}

	groups := spreadsheet.GroupRows(parsed, req.Grouping)
	groups = spreadsheet.ApplySplitSuggested(parsed, groups)

	// Duplicate check against committed receipts. Errors are non-fatal —
	// the preview still renders without duplicate badges.
	duplicates, err := spreadsheet.CheckDuplicates(c.Request().Context(), h.DB, s.HouseholdID, groups)
	if err != nil {
		slog.Warn("spreadsheet preview: duplicate check failed", "err", err, "import_id", s.ImportID)
		duplicates = spreadsheet.DuplicateMap{}
	}

	// Build preview rows (cap at 50 per plan).
	pvByIdx := make(map[int]spreadsheet.ParsedValue, len(parsed))
	for _, pv := range parsed {
		pvByIdx[pv.RowIndex] = pv
	}
	groupByRow := map[int]string{}
	dupByGroup := map[string]*string{}
	for _, g := range groups {
		for _, ri := range g.RowIndices {
			groupByRow[ri] = g.ID
		}
		if existing, ok := duplicates[g.ID]; ok && existing != "" {
			v := existing
			dupByGroup[g.ID] = &v
		}
	}

	// Map transformed row -> transform_origin by looking up the chain's
	// override_cell transforms that touched this row. A row with no matching
	// override is null.
	originByRow := map[int]string{}
	for _, t := range s.Chain.Transforms {
		if t.Kind == spreadsheet.KindOverrideCell {
			originByRow[t.RowIndex] = t.ID
		}
	}

	rowsOut := make([]previewRow, 0, 50)
	limit := 50
	for _, raw := range transformed.Rows {
		if len(rowsOut) >= limit {
			break
		}
		pv, ok := pvByIdx[raw.Index]
		if !ok {
			continue
		}
		var cellErrs map[string]string
		if len(pv.CellErrors) > 0 {
			cellErrs = make(map[string]string, len(pv.CellErrors))
			for k, v := range pv.CellErrors {
				cellErrs[string(k)] = v
			}
		}
		row := previewRow{
			RowIndex:             raw.Index,
			Raw:                  raw.Cells,
			Parsed:               pv,
			CellErrors:           cellErrs,
			DuplicateOfReceiptID: dupByGroup[groupByRow[raw.Index]],
			WouldCreateStore:     pv.Store != "" && !storeExists(c.Request().Context(), h.DB, s.HouseholdID, pv.Store),
			ReceiptGroupID:       groupByRow[raw.Index],
		}
		if id, ok := originByRow[raw.Index]; ok {
			row.TransformOrigin = &id
		}
		rowsOut = append(rowsOut, row)
	}

	// Build groups response.
	groupsOut := make([]previewGroup, 0, len(groups))
	for _, g := range groups {
		gOut := previewGroup{
			GroupID:              g.ID,
			Store:                g.Store,
			Date:                 g.Date,
			RowIndices:           append([]int(nil), g.RowIndices...),
			TotalCents:           g.TotalCents,
			DuplicateOfReceiptID: dupByGroup[g.ID],
			SplitSuggested:       g.SplitSuggested,
		}
		groupsOut = append(groupsOut, gOut)
	}

	// Summary.
	rowsWithErrors := 0
	for _, pv := range parsed {
		if len(pv.CellErrors) > 0 {
			rowsWithErrors++
		}
	}
	newStores := computeNewStores(c.Request().Context(), h.DB, s.HouseholdID, parsed)
	dupCount := 0
	for range duplicates {
		dupCount++
	}

	return previewResponse{
		Rows:   rowsOut,
		Groups: groupsOut,
		Summary: previewSummary{
			Receipts:             len(groups),
			Items:                len(parsed),
			Duplicates:           dupCount,
			NewStores:            newStores,
			RowsWithErrors:       rowsWithErrors,
			RowsAfterSinceFilter: len(parsed),
		},
		ImportRevision: s.Chain.Revision,
	}
}

// storeExists returns true when a store with the same normalized name
// already exists for the household. Case-insensitive — matches
// findOrCreateStore's lookup in internal/spreadsheet/commit.go.
func storeExists(ctx interface{}, db *sql.DB, householdID, storeName string) bool {
	if strings.TrimSpace(storeName) == "" {
		return true // empty name is handled as "no store", not "new store"
	}
	var id string
	err := db.QueryRow(
		`SELECT id FROM stores WHERE household_id = ? AND LOWER(name) = LOWER(?) LIMIT 1`,
		householdID, storeName,
	).Scan(&id)
	return err == nil
}

// computeNewStores returns the distinct set of (normalized) store names
// present in the parsed rows that do NOT already exist for the household.
// Ordering: insertion-order of first appearance, so the frontend renders a
// stable list across preview refreshes.
func computeNewStores(ctx interface{}, db *sql.DB, householdID string, rows []spreadsheet.ParsedValue) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rows {
		if r.Store == "" || seen[r.Store] {
			continue
		}
		seen[r.Store] = true
		if !storeExists(ctx, db, householdID, r.Store) {
			out = append(out, r.Store)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Commit.
// ---------------------------------------------------------------------------

type groupOverride struct {
	Include     *bool `json:"include,omitempty"`
	IsDuplicate *bool `json:"is_duplicate_ok,omitempty"`
}

type commitRequest struct {
	previewRequest
	SaveMappingAs  string                   `json:"save_mapping_as"`
	GroupOverrides map[string]groupOverride `json:"group_overrides"`
}

type commitResponse struct {
	BatchID          string `json:"batch_id"`
	ReceiptsCreated  int    `json:"receipts_created"`
	LineItemsCreated int    `json:"line_items_created"`
	Unmatched        int    `json:"unmatched"`
}

// Commit handles POST /:import_id/commit.
//
// Runs normalization + grouping + duplicate-check again (the preview
// response is transient per plan), then delegates to spreadsheet.Commit
// which owns the database writes. Afterwards, persists the saved mapping
// (if the user named one) AND the implicit "last used" mapping.
func (h *ImportSpreadsheetHandler) Commit(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	importID := c.Param("import_id")
	s, ok := h.loadStaging(importID, householdID)
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
	}

	var req commitRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
	}
	if req.Sheet == "" {
		for k := range s.Parsed {
			req.Sheet = k
			break
		}
	}
	ps, ok := s.Parsed[req.Sheet]
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "sheet not found"})
	}

	// Replay transforms + per-request skips the same way preview does —
	// the commit path MUST normalize from the same inputs the user saw.
	transformed := spreadsheet.ApplyTransforms(ps, s.Chain)
	if len(req.SkipRowIndices) > 0 {
		skipSet := make(map[int]bool, len(req.SkipRowIndices))
		for _, i := range req.SkipRowIndices {
			skipSet[i] = true
		}
		filtered := transformed.Rows[:0:0]
		for _, r := range transformed.Rows {
			if !skipSet[r.Index] {
				filtered = append(filtered, r)
			}
		}
		transformed = &spreadsheet.ParsedSheet{
			Name:          transformed.Name,
			Headers:       transformed.Headers,
			Rows:          filtered,
			ColumnSamples: transformed.ColumnSamples,
			TypeCoverage:  transformed.TypeCoverage,
		}
	}

	mapping := jsonToRoleMap(req.Mapping)

	// Build groups here to run a fresh duplicate check BEFORE delegating to
	// spreadsheet.Commit (which re-runs its own normalize/group internally).
	// CheckDuplicates is read-only so duplicating the work is cheap; the
	// alternative (threading it through CommitInput) would require a plumbing
	// change in the spreadsheet package that's out of scope for Phase 5.
	parsed := make([]spreadsheet.ParsedValue, 0, len(transformed.Rows))
	for _, raw := range transformed.Rows {
		pv := spreadsheet.NormalizeRow(raw, mapping, req.UnitOptions, req.Grouping, req.DateFormat)
		if req.SinceDate != "" && pv.Date != "" && pv.Date < req.SinceDate {
			continue
		}
		parsed = append(parsed, pv)
	}

	// SINCE filter: drop rows whose parsed date is older than since_date
	// by actually deleting them from the transformed sheet so Commit's
	// internal normalize/group sees the same filtered set we did.
	if req.SinceDate != "" {
		keepRows := transformed.Rows[:0:0]
		keepByIdx := map[int]bool{}
		for _, pv := range parsed {
			keepByIdx[pv.RowIndex] = true
		}
		for _, r := range transformed.Rows {
			if keepByIdx[r.Index] {
				keepRows = append(keepRows, r)
			}
		}
		transformed = &spreadsheet.ParsedSheet{
			Name:          transformed.Name,
			Headers:       transformed.Headers,
			Rows:          keepRows,
			ColumnSamples: transformed.ColumnSamples,
			TypeCoverage:  transformed.TypeCoverage,
		}
	}

	groups := spreadsheet.GroupRows(parsed, req.Grouping)
	duplicates, err := spreadsheet.CheckDuplicates(c.Request().Context(), h.DB, householdID, groups)
	if err != nil {
		slog.Warn("spreadsheet commit: duplicate check failed", "err", err)
		duplicates = spreadsheet.DuplicateMap{}
	}

	// Translate group_overrides into IncludedGroupIDs / ConfirmedDuplicates.
	// Default: all groups included, no duplicates confirmed. When the client
	// provides an override, it becomes an explicit allow/deny per group.
	var includedIDs map[string]bool
	confirmed := map[string]bool{}
	if len(req.GroupOverrides) > 0 {
		includedIDs = make(map[string]bool, len(groups))
		for _, g := range groups {
			includedIDs[g.ID] = true
		}
		for gid, ov := range req.GroupOverrides {
			if ov.Include != nil {
				includedIDs[gid] = *ov.Include
			}
			if ov.IsDuplicate != nil && *ov.IsDuplicate {
				confirmed[gid] = true
			}
		}
	}

	in := spreadsheet.CommitInput{
		HouseholdID:         householdID,
		Sheet:               transformed,
		Mapping:             mapping,
		DateFormat:          req.DateFormat,
		CSVOptions:          req.CSVOptions,
		UnitOptions:         req.UnitOptions,
		Grouping:            req.Grouping,
		SourceFilename:      s.SourceFilename,
		SourceType:          s.SourceType,
		IncludedGroupIDs:    includedIDs,
		ConfirmedDuplicates: confirmed,
		Duplicates:          duplicates,
		Now:                 h.now(),
	}

	result, err := spreadsheet.Commit(c.Request().Context(), h.DB, h.MatchEngine, in)
	if err != nil {
		slog.Error("spreadsheet commit failed", "err", err, "import_id", importID)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "commit failed"})
	}

	// Persist saved + last-used mappings. Errors are non-fatal — the commit
	// succeeded; mapping persistence is a convenience.
	configJSON, _ := json.Marshal(map[string]any{
		"sheet":        req.Sheet,
		"mapping":      req.Mapping,
		"date_format":  req.DateFormat,
		"csv_options":  req.CSVOptions,
		"unit_options": req.UnitOptions,
		"grouping":     req.Grouping,
	})
	if strings.TrimSpace(req.SaveMappingAs) != "" {
		if err := h.upsertMapping(householdID, req.SaveMappingAs, s.SourceType, s.Fingerprint, configJSON); err != nil {
			slog.Warn("spreadsheet commit: save mapping failed", "err", err)
		}
	}
	// Always upsert the implicit "last used" row so the next upload recalls
	// these settings without a saved-as name.
	if err := h.upsertMapping(householdID, lastUsedMappingName, s.SourceType, s.Fingerprint, configJSON); err != nil {
		slog.Warn("spreadsheet commit: save last-used failed", "err", err)
	}

	return c.JSON(http.StatusOK, commitResponse{
		BatchID:          result.BatchID,
		ReceiptsCreated:  result.ReceiptsCreated,
		LineItemsCreated: result.LineItemsCreated,
		Unmatched:        result.UnmatchedLineItems,
	})
}

// upsertMapping inserts or updates a row in import_mappings for (household,
// name). On conflict we overwrite config_json, source_fingerprint, and
// last_used_at. SQLite's ON CONFLICT requires a uniqueness constraint; the
// migration 021 schema does not declare one, so we emulate upsert with a
// SELECT-then-INSERT/UPDATE dance inside a transaction.
func (h *ImportSpreadsheetHandler) upsertMapping(householdID, name, sourceType, fingerprint string, configJSON []byte) error {
	if sourceType != "csv" && sourceType != "xlsx" {
		return fmt.Errorf("invalid source_type %q", sourceType)
	}
	tx, err := h.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var id string
	err = tx.QueryRow(
		`SELECT id FROM import_mappings WHERE household_id = ? AND name = ?`,
		householdID, name,
	).Scan(&id)
	now := h.now()
	switch {
	case err == nil:
		if _, err := tx.Exec(
			`UPDATE import_mappings
			 SET source_type = ?, source_fingerprint = ?, config_json = ?, last_used_at = ?
			 WHERE id = ?`,
			sourceType, fingerprint, string(configJSON), now, id,
		); err != nil {
			return err
		}
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.Exec(
			`INSERT INTO import_mappings (id, household_id, name, source_type, source_fingerprint, config_json, last_used_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			uuid.New().String(), householdID, name, sourceType, fingerprint, string(configJSON), now,
		); err != nil {
			return err
		}
	default:
		return err
	}
	return tx.Commit()
}

// ---------------------------------------------------------------------------
// Delete.
// ---------------------------------------------------------------------------

// Delete handles DELETE /:import_id — purges staging cache + raw file + staging.json.
func (h *ImportSpreadsheetHandler) Delete(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	importID := c.Param("import_id")
	s, ok := h.loadStaging(importID, householdID)
	if !ok {
		return c.NoContent(http.StatusNoContent) // idempotent
	}
	h.staging.Delete(importID)
	h.previewCache.Delete(importID)
	_ = spreadsheet.DeleteStaging(h.stagingDir(), importID)
	_ = s // silences unused-variable warning in some Go versions
	return c.NoContent(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Janitor.
// ---------------------------------------------------------------------------

// runJanitor sweeps the staging directory every 30 minutes and deletes
// staging directories whose mtime is older than 24h. Matches plan §Rollout
// — the formal janitor lands in Phase 12; this is the interim.
func (h *ImportSpreadsheetHandler) runJanitor() {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-h.stopJanitor:
			return
		case <-ticker.C:
			h.janitorSweep(24 * time.Hour)
		}
	}
}

// janitorSweep walks the on-disk staging directory and removes any subdir
// idle longer than maxIdle. Also evicts the in-memory caches for removed
// import IDs so subsequent requests 404 cleanly.
func (h *ImportSpreadsheetHandler) janitorSweep(maxIdle time.Duration) {
	dir := h.stagingDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("spreadsheet janitor: readdir failed", "err", err)
		}
		return
	}
	cutoff := h.now().Add(-maxIdle)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			importID := e.Name()
			h.staging.Delete(importID)
			h.previewCache.Delete(importID)
			if err := os.RemoveAll(filepath.Join(dir, importID)); err != nil {
				slog.Warn("spreadsheet janitor: remove failed", "err", err, "import_id", importID)
			}
		}
	}
}

// Compile-time assertion: *matcher.Engine implements spreadsheet.MatchEngine.
var _ spreadsheet.MatchEngine = (*matcher.Engine)(nil)
