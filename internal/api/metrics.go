// Package api — metrics.go exposes Prometheus-compatible counters, gauges,
// and histograms for operational visibility. The /metrics endpoint is served
// by promhttp and intentionally unauthenticated: operators are expected to
// scrape it from a trusted network, or firewall it, or expose it on a
// separate metrics port. Never put secrets in metric labels.
package api

import (
	"context"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
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
	workerQueueDepth prometheus.Gauge

	// LLM token consumption (counter; labeled by provider/model/type).
	llmTokensTotal *prometheus.CounterVec

	// Image preprocessing fallback rate (counter; labeled by reason).
	preprocessFallbacksTotal *prometheus.CounterVec

	// Storage bytes on disk under DATA_DIR/receipts/ (gauge; sampled
	// periodically; labeled by type=original|processed).
	storageBytes *prometheus.GaugeVec

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
			Help: "Total bytes on disk under DATA_DIR/receipts/, by file type (original|processed).",
		},
		[]string{"type"},
	)

	for _, c := range []prometheus.Collector{
		m.httpRequestsTotal,
		m.httpRequestDuration,
		m.workerQueueDepth,
		m.llmTokensTotal,
		m.preprocessFallbacksTotal,
		m.storageBytes,
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
	if cfg.DataDir != "" {
		m.wg.Add(1)
		go m.sampleStorageBytes(cfg.DataDir, storageInterval)
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

// sampleStorageBytes ticks every `interval` and updates storageBytes by
// walking DATA_DIR/receipts/. Files whose basename starts with "processed_"
// are counted under type=processed; everything else under type=original.
// Walk errors are logged (Warn) and do NOT update the gauge, so a transient
// I/O failure leaves the previous sample in place rather than zeroing it.
func (m *Metrics) sampleStorageBytes(dataDir string, interval time.Duration) {
	defer m.wg.Done()
	root := filepath.Join(dataDir, "receipts")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sample := func() {
		var origBytes, procBytes int64
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
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
			if strings.HasPrefix(name, "processed_") {
				procBytes += sz
			} else {
				origBytes += sz
			}
			return nil
		})
		// filepath.WalkDir returns nil if root doesn't exist only on the
		// walkErr path above. On a real error other than that, log but
		// do not zero the gauge.
		if err != nil && !isMissingPathErr(err) {
			slog.Warn("metrics: storage walk failed", "root", root, "err", err)
			return
		}
		m.storageBytes.WithLabelValues("receipts_original").Set(float64(origBytes))
		m.storageBytes.WithLabelValues("receipts_processed").Set(float64(procBytes))
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

