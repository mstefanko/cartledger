package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
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

func main() {
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
		log.Fatalf("open database: %v", err)
	}
	defer database.Close()

	// Run migrations.
	if err := db.RunMigrations(database); err != nil {
		log.Fatalf("run migrations: %v", err)
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
		log.Printf("LLM provider: claude (direct API, model=%s)", cfg.LLMModel)
	case "mock":
		llmClient = llm.NewMockClient()
		log.Println("LLM provider: mock")
	default:
		// Auto-detect: config.Validate guarantees AnthropicAPIKey is set when
		// LLMProvider != "mock", so this branch is safe.
		llmClient = llm.NewClaudeClient(cfg.AnthropicAPIKey, cfg.LLMModel)
		log.Printf("LLM provider: claude (direct API, model=%s)", cfg.LLMModel)
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
		log.Printf("starting server on %s", addr)
		if err := e.Start(addr); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Wait for interrupt signal.
	<-ctx.Done()
	log.Println("shutting down...")

	// Stop HTTP server FIRST so no new Submit calls arrive at the worker.
	// 10s is plenty for in-flight HTTP requests (uploads are multipart but not
	// long-running — the LLM work is async in the worker).
	httpShutdownCtx, httpCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer httpCancel()
	if err := e.Shutdown(httpShutdownCtx); err != nil {
		log.Printf("http shutdown error: %v", err)
	}
	log.Println("http server stopped")

	// Now drain the worker. 30s deadline: finish whatever LLM calls can finish,
	// and mark the rest as 'pending' so they're re-enqueued on next boot.
	workerShutdownCtx, workerCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer workerCancel()
	if err := receiptWorker.Shutdown(workerShutdownCtx); err != nil {
		log.Printf("worker shutdown error: %v", err)
	}
	log.Println("server stopped")
}
