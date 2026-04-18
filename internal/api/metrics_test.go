package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/prometheus/client_golang/prometheus"
)

type stubQueue struct{ depth int }

func (s *stubQueue) QueueDepth() int { return s.depth }

// TestMetricsNewAndClose verifies that NewMetrics wires up every collector
// with a private registry, records some values, and that Close() shuts the
// samplers down cleanly. We emit a value for each vec before gathering
// because CounterVec/HistogramVec only produce output for observed label
// tuples — an unused vec is correctly absent from a Gather() response.
func TestMetricsNewAndClose(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := NewMetrics(MetricsConfig{
		Registerer:            reg,
		Worker:                &stubQueue{depth: 7},
		QueueSampleInterval:   20 * time.Millisecond,
		StorageSampleInterval: time.Hour, // don't let storage walk fire
	})
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}

	// Give the queue-depth sampler at least one tick.
	time.Sleep(60 * time.Millisecond)

	// Exercise each vec so Gather() includes a sample for it.
	e := echo.New()
	e.Use(m.HTTPMiddleware)
	e.GET("/x", func(c echo.Context) error { return c.NoContent(204) })
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	m.RecordLLMTokens("claude", "claude-sonnet-4-20250514", 10, 5)
	m.RecordPreprocessFallback("decode_failed")
	m.storageBytes.WithLabelValues("receipts_original").Set(0)

	m.Close()
	// Second Close must be a no-op (stopOnce guard).
	m.Close()

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	required := map[string]bool{
		"cartledger_http_requests_total":           false,
		"cartledger_http_request_duration_seconds": false,
		"cartledger_worker_queue_depth":            false,
		"cartledger_llm_tokens_total":              false,
		"cartledger_preprocess_fallbacks_total":    false,
		"cartledger_storage_bytes":                 false,
	}
	for _, mf := range mfs {
		if _, ok := required[mf.GetName()]; ok {
			required[mf.GetName()] = true
		}
	}
	for name, seen := range required {
		if !seen {
			t.Errorf("metric missing from registry: %s", name)
		}
	}
}

// TestHTTPMiddlewareLabels confirms the middleware uses the route TEMPLATE
// (c.Path()) — not the literal URL path — as the label value. A regression
// here would cause cardinality explosion in production.
func TestHTTPMiddlewareLabels(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := NewMetrics(MetricsConfig{
		Registerer:            reg,
		StorageSampleInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	defer m.Close()

	e := echo.New()
	e.Use(m.HTTPMiddleware)
	e.GET("/things/:id", func(c echo.Context) error { return c.String(http.StatusCreated, "ok") })

	for _, id := range []string{"alpha", "beta", "gamma"} {
		req := httptest.NewRequest(http.MethodGet, "/things/"+id, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
	}

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	var seen int
	for _, mf := range mfs {
		if mf.GetName() != "cartledger_http_requests_total" {
			continue
		}
		for _, me := range mf.GetMetric() {
			labels := map[string]string{}
			for _, l := range me.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			if labels["route"] != "/things/:id" {
				t.Errorf("route label wanted /things/:id; got %q", labels["route"])
			}
			if labels["status_class"] != "2xx" {
				t.Errorf("status_class wanted 2xx; got %q", labels["status_class"])
			}
			if labels["method"] != http.MethodGet {
				t.Errorf("method wanted GET; got %q", labels["method"])
			}
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("expected exactly 1 series (single route template), got %d", seen)
	}
}

func TestStatusClass(t *testing.T) {
	cases := map[int]string{
		100: "other",
		200: "2xx",
		301: "3xx",
		404: "4xx",
		500: "5xx",
		599: "5xx",
		700: "other",
	}
	for code, want := range cases {
		if got := statusClass(code); got != want {
			t.Errorf("statusClass(%d)=%q want %q", code, got, want)
		}
	}
}

// TestHandlerServesMetrics asserts that /metrics serves the Prometheus text
// format exposition. Uses the default registry because promhttp.Handler()
// is bound to it; registration conflicts from other tests are avoided by
// guarding the test-once execution.
func TestHandlerServesMetrics(t *testing.T) {
	m := &Metrics{}
	h := m.Handler()
	e := echo.New()
	e.GET("/metrics", h)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	body := rec.Body.String()
	// The default registry always exposes go_goroutines etc.
	if !strings.Contains(body, "# HELP") {
		t.Errorf("response missing HELP lines; body head=%q", body[:min(200, len(body))])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
