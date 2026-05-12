package runner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
	"github.com/mstefanko/cartledger/internal/enrichment/providers"
	"github.com/mstefanko/cartledger/internal/enrichment/providers/openfoodfacts"
	"github.com/mstefanko/cartledger/internal/enrichment/providers/urlprovider"
	"github.com/mstefanko/cartledger/internal/enrichment/providers/usda"
	estore "github.com/mstefanko/cartledger/internal/enrichment/store"
	"github.com/mstefanko/cartledger/internal/models"
	"github.com/mstefanko/cartledger/internal/sqliteutil"
	"github.com/mstefanko/cartledger/internal/ws"
)

const (
	TriggerManualLookup  = "manual_lookup"
	TriggerManualRefresh = "manual_refresh"

	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusPartial   = "partial"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

type Service struct {
	DB        *sql.DB
	Cfg       *config.Config
	Hub       Broadcaster
	Providers map[string]providers.Provider
	Store     estore.Repository
	Limiter   *ProviderLimiter
	Worker    *Worker
	Log       *slog.Logger
}

type Broadcaster interface {
	Broadcast(ws.Message)
}

type QueueJobRequest struct {
	HouseholdID       string
	ProductID         string
	RequestedByUserID string
	Trigger           string
	LookupKey         string
	RequestedSources  []string
}

type Job struct {
	ID                string     `json:"id"`
	HouseholdID       string     `json:"household_id,omitempty"`
	ProductID         string     `json:"product_id"`
	RequestedByUserID *string    `json:"requested_by_user_id,omitempty"`
	Trigger           string     `json:"trigger"`
	LookupKey         string     `json:"lookup_key"`
	RequestedSources  []string   `json:"requested_sources,omitempty"`
	Status            string     `json:"status"`
	AttemptCount      int        `json:"attempt_count"`
	NextAttemptAt     *time.Time `json:"next_attempt_at,omitempty"`
	LastError         *string    `json:"last_error,omitempty"`
	QueuedAt          time.Time  `json:"queued_at"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	FinishedAt        *time.Time `json:"finished_at,omitempty"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type productRow struct {
	ID          string
	HouseholdID string
	Name        string
	Brand       *string
	UPC         *string
}

type settingsRow struct {
	ManualLookupEnabled          bool
	AutoOnScanEnabled            bool
	ScheduledSweepEnabled        bool
	ProviderOpenFoodFactsEnabled bool
	ProviderUSDAFDCEnabled       bool
	ProviderKrogerEnabled        bool
	FirstRunBackfillLimit        int
}

func NewService(database *sql.DB, cfg *config.Config, hub *ws.Hub) *Service {
	allowPrivate := cfg != nil && cfg.AllowPrivateIntegrations
	providerList := []providers.Provider{
		urlprovider.New(allowPrivate),
		openfoodfacts.New(allowPrivate),
		usda.New(allowPrivate),
	}
	return NewServiceWithProviders(database, cfg, hub, providerList)
}

func NewServiceWithProviders(database *sql.DB, cfg *config.Config, hub Broadcaster, providerList []providers.Provider) *Service {
	providerMap := make(map[string]providers.Provider, len(providerList))
	for _, provider := range providerList {
		if provider != nil {
			providerMap[normalizeSource(provider.Name())] = provider
		}
	}
	return &Service{
		DB:        database,
		Cfg:       cfg,
		Hub:       hub,
		Providers: providerMap,
		Store:     estore.Repository{DB: database},
		Limiter:   NewProviderLimiter(),
		Log:       slog.Default(),
	}
}

func (s *Service) SetWorker(worker *Worker) {
	s.Worker = worker
}

func (s *Service) QueueJob(ctx context.Context, req QueueJobRequest) (Job, bool, error) {
	if strings.TrimSpace(req.Trigger) == "" {
		req.Trigger = TriggerManualLookup
	}
	req.Trigger = strings.TrimSpace(req.Trigger)
	req.LookupKey = strings.TrimSpace(req.LookupKey)
	if req.LookupKey == "" {
		return Job{}, false, errors.New("lookup key is required")
	}
	sourceJSON := ""
	if len(req.RequestedSources) > 0 {
		normalized := normalizeSources(req.RequestedSources)
		data, err := json.Marshal(normalized)
		if err != nil {
			return Job{}, false, err
		}
		sourceJSON = string(data)
	}

	var existingID string
	err := s.DB.QueryRowContext(ctx,
		`SELECT id
		   FROM product_enrichment_jobs
		  WHERE household_id = ?
		    AND product_id = ?
		    AND trigger = ?
		    AND lookup_key = ?
		    AND status IN ('queued', 'running')
		  ORDER BY queued_at DESC
		  LIMIT 1`,
		req.HouseholdID, req.ProductID, req.Trigger, req.LookupKey,
	).Scan(&existingID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, err
	}
	if existingID != "" {
		job, err := s.GetJob(ctx, req.HouseholdID, req.ProductID, existingID)
		if err == nil && s.Worker != nil && job.Status == StatusQueued {
			_ = s.Worker.Submit(job.ID)
		}
		return job, false, err
	}

	var id string
	err = s.DB.QueryRowContext(ctx,
		`INSERT INTO product_enrichment_jobs
		    (id, household_id, product_id, requested_by_user_id, trigger, lookup_key, requested_sources, status, queued_at, updated_at)
		 VALUES (lower(hex(randomblob(16))), ?, ?, ?, ?, ?, ?, 'queued', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		 RETURNING id`,
		req.HouseholdID, req.ProductID, nullableUserID(req.RequestedByUserID), req.Trigger, req.LookupKey, nullableString(sourceJSON),
	).Scan(&id)
	if err != nil {
		if sqliteutil.IsUniqueConstraint(err) {
			job, getErr := s.findActiveJob(ctx, req.HouseholdID, req.ProductID, req.Trigger, req.LookupKey)
			if getErr == nil && s.Worker != nil && job.Status == StatusQueued {
				_ = s.Worker.Submit(job.ID)
			}
			return job, false, getErr
		}
		return Job{}, false, err
	}
	job, err := s.GetJob(ctx, req.HouseholdID, req.ProductID, id)
	if err != nil {
		return Job{}, false, err
	}
	if s.Worker != nil {
		_ = s.Worker.Submit(job.ID)
	}
	return job, true, nil
}

func (s *Service) ListJobs(ctx context.Context, householdID, productID string, limit int) ([]Job, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, household_id, product_id, requested_by_user_id, trigger, lookup_key,
		        requested_sources, status, attempt_count, next_attempt_at, last_error,
		        queued_at, started_at, finished_at, updated_at
		   FROM product_enrichment_jobs
		  WHERE household_id = ? AND product_id = ?
		  ORDER BY queued_at DESC
		  LIMIT ?`,
		householdID, productID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]Job, 0)
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *Service) GetJob(ctx context.Context, householdID, productID, jobID string) (Job, error) {
	return scanJob(s.DB.QueryRowContext(ctx,
		`SELECT id, household_id, product_id, requested_by_user_id, trigger, lookup_key,
		        requested_sources, status, attempt_count, next_attempt_at, last_error,
		        queued_at, started_at, finished_at, updated_at
		   FROM product_enrichment_jobs
		  WHERE household_id = ? AND product_id = ? AND id = ?`,
		householdID, productID, jobID,
	))
}

func (s *Service) ProcessJob(ctx context.Context, jobID string) error {
	job, err := s.getJobByID(ctx, jobID)
	if err != nil {
		return err
	}
	if job.Status != StatusQueued {
		return nil
	}
	if err := s.markJobRunning(ctx, job); err != nil {
		return err
	}

	product, err := s.loadProduct(ctx, job.HouseholdID, job.ProductID)
	if err != nil {
		s.finishJob(ctx, job, StatusFailed, map[string]string{}, err.Error(), 0)
		return err
	}
	settings, err := s.loadSettings(ctx, job.HouseholdID)
	if err != nil {
		s.finishJob(ctx, job, StatusFailed, map[string]string{}, "failed to load enrichment settings", 0)
		return err
	}
	if !s.globalEnabled() {
		msg := "product enrichment is disabled"
		s.finishJob(ctx, job, StatusFailed, map[string]string{}, msg, 0)
		return errors.New(msg)
	}
	if isManualTrigger(job.Trigger) && !settings.ManualLookupEnabled {
		msg := "manual enrichment lookup is disabled"
		s.finishJob(ctx, job, StatusFailed, map[string]string{}, msg, 0)
		return errors.New(msg)
	}

	input := providers.LookupInput{
		HouseholdID:  job.HouseholdID,
		ProductID:    job.ProductID,
		ProductName:  product.Name,
		Brand:        product.Brand,
		UPC:          product.UPC,
		LookupKey:    job.LookupKey,
		AllowPrivate: s.Cfg != nil && s.Cfg.AllowPrivateIntegrations,
	}
	if key, ok := strings.CutPrefix(job.LookupKey, "upc:"); ok && key != "" {
		input.UPC = &key
	}
	if rawURL, ok := strings.CutPrefix(job.LookupKey, "url:"); ok && rawURL != "" {
		input.URL = &rawURL
	}
	input.USDAAPIKey, _ = s.usdaAPIKey(ctx, job.HouseholdID)

	selected, providerStatus, providerErrors := s.providerPlan(job, settings, input)
	if len(selected) == 0 {
		msg := "no enabled enrichment providers are available"
		if len(providerErrors) > 0 {
			msg = strings.Join(providerErrors, "; ")
		}
		s.finishJob(ctx, job, StatusFailed, providerStatus, msg, 0)
		return errors.New(msg)
	}

	successes := 0
	storedSuggestions := 0
	rateLimited := false
	nextAttempt := time.Time{}

	for _, name := range selected {
		provider := s.Providers[name]
		if provider == nil {
			providerStatus[name] = "skipped"
			providerErrors = append(providerErrors, name+": provider unavailable")
			continue
		}
		if limited, ok := provider.(providers.RateLimitedProvider); ok && s.Limiter != nil {
			if err := s.Limiter.Wait(ctx, limited.RateLimitKey(), limited.MinInterval()); err != nil {
				providerStatus[name] = "failed"
				providerErrors = append(providerErrors, name+": "+err.Error())
				continue
			}
		}
		lookupCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		metadata, err := provider.Lookup(lookupCtx, input)
		cancel()
		if err != nil {
			if isRateLimitError(err) {
				rateLimited = true
				nextAttempt = retryAt(job.AttemptCount)
			}
			providerStatus[name] = "failed"
			providerErrors = append(providerErrors, name+": "+err.Error())
			continue
		}
		providerStatus[name] = "succeeded"
		successes++
		for _, item := range metadata {
			count, err := s.storeMetadataResult(ctx, job, item)
			if err != nil {
				providerStatus[name] = "failed"
				providerErrors = append(providerErrors, name+": "+err.Error())
				continue
			}
			storedSuggestions += count
		}
	}

	if rateLimited && successes == 0 {
		msg := strings.Join(providerErrors, "; ")
		if msg == "" {
			msg = "provider rate limited"
		}
		_, _ = s.DB.ExecContext(ctx,
			`UPDATE product_enrichment_jobs
			    SET status = 'queued',
			        attempt_count = CASE WHEN attempt_count > 0 THEN attempt_count - 1 ELSE 0 END,
			        started_at = NULL,
			        next_attempt_at = ?,
			        last_error = ?,
			        updated_at = CURRENT_TIMESTAMP
			  WHERE id = ? AND household_id = ? AND product_id = ?`,
			nextAttempt.Format("2006-01-02 15:04:05"), msg, job.ID, job.HouseholdID, job.ProductID,
		)
		return nil
	}

	status := StatusSucceeded
	if successes == 0 {
		status = StatusFailed
	} else if len(providerErrors) > 0 {
		status = StatusPartial
	}
	errMsg := strings.Join(providerErrors, "; ")
	s.finishJob(ctx, job, status, providerStatus, errMsg, storedSuggestions)
	return nil
}

func (s *Service) storeMetadataResult(ctx context.Context, job Job, item providers.Metadata) (int, error) {
	if item.Source == "" {
		return 0, nil
	}
	sourceURL := strings.TrimSpace(item.SourceURL)
	var linkID *string
	if sourceURL != "" {
		confidence := nullableConfidence(item.Confidence)
		id, err := s.Store.UpsertProductLink(ctx, estore.ProductLinkInput{
			ProductID:        job.ProductID,
			Source:           item.Source,
			ExternalID:       item.SourceRecordID,
			URL:              sourceURL,
			Label:            sourceLabelPtr(item.Source),
			FetchedAt:        item.FetchedAt,
			HTTPStatus:       item.HTTPStatus,
			ContentHash:      ptrStringValue(item.ContentHash),
			LastError:        item.LastError,
			SourceConfidence: confidence,
		})
		if err != nil {
			return 0, err
		}
		linkID = &id
	}
	var sourceURLPtr *string
	if sourceURL != "" {
		sourceURLPtr = &sourceURL
	}
	confidence := nullableConfidence(item.Confidence)
	metadataID, err := s.Store.UpsertMetadata(ctx, estore.MetadataInput{
		HouseholdID:    job.HouseholdID,
		ProductID:      job.ProductID,
		ProductLinkID:  linkID,
		Source:         item.Source,
		SourceRecordID: item.SourceRecordID,
		SourceURL:      sourceURLPtr,
		LookupKey:      &item.LookupKey,
		Payload:        item.Payload,
		ContentHash:    item.ContentHash,
		FetchedAt:      item.FetchedAt,
		ExpiresAt:      item.ExpiresAt,
		HTTPStatus:     item.HTTPStatus,
		LastError:      item.LastError,
		Confidence:     confidence,
	})
	if err != nil {
		return 0, err
	}
	ids, err := s.Store.StoreSuggestions(ctx, job.ProductID, linkID, &metadataID, item.Suggestions, job.Trigger == TriggerManualRefresh)
	if err != nil {
		return 0, err
	}
	return len(ids), nil
}

func (s *Service) providerPlan(job Job, settings settingsRow, input providers.LookupInput) ([]string, map[string]string, []string) {
	requested := normalizeSources(job.RequestedSources)
	requestedSet := map[string]struct{}{}
	for _, source := range requested {
		requestedSet[source] = struct{}{}
	}
	explicit := len(requestedSet) > 0
	allows := func(source string) bool {
		if !explicit {
			return true
		}
		_, ok := requestedSet[source]
		return ok
	}
	providerStatus := map[string]string{}
	var providerErrors []string
	skip := func(source, reason string) {
		if !explicit || !allows(source) {
			return
		}
		providerStatus[source] = "skipped"
		providerErrors = append(providerErrors, source+": "+reason)
	}

	out := make([]string, 0, 2)
	if input.URL != nil && allows("url") {
		out = append(out, "url")
		return out, providerStatus, providerErrors
	}
	if input.UPC != nil && strings.TrimSpace(*input.UPC) != "" {
		if allows("openfoodfacts") {
			if settings.ProviderOpenFoodFactsEnabled {
				out = append(out, "openfoodfacts")
			} else {
				skip("openfoodfacts", "provider disabled")
			}
		}
		if allows("usda_fdc") {
			switch {
			case !settings.ProviderUSDAFDCEnabled:
				skip("usda_fdc", "provider disabled")
			case strings.TrimSpace(input.USDAAPIKey) == "":
				skip("usda_fdc", "api key not configured")
			default:
				out = append(out, "usda_fdc")
			}
		}
		skip("kroger", "not implemented in this phase")
	}
	return out, providerStatus, providerErrors
}

func (s *Service) finishJob(ctx context.Context, job Job, status string, providerStatus map[string]string, errMsg string, storedSuggestions int) {
	var lastError interface{}
	if strings.TrimSpace(errMsg) != "" {
		lastError = errMsg
	}
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE product_enrichment_jobs
		    SET status = ?, last_error = ?, finished_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		  WHERE id = ? AND household_id = ? AND product_id = ?`,
		status, lastError, job.ID, job.HouseholdID, job.ProductID,
	); err != nil && s.Log != nil {
		s.Log.Warn("enrichment: failed to finish job", "job_id", job.ID, "err", err)
	}
	if storedSuggestions > 0 {
		s.broadcastProductUpdated(job.HouseholdID, job.ProductID)
	}
	if isTerminalStatus(status) {
		s.broadcastJobUpdated(job.HouseholdID, job.ProductID, job.ID, status, providerStatus, errMsg)
	}
}

func (s *Service) markJobRunning(ctx context.Context, job Job) error {
	result, err := s.DB.ExecContext(ctx,
		`UPDATE product_enrichment_jobs
		    SET status = 'running',
		        attempt_count = attempt_count + 1,
		        started_at = COALESCE(started_at, CURRENT_TIMESTAMP),
		        next_attempt_at = NULL,
		        last_error = NULL,
		        updated_at = CURRENT_TIMESTAMP
		  WHERE id = ? AND household_id = ? AND product_id = ? AND status = 'queued'`,
		job.ID, job.HouseholdID, job.ProductID,
	)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Service) loadProduct(ctx context.Context, householdID, productID string) (productRow, error) {
	var p productRow
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, household_id, name, brand, upc
		   FROM products
		  WHERE id = ? AND household_id = ?`,
		productID, householdID,
	).Scan(&p.ID, &p.HouseholdID, &p.Name, &p.Brand, &p.UPC)
	return p, err
}

func (s *Service) loadSettings(ctx context.Context, householdID string) (settingsRow, error) {
	if _, err := s.DB.ExecContext(ctx,
		`INSERT OR IGNORE INTO product_enrichment_settings (household_id)
		 VALUES (?)`,
		householdID,
	); err != nil {
		return settingsRow{}, err
	}
	var raw struct {
		manual, auto, sweep, off, usda, kroger int
		limit                                  int
	}
	err := s.DB.QueryRowContext(ctx,
		`SELECT manual_lookup_enabled, auto_on_scan_enabled, scheduled_sweep_enabled,
		        provider_openfoodfacts_enabled, provider_usda_fdc_enabled, provider_kroger_enabled,
		        first_run_backfill_limit
		   FROM product_enrichment_settings
		  WHERE household_id = ?`,
		householdID,
	).Scan(&raw.manual, &raw.auto, &raw.sweep, &raw.off, &raw.usda, &raw.kroger, &raw.limit)
	if err != nil {
		return settingsRow{}, err
	}
	return settingsRow{
		ManualLookupEnabled:          raw.manual != 0,
		AutoOnScanEnabled:            raw.auto != 0,
		ScheduledSweepEnabled:        raw.sweep != 0,
		ProviderOpenFoodFactsEnabled: raw.off != 0,
		ProviderUSDAFDCEnabled:       raw.usda != 0,
		ProviderKrogerEnabled:        raw.kroger != 0,
		FirstRunBackfillLimit:        raw.limit,
	}, nil
}

func (s *Service) usdaAPIKey(ctx context.Context, householdID string) (string, string) {
	store := db.NewIntegrationStore(s.DB)
	integration, err := store.GetByType(ctx, householdID, models.IntegrationTypeUSDAFDC)
	if err == nil && integration != nil && integration.Enabled {
		var cfg models.USDAFDCConfig
		if json.Unmarshal(integration.Config, &cfg) == nil && strings.TrimSpace(cfg.APIKey) != "" {
			return strings.TrimSpace(cfg.APIKey), "household"
		}
	}
	if s.Cfg != nil && strings.TrimSpace(s.Cfg.USDAFDCAPIKey) != "" {
		return strings.TrimSpace(s.Cfg.USDAFDCAPIKey), "env"
	}
	return "", ""
}

func (s *Service) globalEnabled() bool {
	return s.Cfg == nil || s.Cfg.ProductEnrichmentEnabled
}

func (s *Service) getJobByID(ctx context.Context, jobID string) (Job, error) {
	return scanJob(s.DB.QueryRowContext(ctx,
		`SELECT id, household_id, product_id, requested_by_user_id, trigger, lookup_key,
		        requested_sources, status, attempt_count, next_attempt_at, last_error,
		        queued_at, started_at, finished_at, updated_at
		   FROM product_enrichment_jobs
		  WHERE id = ?`,
		jobID,
	))
}

func (s *Service) findActiveJob(ctx context.Context, householdID, productID, trigger, lookupKey string) (Job, error) {
	return scanJob(s.DB.QueryRowContext(ctx,
		`SELECT id, household_id, product_id, requested_by_user_id, trigger, lookup_key,
		        requested_sources, status, attempt_count, next_attempt_at, last_error,
		        queued_at, started_at, finished_at, updated_at
		   FROM product_enrichment_jobs
		  WHERE household_id = ? AND product_id = ? AND trigger = ? AND lookup_key = ?
		    AND status IN ('queued', 'running')
		  ORDER BY queued_at DESC
		  LIMIT 1`,
		householdID, productID, trigger, lookupKey,
	))
}

type scanner interface {
	Scan(dest ...interface{}) error
}

func scanJob(row scanner) (Job, error) {
	var job Job
	var userID, requestedSources, lastError sql.NullString
	var nextAttempt, startedAt, finishedAt sql.NullTime
	if err := row.Scan(
		&job.ID, &job.HouseholdID, &job.ProductID, &userID, &job.Trigger, &job.LookupKey,
		&requestedSources, &job.Status, &job.AttemptCount, &nextAttempt, &lastError,
		&job.QueuedAt, &startedAt, &finishedAt, &job.UpdatedAt,
	); err != nil {
		return job, err
	}
	if userID.Valid {
		job.RequestedByUserID = &userID.String
	}
	if requestedSources.Valid && strings.TrimSpace(requestedSources.String) != "" {
		_ = json.Unmarshal([]byte(requestedSources.String), &job.RequestedSources)
	}
	if nextAttempt.Valid {
		job.NextAttemptAt = &nextAttempt.Time
	}
	if lastError.Valid {
		job.LastError = &lastError.String
	}
	if startedAt.Valid {
		job.StartedAt = &startedAt.Time
	}
	if finishedAt.Valid {
		job.FinishedAt = &finishedAt.Time
	}
	return job, nil
}

func normalizeSources(sources []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(sources))
	for _, source := range sources {
		source = normalizeSource(source)
		if source == "" {
			continue
		}
		if _, ok := seen[source]; ok {
			continue
		}
		seen[source] = struct{}{}
		out = append(out, source)
	}
	return out
}

func normalizeSource(source string) string {
	source = strings.ToLower(strings.TrimSpace(source))
	switch source {
	case "usda":
		return "usda_fdc"
	case "open_food_facts":
		return "openfoodfacts"
	}
	return source
}

func retryAt(attemptCount int) time.Time {
	backoff := time.Duration(1<<min(attemptCount, 6)) * time.Minute
	return time.Now().UTC().Add(backoff)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func isManualTrigger(trigger string) bool {
	return trigger == TriggerManualLookup || trigger == TriggerManualRefresh
}

func isTerminalStatus(status string) bool {
	switch status {
	case StatusSucceeded, StatusPartial, StatusFailed, StatusCancelled:
		return true
	default:
		return false
	}
}

func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "rate limit") || strings.Contains(msg, "rate limited") || strings.Contains(msg, "429")
}

func sourceLabelPtr(source string) *string {
	switch source {
	case "openfoodfacts":
		return stringPtr("Open Food Facts")
	case "usda_fdc":
		return stringPtr("USDA FoodData Central")
	case "kroger":
		return stringPtr("Kroger product page")
	case "url":
		return stringPtr("Product URL")
	default:
		if source == "" {
			return nil
		}
		return &source
	}
}

func nullableConfidence(value float64) *float64 {
	if value <= 0 {
		return nil
	}
	return &value
}

func ptrStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func nullableString(value string) interface{} {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func nullableUserID(value string) interface{} {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func stringPtr(value string) *string { return &value }

func (s *Service) broadcastProductUpdated(householdID, productID string) {
	if s.Hub == nil {
		return
	}
	s.Hub.Broadcast(ws.Message{
		Type:      ws.EventProductUpdated,
		Household: householdID,
		Payload: map[string]interface{}{
			"product_id":     productID,
			"changed_fields": []string{"enrichment_suggestions"},
		},
	})
}

func (s *Service) broadcastJobUpdated(householdID, productID, jobID, status string, providerStatus map[string]string, errMsg string) {
	if s.Hub == nil {
		return
	}
	var errValue interface{}
	if strings.TrimSpace(errMsg) != "" {
		errValue = errMsg
	}
	s.Hub.Broadcast(ws.Message{
		Type:      ws.EventProductEnrichmentJobUpdated,
		Household: householdID,
		Payload: map[string]interface{}{
			"product_id":      productID,
			"job_id":          jobID,
			"status":          status,
			"provider_status": providerStatus,
			"error":           errValue,
		},
	})
}
