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

- **Backend:** Go 1.25, Echo framework, SQLite
- **Frontend:** React 19, TypeScript, Vite, Tailwind CSS 4

## Prerequisites

- [Go 1.25+](https://go.dev/dl/)
- [Node.js 18+](https://nodejs.org/) (for frontend development)
- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) **or** an API key for [Anthropic Claude](https://console.anthropic.com/) / [Google Gemini](https://ai.google.dev/) (for receipt scanning)

## Quick Start

### 1. Clone and configure

```bash
git clone https://github.com/mstefanko/cartledger.git
cd cartledger
cp .env.example .env
```

Edit `.env` — if you have the Claude Code CLI installed, **no API key is needed**. The server auto-detects the CLI and uses your subscription billing. Otherwise, set `ANTHROPIC_API_KEY` or `GEMINI_API_KEY`.

### 2. Run with Docker (recommended)

```bash
docker-compose up --build
```

The app will be available at `http://localhost:8080`.

### 3. Run locally (development)

**Backend:**

```bash
go run ./cmd/server
```

The server starts on port 8080 by default and serves the built frontend.

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

**Frontend:**

```bash
cd web
npm test
```

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | Server port |
| `DATA_DIR` | `./data` | SQLite DB and uploads directory |
| `LLM_PROVIDER` | *(auto)* | `claude-cli` (CLI subscription), `claude` (API), `gemini`, `mock`, or empty for auto-detect |
| `ANTHROPIC_API_KEY` | — | Required if `LLM_PROVIDER=claude` |
| `GEMINI_API_KEY` | — | Required if `LLM_PROVIDER=gemini` |
| `JWT_SECRET` | `change-me-in-production` | JWT signing key |
| `MEALIE_URL` | — | Optional Mealie instance URL |
| `MEALIE_TOKEN` | — | Optional Mealie API token |

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
migrations/          SQL migration files
web/                 React frontend (Vite + TypeScript)
```
