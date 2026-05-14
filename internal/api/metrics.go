// Package api — metrics.go exposes Prometheus-compatible counters, gauges,
// and histograms for operational visibility. The /metrics endpoint is served
// by promhttp and intentionally unauthenticated: operators are expected to
// scrape it from a trusted network, or firewall it, or expose it on a
// separate metrics port. Never put secrets in metric labels.
package api

import (
	"context"
	"database/sql"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mstefanko/cartledger/internal/storage"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// QueueDepthReporter is implemented by any worker whose queue depth we want
// to track. Keeping this as an interface (not a direct worker import) lets
// NewMetrics stay free of an internal/worker → internal/api cycle risk.
type QueueDepthReporter interface {
	QueueDepth() int
}

// Metrics bundles every Prometheus collector the process exposes. All
// collectors are registered with the provided prometheus.Registerer (default
// registry when nil). Call Close() to stop the background samplers.
//
// Metric names are snake_case and prefixed with `cartledger_`. Label
// cardinality is kept bounded:
//
//   - HTTP route labels come from c.Path() (Echo's route template, e.g.
//     "/api/v1/receipts/:id"), not the literal URL, to avoid an unbounded
//     path explosion from IDs and path params.
//   - HTTP status is reduced to a status-class label ("2xx", "3xx",
//     "4xx", "5xx") rather than the exact code — a 5x reduction for
//     minimal loss of operator information.
//   - LLM token labels are (provider, model, type). provider+model is a
//     small fixed set in practice.
type Metrics struct {
	reg prometheus.Registerer

	// HTTP server — request rate + latency.
	httpRequestsTotal   *prometheus.CounterVec
	httpRequestDuration *prometheus.HistogramVec

	// Worker queue depth (gauge; sampled periodically).
	workerQueueDepth                 prometheus.Gauge
	enrichmentQueueDepth             *prometheus.GaugeVec
	enrichmentJobsQueuedTotal        *prometheus.CounterVec
	enrichmentJobsFinishedTotal      *prometheus.CounterVec
	enrichmentProviderLatencySeconds *prometheus.HistogramVec
	enrichmentProviderRequestsTotal  *prometheus.CounterVec

	// LLM token consumption (counter; labeled by provider/model/type).
	llmTokensTotal *prometheus.CounterVec

	// Image preprocessing fallback rate (counter; labeled by reason).
	preprocessFallbacksTotal *prometheus.CounterVec

	// Storage bytes on disk under DATA_DIR/receipts/ (gauge; sampled
	// periodically; labeled by type=receipts_original|receipts_processed|product_images).
	storageBytes *prometheus.GaugeVec
	// Storage row health from receipt_images/product_images.
	storageImageRows *prometheus.GaugeVec

	// Retention deletions: counter of original image files removed by the
	// retention janitor, labeled by reason ("age" for the normal mtime-based
	// sweep). Processed images are never deleted.
	retentionDeletedTotal *prometheus.CounterVec

	// Backup operations. Wired to internal/backup.Runner via the Record*
	// helpers below. Duration is a histogram keyed on the final status so
	// operators can separate successful-run latency from failed-run latency.
	// Size is a gauge (last observed bytes of the most recent complete
	// archive). Missing-images is a counter summed across all runs.
	backupDurationSeconds *prometheus.HistogramVec
	backupSizeBytes       prometheus.Gauge
	backupMissingImages   prometheus.Counter

	// Background sampler lifecycle.
	stopOnce sync.Once
	stop     chan struct{}
	wg       sync.WaitGroup
}

// MetricsConfig is the optional tuning knob bundle passed to NewMetrics.
// Zero values fall back to sensible defaults.
type MetricsConfig struct {
	// Registerer to register collectors on; nil => prometheus.DefaultRegisterer.
	Registerer prometheus.Registerer
	// DataDir is the DATA_DIR root. The storage-bytes sampler walks
	// DATA_DIR/receipts/ when this is non-empty.
	DataDir string
	// Database enables storage row health gauges for pruned originals and
	// missing active image rows.
	Database *sql.DB
	// Worker whose queue-depth is sampled. nil disables the sampler.
	Worker QueueDepthReporter
	// QueueSampleInterval — how often to sample queue depth (default 5s).
	QueueSampleInterval time.Duration
	// StorageSampleInterval — how often to walk storage (default 1m).
	StorageSampleInterval time.Duration
}

// NewMetrics constructs, registers, and starts a Metrics instance. It is
// safe to call once per process — additional calls would fail registration
// because metric names must be unique in a registry.
func NewMetrics(cfg MetricsConfig) (*Metrics, error) {
	reg := cfg.Registerer
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}

	m := &Metrics{
		reg:  reg,
		stop: make(chan struct{}),
	}

	m.httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cartledger_http_requests_total",
			Help: "Total HTTP requests processed, by method, route template, and status class.",
		},
		[]string{"method", "route", "status_class"},
	)
	m.httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "cartledger_http_request_duration_seconds",
			Help:    "HTTP request latency in seconds, by method, route template, and status class.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		},
		[]string{"method", "route", "status_class"},
	)
	m.workerQueueDepth = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "cartledger_worker_queue_depth",
			Help: "Number of receipt jobs currently queued in the worker pool.",
		},
	)
	m.enrichmentQueueDepth = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cartledger_enrichment_queue_depth",
			Help: "Product enrichment job rows by status.",
		},
		[]string{"status"},
	)
	m.enrichmentJobsQueuedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cartledger_enrichment_jobs_queued_total",
			Help: "Product enrichment jobs queued by trigger.",
		},
		[]string{"trigger"},
	)
	m.enrichmentJobsFinishedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cartledger_enrichment_jobs_finished_total",
			Help: "Product enrichment jobs finished by trigger, terminal status, and provider.",
		},
		[]string{"trigger", "status", "provider"},
	)
	m.enrichmentProviderLatencySeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "cartledger_enrichment_provider_latency_seconds",
			Help:    "External product enrichment provider lookup latency in seconds.",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 15, 30},
		},
		[]string{"provider"},
	)
	m.enrichmentProviderRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cartledger_enrichment_provider_requests_total",
			Help: "External product enrichment provider requests by provider and status class.",
		},
		[]string{"provider", "http_status"},
	)
	m.llmTokensTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cartledger_llm_tokens_total",
			Help: "Total LLM tokens consumed, by provider, model, and type (input|output).",
		},
		[]string{"provider", "model", "type"},
	)
	m.preprocessFallbacksTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cartledger_preprocess_fallbacks_total",
			Help: "Number of times image preprocessing fell back to the raw image, by reason.",
		},
		[]string{"reason"},
	)
	m.storageBytes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cartledger_storage_bytes",
			Help: "Total bytes on disk under DATA_DIR image storage, by file type.",
		},
		[]string{"type"},
	)
	m.storageImageRows = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cartledger_storage_image_rows",
			Help: "Image metadata row counts by state (pruned_originals|missing_active).",
		},
		[]string{"state"},
	)
	m.retentionDeletedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cartledger_retention_deleted_total",
			Help: "Total original receipt image files removed by the retention janitor, by reason.",
		},
		[]string{"reason"},
	)
	m.backupDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "cartledger_backup_duration_seconds",
			Help:    "Time to produce a backup archive, by final status (complete|failed).",
			Buckets: []float64{0.1, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300, 600},
		},
		[]string{"status"},
	)
	m.backupSizeBytes = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "cartledger_backup_size_bytes",
			Help: "Size in bytes of the most recent successful backup archive.",
		},
	)
	m.backupMissingImages = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "cartledger_backup_missing_images_total",
			Help: "Cumulative count of image files referenced from DB rows but absent from disk across all backup runs.",
		},
	)

	for _, c := range []prometheus.Collector{
		m.httpRequestsTotal,
		m.httpRequestDuration,
		m.workerQueueDepth,
		m.enrichmentQueueDepth,
		m.enrichmentJobsQueuedTotal,
		m.enrichmentJobsFinishedTotal,
		m.enrichmentProviderLatencySeconds,
		m.enrichmentProviderRequestsTotal,
		m.llmTokensTotal,
		m.preprocessFallbacksTotal,
		m.storageBytes,
		m.storageImageRows,
		m.retentionDeletedTotal,
		m.backupDurationSeconds,
		m.backupSizeBytes,
		m.backupMissingImages,
	} {
		if err := reg.Register(c); err != nil {
			return nil, err
		}
	}

	queueInterval := cfg.QueueSampleInterval
	if queueInterval <= 0 {
		queueInterval = 5 * time.Second
	}
	storageInterval := cfg.StorageSampleInterval
	if storageInterval <= 0 {
		storageInterval = time.Minute
	}

	if cfg.Worker != nil {
		m.wg.Add(1)
		go m.sampleQueueDepth(cfg.Worker, queueInterval)
	}
	if cfg.Database != nil {
		m.wg.Add(1)
		go m.sampleEnrichmentQueueDepth(cfg.Database, queueInterval)
	}
	if cfg.DataDir != "" {
		m.wg.Add(1)
		go m.sampleStorageBytes(cfg.DataDir, cfg.Database, storageInterval)
	}

	return m, nil
}

