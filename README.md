# CartLedger

Self-hosted app to track grocery receipts, compare prices, and build smart shopping lists. Scan receipts with AI (Claude or Gemini), track price history, and get analytics on your spending.

## Features

- Receipt scanning via Claude or Gemini AI
- Price tracking and history with analytics dashboard
- Smart product matching (fuzzy + rule-based)
- Shopping lists with price estimates and "Buy Again"
- Unit conversion and price normalization
- Optional Mealie integration for recipe/shopping list import
- Multi-user support with JWT auth
- PWA support
- Real-time updates via WebSocket

## Tech Stack

- **Backend:** Go 1.26, Echo framework, SQLite
- **Frontend:** React 19, TypeScript, Vite, Tailwind CSS 4

## Prerequisites

- [Go 1.26+](https://go.dev/dl/)
- [Node.js 18+](https://nodejs.org/) (for frontend development)
- An API key for [Anthropic Claude](https://console.anthropic.com/) (for receipt scanning)

## Quick Start

### 1. Clone and configure

```bash
git clone https://github.com/mstefanko/cartledger.git
cd cartledger
cp .env.example .env
```

Edit `.env` — set `ANTHROPIC_API_KEY` for receipt scanning.

### 2. Run with Docker (recommended)

```bash
docker-compose up --build
```

The app will be available at `http://localhost:8079`.

### 3. Run locally (development)

**Backend:**

```bash
go run ./cmd/server
```

The server starts on port 8079 by default.

**Frontend (dev mode with hot reload):**

```bash
cd web
npm install
npm run dev
```

The Vite dev server starts on `http://localhost:5173` and proxies API requests to the Go backend.

**Build frontend for production:**

```bash
cd web
npm run build
```

## Running Tests

**Go tests:**

```bash
go test ./...
```

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8079` | Server port |
| `DATA_DIR` | `./data` | SQLite DB and uploads directory |
| `LLM_PROVIDER` | *(auto)* | `claude` (API), `mock`, or empty for auto-detect |
| `LLM_MODEL` | `claude-sonnet-4-20250514` | Claude model ID (e.g., `claude-haiku-4-5-20251001` for cheaper/faster) |
| `ANTHROPIC_API_KEY` | — | Required for receipt scanning |
| `JWT_SECRET` | `change-me-in-production` | JWT signing key |
| `ALLOW_PRIVATE_INTEGRATIONS` | `false` | Allow integration base URLs on loopback/LAN/RFC1918 addresses (self-hosters on a LAN typically want `true`) |

Mealie (and other recipe/shopping integrations) are configured per-household in the UI: **Settings -> Integrations**. No environment variables required.

## Project Structure

```
cmd/server/          Go entry point
internal/
  api/               HTTP handlers and routes
  auth/              JWT auth and password handling
  config/            Configuration loading
  db/                SQLite database layer
  llm/               LLM integration (Claude, Gemini, mock)
  matcher/           Smart product matching (fuzzy, rules)
  models/            Data models
  units/             Unit conversion engine
  worker/            Background job processing
  ws/                WebSocket hub
web/                 React frontend (Vite + TypeScript)
```
