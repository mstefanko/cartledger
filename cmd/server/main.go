package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mstefanko/cartledger/internal/api"
	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
	"github.com/mstefanko/cartledger/internal/llm"
	"github.com/mstefanko/cartledger/internal/locks"
	"github.com/mstefanko/cartledger/internal/matcher"
	"github.com/mstefanko/cartledger/internal/worker"
	"github.com/mstefanko/cartledger/internal/ws"
)

// initLogger configures slog's default logger based on LOG_LEVEL and LOG_FORMAT
// env vars. JSON output on stderr is the default (aggregator-friendly); set
// LOG_FORMAT=text for human-readable dev output.
func initLogger() {
	logLevel := slog.LevelInfo
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		switch strings.ToLower(v) {
		case "debug":
			logLevel = slog.LevelDebug
		case "info":
			logLevel = slog.LevelInfo
		case "warn":
			logLevel = slog.LevelWarn
		case "error":
			logLevel = slog.LevelError
		}
	}
	opts := &slog.HandlerOptions{Level: logLevel}
	var handler slog.Handler = slog.NewJSONHandler(os.Stderr, opts)
	if os.Getenv("LOG_FORMAT") == "text" {
		handler = slog.NewTextHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(handler))
}

// fatalExit logs an error message at Error level and terminates the process.
// Replaces log.Fatalf from the stdlib "log" package.
func fatalExit(msg string, args ...any) {
	slog.Error(msg, args...)
	os.Exit(1)
}

func main() {
	initLogger()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cartledger: configuration error")
		fmt.Fprintln(os.Stderr, "-----------------------------------")
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, "-----------------------------------")
		fmt.Fprintln(os.Stderr, "Set the required environment variables (see .env.example) and try again.")
		os.Exit(1)
	}

	// Open SQLite database with pragmas.
	database, err := db.Open(cfg.DBPath())
	if err != nil {
		fatalExit("open database", "err", err)
	}
	defer database.Close()

	// Run migrations.
	if err := db.RunMigrations(database); err != nil {
		fatalExit("run migrations", "err", err)
	}

	// Start WebSocket hub.
	hub := ws.NewHub()
	go hub.Run()

	// In-memory per-list edit lock store (single-node only; see internal/locks).
	lockStore := locks.NewStore(cfg.LockInactivityTTL)
	defer lockStore.Close()

	// Create LLM client based on configuration.
	var llmClient llm.Client
	switch cfg.LLMProvider {
	case "claude":
		llmClient = llm.NewClaudeClient(cfg.AnthropicAPIKey, cfg.LLMModel)
		slog.Info("llm provider selected", "provider", "claude", "model", cfg.LLMModel)
	case "mock":
		llmClient = llm.NewMockClient()
		slog.Info("llm provider selected", "provider", "mock")
	default:
		// Auto-detect: config.Validate guarantees AnthropicAPIKey is set when
		// LLMProvider != "mock", so this branch is safe.
		llmClient = llm.NewClaudeClient(cfg.AnthropicAPIKey, cfg.LLMModel)
		slog.Info("llm provider selected", "provider", "claude", "model", cfg.LLMModel)
	}

	// Create matching engine and receipt worker.
	matchEngine := matcher.NewEngine(database)
	receiptWorker := worker.NewReceiptWorker(2, llmClient, matchEngine, database, hub, cfg)

	// Set up Echo with router, middleware, and all routes.
	e := api.NewRouter(database, cfg, hub, receiptWorker, lockStore)

	// Graceful shutdown via signal.NotifyContext.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Start server in a goroutine.
	go func() {
		addr := fmt.Sprintf(":%s", cfg.Port)
		slog.Info("server starting", "addr", addr)
		if err := e.Start(addr); err != nil && err != http.ErrServerClosed {
			fatalExit("server error", "err", err)
		}
	}()

	// Wait for interrupt signal.
	<-ctx.Done()
	slog.Info("shutting down")

	// Stop HTTP server FIRST so no new Submit calls arrive at the worker.
	// 10s is plenty for in-flight HTTP requests (uploads are multipart but not
	// long-running — the LLM work is async in the worker).
	httpShutdownCtx, httpCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer httpCancel()
	if err := e.Shutdown(httpShutdownCtx); err != nil {
		slog.Error("http shutdown error", "err", err)
	}
	slog.Info("http server stopped")

	// Now drain the worker. 30s deadline: finish whatever LLM calls can finish,
	// and mark the rest as 'pending' so they're re-enqueued on next boot.
	workerShutdownCtx, workerCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer workerCancel()
	if err := receiptWorker.Shutdown(workerShutdownCtx); err != nil {
		slog.Error("worker shutdown error", "err", err)
	}
	slog.Info("server stopped")
}