// Close stops all background samplers. Safe to call multiple times.
// Does NOT unregister collectors (Prometheus has no clean "unregister all"
// semantic per-instance when using the default registry).
func (m *Metrics) Close() {
	m.stopOnce.Do(func() {
		close(m.stop)
	})
	m.wg.Wait()
}

// Handler returns an echo.HandlerFunc that serves /metrics via promhttp.
func (m *Metrics) Handler() echo.HandlerFunc {
	h := promhttp.Handler()
	return func(c echo.Context) error {
		h.ServeHTTP(c.Response(), c.Request())
		return nil
	}
}

// HTTPMiddleware returns an Echo middleware that increments
// httpRequestsTotal and observes httpRequestDuration for every request. The
// route template (c.Path()) is used as the route label — this is Echo's
// registered pattern (e.g. "/api/v1/receipts/:id"), not the literal URL, so
// cardinality stays bounded regardless of how many distinct IDs are in play.
//
// Unregistered / not-found paths surface as "unknown" to avoid a label
// explosion from arbitrary client-supplied URLs (a bot hitting random
// paths would otherwise create one series per path).
func (m *Metrics) HTTPMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		start := time.Now()
		err := next(c)
		// After the handler runs, c.Path() returns the matched route template.
		// For NotFoundHandler paths it returns "" — relabel as "unknown".
		route := c.Path()
		if route == "" {
			route = "unknown"
		}
		status := c.Response().Status
		labels := prometheus.Labels{
			"method":       c.Request().Method,
			"route":        route,
			"status_class": statusClass(status),
		}
		m.httpRequestsTotal.With(labels).Inc()
		m.httpRequestDuration.With(labels).Observe(time.Since(start).Seconds())
		return err
	}
}

