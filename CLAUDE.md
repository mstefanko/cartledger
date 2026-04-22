# CLAUDE.md

## What is this?

CartLedger — self-hosted grocery receipt tracker. Scan receipts with Claude AI, track prices, get spending analytics. Go backend + React frontend + SQLite.

## Architecture

```
cmd/server/main.go        Entry point — wires config, DB, LLM client, worker, router
internal/
  api/                     Echo HTTP handlers (one file per resource: receipts, products, analytics, etc.)
  api/router.go            All route registration + middleware (CORS, auth, rate-limit)
  auth/                    JWT (householdID + userID in claims)
  config/config.go         All env vars loaded here — single source of truth
  db/                      SQLite via database/sql, migrations in db/migrations/ (numbered sequentially — `ls` to find next slot)
  imaging/preprocess.go    Receipt image preprocessing (contrast, crop, resize)
  llm/                     LLM abstraction: Client interface in client.go, types in types.go
    claude.go              Claude API client — uses tool_use for structured extraction
    prompt.go              Extraction rules (schema is in tool definition, not prompt)
    claude_cli.go          Legacy CLI client (not wired up in main.go)
    mock.go                Mock client for testing
  matcher/                 Product matching: fuzzy, rule-based, normalizer
  models/                  Shared data structs
  units/                   Unit parsing/conversion (lb, oz, gal, etc.)
  worker/                  Background receipt processing (goroutine pool)
  ws/                      WebSocket hub for real-time updates
web/src/                   React 19, TypeScript, Vite, Tailwind CSS 4, TanStack Query
```

## Build & Run

```bash
make dev          # Frontend dev server + Go backend concurrently
make run          # Build frontend + run server
make build        # Production frontend build only
go test ./...     # Run Go tests (no frontend tests)
```

## Key env vars (all in internal/config/config.go)

- `ANTHROPIC_API_KEY` — required
- `LLM_MODEL` — defaults to `claude-sonnet-4-20250514`, set `claude-haiku-4-5-20251001` for cheaper
- `LLM_PROVIDER` — `claude` (default/auto), `mock`
- `PORT` — default 8079
- `DATA_DIR` — default `./data` (SQLite DB + receipt images)
- `JWT_SECRET` — change in production

## LLM Integration (the part you'll touch most)

- `internal/llm/client.go` — `Client` interface: `ExtractReceipt(images [][]byte) (*ReceiptExtraction, error)`
- `internal/llm/claude.go` — Claude API client using **tool_use** (not free-text JSON)
  - Tool schema defined as `receiptTool` var (JSON Schema for `ReceiptExtraction`)
  - Forces tool call via `ToolChoice: ToolChoiceParamOfTool("extract_receipt")`
  - Response parsed from `block.Input` (json.RawMessage) on the tool_use content block
  - Token usage logged after every call
- `internal/llm/prompt.go` — Extraction rules only (schema lives in tool definition)
- `internal/llm/types.go` — `ReceiptExtraction` and `ExtractedItem` structs
- SDK: `github.com/anthropics/anthropic-sdk-go` v1.35.1
  - `ContentBlockUnion` is a flat union struct — access `.Type`, `.Name`, `.Input` directly
  - Sonnet does NOT support `Strict: true` on tools

## Patterns to follow

- **One file per resource** in `internal/api/`
- **Config via env vars** — add new vars to `config.go`, not ad-hoc `os.Getenv`
- **Migrations** — sequential numbered files in `internal/db/migrations/` (NNN_name.up.sql + .down.sql)
- **Error handling** — return `fmt.Errorf("context: %w", err)`, Echo handlers return `echo.NewHTTPError`
- **Frontend API calls** — in `web/src/api/`, consumed via TanStack Query hooks

## Analytics conventions

- Date windows for analytics endpoints are computed in Go (`time.Now().AddDate(...)`), never via SQLite `date('now',...)`.
- Rolling-30-day convention: `/analytics/rhythm` (and similar time-window endpoints) uses `[now-60d, now-30d)` as the prior window and `[now-30d, now+1d)` as the current window for period-over-period comparisons (upper bound is `tomorrow = now.AddDate(0,0,1)`, so today's receipts are included).
- `discount_amount` is stored as a positive value in `line_items` — do NOT negate when summing (e.g., `/analytics/savings`).
- `/analytics/staples` cadence is **calendar-event based** (`COUNT(DISTINCT date)`) — NOT quantity-weighted. That distinguishes it from `/analytics/buy-again`, which divides `AVG(days_gap)` by `AVG(quantity)`. Projection fields are null until the household has >=60 days of history.
- `/analytics/inflation` uses a **Laspeyres fixed-weight basket**: weights are current-period median quantities, so the index measures pure price change. Symmetric exclusion: a product contributes only if it appears in both windows of a comparison pair. Both deltas are suppressed when history < 90/180 days or basket overlap < 50%.
- `/analytics/price-moves` (and analytics queries generally) use `COALESCE(normalized_price, unit_price)` as the effective price. Spreadsheet-imported rows populate `unit_price` but leave `normalized_price` NULL; the fallback ensures they participate in price-move detection. Only moves with |pct_change| >= 10% (`priceMoveThresholdPct`) are surfaced; sub-threshold results are filtered in Go after the SQL query.
- **Analytics category filter** — clicking a `CategoryBreakdown` slice or row sets `selectedCategory` in `AnalyticsPage` state and filters the Staples and Product Trends sections client-side; no additional API call is made.

## Gotchas

- `web/dist/` is gitignored — frontend is built separately, not embedded in Go binary
- The `claude_cli.go` client exists but is dead code (not wired in main.go switch)
- SQLite — no concurrent write support, single-writer model
- Receipt images stored on disk at `DATA_DIR/receipts/{uuid}/`
- Planning docs (`PLAN-*.md`, `ANALYSIS-*.md`, etc.) are gitignored
- **WebSocket lifecycle** — `connectWebSocket(queryClient)` in `web/src/api/ws.ts` is mounted exactly once in `AppLayout` (always post-auth via `ProtectedRoute`). Do NOT call it from individual components. Cleanup via `disconnectWebSocket()` runs on unmount/logout. The socket drives React Query cache invalidation for `receipt.complete`, `list.*`, `product.updated`, and `store.updated` messages.
