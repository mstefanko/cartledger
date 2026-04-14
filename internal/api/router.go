package api

import (
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
	"github.com/mstefanko/cartledger/internal/worker"
	"github.com/mstefanko/cartledger/internal/ws"
	"github.com/mstefanko/cartledger/web"
)

// NewRouter creates and configures the Echo router with all middleware and routes.
func NewRouter(database *sql.DB, cfg *config.Config, hub *ws.Hub, receiptWorker *worker.ReceiptWorker) *echo.Echo {
	e := echo.New()
	e.HideBanner = true

	// --- Global middleware ---

	// Request logging.
	e.Use(middleware.Logger())

	// Panic recovery.
	e.Use(middleware.Recover())

	// CORS: permissive for dev (JWT_SECRET == default), same-origin for prod.
	if cfg.JWTSecret == "change-me-in-production" {
		e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
			AllowOrigins: []string{"*"},
			AllowMethods: []string{
				http.MethodGet, http.MethodPost, http.MethodPut,
				http.MethodPatch, http.MethodDelete, http.MethodOptions,
			},
			AllowHeaders:     []string{"Authorization", "Content-Type"},
			AllowCredentials: false,
		}))
	} else {
		e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
			AllowOrigins:     []string{},
			AllowMethods:     []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete},
			AllowHeaders:     []string{"Authorization", "Content-Type"},
			AllowCredentials: false,
		}))
	}

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

	// Health check (public).
	public.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	// --- Mount handlers ---

	authHandler := &AuthHandler{DB: database, Cfg: cfg}
	authHandler.RegisterRoutes(public, publicRateLimited, protected)

	storeHandler := &StoreHandler{DB: database, Cfg: cfg}
	storeHandler.RegisterRoutes(protected)

	productHandler := &ProductHandler{DB: database, Cfg: cfg}
	productHandler.RegisterRoutes(protected)

	aliasHandler := &AliasHandler{DB: database, Cfg: cfg}
	aliasHandler.RegisterRoutes(protected)

	receiptHandler := &ReceiptHandler{DB: database, Cfg: cfg, Worker: receiptWorker}
	receiptHandler.RegisterRoutes(protected)

	matchingHandler := &MatchingHandler{DB: database, Cfg: cfg}
	matchingHandler.RegisterRoutes(protected)

	listHandler := &ListHandler{DB: database, Cfg: cfg, Hub: hub}
	listHandler.RegisterRoutes(protected)

	exportHandler := &ExportHandler{DB: database, Cfg: cfg}
	exportHandler.RegisterRoutes(protected)

	importHandler := &ImportHandler{DB: database, Cfg: cfg}
	importHandler.RegisterRoutes(protected)

	conversionHandler := &ConversionHandler{DB: database, Cfg: cfg}
	conversionHandler.RegisterRoutes(protected)

	analyticsHandler := &AnalyticsHandler{DB: database, Cfg: cfg}
	analyticsHandler.RegisterRoutes(protected)

	// Serve uploaded receipt images. Paths stored in DB are relative to
	// the working directory (e.g., "data/receipts/{id}/1.jpg").
	protected.GET("/files/*", func(c echo.Context) error {
		filePath := c.Param("*")

		// Path traversal protection.
		if containsDotDot(filePath) {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid path"})
		}

		return c.File(filePath)
	})

	// WebSocket endpoint (auth via query param, not middleware).
	wsHandler := &WSHandler{Hub: hub, JWTSecret: cfg.JWTSecret}
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