// statusClass reduces an HTTP status code to its class label
// ("2xx", "3xx", "4xx", "5xx"). Anything outside 200–599 is "other".
func statusClass(code int) string {
	switch {
	case code >= 200 && code < 300:
		return "2xx"
	case code >= 300 && code < 400:
		return "3xx"
	case code >= 400 && code < 500:
		return "4xx"
	case code >= 500 && code < 600:
		return "5xx"
	default:
		return "other"
	}
}

// RecordLLMTokens is called by LLM clients to increment token counters.
// Safe for nil receiver — callers don't have to null-check.
func (m *Metrics) RecordLLMTokens(provider, model string, inputTokens, outputTokens int64) {
	if m == nil {
		return
	}
	if inputTokens > 0 {
		m.llmTokensTotal.WithLabelValues(provider, model, "input").Add(float64(inputTokens))
	}
	if outputTokens > 0 {
		m.llmTokensTotal.WithLabelValues(provider, model, "output").Add(float64(outputTokens))
	}
}

func (m *Metrics) RecordEnrichmentJobQueued(trigger string) {
	if m == nil {
		return
	}
	if trigger == "" {
		trigger = "unknown"
	}
	m.enrichmentJobsQueuedTotal.WithLabelValues(trigger).Inc()
}

func (m *Metrics) RecordEnrichmentJobFinished(trigger, status, provider string) {
	if m == nil {
		return
	}
	if trigger == "" {
		trigger = "unknown"
	}
	if status == "" {
		status = "unknown"
	}
	if provider == "" {
		provider = "none"
	}
	m.enrichmentJobsFinishedTotal.WithLabelValues(trigger, status, provider).Inc()
}

