package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/models"
)

func TestUSDAFDCIntegrationTestPingsEndpoint(t *testing.T) {
	var hit atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit.Store(true)
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if got := r.URL.Query().Get("api_key"); got != "test-key" {
			t.Errorf("api_key = %q, want test-key", got)
		}
		if got := r.URL.Query().Get("query"); got == "" {
			t.Errorf("query was empty")
		}
		if got := r.URL.Query().Get("pageSize"); got != "1" {
			t.Errorf("pageSize = %q, want 1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"foods":[]}`))
	}))
	defer server.Close()

	oldBase := usdaFDCIntegrationTestBase
	usdaFDCIntegrationTestBase = server.URL
	defer func() { usdaFDCIntegrationTestBase = oldBase }()

	handler := NewIntegrationHandler(nil, &config.Config{})
	e := echo.New()
	c, rec := makeContext(e, http.MethodPost, "/api/v1/integrations/usda_fdc/test", `{"api_key":"test-key"}`, "", "")
	c.SetParamNames("type")
	c.SetParamValues(models.IntegrationTypeUSDAFDC)

	if err := handler.Test(c); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !hit.Load() {
		t.Fatalf("USDA test endpoint was not called")
	}
	var body testResult
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !body.OK || body.Message != "connected" {
		t.Fatalf("response = %+v, want connected result", body)
	}
}
