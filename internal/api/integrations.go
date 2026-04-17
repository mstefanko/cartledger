package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
	"github.com/mstefanko/cartledger/internal/models"
)

// IntegrationHandler exposes household-scoped CRUD + test endpoints for
// external-service integrations (Mealie today; Tandoor/Grocy/etc. later).
type IntegrationHandler struct {
	DB    *sql.DB
	Cfg   *config.Config
	Store *db.IntegrationStore
}

// NewIntegrationHandler constructs an IntegrationHandler with a backing store.
func NewIntegrationHandler(database *sql.DB, cfg *config.Config) *IntegrationHandler {
	return &IntegrationHandler{
		DB:    database,
		Cfg:   cfg,
		Store: db.NewIntegrationStore(database),
	}
}

// RegisterRoutes mounts integration endpoints onto the protected group.
func (h *IntegrationHandler) RegisterRoutes(protected *echo.Group) {
	g := protected.Group("/integrations")
	g.GET("", h.List)
	g.PUT("/:type", h.Upsert)
	g.DELETE("/:type", h.Delete)
	g.POST("/:type/test", h.Test)
}

// --- Request / response types ---

// mealieSaveRequest is the body accepted by PUT /integrations/mealie.
type mealieSaveRequest struct {
	BaseURL string `json:"base_url"`
	Token   string `json:"token"`
	// Enabled defaults to true when omitted on create; we treat *bool so the
	// caller can explicitly set false.
	Enabled *bool `json:"enabled,omitempty"`
}

// integrationResponse is the masked shape returned to clients. The token is
// never present — only `configured` signals that a token is on file.
type integrationResponse struct {
	Type       string `json:"type"`
	Enabled    bool   `json:"enabled"`
	Configured bool   `json:"configured"`
	BaseURL    string `json:"base_url,omitempty"`
}

// testResult is the response body for POST /integrations/:type/test.
type testResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// --- Handlers ---

// List returns all integrations configured for the household (no tokens).
// GET /api/v1/integrations
func (h *IntegrationHandler) List(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	ctx := c.Request().Context()

	rows, err := h.Store.GetByHousehold(ctx, householdID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	out := make([]integrationResponse, 0, len(rows))
	for _, it := range rows {
		out = append(out, toIntegrationResponse(it))
	}
	return c.JSON(http.StatusOK, out)
}

// Upsert creates or replaces the integration row for the given type.
// PUT /api/v1/integrations/:type  body: {base_url, token, enabled?}
func (h *IntegrationHandler) Upsert(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	integrationType := c.Param("type")

	switch integrationType {
	case models.IntegrationTypeMealie:
		return h.upsertMealie(c, householdID)
	default:
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "unknown integration type"})
	}
}

func (h *IntegrationHandler) upsertMealie(c echo.Context, householdID string) error {
	var req mealieSaveRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	req.BaseURL = strings.TrimSpace(req.BaseURL)
	req.Token = strings.TrimSpace(req.Token)
	if req.BaseURL == "" || req.Token == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "base_url and token are required"})
	}

	// SSRF guard: validate the URL before we persist it — otherwise a later
	// call to GET /status on this row would hit an internal address.
	u, err := validateIntegrationURL(req.BaseURL, h.Cfg.AllowPrivateIntegrations)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": sanitizeURLError(err)})
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	cfgBytes, err := json.Marshal(models.MealieConfig{
		Version: 1,
		BaseURL: u.String(),
		Token:   req.Token,
	})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "marshal config"})
	}

	it := &models.Integration{
		HouseholdID: householdID,
		Type:        models.IntegrationTypeMealie,
		Enabled:     enabled,
		Config:      cfgBytes,
	}
	if err := h.Store.Upsert(c.Request().Context(), it); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusOK, toIntegrationResponse(*it))
}

