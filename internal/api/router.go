package api

import (
	"context"
	"database/sql"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"golang.org/x/time/rate"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/locks"
	"github.com/mstefanko/cartledger/internal/worker"
	"github.com/mstefanko/cartledger/internal/ws"
	"github.com/mstefanko/cartledger/web"
)

// NewRouter creates and configures the Echo router with all middleware and routes.
func NewRouter(database *sql.DB, cfg *config.Config, hub *ws.Hub, receiptWorker *worker.ReceiptWorker, lockStore *locks.Store) *echo.Echo {
	e := echo.New()
	e.HideBanner = true

	// --- Global middleware ---

	// Panic recovery must be first so every later middleware is protected.
	e.Use(middleware.Recover())

	// Real-IP resolution. MUST run before logging, rate-limiting, and
	// security-headers middleware so they see the true client IP / proto.
	// Only honors X-Forwarded-* when the direct peer matches a TRUST_PROXY CIDR.
	e.Use(RealIP(cfg.TrustedProxies))

	// Request logging.
	e.Use(middleware.Logger())

	// Security response headers (CSP, HSTS on TLS, framing/referrer/MIME hardening).
	e.Use(SecurityHeaders())

	// CORS: cookie auth requires AllowCredentials=true AND a concrete origin
	// allow-list (browsers refuse credentialed requests to "*"). We reuse
	// cfg.AllowedOrigins — the same list used for WS Origin validation — so
	// operators have a single config knob. In same-origin deploys (frontend
	// and API on the same host), CORS never actually fires; this block is
	// only relevant to dev (Vite on 5173 -> Go on 8079) and future split
	// deployments.
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: cfg.AllowedOrigins,
		AllowMethods: []string{
			http.MethodGet, http.MethodPost, http.MethodPut,
			http.MethodPatch, http.MethodDelete, http.MethodOptions,
		},
		AllowHeaders:     []string{"Authorization", "Content-Type", "X-API-Key"},
		AllowCredentials: true,
	}))

	// --- Route groups ---

	v1 := e.Group("/api/v1")

	// Public routes (no auth required), with rate limiting on auth endpoints.
	public := v1.Group("")
	authRateLimiter := middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
		Skipper: middleware.DefaultSkipper,
		Store: middleware.NewRateLimiterMemoryStoreWithConfig(
			middleware.RateLimiterMemoryStoreConfig{
				Rate:      rate.Every(6 * time.Second), // 10 requests per minute
				Burst:     10,
				ExpiresIn: 3 * time.Minute,
			},
		),
		IdentifierExtractor: func(ctx echo.Context) (string, error) {
			return ctx.RealIP(), nil
		},
		ErrorHandler: func(context echo.Context, err error) error {
			return context.JSON(http.StatusForbidden, map[string]string{"error": "rate limit identifier error"})
		},
		DenyHandler: func(context echo.Context, identifier string, err error) error {
			return context.JSON(http.StatusTooManyRequests, map[string]string{"error": "too many requests, please try again later"})
		},
	})
	publicRateLimited := v1.Group("", authRateLimiter)

	// Protected routes (auth required).
	protected := v1.Group("")
	protected.Use(auth.JWTMiddleware(cfg.JWTSecret))

	// --- Health / readiness / liveness probes (all public, no auth) ---

	// pingDB runs a short-timeout DB ping; returns the error (or nil).
	pingDB := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return database.PingContext(ctx)
	}

	// /health — liveness + DB connectivity. 503 if the DB is unreachable.
	public.GET("/health", func(c echo.Context) error {
		if err := pingDB(); err != nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{
				"status": "unhealthy",
				"error":  "database unreachable",
			})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	// /readyz — ready to serve traffic. DB must ping AND worker must be wired.
	// The worker exposes no IsRunning hook, so presence is the best we can check.
	public.GET("/readyz", func(c echo.Context) error {
		if err := pingDB(); err != nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{
				"status": "not_ready",
				"error":  "database unreachable",
			})
		}
		if receiptWorker == nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{
				"status": "not_ready",
				"error":  "worker not initialized",
			})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ready"})
	})

	// /livez — process is up. Intentionally does NOT touch the DB so that a
	// failing DB doesn't cause the orchestrator to kill the pod.
	public.GET("/livez", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "alive"})
	})

	// --- Mount handlers ---

	authHandler := &AuthHandler{DB: database, Cfg: cfg}
	authHandler.RegisterRoutes(public, publicRateLimited, protected)

	storeHandler := &StoreHandler{DB: database, Cfg: cfg}
	storeHandler.RegisterRoutes(protected)

	productHandler := &ProductHandler{DB: database, Cfg: cfg}
	productHandler.RegisterRoutes(protected)

	groupHandler := &GroupHandler{DB: database, Cfg: cfg}
	groupHandler.RegisterRoutes(protected)

	aliasHandler := &AliasHandler{DB: database, Cfg: cfg}
	aliasHandler.RegisterRoutes(protected)

	receiptHandler := &ReceiptHandler{DB: database, Cfg: cfg, Worker: receiptWorker}
	receiptHandler.RegisterRoutes(protected)

	matchingHandler := &MatchingHandler{DB: database, Cfg: cfg}
	matchingHandler.RegisterRoutes(protected)

	listHandler := &ListHandler{DB: database, Cfg: cfg, Hub: hub, Locks: lockStore}
	listHandler.RegisterRoutes(protected)

	exportHandler := &ExportHandler{DB: database, Cfg: cfg}
	exportHandler.RegisterRoutes(protected)

	integrationHandler := NewIntegrationHandler(database, cfg)
	integrationHandler.RegisterRoutes(protected)

	importHandler := &ImportHandler{DB: database, Cfg: cfg, Integrations: integrationHandler.Store}
	importHandler.RegisterRoutes(protected)

	conversionHandler := &ConversionHandler{DB: database, Cfg: cfg}
	conversionHandler.RegisterRoutes(protected)

	analyticsHandler := &AnalyticsHandler{DB: database, Cfg: cfg}
	analyticsHandler.RegisterRoutes(protected)

	reviewHandler := &ReviewHandler{DB: database, Cfg: cfg, Hub: hub}
	reviewHandler.RegisterRoutes(protected)

	// Serve uploaded receipt images. Auth is cookie-first (browsers DO send
	// cookies on same-origin <img src=>); ?token= query fallback is kept but
	// emits a deprecation warning so operators can monitor usage.
	v1.GET("/files/*", func(c echo.Context) error {
		if _, err := auth.AuthenticateWithQueryToken(c, cfg.JWTSecret); err != nil {
			return err
		}
		filePath := c.Param("*")
		if containsDotDot(filePath) {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid path"})
		}
		return c.File(filePath)
	})

	// WebSocket endpoint — auth handled inside the handler (multi-source
	// reader: cookie, Bearer, X-API-Key, or ?token= fallback).
	wsHandler := NewWSHandler(hub, cfg)
	v1.GET("/ws", wsHandler.HandleWS)

	// --- Static file serving (catch-all for SPA) ---

	distFS, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		e.Logger.Fatal("create sub fs: ", err)
	}
	fsHandler := http.FileServer(http.FS(distFS))
	indexHTML, _ := fs.ReadFile(distFS, "index.html")

	e.GET("/*", func(c echo.Context) error {
		path := c.Request().URL.Path[1:] // strip leading /
		if path == "" {
			path = "index.html"
		}
		// Check if the file exists in dist/
		if _, err := fs.Stat(distFS, path); err == nil {
			fsHandler.ServeHTTP(c.Response(), c.Request())
			return nil
		}
		// SPA fallback: serve index.html for client-side routing
		return c.HTMLBlob(http.StatusOK, indexHTML)
	})

	return e
}

// containsDotDot checks for path traversal attempts.
func containsDotDot(path string) bool {
	for _, seg := range strings.Split(path, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}
