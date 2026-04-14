package api

import (
	"database/sql"
	"io/fs"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

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

	// Public routes (no auth required).
	public := v1.Group("")

	// Protected routes (auth required).
	protected := v1.Group("")
	protected.Use(auth.JWTMiddleware(cfg.JWTSecret))

	// Health check (public).
	public.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	// --- Mount handlers ---

	authHandler := &AuthHandler{DB: database, Cfg: cfg}
	authHandler.RegisterRoutes(public, protected)

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

	// WebSocket endpoint (auth via query param, not middleware).
	wsHandler := &WSHandler{Hub: hub, JWTSecret: cfg.JWTSecret}
	v1.GET("/ws", wsHandler.HandleWS)

	// --- Static file serving (catch-all for SPA) ---

	distFS, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		e.Logger.Fatal("create sub fs: ", err)
	}
	fileHandler := http.FileServer(http.FS(distFS))
	e.GET("/*", echo.WrapHandler(fileHandler))

	return e
}
