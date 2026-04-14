package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/mstefanko/cartledger/internal/api"
	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
	"github.com/mstefanko/cartledger/internal/llm"
	"github.com/mstefanko/cartledger/internal/matcher"
	"github.com/mstefanko/cartledger/internal/worker"
	"github.com/mstefanko/cartledger/internal/ws"
)

func main() {
	cfg := config.Load()

	if cfg.JWTSecret == "change-me-in-production" {
		log.Println("WARNING: Using default JWT secret. Set JWT_SECRET environment variable for production.")
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

	// Create LLM client based on configuration.
	var llmClient llm.Client
	switch cfg.LLMProvider {
	case "claude":
		llmClient = llm.NewClaudeClient(cfg.AnthropicAPIKey)
	case "claude-cli":
		cli, err := llm.NewCLIClient()
		if err != nil {
			log.Fatalf("claude-cli provider: %v", err)
		}
		llmClient = cli
		log.Println("using Claude CLI (subscription billing)")
	case "mock":
		llmClient = llm.NewMockClient()
	default:
		// Auto-detect: use API if key is set, else try CLI, else mock.
		if cfg.AnthropicAPIKey != "" {
			llmClient = llm.NewClaudeClient(cfg.AnthropicAPIKey)
		} else if cli, err := llm.NewCLIClient(); err == nil {
			llmClient = cli
			log.Println("no API key found, using Claude CLI (subscription billing)")
		} else {
			llmClient = llm.NewMockClient()
			log.Println("no API key or Claude CLI found, using mock provider")
		}
	}

	// Create matching engine and receipt worker.
	matchEngine := matcher.NewEngine(database)
	receiptWorker := worker.NewReceiptWorker(2, llmClient, matchEngine, database, hub, cfg)

	// Set up Echo with router, middleware, and all routes.
	e := api.NewRouter(database, cfg, hub, receiptWorker)

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

	// Give outstanding requests 10 seconds to complete.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("shutdown error: %v", err)
	}
	log.Println("server stopped")
}
