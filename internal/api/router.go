package api

import (
	"context"
	"database/sql"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/backup"
	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
	"github.com/mstefanko/cartledger/internal/llm"
	"github.com/mstefanko/cartledger/internal/locks"
	"github.com/mstefanko/cartledger/internal/worker"
	"github.com/mstefanko/cartledger/internal/ws"
	"github.com/mstefanko/cartledger/web"
)

// NewRouter creates and configures the Echo router with all middleware and routes.
//
// bootstrap may be nil when the caller has no first-run token (e.g. tests
// that stand up a router with pre-populated users); the Setup handler then
// rejects every call with 401, which matches the user-facing behavior of
// "setup already completed".
func NewRouter(database *sql.DB, cfg *config.Config, hub *ws.Hub, receiptWorker *worker.ReceiptWorker, lockStore *locks.Store, bootstrap *Bootstrap, llmGuard *llm.GuardedExtractor, metrics *Metrics, backupRunner *backup.Runner, backupStore *db.BackupStore) (*echo.Echo, *RateLimiter) {
	e := echo.New()
	e.HideBanner = true

	// --- Global middleware ---

	// Panic recovery must be first so every later middleware is protected.
	e.Use(middleware.Recover())

	// Real-IP resolution. MUST run before logging, rate-limiting, and
	// security-headers middleware so they see the true client IP / proto.
	// Only honors X-Forwarded-* when the direct peer matches a TRUST_PROXY CIDR.
	e.Use(RealIP(cfg.TrustedProxies))

	// Prometheus HTTP metrics. Runs early so every downstream handler shows
	// up in the latency histogram, but after Recover (a panicked handler
	// still produces a metric via the deferred observation in the wrapped
	// handler) and after RealIP (so future per-client labels would see the
	// resolved IP — we do NOT label on IP today to avoid cardinality blow-up).
	if metrics != nil {
		e.Use(metrics.HTTPMiddleware)
	}

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

	// --- Rate limiter (tiered, in-memory) ---
	// Key design points (see internal/api/ratelimit.go for the full rationale):
	//   * auth tier (login/setup/join): 5rps/b10 per IP — brute-force defense.
	//   * read tier (GET): 20rps/b40 per household.
	//   * write tier (non-GET): 10rps/b20 per household.
	//   * worker-submit (POST /receipts/scan): 3rps/b6 per household — hard
	//     cap on LLM cost surface.
	//   * global (protected fallback): 50rps/b100 per household/IP.
	// RATE_LIMIT_ENABLED=false turns the middleware into a pass-through for
	// local testing; default is enabled.
	rateLimiter := NewRateLimiter(cfg.RateLimitEnabled)

	// --- Route groups ---

	v1 := e.Group("/api/v1")

	// Public routes (no auth required).
	public := v1.Group("")
	// Public sub-group gated by the auth-tier rate limiter (login/setup/join).
	// Keyed on c.RealIP() because JWT claims aren't populated on these
	// unauthenticated endpoints.
	publicRateLimited := v1.Group("", rateLimiter.Middleware(TierAuth))

	// Protected routes (auth required). JWTMiddleware populates household_id
	// and user_id on the context, which the per-method rate limiter reads
	// below — so JWTMiddleware MUST be first.
	protected := v1.Group("")
	protected.Use(auth.JWTMiddleware(cfg.JWTSecret))
	// Global catch-all for any authenticated traffic — protects against
	// cheap-but-high-volume endpoints slipping past narrower buckets.
	protected.Use(rateLimiter.Middleware(TierGlobal))
	// Per-method rate limiter: GETs -> read tier, non-GETs -> write tier.
	// Overrides tighten specific high-cost routes:
	//   POST /api/v1/receipts/scan            -> worker-submit (LLM-triggering)
	//   POST /api/v1/receipts/:id/reprocess   -> worker-submit (LLM-triggering;
	//                                            same cost surface as scan,
	//                                            hard-capped to prevent a
	//                                            retry-click storm from blowing
	//                                            the per-household budget)
	protected.Use(rateLimiter.ProtectedMethodMiddleware(map[string]string{
		"/api/v1/receipts/scan":          TierWorkerSubmit,
		"/api/v1/receipts/:id/reprocess": TierWorkerSubmit,
	}))

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

	// /metrics — Prometheus scrape endpoint. Intentionally unauthenticated
	// (standard Prometheus convention: operators firewall the metrics
	// endpoint or expose it on a separate port). SECURITY NOTE: this
	// endpoint leaks operational details (request rates, queue depths,
	// storage sizes, LLM token counts per model). Operators running the
	// binary on an internet-facing host MUST either:
	//   (a) restrict /metrics at the reverse-proxy / firewall layer, or
	//   (b) front the scraper with a private network / VPN.
	// Registered at the root (not under /api/v1) to match the de-facto
	// Prometheus convention that scrapers expect.
	if metrics != nil {
		e.GET("/metrics", metrics.Handler())
	}

	// --- Mount handlers ---

	authHandler := &AuthHandler{DB: database, Cfg: cfg, Bootstrap: bootstrap}
	authHandler.RegisterRoutes(public, publicRateLimited, protected)

	storeHandler := &StoreHandler{DB: database, Cfg: cfg}
	storeHandler.RegisterRoutes(protected)

	productHandler := &ProductHandler{DB: database, Cfg: cfg}
	productHandler.RegisterRoutes(protected)

	groupHandler := &GroupHandler{DB: database, Cfg: cfg}
	groupHandler.RegisterRoutes(protected)

	aliasHandler := &AliasHandler{DB: database, Cfg: cfg}
	aliasHandler.RegisterRoutes(protected)

	receiptHandler := &ReceiptHandler{DB: database, Cfg: cfg, Worker: receiptWorker, Guard: llmGuard}
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

	adminHandler := &AdminHandler{DB: database, Cfg: cfg, Guard: llmGuard}
	adminHandler.RegisterRoutes(protected)

	// Backup admin surface. The Runner + BackupStore are constructed in
	// cmd/server/serve.go so the same instances drive the CLI and HTTP.
	if backupRunner != nil && backupStore != nil {
		backupHandler := &BackupHandler{
			DB:      database,
			Cfg:     cfg,
			Runner:  backupRunner,
			Store:   backupStore,
			Log:     slog.Default(),
			Limiter: rateLimiter,
		}
		backupHandler.RegisterRoutes(protected)
	}

	// Serve uploaded receipt images. Auth is cookie-first (browsers DO send
	// cookies on same-origin <img src=>); ?token= query fallback is kept but
	// emits a deprecation warning so operators can monitor usage.
	//
	// SECURITY: The path param is UNTRUSTED. We rebase it onto
	// <DataDir>/receipts and reject anything that escapes that subtree. We
	// also enforce receipt ownership: the first path segment is the receipt
	// UUID, and it must belong to the caller's household. Non-owners get 404
	// (not 403) to avoid leaking receipt existence across households.
	//
	// Expected layout: <DataDir>/receipts/<receipt_uuid>/<image_file>
	absBase, baseErr := filepath.Abs(filepath.Join(cfg.DataDir, "receipts"))
	v1.GET("/files/*", func(c echo.Context) error {
		claims, err := auth.AuthenticateWithQueryToken(c, cfg.JWTSecret)
		if err != nil {
			return err
		}
		if baseErr != nil {
			// Misconfigured data dir — fail closed.
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "storage unavailable"})
		}

		rawPath := c.Param("*")
		// Belt-and-suspenders: reject obvious traversal before doing path math.
		if containsDotDot(rawPath) {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid path"})
		}

		// Receipt image paths are stored as filepath.Join(DataDir, "receipts",
		// <uuid>, <file>) in internal/api/receipts.go and processed_* writes in
		// internal/worker/receipt.go. Depending on whether DataDir is absolute
		// (Docker: /data) or relative (dev: ./data), the stored value is
		// correspondingly absolute or relative. The SPA requests that stored
		// path verbatim via /api/v1/files/<path>, so we see forms like:
		//   /api/v1/files//data/receipts/<uuid>/1.jpg   (absolute DataDir)
		//   /api/v1/files/data/receipts/<uuid>/1.jpg    (relative DataDir)
		//   /api/v1/files/<uuid>/1.jpg                  (receipt-relative — ideal)
		//
		// Normalize by stripping any "…/receipts/" prefix: whatever remains is
		// receipt-relative and begins with the UUID. If no "receipts/" segment
		// is found, treat the whole thing as already receipt-relative. All
		// three forms converge on the containment + ownership checks below.
		trimmed := strings.TrimLeft(rawPath, "/")
		if idx := strings.LastIndex(trimmed, "receipts/"); idx >= 0 {
			trimmed = trimmed[idx+len("receipts/"):]
		}
		joined := filepath.Join(absBase, filepath.Clean("/"+trimmed))
		absTarget, err := filepath.Abs(joined)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid path"})
		}
		// Enforce containment: absTarget must live under absBase.
		if absTarget != absBase && !strings.HasPrefix(absTarget, absBase+string(os.PathSeparator)) {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid path"})
		}

		// First segment of the (trimmed, forward-slash) path is the receipt UUID.
		segs := strings.SplitN(trimmed, "/", 2)
		if len(segs) < 1 || segs[0] == "" {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
		}
		receiptID := segs[0]

		// Ownership check: receipt must exist AND belong to caller's household.
		// On ErrNoRows or mismatch return 404 — revealing 403 would leak
		// receipt IDs across households.
		var householdID string
		qerr := database.QueryRow("SELECT household_id FROM receipts WHERE id = ?", receiptID).Scan(&householdID)
		if qerr == sql.ErrNoRows || (qerr == nil && householdID != claims.HouseholdID) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
		}
		if qerr != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}

		return c.File(absTarget)
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

	return e, rateLimiter
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