func (m *Metrics) RecordEnrichmentProviderRequest(provider, httpStatus string) {
	if m == nil {
		return
	}
	if provider == "" {
		provider = "unknown"
	}
	if httpStatus == "" {
		httpStatus = "unknown"
	}
	m.enrichmentProviderRequestsTotal.WithLabelValues(provider, httpStatus).Inc()
}

func (m *Metrics) ObserveEnrichmentProviderLatency(provider string, seconds float64) {
	if m == nil {
		return
	}
	if provider == "" {
		provider = "unknown"
	}
	if seconds < 0 {
		seconds = 0
	}
	m.enrichmentProviderLatencySeconds.WithLabelValues(provider).Observe(seconds)
}

// RecordPreprocessFallback is called by imaging.PreprocessReceipt when the
// fallback path fires (e.g. decode error). Safe for nil receiver.
func (m *Metrics) RecordPreprocessFallback(reason string) {
	if m == nil {
		return
	}
	if reason == "" {
		reason = "unknown"
	}
	m.preprocessFallbacksTotal.WithLabelValues(reason).Inc()
}

// RecordRetentionDeleted is called by the retention janitor each time it
// removes an original receipt image file. Safe for nil receiver.
func (m *Metrics) RecordRetentionDeleted(reason string, n int) {
	if m == nil || n <= 0 {
		return
	}
	if reason == "" {
		reason = "age"
	}
	m.retentionDeletedTotal.WithLabelValues(reason).Add(float64(n))
}

// RecordBackupDuration is called by internal/backup.Runner once per backup
// run. status is "complete" or "failed". Safe for nil receiver so the runner
// can skip a nil-check when metrics aren't wired (tests).
func (m *Metrics) RecordBackupDuration(status string, d time.Duration) {
	if m == nil {
		return
	}
	if status == "" {
		status = "unknown"
	}
	m.backupDurationSeconds.WithLabelValues(status).Observe(d.Seconds())
}

// RecordBackupSize updates the gauge with the archive size from the most
// recent successful backup. Safe for nil receiver.
func (m *Metrics) RecordBackupSize(bytes int64) {
	if m == nil || bytes < 0 {
		return
	}
	m.backupSizeBytes.Set(float64(bytes))
}

// RecordBackupMissingImages increments the cumulative missing-images counter
// by `n`. Safe for nil receiver and no-op when n <= 0.
func (m *Metrics) RecordBackupMissingImages(n int) {
	if m == nil || n <= 0 {
		return
	}
	m.backupMissingImages.Add(float64(n))
}

// sampleQueueDepth ticks every `interval` and snapshots worker.QueueDepth()
// into workerQueueDepth. Exits when m.stop is closed.
func (m *Metrics) sampleQueueDepth(w QueueDepthReporter, interval time.Duration) {
	defer m.wg.Done()
	t := time.NewTicker(interval)
	defer t.Stop()
	// Prime the gauge immediately so scrapes before the first tick aren't blank.
	m.workerQueueDepth.Set(float64(w.QueueDepth()))
	for {
		select {
		case <-t.C:
			m.workerQueueDepth.Set(float64(w.QueueDepth()))
		case <-m.stop:
			return
		}
	}
}

func (m *Metrics) sampleEnrichmentQueueDepth(database *sql.DB, interval time.Duration) {
	defer m.wg.Done()
	t := time.NewTicker(interval)
	defer t.Stop()
	sample := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		rows, err := database.QueryContext(ctx,
			`SELECT status, COUNT(*)
			   FROM product_enrichment_jobs
			  WHERE status IN ('queued', 'running')
			  GROUP BY status`,
		)
		if err != nil {
			return
		}
		defer rows.Close()
		counts := map[string]float64{"queued": 0, "running": 0}
		for rows.Next() {
			var status string
			var count int
			if err := rows.Scan(&status, &count); err != nil {
				return
			}
			counts[status] = float64(count)
		}
		for status, count := range counts {
			m.enrichmentQueueDepth.WithLabelValues(status).Set(count)
		}
	}
	sample()
	for {
		select {
		case <-t.C:
			sample()
		case <-m.stop:
			return
		}
	}
}