// Delete removes the integration row for (household, type).
// DELETE /api/v1/integrations/:type
func (h *IntegrationHandler) Delete(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	integrationType := c.Param("type")

	deleted, err := h.Store.Delete(c.Request().Context(), householdID, integrationType)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if !deleted {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "integration not found"})
	}
	return c.NoContent(http.StatusNoContent)
}

// Test pings the integration with submitted credentials without persisting.
// POST /api/v1/integrations/:type/test  body: same as PUT
func (h *IntegrationHandler) Test(c echo.Context) error {
	integrationType := c.Param("type")

	switch integrationType {
	case models.IntegrationTypeMealie:
		return h.testMealie(c)
	default:
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "unknown integration type"})
	}
}

func (h *IntegrationHandler) testMealie(c echo.Context) error {
	var req mealieSaveRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	req.BaseURL = strings.TrimSpace(req.BaseURL)
	req.Token = strings.TrimSpace(req.Token)
	if req.BaseURL == "" || req.Token == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "base_url and token are required"})
	}

	u, err := validateIntegrationURL(req.BaseURL, h.Cfg.AllowPrivateIntegrations)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": sanitizeURLError(err)})
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()

	ok, msg := pingMealie(ctx, u.String(), req.Token)
	// Always return 200 with {ok, message} so the frontend can render a
	// connection result inline regardless of outcome. On failure, `message`
	// is a short generic status — never the upstream body.
	return c.JSON(http.StatusOK, testResult{OK: ok, Message: msg})
}

// pingMealie performs a short GET against the Mealie /api/app/about endpoint
// with a 5s timeout and redirects disabled. It returns a generic status string
// on failure — NEVER the upstream response body, which could leak data from
// an internal service an attacker probed through this endpoint.
func pingMealie(ctx context.Context, baseURL, token string) (bool, string) {
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	target := strings.TrimRight(baseURL, "/") + "/api/app/about"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return false, "invalid request"
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return false, classifyNetError(err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return true, "connected"
	case resp.StatusCode == http.StatusUnauthorized:
		return false, "401 unauthorized"
	case resp.StatusCode == http.StatusForbidden:
		return false, "403 forbidden"
	case resp.StatusCode == http.StatusNotFound:
		return false, "404 not found (wrong base URL?)"
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return false, "unexpected redirect"
	default:
		return false, http.StatusText(resp.StatusCode)
	}
}

// classifyNetError reduces an http client error to a short opaque string.
// We never return err.Error() directly because it can include URL fragments
// from internal addresses if validation was bypassed.
func classifyNetError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "context deadline exceeded"), strings.Contains(msg, "Timeout"):
		return "timeout"
	case strings.Contains(msg, "connection refused"):
		return "connection refused"
	case strings.Contains(msg, "no such host"):
		return "host not found"
	case strings.Contains(msg, "tls"), strings.Contains(msg, "x509"):
		return "tls error"
	default:
		return "connection failed"
	}
}

// sanitizeURLError converts validateIntegrationURL errors into short strings
// suitable for the client response.
func sanitizeURLError(err error) string {
	switch {
	case errors.Is(err, errPrivateAddressBlocked):
		return "private/loopback address not allowed"
	case errors.Is(err, errInvalidScheme):
		return "base_url scheme must be http or https"
	case errors.Is(err, errInvalidURL):
		return "base_url is not a valid URL"
	}
	// Resolve errors land here — keep the message generic.
	return "unable to validate base_url"
}

// toIntegrationResponse masks an Integration row to the client shape. For
// Mealie, we surface base_url (but not token). Unknown types return just the
// type + enabled flags.
func toIntegrationResponse(it models.Integration) integrationResponse {
	resp := integrationResponse{
		Type:       it.Type,
		Enabled:    it.Enabled,
		Configured: true,
	}
	if it.Type == models.IntegrationTypeMealie {
		var cfg models.MealieConfig
		if err := json.Unmarshal(it.Config, &cfg); err == nil {
			resp.BaseURL = cfg.BaseURL
		}
	}
	return resp
}
