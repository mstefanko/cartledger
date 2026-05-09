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

For day-to-day or production use, keep durable data outside the repo working
tree. On macOS, a good local path is:

```bash
DATA_DIR="/Users/you/Library/Application Support/cartledger"
```

For disk-loss protection, pair that directory with Time Machine or another
machine-level backup. On macOS, verify inclusion with
`tmutil isexcluded "/Users/you/Library/Application Support/cartledger"`.

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
| `DATA_DIR` | `./data` | SQLite DB, receipt/product images, backups, and restore staging. Use an absolute path outside the repo for durable data; Docker/Unraid should use `/data` |
| `BACKUP_RETAIN_COUNT` | `14` | Completed backup archives to keep under `DATA_DIR/backups` |
| `LLM_PROVIDER` | *(auto)* | `claude` (API), `mock`, or empty for auto-detect |
| `LLM_MODEL` | `claude-sonnet-4-20250514` | Claude model ID (e.g., `claude-haiku-4-5-20251001` for cheaper/faster) |
| `ANTHROPIC_API_KEY` | — | Required for receipt scanning |
| `JWT_SECRET` | `change-me-in-production` | JWT signing key |
| `SMTP_HOST` | *(empty)* | SMTP server hostname. Leave empty to disable outbound email |
| `SMTP_PORT` | `587` | SMTP server port |
| `SMTP_USER` | *(empty)* | SMTP username |
| `SMTP_PASS` | *(empty)* | SMTP password or app password |
| `SMTP_FROM` | *(empty)* | Sender address, required when `SMTP_HOST` is set |
| `SMTP_TLS_MODE` | `starttls` | `none`, `starttls`, or `tls` |
| `APP_BASE_URL` | `http://localhost:8079` | Public app URL for reset and invite links; required when email is enabled |
| `ALLOW_PRIVATE_INTEGRATIONS` | `false` | Allow integration base URLs on loopback/LAN/RFC1918 addresses (self-hosters on a LAN typically want `true`) |

Mealie (and other recipe/shopping integrations) are configured per-household in the UI: **Settings -> Integrations**. No environment variables required.

## Unraid / Docker Storage

CartLedger v1 supports local filesystem storage only. For Unraid, mount one durable host directory into the container and keep all mutable state under `/data`:

```yaml
volumes:
  - /mnt/user/appdata/cartledger:/data
environment:
  - DATA_DIR=/data
```

The host directory must be writable by container uid/gid `10001:10001`. `/data` contains `cartledger.db`, receipt images, product images, backups, and restore staging. Copy backups off the appdata share or protect that share with your normal Unraid backup tooling; receipt images may include sensitive purchase, payment, or loyalty details.

## Backups and Restore

Create a managed backup archive:

```bash
cartledger backup
```

Archives are written to `DATA_DIR/backups/` and recorded in the `backups`
table. Restore into a fresh data directory with:

```bash
DATA_DIR="/path/to/fresh/cartledger-data" cartledger restore /path/to/backup.tar.gz
```

On macOS, daily backups can be installed with:

```bash
scripts/install-backup-launchd.sh --data-dir "/Users/you/Library/Application Support/cartledger"
```

### Email and Password Recovery

Password reset and invite emails are disabled when `SMTP_HOST` is empty. In that mode, reset requests still return success so account existence is not exposed; an operator can reset a password from the server with:

```bash
cartledger reset-password user@example.com
```

Gmail app-password example:

```bash
SMTP_HOST=smtp.gmail.com
SMTP_PORT=587
SMTP_USER=your-address@gmail.com
SMTP_PASS=your-16-character-app-password
SMTP_FROM="CartLedger <your-address@gmail.com>"
SMTP_TLS_MODE=starttls
APP_BASE_URL=https://cartledger.example.com
```

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