// sampleStorageBytes ticks every `interval` and updates storageBytes by
// walking DATA_DIR/receipts/. Files whose basename starts with "processed_"
// are counted under type=processed; everything else under type=original.
// Walk errors are logged (Warn) and do NOT update the gauge, so a transient
// I/O failure leaves the previous sample in place rather than zeroing it.
func (m *Metrics) sampleStorageBytes(dataDir string, database *sql.DB, interval time.Duration) {
	defer m.wg.Done()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sample := func() {
		var origBytes, procBytes, productBytes int64
		err := filepath.WalkDir(dataDir, func(path string, d fs.DirEntry, walkErr error) error {
			// Respect cancellation so Close() doesn't block on a long walk.
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if walkErr != nil {
				// Missing root is fine — first-boot state. Other errors,
				// keep walking the rest of the tree.
				if strings.Contains(walkErr.Error(), "no such file") {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			name := d.Name()
			sz := info.Size()
			slashed := filepath.ToSlash(path)
			switch {
			case strings.Contains(slashed, "/products/"):
				productBytes += sz
			case strings.Contains(slashed, "/receipts/") && (strings.Contains(slashed, "/processed/") || strings.HasPrefix(name, "processed_")):
				procBytes += sz
			case strings.Contains(slashed, "/receipts/"):
				origBytes += sz
			}
			return nil
		})
		// filepath.WalkDir returns nil if root doesn't exist only on the
		// walkErr path above. On a real error other than that, log but
		// do not zero the gauge.
		if err != nil && !isMissingPathErr(err) {
			slog.Warn("metrics: storage walk failed", "root", dataDir, "err", err)
			return
		}
		m.storageBytes.WithLabelValues("receipts_original").Set(float64(origBytes))
		m.storageBytes.WithLabelValues("receipts_processed").Set(float64(procBytes))
		m.storageBytes.WithLabelValues("product_images").Set(float64(productBytes))
		if database != nil {
			pruned, missing := storageImageRowCounts(context.Background(), database, dataDir)
			m.storageImageRows.WithLabelValues("pruned_originals").Set(float64(pruned))
			m.storageImageRows.WithLabelValues("missing_active").Set(float64(missing))
		}
	}

	// Prime immediately.
	sample()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			sample()
		case <-m.stop:
			return
		}
	}
}

func storageImageRowCounts(ctx context.Context, database *sql.DB, dataDir string) (int, int) {
	var pruned int
	_ = database.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM receipt_images WHERE kind = 'original' AND deleted_at IS NOT NULL`,
	).Scan(&pruned)

	localStore, err := storage.NewLocal(dataDir)
	if err != nil {
		return pruned, 0
	}
	var keys []string
	if rows, err := database.QueryContext(ctx,
		`SELECT storage_key FROM receipt_images WHERE deleted_at IS NULL
		 UNION ALL
		 SELECT image_path FROM product_images WHERE image_path IS NOT NULL AND image_path != ''`,
	); err == nil {
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err == nil && key != "" {
				keys = append(keys, filepath.ToSlash(key))
			}
		}
		rows.Close()
	}
	var missing int
	for _, key := range keys {
		p, err := localStore.Path(key)
		if err != nil {
			missing++
			continue
		}
		if _, err := os.Stat(p); err != nil && os.IsNotExist(err) {
			missing++
		}
	}
	return pruned, missing
}

// isMissingPathErr is a lightweight check for "root does not exist" errors
// returned from filepath.WalkDir when DATA_DIR/receipts/ hasn't been
// created yet (fresh install before any scan).
func isMissingPathErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no such file") || strings.Contains(msg, "cannot find")
}
