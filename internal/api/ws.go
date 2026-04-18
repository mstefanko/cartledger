package api

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/ws"
)

// newUpgrader builds a WebSocket upgrader whose CheckOrigin compares the
// request's Origin header against the configured ALLOWED_ORIGINS list.
//
// Why this matters: with cookie authentication, a malicious page on
// https://evil.example can trigger a WebSocket upgrade to our server from the
// victim's browser, and the browser will attach our session cookie (the
// WebSocket spec does not enforce same-origin policy — browsers historically
// let ws:// requests go cross-origin with credentials). Rejecting un-allowed
// Origin headers is the correct CSRF-analog defense.
//
// Allowlist matching: exact scheme+host compare, case-insensitive, trailing
// slash stripped. No wildcards — explicit list only.
//
// Missing/empty Origin: reject. Browsers always send Origin on WS upgrades;
// a missing header indicates a non-browser client (cURL, a proxy, a scanner)
// that should authenticate against a REST endpoint via X-API-Key instead.
func newUpgrader(allowedOrigins []string) websocket.Upgrader {
	normalized := make([]string, 0, len(allowedOrigins))
	for _, o := range allowedOrigins {
		o = strings.TrimRight(strings.TrimSpace(o), "/")
		if o != "" {
			normalized = append(normalized, strings.ToLower(o))
		}
	}
	return websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			origin := strings.TrimRight(strings.TrimSpace(r.Header.Get("Origin")), "/")
			if origin == "" {
				slog.Warn("ws: upgrade rejected — missing Origin header", "remote", r.RemoteAddr)
				return false
			}
			lower := strings.ToLower(origin)
			for _, allowed := range normalized {
				if lower == allowed {
					return true
				}
			}
			slog.Warn("ws: upgrade rejected — origin not in allow-list",
				"origin", origin, "remote", r.RemoteAddr)
			return false
		},
	}
}

// WSHandler handles WebSocket upgrade requests.
type WSHandler struct {
	Hub       *ws.Hub
	Cfg       *config.Config
	JWTSecret string

	upgrader websocket.Upgrader
}

// NewWSHandler builds a WSHandler with the configured Origin allow-list
// captured into the upgrader.
func NewWSHandler(hub *ws.Hub, cfg *config.Config) *WSHandler {
	return &WSHandler{
		Hub:       hub,
		Cfg:       cfg,
		JWTSecret: cfg.JWTSecret,
		upgrader:  newUpgrader(cfg.AllowedOrigins),
	}
}

// HandleWS upgrades the HTTP connection to a WebSocket after authenticating
// via the multi-source token reader (cookie first, then Bearer/X-API-Key,
// then ?token= query fallback with a deprecation warning).
func (h *WSHandler) HandleWS(c echo.Context) error {
	claims, err := auth.AuthenticateWithQueryToken(c, h.JWTSecret)
	if err != nil {
		// echo.NewHTTPError path — translate to JSON for browser-facing clients.
		if he, ok := err.(*echo.HTTPError); ok {
			msg := "unauthorized"
			if s, ok := he.Message.(string); ok && s != "" {
				msg = s
			}
			return c.JSON(he.Code, map[string]string{"error": msg})
		}
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	}

	conn, err := h.upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		slog.Error("ws: upgrade error", "err", err)
		return nil // Upgrade already wrote a response on error.
	}

	ws.ServeWS(h.Hub, conn, claims.HouseholdID, claims.UserID)
	return nil
}
