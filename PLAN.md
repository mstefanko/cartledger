# CartLedger — Architecture & Implementation Plan

> Go backend, React frontend, LLM vision receipt scanning.
> Design system: Kraken (`DESIGN.md`). Self-hosted via Docker Compose.

---

## Table of Contents

1. [Overview](#1-overview)
2. [Architecture](#2-architecture)
3. [Data Model](#3-data-model)
4. [Backend (Go)](#4-backend-go)
5. [Frontend (React)](#5-frontend-react)
6. [Receipt Scanning Pipeline](#6-receipt-scanning-pipeline)
7. [Product Matching & Normalization](#7-product-matching--normalization)
8. [Unit Conversion & Price Math](#8-unit-conversion--price-math)
9. [Mealie Integration](#9-mealie-integration)
10. [Real-time & Multi-user](#10-real-time--multi-user)
11. [Analytics](#11-analytics)
12. [Export & Sharing](#12-export--sharing)
13. [Project Setup](#13-project-setup)
14. [Implementation Phases](#14-implementation-phases)

---

## 1. Overview

### What It Does
- **Scan receipts** via phone camera → LLM vision extracts line items, prices, store, date
- **Match items** to a canonical product database (like Actual Budget matches bank transactions to payees)
- **Track prices** per product per store over time, with unit normalization (Costco bulk vs ShopRite single)
- **Shared shopping lists** with live collaboration, estimated costs, and cheapest-store indicators
- **Analytics** with sparklines, trend percentages, deal flagging, and per-store breakdowns
- **Import from Mealie** — recipes and shopping lists, with cost overlay

### Who It's For
- You and your wife, v1. Multi-user household with concurrent access.

---

## 2. Architecture

```
┌─────────────────────────────────────────────────────┐
│                    Docker Compose                    │
│                                                     │
│  ┌───────────────────────────────────────────────┐  │
│  │  React PWA (Vite + TypeScript)                │  │
│  │  Served by Go binary (embedded static files)  │  │
│  └──────────────────┬────────────────────────────┘  │
│                     │ HTTP/WebSocket                 │
│  ┌──────────────────▼────────────────────────────┐  │
│  │  Go API Server (Echo)                         │  │
│  │  ├── REST API (/api/v1/...)                   │  │
│  │  ├── WebSocket Hub (live collaboration)       │  │
│  │  ├── Background Workers (receipt processing)  │  │
│  │  └── LLM Client (Claude/Gemini vision API)   │  │
│  │         │                                     │  │
│  │     SQLite (cartledger.db)                    │  │
│  └───────────────────────────────────────────────┘  │
│                                                     │
│  Optional:                                          │
│  ┌───────────────────────────────────────────────┐  │
│  │  Mealie Instance (separate container)         │  │
│  │  └── API at /api/ for recipe/list import      │  │
│  └───────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────┘
```

### Key Decisions

| Decision | Choice | Why |
|----------|--------|-----|
| Backend | Go + Echo | Single binary, 20MB Docker image, goroutines for background work, official Anthropic SDK |
| Database | SQLite (modernc.org/sqlite) | Pure Go, no CGO, one-file backup, proven at this scale (Actual, Mealie, Grocy) |
| Frontend | React 19 + TypeScript + Vite | Best AI agent support, largest ecosystem, v0/Cursor/Claude all excel |
| Styling | Tailwind CSS 4 | Design tokens from DESIGN.md map directly to Tailwind config |
| Real-time | WebSockets (gorilla/websocket) | Shared list collaboration, receipt processing status |
| LLM Vision | Claude claude-sonnet-4-20250514 (primary), Gemini Flash 2.0 (fallback) | Best accuracy for structured extraction, Gemini as cheap fallback |
| Money | shopspring/decimal | No float rounding in price calculations |
| Units | martinlindhe/unit + custom food density table | Only Go lib with cooking units (cups, tbsp, tsp) |
| Static serving | embed.FS in Go binary | Single binary serves everything, no nginx needed |

---

## 3. Data Model

### Core Schema (SQLite)

```sql
-- Household (the shared space — "The Stefankos")
-- Single household per deployment for v1. Multi-household is a future additive change.
CREATE TABLE households (
    id          TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    name        TEXT NOT NULL,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Users
CREATE TABLE users (
    id            TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    household_id  TEXT NOT NULL REFERENCES households(id),
    email         TEXT NOT NULL UNIQUE,
    name          TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Stores (sidebar navigation — like Actual's "accounts")
CREATE TABLE stores (
    id            TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    household_id  TEXT NOT NULL REFERENCES households(id),
    name          TEXT NOT NULL,           -- "Costco", "ShopRite", "Acme"
    display_order INTEGER DEFAULT 0,
    icon          TEXT,                    -- emoji or icon name
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(household_id, name)
);

-- Canonical Products (the normalized product database)
-- This is the equivalent of Actual's "payees" or Mealie's "foods"
CREATE TABLE products (
    id                TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    household_id      TEXT NOT NULL REFERENCES households(id),
    name              TEXT NOT NULL,           -- "Chicken Breast, Boneless"
    category          TEXT,                    -- "Meat", "Produce", "Dairy"
    default_unit      TEXT,                    -- "lb", "oz", "each"
    notes             TEXT,
    last_purchased_at DATE,                    -- denormalized: updated on receipt match
    purchase_count    INTEGER DEFAULT 0,       -- denormalized: total times purchased
    created_at        DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(household_id, name)
);

-- Product Aliases (maps receipt abbreviations to canonical products)
-- Like Actual's imported_payee → payee mapping
CREATE TABLE product_aliases (
    id          TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    product_id  TEXT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    alias       TEXT NOT NULL,            -- "BNLS CHKN BRST", "CHICKEN BRST BNL"
    store_id    TEXT REFERENCES stores(id), -- optional: alias specific to a store
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    -- NOTE: UNIQUE(alias, store_id) doesn't work for NULL store_id
    -- (SQL standard: each NULL is distinct). Use partial indexes instead.
    UNIQUE(alias, store_id)
);
-- Partial unique indexes for aliases (handles NULL store_id correctly)
CREATE UNIQUE INDEX idx_alias_global ON product_aliases(alias) WHERE store_id IS NULL;
CREATE UNIQUE INDEX idx_alias_store ON product_aliases(alias, store_id) WHERE store_id IS NOT NULL;

-- Matching Rules (like Actual Budget's rules engine)
CREATE TABLE matching_rules (
    id            TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    household_id  TEXT NOT NULL REFERENCES households(id),
    priority      INTEGER DEFAULT 0,       -- higher = checked first
    condition_op  TEXT NOT NULL,            -- "contains", "starts_with", "matches", "exact"
    condition_val TEXT NOT NULL,            -- "CHKN" or regex pattern
    store_id      TEXT REFERENCES stores(id), -- optional: rule only for this store
    product_id    TEXT NOT NULL REFERENCES products(id),
    category      TEXT,                    -- auto-assign category too
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Receipts
CREATE TABLE receipts (
    id            TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    household_id  TEXT NOT NULL REFERENCES households(id),
    store_id      TEXT REFERENCES stores(id),
    scanned_by    TEXT REFERENCES users(id),
    receipt_date  DATE NOT NULL,
    subtotal      TEXT,                    -- decimal string
    tax           TEXT,
    total         TEXT,
    image_paths   TEXT,                    -- JSON array of image paths ["receipts/abc/1.jpg", "receipts/abc/2.jpg"]
    raw_llm_json  TEXT,                    -- raw LLM extraction for audit/debugging
    status        TEXT DEFAULT 'pending' CHECK (status IN ('pending', 'matched', 'reviewed')),
    llm_provider  TEXT,                    -- 'claude', 'gemini' — which LLM processed this receipt
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Line Items (individual items on a receipt)
CREATE TABLE line_items (
    id            TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    receipt_id    TEXT NOT NULL REFERENCES receipts(id) ON DELETE CASCADE,
    product_id    TEXT REFERENCES products(id),  -- NULL if unmatched
    raw_name      TEXT NOT NULL,           -- "BNLS CHKN BRST" exactly as on receipt
    quantity      TEXT NOT NULL DEFAULT '1', -- decimal string
    unit          TEXT,                    -- "lb", "oz", "each", NULL
    unit_price    TEXT,                    -- price per unit (decimal)
    total_price   TEXT NOT NULL,           -- line total (decimal)
    matched       TEXT DEFAULT 'unmatched' CHECK (matched IN ('unmatched', 'auto', 'manual', 'rule')),
    confidence    REAL,                    -- LLM confidence 0-1
    line_number   INTEGER,                -- position on receipt
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Product Prices (denormalized for fast analytics)
-- Populated on line_item match to a product
CREATE TABLE product_prices (
    id            TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    product_id    TEXT NOT NULL REFERENCES products(id),
    store_id      TEXT NOT NULL REFERENCES stores(id),
    receipt_id    TEXT NOT NULL REFERENCES receipts(id),
    receipt_date  DATE NOT NULL,
    quantity      TEXT NOT NULL,           -- decimal
    unit          TEXT NOT NULL,
    unit_price    TEXT NOT NULL,           -- decimal: price per unit
    normalized_price TEXT,                 -- decimal: price per standard unit (per oz, per each)
    normalized_unit  TEXT,                 -- the standard unit used
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Shopping Lists
CREATE TABLE shopping_lists (
    id            TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    household_id  TEXT NOT NULL REFERENCES households(id),
    name          TEXT NOT NULL,
    created_by    TEXT REFERENCES users(id),
    status        TEXT DEFAULT 'active' CHECK (status IN ('active', 'completed', 'archived')),
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Shopping List Items
CREATE TABLE shopping_list_items (
    id            TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    list_id       TEXT NOT NULL REFERENCES shopping_lists(id) ON DELETE CASCADE,
    product_id    TEXT REFERENCES products(id),
    name          TEXT NOT NULL,            -- display name (may differ from product)
    quantity      TEXT NOT NULL DEFAULT '1',
    unit          TEXT,
    checked       BOOLEAN DEFAULT FALSE,
    checked_by    TEXT REFERENCES users(id),
    sort_order    INTEGER DEFAULT 0,
    notes         TEXT,
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Unit Conversions (food-specific density conversions)
-- e.g., 1 cup of flour = 4.25 oz, 1 cup of sugar = 7.05 oz
CREATE TABLE unit_conversions (
    id            TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    product_id    TEXT REFERENCES products(id), -- NULL = generic conversion
    from_unit     TEXT NOT NULL,
    to_unit       TEXT NOT NULL,
    factor        TEXT NOT NULL,            -- decimal: multiply from_quantity by this
    UNIQUE(product_id, from_unit, to_unit)
);

-- Product Images (user-uploaded photos: packaging, nutrition labels, shelf location)
-- Attached to the PRODUCT (not the transaction) — useful every time you see the item
CREATE TABLE product_images (
    id            TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    product_id    TEXT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    image_path    TEXT NOT NULL,            -- "products/abc/photo1.jpg"
    type          TEXT DEFAULT 'photo' CHECK (type IN ('photo', 'nutrition', 'packaging')),
    caption       TEXT,                     -- "Bottom shelf at Costco, near the kimchi"
    is_primary    BOOLEAN DEFAULT FALSE,    -- shown as thumbnail in lists/tables
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Product Links (back-references to Mealie, URLs, external sources)
-- Separate table because a product can link to multiple mealie recipes
CREATE TABLE product_links (
    id            TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    product_id    TEXT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    source        TEXT NOT NULL,            -- 'mealie_food', 'mealie_recipe', 'url'
    external_id   TEXT,                     -- mealie food ID or recipe slug
    url           TEXT NOT NULL,            -- clickable link back to mealie/source
    label         TEXT,                     -- "Chicken Stir Fry (Mealie)"
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- FTS5 virtual tables for search
-- NOTE: content_rowid=rowid uses SQLite's implicit integer rowid, NOT the TEXT id column.
-- All FTS sync triggers MUST reference the implicit rowid, not the text PK.
CREATE VIRTUAL TABLE products_fts USING fts5(name, category, content=products, content_rowid=rowid);
CREATE VIRTUAL TABLE product_aliases_fts USING fts5(alias, content=product_aliases, content_rowid=rowid);

-- FTS5 sync triggers (REQUIRED — without these, search index goes stale)
CREATE TRIGGER products_fts_insert AFTER INSERT ON products BEGIN
    INSERT INTO products_fts(rowid, name, category) VALUES (NEW.rowid, NEW.name, NEW.category);
END;
CREATE TRIGGER products_fts_update AFTER UPDATE ON products BEGIN
    INSERT INTO products_fts(products_fts, rowid, name, category) VALUES ('delete', OLD.rowid, OLD.name, OLD.category);
    INSERT INTO products_fts(rowid, name, category) VALUES (NEW.rowid, NEW.name, NEW.category);
END;
CREATE TRIGGER products_fts_delete AFTER DELETE ON products BEGIN
    INSERT INTO products_fts(products_fts, rowid, name, category) VALUES ('delete', OLD.rowid, OLD.name, OLD.category);
END;

CREATE TRIGGER aliases_fts_insert AFTER INSERT ON product_aliases BEGIN
    INSERT INTO product_aliases_fts(rowid, alias) VALUES (NEW.rowid, NEW.alias);
END;
CREATE TRIGGER aliases_fts_update AFTER UPDATE ON product_aliases BEGIN
    INSERT INTO product_aliases_fts(product_aliases_fts, rowid, alias) VALUES ('delete', OLD.rowid, OLD.alias);
    INSERT INTO product_aliases_fts(rowid, alias) VALUES (NEW.rowid, NEW.alias);
END;
CREATE TRIGGER aliases_fts_delete AFTER DELETE ON product_aliases BEGIN
    INSERT INTO product_aliases_fts(product_aliases_fts, rowid, alias) VALUES ('delete', OLD.rowid, OLD.alias);
END;

-- Indexes
CREATE INDEX idx_line_items_receipt ON line_items(receipt_id);
CREATE INDEX idx_line_items_product ON line_items(product_id);
CREATE INDEX idx_product_prices_product ON product_prices(product_id, receipt_date);
CREATE INDEX idx_product_prices_store ON product_prices(store_id, receipt_date);
CREATE INDEX idx_product_aliases_alias ON product_aliases(alias);
CREATE INDEX idx_receipts_store ON receipts(store_id, receipt_date);
CREATE INDEX idx_receipts_date ON receipts(receipt_date);
CREATE INDEX idx_matching_rules_priority ON matching_rules(household_id, priority DESC);
CREATE INDEX idx_product_images_product ON product_images(product_id);
CREATE INDEX idx_product_links_product ON product_links(product_id);
CREATE INDEX idx_product_links_source ON product_links(source, external_id);
```

### Entity Relationship Summary

```
Household ("The Stefankos")
 ├── Users (Mike, Sarah)
 ├── Stores          ← sidebar navigation (like Actual accounts)
 ├── Products         ← canonical product catalog
 │    ├── Aliases     ← receipt name → product mappings
 │    ├── Prices      ← price history per store
 │    ├── Images      ← user photos (packaging, nutrition labels)
 │    └── Links       ← back-references to Mealie foods/recipes, URLs
 ├── Receipts         ← scanned receipts with original images for audit
 │    └── LineItems   ← each row on a receipt, linked to product
 ├── ShoppingLists
 │    └── ListItems   ← each item, linked to product for pricing
 ├── MatchingRules    ← auto-categorization (like Actual rules)
 └── UnitConversions  ← food-specific measurement conversions
```

---

## 4. Backend (Go)

### Project Structure

```
cartledger/
├── cmd/
│   └── server/
│       └── main.go              # Entry point, config, startup
├── internal/
│   ├── api/
│   │   ├── router.go            # Echo router setup, middleware
│   │   ├── auth.go              # Setup, login, invite, join endpoints
│   │   ├── receipts.go          # POST /receipts/scan, GET /receipts/:id
│   │   ├── products.go          # CRUD + merge + alias management + image upload
│   │   ├── stores.go            # CRUD, reorder
│   │   ├── lists.go             # Shopping list CRUD + items
│   │   ├── matching.go          # Rules CRUD, manual match, auto-match
│   │   ├── analytics.go         # Price trends, sparklines data, stats
│   │   ├── import.go            # Mealie import endpoints (with product_links creation)
│   │   ├── export.go            # Share/export endpoints
│   │   └── ws.go                # WebSocket hub for real-time
│   ├── db/
│   │   ├── sqlite.go            # Connection, migrations, pragmas
│   │   ├── migrations/          # Numbered SQL migration files
│   │   └── queries/             # SQL query files (sqlc or hand-written)
│   ├── llm/
│   │   ├── client.go            # LLM provider interface
│   │   ├── claude.go            # Anthropic Claude vision implementation
│   │   ├── gemini.go            # Google Gemini Flash implementation
│   │   ├── prompt.go            # Receipt extraction prompt templates
│   │   └── types.go             # Structured extraction types
│   ├── matcher/
│   │   ├── engine.go            # Matching engine (rules → fuzzy → unmatched)
│   │   ├── fuzzy.go             # Fuzzy string matching (Levenshtein, trigram)
│   │   ├── rules.go             # Rule evaluation
│   │   └── normalizer.go        # Text normalization (lowercase, remove punctuation)
│   ├── units/
│   │   ├── converter.go         # Unit conversion with food density support
│   │   ├── parser.go            # Parse "1 1/2 cups" → quantity + unit
│   │   └── price.go             # Price normalization (per-oz, per-unit)
│   ├── mealie/
│   │   └── client.go            # Mealie API client for import
│   ├── ws/
│   │   ├── hub.go               # WebSocket connection hub
│   │   └── messages.go          # Message types for real-time updates
│   └── models/
│       └── models.go            # Shared domain types
├── web/                         # React app (embedded at build time)
│   └── dist/                    # Built React static files
├── migrations/
│   ├── 001_initial.sql
│   └── ...
├── Dockerfile
├── docker-compose.yml
├── go.mod
└── go.sum
```

### Key Go Dependencies

```go
// go.mod
module github.com/mstefanko/cartledger

go 1.23

require (
    github.com/labstack/echo/v4      // HTTP framework
    modernc.org/sqlite                // Pure Go SQLite (no CGO)
    github.com/anthropics/anthropic-sdk-go // Claude API
    github.com/google/generative-ai-go    // Gemini API
    github.com/golang-jwt/jwt/v5           // JWT standard library
    github.com/labstack/echo-jwt/v4       // Echo JWT middleware
    github.com/gorilla/websocket          // WebSocket
    github.com/shopspring/decimal         // Money math
    github.com/martinlindhe/unit          // Unit conversions (cups, oz, etc.)
    github.com/lithammer/fuzzysearch      // Fuzzy string matching
    github.com/golang-migrate/migrate/v4  // DB migrations
    golang.org/x/crypto                   // bcrypt for passwords
)
```

### SQLite Configuration

```go
// internal/db/sqlite.go
func Open(path string) (*sql.DB, error) {
    db, err := sql.Open("sqlite", path)
    if err != nil {
        return nil, err
    }
    // Performance pragmas for a small self-hosted app
    pragmas := []string{
        "PRAGMA journal_mode=WAL",          // concurrent reads
        "PRAGMA synchronous=NORMAL",        // faster writes, safe with WAL
        "PRAGMA foreign_keys=ON",
        "PRAGMA busy_timeout=5000",         // 5s wait on locks
        "PRAGMA cache_size=-20000",         // 20MB cache
        "PRAGMA mmap_size=268435456",       // 256MB memory-mapped I/O
    }
    for _, p := range pragmas {
        db.Exec(p)
    }
    return db, nil
}
```

### Background Receipt Processing

```go
// No need for Redis/Celery — goroutines with a buffered channel
type ReceiptWorker struct {
    jobs    chan ReceiptJob
    llm     llm.Client
    matcher *matcher.Engine
    db      *sql.DB
    hub     *ws.Hub
}

func NewReceiptWorker(concurrency int, ...) *ReceiptWorker {
    w := &ReceiptWorker{
        jobs: make(chan ReceiptJob, 100),
        // ...
    }
    for i := 0; i < concurrency; i++ {
        go w.process()
    }
    return w
}

func (w *ReceiptWorker) process() {
    for job := range w.jobs {
        // 1. Send image to LLM vision API
        extracted := w.llm.ExtractReceipt(job.ImagePath)

        // 2. Run matching engine on each line item
        for _, item := range extracted.Items {
            match := w.matcher.Match(item.RawName, extracted.StoreName)
            // ...
        }

        // 3. Save to DB
        // 4. Notify via WebSocket
        w.hub.Broadcast(ws.ReceiptProcessed{ID: job.ReceiptID})
    }
}
```

---

## 5. Frontend (React)

### Project Structure

```
web/
├── src/
│   ├── main.tsx
│   ├── App.tsx
│   ├── api/
│   │   ├── client.ts            # Fetch wrapper, auth headers
│   │   ├── receipts.ts          # Receipt API calls
│   │   ├── products.ts          # Product API calls
│   │   ├── lists.ts             # Shopping list API calls
│   │   └── ws.ts                # WebSocket client
│   ├── components/
│   │   ├── ui/                  # Design system primitives (from DESIGN.md)
│   │   │   ├── Button.tsx
│   │   │   ├── Badge.tsx
│   │   │   ├── Input.tsx
│   │   │   ├── Table.tsx        # Virtualized table (like Actual's)
│   │   │   ├── Sidebar.tsx
│   │   │   ├── Modal.tsx
│   │   │   └── Sparkline.tsx
│   │   ├── receipts/
│   │   │   ├── ReceiptScanner.tsx    # Camera capture + upload
│   │   │   ├── ReceiptReview.tsx     # Line-by-line matching UI
│   │   │   ├── ReceiptLineRow.tsx    # Single line item with match controls
│   │   │   └── ReceiptHistory.tsx    # Past receipts list
│   │   ├── products/
│   │   │   ├── ProductCatalog.tsx    # All products table
│   │   │   ├── ProductDetail.tsx     # Price history, aliases, transactions
│   │   │   ├── ProductMerge.tsx      # Merge two products
│   │   │   └── AliasManager.tsx      # Manage name aliases
│   │   ├── lists/
│   │   │   ├── ShoppingList.tsx      # Active list with prices
│   │   │   ├── ListItem.tsx          # Checkable item with price
│   │   │   └── ListShare.tsx         # Export/share modal
│   │   ├── stores/
│   │   │   └── StoreView.tsx         # All transactions for a store
│   │   ├── analytics/
│   │   │   ├── Dashboard.tsx         # Overview charts
│   │   │   ├── PriceTrends.tsx       # Product price over time
│   │   │   └── TripSummary.tsx       # Per-trip cost analysis
│   │   ├── matching/
│   │   │   ├── RuleEditor.tsx        # Create/edit matching rules
│   │   │   └── RuleList.tsx          # All rules
│   │   └── import/
│   │       └── MealieImport.tsx      # Import from Mealie
│   ├── hooks/
│   │   ├── useWebSocket.ts
│   │   ├── useProducts.ts
│   │   └── usePriceFormat.ts
│   ├── lib/
│   │   ├── units.ts             # Client-side unit display helpers
│   │   ├── price.ts             # Format "$1.49/oz", price comparisons
│   │   └── share.ts             # Web Share API / clipboard helpers
│   ├── styles/
│   │   └── tailwind.css
│   └── types/
│       └── index.ts             # Shared TypeScript types matching Go models
├── tailwind.config.ts           # Tokens from DESIGN.md
├── vite.config.ts
├── tsconfig.json
└── package.json
```

### Design System → Tailwind Config

Map DESIGN.md tokens directly into Tailwind:

```typescript
// tailwind.config.ts
import type { Config } from 'tailwindcss'

export default {
  content: ['./src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        brand: {
          DEFAULT: '#7132f5',       // Kraken Purple — primary CTA
          dark: '#5741d8',          // outlined button borders
          deep: '#5b1ecf',          // deepest purple
          subtle: 'rgba(133,91,251,0.16)', // subtle backgrounds
        },
        neutral: {
          900: '#101114',           // near-black, primary text
          600: '#484b5e',           // badge text
          500: '#686b82',           // cool gray, borders
          400: '#9497a9',           // silver blue, secondary text
          200: '#dedee5',           // border gray, dividers
          50: 'rgba(148,151,169,0.08)', // secondary button bg
        },
        success: {
          DEFAULT: '#149e61',
          dark: '#026b3f',
          subtle: 'rgba(20,158,97,0.16)',
        },
        // Grocery-specific semantic colors (extending Kraken)
        deal: {
          DEFAULT: '#149e61',       // reuse success green for "good deal"
          subtle: 'rgba(20,158,97,0.16)',
        },
        expensive: {
          DEFAULT: '#e5484d',       // red for price increases
          subtle: 'rgba(229,72,77,0.16)',
        },
      },
      fontFamily: {
        display: ['IBM Plex Sans', 'Helvetica', 'Arial', 'sans-serif'],
        body: ['Helvetica Neue', 'Helvetica', 'Arial', 'sans-serif'],
      },
      fontSize: {
        'hero': ['48px', { lineHeight: '1.17', letterSpacing: '-1px', fontWeight: '700' }],
        'section': ['36px', { lineHeight: '1.22', letterSpacing: '-0.5px', fontWeight: '700' }],
        'subhead': ['28px', { lineHeight: '1.29', letterSpacing: '-0.5px', fontWeight: '700' }],
        'feature': ['22px', { lineHeight: '1.20', fontWeight: '600' }],
        'body': ['16px', { lineHeight: '1.38', fontWeight: '400' }],
        'body-medium': ['16px', { lineHeight: '1.38', fontWeight: '500' }],
        'caption': ['14px', { lineHeight: '1.43', fontWeight: '400' }],
        'small': ['12px', { lineHeight: '1.33', fontWeight: '400' }],
      },
      borderRadius: {
        'sm': '3px',
        'md': '6px',
        'DEFAULT': '8px',
        'lg': '10px',
        'xl': '12px',   // button standard
        '2xl': '16px',
      },
      boxShadow: {
        'subtle': 'rgba(0,0,0,0.03) 0px 4px 24px',
        'micro': 'rgba(16,24,40,0.04) 0px 1px 4px',
      },
    },
  },
} satisfies Config
```

### Key UI Patterns

#### Layout — Sidebar + Main Content (Actual Budget Style)

```
┌──────────────┬──────────────────────────────────────┐
│  CartLedger   │  Shopping Lists                      │
│              │                                      │
│  LISTS       │  ┌──────────────────────────────────┐│
│  ▸ Weekly    │  │ Weekly Groceries         $142.50 ││
│  ▸ Costco    │  │                                  ││
│              │  │ ☐ Chicken Breast  2lb    $8.99   ││
│  STORES      │  │   Costco $6.99 | ShopRite $8.99 ││
│  🏪 Costco   │  │ ☐ Bananas       1 bunch  $0.69  ││
│  🏪 ShopRite │  │ ☑ Milk, 2%      1 gal   $4.29   ││
│  🏪 Acme     │  │   ...                            ││
│              │  └──────────────────────────────────┘│
│  PAGES       │                                      │
│  📊 Analytics│                                      │
│  📦 Products │                                      │
│  📋 Rules    │                                      │
│  📸 Receipts │                                      │
│  ⬇️  Import   │                                      │
└──────────────┴──────────────────────────────────────┘
```

#### Receipt Review — Mealie-inspired Line Matching

After scanning, user sees each line extracted from the receipt. Each row is editable — like Mealie's recipe ingredient editor but for receipt items. Unmatched items highlighted, autocomplete to match to existing product or create new.

```
┌──────────────────────────────────────────────────────┐
│  Receipt: Costco — April 12, 2026                    │
│  Status: 8 matched, 3 need review                    │
│  [ View Original Receipt 📷 ]  [ View Raw JSON 🔍 ] │
│                                                      │
│  Raw Receipt Text        Match              Price    │
│  ─────────────────────────────────────────────────── │
│  ✅ BNLS CHKN BRST    → Chicken Breast     $12.99   │
│  ✅ ORG BNNAS 3LB     → Bananas, Organic    $2.49   │
│  ⚠️  KS PREM PPR TWL  → [Select product ▼]  $18.99  │
│                          ┌─────────────────┐         │
│                          │ Paper Towels     │         │
│                          │ KS Paper Towels  │         │
│                          │ + Create new...  │         │
│                          └─────────────────┘         │
│  ⚠️  ORG VAN EXTRCT    → [Select product ▼]  $8.49  │
│  ✅ 2% MILK GAL       → Milk, 2%            $4.29   │
│                                                      │
│  [ Create Rule from Selection ]  [ Confirm All ✓ ]   │
└──────────────────────────────────────────────────────┘
```

**Key UX behaviors (borrowed from Actual + Mealie):**
- Tab/Enter keyboard navigation between rows (like Actual's spreadsheet)
- Autocomplete dropdown with fuzzy search against products + aliases
- "Create Rule" button: when you match "BNLS CHKN BRST" → "Chicken Breast", offer to create a rule so it auto-matches next time (like Actual's auto-rule creation from payee rename)
- Confidence indicator (✅ high confidence auto-match, ⚠️ needs review)
- Inline quantity/unit editing
- Raw receipt image shown alongside for reference

#### Product Detail View

Click any product → see all transactions across stores with sparkline.

```
┌──────────────────────────────────────────────────────┐
│  Chicken Breast, Boneless                            │
│  Category: Meat          Default Unit: lb            │
│                                                      │
│  Price Trend (6 months)     ▁▂▃▂▄▅▃▂  +8% ↑        │
│                                                      │
│  PHOTOS                        [ + Add Photo ]       │
│  ┌─────┐ ┌─────┐ ┌─────┐                            │
│  │     │ │     │ │     │  ← user-uploaded: packaging,│
│  │ 📷  │ │ 📷  │ │ 📷  │    nutrition label, shelf   │
│  └─────┘ └─────┘ └─────┘    location, etc.           │
│                                                      │
│  ALIASES                                             │
│  "BNLS CHKN BRST" (Costco) · "CHICKEN BRST BNL"    │
│  (ShopRite) · "BONE-IN CHKN" (Acme)                 │
│  [ + Add Alias ]  [ Merge with Another Product ]     │
│                                                      │
│  PRICE COMPARISON                                    │
│  Costco     $3.49/lb   (3lb pack)     Best ✓        │
│  ShopRite   $4.99/lb   (1.5lb pack)                 │
│  Acme       $5.49/lb   (1lb pack)                   │
│                                                      │
│  ALL TRANSACTIONS                                    │
│  Date         Store      Qty    Unit Price  Total    │
│  2026-04-12   Costco     3 lb   $3.49/lb   $10.47   │
│  2026-04-05   ShopRite   1.5lb  $4.99/lb   $7.49    │
│  2026-03-28   Costco     3 lb   $3.29/lb   $9.87    │
│  2026-03-15   Acme       1 lb   $5.49/lb   $5.49    │
│  ...                                                 │
│                                                      │
│  LINKED IN MEALIE             (from product_links)   │
│  🔗 Chicken Breast (food)     ← opens Mealie food    │
│  🔗 Chicken Stir Fry (recipe) ← opens Mealie recipe  │
│     └── uses 1 lb ($3.49 at Costco)                 │
│  🔗 Grilled Chicken Salad (recipe)                   │
│     └── uses 0.5 lb ($1.75)                         │
└──────────────────────────────────────────────────────┘
```

---

## 6. Receipt Scanning Pipeline

### LLM Vision Prompt

```go
// internal/llm/prompt.go
const receiptExtractionPrompt = `Extract all items from this grocery receipt image.
Return a JSON object with this exact structure:

{
  "store_name": "string",
  "store_address": "string or null",
  "date": "YYYY-MM-DD",
  "items": [
    {
      "raw_name": "exact text from receipt",
      "suggested_name": "Human-readable canonical product name",
      "suggested_category": "Meat|Produce|Dairy|Bakery|Frozen|Pantry|Snacks|Beverages|Household|Health|Other",
      "quantity": 1.0,
      "unit": "lb" | "oz" | "gal" | "each" | "pack" | null,
      "unit_price": 3.49 or null,
      "total_price": 3.49,
      "line_number": 1,
      "confidence": 0.95
    }
  ],
  "subtotal": 0.00,
  "tax": 0.00,
  "total": 0.00,
  "confidence": 0.95
}

Rules:
- raw_name must be EXACTLY as printed on receipt (preserve abbreviations)
- suggested_name should be a clean, human-readable product name (e.g., "BNLS CHKN BRST" → "Chicken Breast, Boneless")
- suggested_category must be one of the listed categories
- If quantity and unit_price are visible, include both
- If only total_price is visible, set unit_price to null and quantity to 1
- If quantity/weight is embedded in the item name (e.g., "3LB" in "BNLS CHKN BRST 3LB"), extract it
- unit should be standardized: lb, oz, gal, qt, pt, each, pack, ct
- Omit non-grocery items (bag fees, bottle deposits) but include tax/total
- Per-item confidence score: 0.95+ for clearly readable, 0.7-0.95 for partially obscured, <0.7 for guesses`
```

### Processing Flow

```
Phone Camera → Upload Image
       │
       ▼
   Save to disk (receipts/{id}/original.jpg)
       │
       ▼
   Send to LLM Vision API (Claude → Gemini fallback)
       │
       ▼
   Parse JSON response → validate schema
       │
       ▼
   Store/create store if new
       │
       ▼
   For each item:
     1. Run matching rules (priority order)
     2. Fuzzy match against product aliases (trigram similarity > 0.7)
     3. Fuzzy match against product names (Levenshtein distance)
     4. If match found with confidence > 0.85 → auto-match
     5. If match found with confidence 0.5-0.85 → suggest, needs review
     6. If no match → unmatched, needs review
       │
       ▼
   Save receipt + line items to DB
       │
       ▼
   WebSocket notify → user sees review screen
```

---

## 7. Product Matching & Normalization

### Three-Stage Matching Engine (Inspired by Actual Budget)

```go
// internal/matcher/engine.go

type MatchResult struct {
    ProductID  string
    Confidence float64
    Method     string // "rule", "alias", "fuzzy", "unmatched"
}

func (e *Engine) Match(rawName string, storeID string) MatchResult {
    normalized := e.normalizer.Normalize(rawName)

    // Stage 1: Explicit rules (like Actual's rule engine)
    // Check rules in priority order
    if result := e.matchByRules(normalized, storeID); result != nil {
        return *result // confidence: 1.0, method: "rule"
    }

    // Stage 2: Exact alias match
    // Check product_aliases table (store-specific first, then global)
    if result := e.matchByAlias(normalized, storeID); result != nil {
        return *result // confidence: 0.95, method: "alias"
    }

    // Stage 3: Fuzzy matching
    // Trigram similarity against all aliases + product names
    if result := e.matchByFuzzy(normalized, storeID); result != nil {
        return *result // confidence: 0.5-0.9, method: "fuzzy"
    }

    return MatchResult{Method: "unmatched", Confidence: 0}
}
```

### Auto-Rule Creation (Key Actual Budget Pattern)

When a user manually matches a line item, offer to create a rule:

```
User matches "KS PREM PPR TWL" → "Paper Towels, Kirkland"

System prompts:
  "Always match 'KS PREM PPR TWL' to Paper Towels, Kirkland?"
  [ Just this time ]  [ Create Rule ✓ ]

If "Create Rule":
  INSERT INTO matching_rules
    (condition_op: "exact", condition_val: "KS PREM PPR TWL",
     store_id: costco_id, product_id: paper_towels_id)
```

### Product Merging

Two products that turn out to be the same thing can be merged:

```go
// internal/api/products.go
// POST /api/v1/products/merge
// Body: { "keep_id": "abc", "merge_id": "def" }
//
// 1. Move all aliases from merge → keep
// 2. Move all line_items from merge → keep
// 3. Move all product_prices from merge → keep
// 4. Update all shopping_list_items from merge → keep
// 5. Update all matching_rules from merge → keep
// 6. Move all product_images from merge → keep
// 7. Move all product_links from merge → keep
// 8. Delete merge product
// All in a single transaction.
```

---

## 8. Unit Conversion & Price Math

### Go Implementation

```go
// internal/units/converter.go

// StandardUnits — what we normalize prices to
var StandardUnits = map[string]string{
    "weight": "oz",    // normalize all weight to ounces
    "volume": "fl_oz", // normalize all volume to fluid ounces
    "count":  "each",  // normalize count items to each
}

// Built-in conversions (weight)
var weightConversions = map[string]decimal.Decimal{
    "lb":  decimal.NewFromFloat(16),    // 1 lb = 16 oz
    "oz":  decimal.NewFromInt(1),
    "kg":  decimal.NewFromFloat(35.274), // 1 kg = 35.274 oz
    "g":   decimal.NewFromFloat(0.03527),
}

// Food-specific density (volume → weight)
// Loaded from unit_conversions table, seeded with common values
var defaultDensities = map[string]decimal.Decimal{
    // product_name → oz per cup
    "flour, all-purpose": decimal.NewFromFloat(4.25),
    "sugar, granulated":  decimal.NewFromFloat(7.05),
    "sugar, brown":       decimal.NewFromFloat(7.7),
    "butter":             decimal.NewFromFloat(8.0),
    "milk":               decimal.NewFromFloat(8.6),
    "rice, uncooked":     decimal.NewFromFloat(6.7),
    "oats":               decimal.NewFromFloat(3.0),
}

// NormalizePrice returns price per standard unit
func (c *Converter) NormalizePrice(
    totalPrice decimal.Decimal,
    quantity decimal.Decimal,
    unit string,
    productID string,
) (pricePerUnit decimal.Decimal, normalizedUnit string, err error) {
    // ...conversion logic using martinlindhe/unit for standard conversions
    // and unit_conversions table for food-specific density
}
```

### Recipe Cost Calculation

```go
// "1/4 cup sugar" → how much does it cost?
//
// 1. Parse "1/4 cup sugar" → quantity=0.25, unit="cup", product="sugar"
// 2. Convert 0.25 cup sugar → 1.7625 oz (using density 7.05 oz/cup)
// 3. Look up cheapest price for sugar: $0.049/oz (Costco, 25lb bag)
// 4. 1.7625 oz × $0.049/oz = $0.086 → display as "$0.09"
```

### Fraction Parsing

```go
// internal/units/parser.go
// Parse "1 1/2 cups" → (1.5, "cup")
// Parse "3 lb" → (3.0, "lb")
// Parse "1/4 cup" → (0.25, "cup")
//
// Uses math/big.Rat for fraction parsing.
// Mixed number detection via regex: `(\d+)\s+(\d+/\d+)\s*(.*)`
// Simple fraction: `(\d+/\d+)\s*(.*)`
// Decimal: `(\d+\.?\d*)\s*(.*)`
```

No external library needed for this — it's ~50 lines of Go with `math/big.Rat`.

---

## 9. Mealie Integration

### Import Flow

Mealie exposes a full REST API. We connect to a user's Mealie instance and pull data.

```go
// internal/mealie/client.go

type MealieClient struct {
    BaseURL string
    Token   string
    http    *http.Client
}

// Import a single recipe → creates products + shopping list with costs
func (c *MealieClient) ImportRecipe(recipeSlug string) (*ImportResult, error) {
    recipe, _ := c.GetRecipe(recipeSlug)

    result := &ImportResult{RecipeName: recipe.Name}

    for _, ing := range recipe.Ingredients {
        // ing has: quantity, unit, food, note, original_text

        // 1. Match food to our product catalog
        product := matcher.Match(ing.Food.Name, "")
        if product.Method == "unmatched" {
            // Create new product from Mealie food
            product = createProductFromMealieFood(ing.Food)
        }

        // 2. Create bidirectional link back to Mealie
        //    - Link product → mealie food
        createProductLink(product.ID, "mealie_food", ing.Food.ID,
            fmt.Sprintf("%s/foods/%s", c.BaseURL, ing.Food.ID),
            ing.Food.Name+" (Mealie)")
        //    - Link product → this mealie recipe
        createProductLink(product.ID, "mealie_recipe", recipeSlug,
            fmt.Sprintf("%s/recipes/%s", c.BaseURL, recipeSlug),
            recipe.Name)

        // 3. Calculate cost from our price database
        cost := priceEngine.CalculateCost(product.ID, ing.Quantity, ing.Unit)

        result.Items = append(result.Items, ImportedItem{
            Product:  product,
            Quantity: ing.Quantity,
            Unit:     ing.Unit.Name,
            Cost:     cost,
        })
    }

    return result, nil
}

// Available Mealie API endpoints we use:
// GET  /api/recipes              — list all recipes
// GET  /api/recipes/{slug}       — get recipe with ingredients
// GET  /api/households/shopping/lists — get shopping lists
// GET  /api/foods                — get all foods (ingredient catalog)
// POST /api/parser/ingredients   — parse raw text into structured ingredients
//
// Auth: Bearer token from Mealie settings → API Tokens
```

### What We Import

| Mealie Resource | What We Create |
|----------------|---------------|
| Food | Product (matched or new) + `product_links` entry (source: `mealie_food`) |
| Recipe | Shopping list with cost annotations + `product_links` entry per ingredient (source: `mealie_recipe`) |
| Shopping List | Shopping list with items matched to products |
| Ingredient Unit | Unit conversion entries |

### Mealie Links in the Product Detail View

When viewing a product that was imported from or linked to Mealie:

```
LINKED IN MEALIE
🔗 Chicken Breast (food)          ← click opens Mealie food page
🔗 Chicken Stir Fry (recipe)      ← click opens Mealie recipe
    └── uses 1 lb ($3.49 at Costco)
🔗 Grilled Chicken Salad (recipe)
    └── uses 0.5 lb ($1.75)
```

Links are stored in `product_links` and rendered as clickable external links. Data stays in Mealie — we hold references + cost calculations.

---

## 10. Real-time & Multi-user

### WebSocket Architecture

```go
// internal/ws/hub.go

type Hub struct {
    households map[string]map[*Client]bool // household_id → connected clients
    broadcast  chan Message
    register   chan *Client
    unregister chan *Client
}

// Message types
type Message struct {
    Type      string      `json:"type"`
    Household string      `json:"household"`
    Payload   interface{} `json:"payload"`
}

// Events sent to clients:
// "list.item.checked"    — someone checked off an item
// "list.item.added"      — new item added to list
// "list.item.removed"    — item removed
// "receipt.processing"   — receipt scan started
// "receipt.complete"     — receipt scan done, review ready
// "receipt.matched"      — line item was matched during review
// "product.updated"      — product name/category changed
```

### Concurrency Model

- Shopping lists use **optimistic concurrency** — last write wins on individual items (fine for 2 users)
- Receipt review: only one user edits a receipt at a time (soft lock via WebSocket presence)
- All list mutations broadcast via WebSocket so the other user's view updates instantly

---

## 11. Analytics

### API Endpoints

```
GET /api/v1/analytics/overview
  → total spent this month, vs last month, trip count, avg trip cost

GET /api/v1/analytics/products/:id/trend
  → price history array for sparkline, % change, min/max/avg per store

GET /api/v1/analytics/products?sort=price_change&order=desc
  → all products with trend data, sortable table

GET /api/v1/analytics/stores/:id/summary
  → total spent, item count, avg trip cost, price leaders

GET /api/v1/analytics/trips
  → all receipts as "trips", total per trip, chart data over time

GET /api/v1/analytics/deals
  → items where latest price is significantly below average (flagged as deals)

GET /api/v1/analytics/buy-again
  → products predicted to need repurchasing soon, sorted by urgency
```

### Buy Again Prediction

Pure SQL analytics on existing purchase history — no ML, no stock tracking, no manual thresholds. Works because we have complete purchase dates and quantities from receipt scans.

**Algorithm: quantity-aware interval prediction**

```
For each product with 3+ purchases:
  avg_days_per_unit = avg(days between purchases) / avg(quantity per purchase)
  est_supply_days   = last_quantity * avg_days_per_unit
  days_since_last   = today - last_purchase_date
  urgency_ratio     = days_since_last / est_supply_days
```

This handles the bulk-buy edge case naturally: if you normally buy 1 tub of coffee every 30 days but grab 2 because they're on sale, `est_supply_days` becomes 60 instead of 30. The algorithm won't ping you until day 48 (80% of 60).

```sql
SELECT product_id,
       AVG(days_gap) as avg_days_between,
       AVG(quantity) as avg_qty,
       AVG(days_gap) / AVG(quantity) as avg_days_per_unit,
       last_qty * (AVG(days_gap) / AVG(quantity)) as est_supply_days,
       julianday('now') - julianday(last_date) as days_since_last
FROM (
    SELECT pp.product_id,
           CAST(pp.quantity AS REAL) as quantity,
           pp.receipt_date,
           julianday(LEAD(pp.receipt_date) OVER w) - julianday(pp.receipt_date) as days_gap,
           LAST_VALUE(pp.receipt_date) OVER w as last_date,
           LAST_VALUE(CAST(pp.quantity AS REAL)) OVER w as last_qty
    FROM product_prices pp
    JOIN products p ON p.id = pp.product_id
    WHERE p.household_id = :household_id
    WINDOW w AS (PARTITION BY pp.product_id ORDER BY pp.receipt_date
                 ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING)
)
GROUP BY product_id
HAVING COUNT(*) >= 3
```

**Frontend: "Buy Again" section on dashboard and as smart list generator**

```
LIKELY NEED SOON
🔴 Milk, 2%          — every 6 days, last bought 7 days ago
🟡 Bananas            — every 9 days, last bought 8 days ago
🟢 Chicken Breast     — every 14 days, last bought 10 days ago
⚪ Decaf Coffee (2x)  — ~60 day supply, last bought 15 days ago

[ Add all to shopping list ]  [ Add selected ]
```

Urgency levels:
- 🔴 `urgency_ratio > 1.0` — overdue, probably need to buy
- 🟡 `urgency_ratio > 0.8` — running low soon
- 🟢 `urgency_ratio > 0.6` — on the horizon
- ⚪ `urgency_ratio < 0.6` — well stocked

### Sparkline Data

```go
// Returns last N price points for a product, suitable for a tiny sparkline
type SparklinePoint struct {
    Date  string          `json:"date"`
    Price decimal.Decimal `json:"price"` // normalized price per standard unit
    Store string          `json:"store"`
}

// Frontend renders with a lightweight SVG sparkline component
// (no charting library needed for sparklines — 20 lines of SVG path math)
```

### Charting Library

For the full analytics dashboard charts (trip costs over time, category breakdowns):

- **Recharts** — React-native, composable, good AI generation support
- Lightweight enough for this use case, no D3 complexity

---

## 12. Export & Sharing

### Web Share API

```typescript
// src/lib/share.ts
export async function shareList(list: ShoppingList) {
  const text = formatListForShare(list)

  if (navigator.share) {
    // Native share sheet — works on mobile (WhatsApp, Messages, etc.)
    await navigator.share({
      title: list.name,
      text: text,
    })
  } else {
    // Fallback: copy to clipboard
    await navigator.clipboard.writeText(text)
  }
}

function formatListForShare(list: ShoppingList): string {
  // "Weekly Groceries — Est. $142.50\n\n"
  // "☐ Chicken Breast (2 lb) — $8.99\n"
  // "☐ Bananas (1 bunch) — $0.69\n"
  // ...
  const lines = list.items.map(item =>
    `☐ ${item.name}${item.quantity ? ` (${item.quantity} ${item.unit || ''})` : ''}${item.estimatedPrice ? ` — $${item.estimatedPrice}` : ''}`
  )
  const total = list.items.reduce((sum, i) => sum + (i.estimatedPrice || 0), 0)
  return `${list.name} — Est. $${total.toFixed(2)}\n\n${lines.join('\n')}`
}
```

The Web Share API opens the native share sheet on mobile — the user picks WhatsApp, iMessage, etc. No integration needed on our side. Line-by-line formatted text is universally readable.

---

## 13. Project Setup

### Docker Compose

```yaml
# docker-compose.yml
services:
  cartledger:
    build: .
    ports:
      - "8080:8080"
    volumes:
      - ./data:/data          # SQLite DB + receipt images
    environment:
      - CARTLEDGER_DB_PATH=/data/cartledger.db
      - CARTLEDGER_UPLOAD_DIR=/data/receipts
      - ANTHROPIC_API_KEY=${ANTHROPIC_API_KEY}
      - GEMINI_API_KEY=${GEMINI_API_KEY}      # optional fallback
      - MEALIE_URL=${MEALIE_URL}              # optional
      - MEALIE_TOKEN=${MEALIE_TOKEN}          # optional
    restart: unless-stopped

  # Optional: Mealie alongside (if not already running)
  # mealie:
  #   image: ghcr.io/mealie-recipes/mealie:latest
  #   ports:
  #     - "9925:9000"
  #   volumes:
  #     - ./mealie-data:/app/data
```

### Dockerfile (Multi-stage)

```dockerfile
# Stage 1: Build React frontend
FROM node:22-alpine AS frontend
WORKDIR /app/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Stage 2: Build Go binary
FROM golang:1.23-alpine AS backend
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -o cartledger ./cmd/server

# Stage 3: Minimal runtime
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=backend /app/cartledger /usr/local/bin/
EXPOSE 8080
ENTRYPOINT ["cartledger"]
```

### Initial Go Setup Commands

```bash
mkdir cartledger && cd cartledger
go mod init github.com/mstefanko/cartledger

# Create frontend
npm create vite@latest web -- --template react-ts
cd web && npm install && cd ..

# Key Go dependencies
go get github.com/labstack/echo/v4
go get modernc.org/sqlite
go get github.com/anthropics/anthropic-sdk-go
go get github.com/gorilla/websocket
go get github.com/shopspring/decimal
go get github.com/martinlindhe/unit
go get github.com/lithammer/fuzzysearch
go get github.com/golang-migrate/migrate/v4

# Key React dependencies
cd web
npm install recharts @tanstack/react-query @tanstack/react-table
npm install -D tailwindcss @tailwindcss/vite
```

---

## 14. Implementation Phases

### Phase 1: Foundation (Week 1-2)
**Goal: Scan a receipt, see line items, manually match them**

- [ ] Go project scaffold (Echo, SQLite, migrations)
- [ ] Core DB schema (households, users, stores, products, receipts, line_items)
- [ ] Auth (simple session-based, 2 users)
- [ ] React scaffold (Vite, Tailwind with DESIGN.md tokens, router)
- [ ] Sidebar layout (stores + pages navigation)
- [ ] Receipt upload endpoint + LLM vision integration (Claude)
- [ ] Receipt review screen (line-by-line matching UI)
- [ ] Basic product CRUD (create during matching)
- [ ] Product alias creation on match
- [ ] Docker Compose setup

### Phase 2: Lists & Prices (Week 3-4)
**Goal: Shopping lists with prices, basic price tracking**

- [ ] Shopping list CRUD with items
- [ ] Link list items to products → show price from DB
- [ ] Price comparison per item (cheapest store indicator)
- [ ] Product detail view (all transactions, per-store prices)
- [ ] WebSocket setup for real-time list updates
- [ ] Shared list collaboration (check items, add items)
- [ ] Web Share API for list export (WhatsApp, etc.)

### Phase 3: Smart Matching (Week 5-6)
**Goal: Less manual work matching receipts over time**

- [ ] Matching rules engine (condition/action like Actual)
- [ ] Auto-rule creation from manual matches
- [ ] Fuzzy matching with confidence scores
- [ ] Rule management UI (list, edit, delete, reorder priority)
- [ ] Product merge functionality
- [ ] Alias management UI
- [ ] FTS5 search across products and aliases

### Phase 4: Unit Conversion & Recipe Costs (Week 7-8)
**Goal: Normalize prices across stores, calculate recipe costs**

- [ ] Unit parser ("1 1/2 cups" → quantity + unit)
- [ ] Price normalization (price per oz across weight/volume)
- [ ] Food-specific density conversions (cup of flour → oz)
- [ ] Unit conversion management UI
- [ ] Mealie API integration (import recipes, foods, lists)
- [ ] Recipe cost calculation from price database
- [ ] Shopping list cost estimation

### Phase 5: Analytics & Polish (Week 9-10)
**Goal: See trends, flag deals, compare stores**

- [ ] Price trend sparklines per product
- [ ] % change badges (up/down from last purchase)
- [ ] Store view (all transactions for a store, like Actual's account view)
- [ ] Trip cost chart over time (Recharts)
- [ ] Deal flagging (significantly below average price)
- [ ] Sortable product table with trends
- [ ] Analytics dashboard (monthly spend, trip averages)
- [ ] "Buy Again" prediction (quantity-aware interval algorithm on purchase history)
- [ ] "Buy Again" dashboard section + "Add to shopping list" action
- [ ] PWA manifest + service worker for mobile install

---

## Appendix: Verified Technical Decisions

### Mobile / Native App Strategy

**Decision: PWA for v1, API-first architecture enables native apps later.**

The "scan now, review later" pattern is key — the phone's job is just to take a photo and upload it. All the complex UX (matching, editing, analytics) happens in the browser on phone or desktop. This strongly favors PWA over native.

**Receipt scanning from PWA works well enough** with one trick: use `<input type="file" accept="image/*" capture="environment">` instead of `getUserMedia()`. This opens the **native camera app** on both Android and iOS, giving full autofocus, flash, and resolution. The user takes the photo with the native camera, then our app receives the image. No camera API limitations apply.

**Platform status (2026):**
- **Android Chrome**: Full PWA support — installable, background sync, push notifications, `capture="environment"` opens native camera with full controls
- **iOS Safari**: Weaker — no background sync, recurring permission bugs in standalone mode, EU users lose standalone entirely (DMA). But `capture="environment"` still works for the camera, which is all we need for v1

**Why we don't need to decide about native now:**
The Go API is already structured as a clean REST API + WebSocket. Any native client (React Native, Swift, Kotlin) would just be a different frontend hitting the same endpoints. No architectural changes needed. The API contract (`/api/v1/...`) is the boundary.

**If camera UX proves insufficient later:** React Native + Expo + VisionCamera is the escape hatch. Expect ~30-50% code sharing (logic/types/API client, not UI components). But this is unlikely to be needed given the `capture="environment"` approach.

**No Electron/Tauri needed:** The PWA installs to the dock on macOS and home screen on mobile. A desktop wrapper adds nothing.

### Verified: Anthropic Go SDK Multi-Image Support

**Status: CONFIRMED.** The SDK fully supports multiple images in a single Messages call.

```go
// Send 3 photos of a long Costco receipt in one request
anthropic.NewUserMessage(
    anthropic.NewImageBlockBase64("image/jpeg", base64Photo1),
    anthropic.NewImageBlockBase64("image/jpeg", base64Photo2),
    anthropic.NewImageBlockBase64("image/jpeg", base64Photo3),
    anthropic.NewTextBlock(receiptExtractionPrompt),
)
```

- `NewUserMessage` accepts variadic `ContentBlockParamUnion` — mix text and images freely
- Up to 600 images per request, max 8000x8000px per image
- Images before text yields best results
- Single structured JSON response covers all images

### Verified: FTS5 Works with modernc.org/sqlite

**Status: CONFIRMED.** FTS5 is compiled in by default — no build tags or configuration needed.

The transpiled Go source includes `-DSQLITE_ENABLE_FTS5` in generator flags and declares `const SQLITE_ENABLE_FTS5 = 1` with 6,021 FTS5-related symbols. PocketBase (production Go app) confirms FTS5 works out of the box with modernc.org/sqlite.

Also available: trigram tokenizer for substring/fuzzy matching, BM25 ranking, highlight/snippet functions. All usable for product autocomplete search.

### Auth Stack

**Decision: Hand-roll with `golang-jwt/jwt/v5` + Echo JWT middleware.**

No all-in-one auth library exists for Go API apps (Authboss targets server-rendered HTML, Goth is OAuth-only). External auth services (Authelia, Zitadel, Keycloak) are all overkill for 2 users. Every comparable self-hosted app (Receipt Wrangler, Mealie, Immich) hand-rolls auth.

**Stack (~400 lines of Go):**
- `golang-jwt/jwt/v5` — the undisputed standard (9k stars), HMAC-SHA256 signing
- `labstack/echo-jwt/v4` — Echo's official middleware, wraps golang-jwt, handles token extraction + validation + context injection
- `golang.org/x/crypto/bcrypt` — password hashing
- SQLite `users` table + optional refresh token table

**New User Flow:**
```
FIRST BOOT:
  GET /api/v1/status → { "needs_setup": true }
  → Show setup page (only route accessible when no users exist)

  POST /api/v1/setup
    { household_name: "The Stefankos", user_name: "Mike", email, password }
  → Single form, single submit
  → Creates household + user in one transaction, returns JWT
  → Race condition guard: use a sync.Once or BEGIN IMMEDIATE transaction
    that checks user count inside the transaction (SQLite serializes writes)
  → Redirect to dashboard with onboarding prompt

INVITE (link-based, no SMTP needed for v1):
  POST /api/v1/invite  (authenticated)
  → Returns { link: "https://cartledger.local/join/eyJ...", expires_in: "7 days" }
  → Invite token is a signed JWT (household_id + 7-day expiry, no DB table needed)
  → Frontend shows: [ Copy Link ] [ Share ] (Web Share API → WhatsApp, iMessage, etc.)

  GET /api/v1/invite/:token/validate  (unauthenticated)
  → Returns { household_name: "The Stefankos", invited_by: "Mike" } or 401 if expired

  POST /api/v1/join  (unauthenticated)
    { token, user_name: "Sarah", email, password }
  → Validates invite JWT, creates user, returns auth JWT
  → Redirect to dashboard (same household data visible immediately)

LOGIN:
  POST /api/v1/login  { email, password }
  → Returns JWT (30-day expiry), stored in localStorage

ALL OTHER ROUTES:
  Authorization: Bearer <jwt>
  JWT claims: { user_id, household_id, exp }
  Protected by labstack/echo-jwt/v4 middleware

WEBSOCKET:
  GET /api/v1/ws?token=<jwt>
  → JWT passed as query param (browser WebSocket API can't send headers)
  → Validate before upgrade
```

**Single household per deployment.** `POST /api/v1/setup` only works when 0 users exist. New users join exclusively via invite. Multi-household is a future additive change (the `household_id` FK is already on every table).

---

## Appendix: Pack Sizes, Units of Measure & Grocy Comparison

### Pack Sizes: Two Products Is Fine for v1

Grocy models this with a 4-tier QU (quantity unit) system: separate units for purchase, stock, consume, and price display, with product-specific conversion overrides. That's powerful but complex.

**Our approach for v1: treat different pack sizes as separate products.**

- "Kirkland Water 40pk" and "Poland Spring 1L" are separate products
- They already appear as different items on receipts from different stores
- Price normalization (Phase 4) compares them: both get a `normalized_price` per fl oz
- The product detail view shows per-unit cost across pack sizes naturally
- The `unit_conversions` table (already in schema) is the hook for explicit pack-size relationships later

**Why this is the right call:**
- Our input is receipt text, not barcodes. Receipts already separate pack sizes.
- Merging products later (already planned) is additive. Splitting a combined product is destructive.
- Grocy's QU system is designed for inventory management (consume 1 bottle from a case). We're tracking prices, not stock levels.

**Phase 4+ upgrade path:** Add a `parent_product_id` FK on `products` for "Kirkland Water 40pk is a variant of Water." This enables a grouped product view without the full QU conversion cascade.

### What We Skip from Grocy and Why

| Grocy Feature | Our App | Rationale |
|---------------|---------|-----------|
| Expiry/best-before tracking | Skip | We're a price tracker, not inventory manager. Use Grocy alongside if needed. |
| Stock levels / consume flow | Skip | We track purchases, not pantry state. |
| Min stock thresholds | "Buy Again" prediction replaces this | Our purchase-frequency algorithm is smarter and requires zero manual setup. |
| Barcode scanning | Skip for v1 | Our input is receipt photos. Could add barcode lookup (Open Food Facts API) later to enrich products. |
| 4-tier QU conversion system | Simplified `unit_conversions` table | Full QU system is overkill. Our table handles food densities (1 cup flour → oz) which is what recipe costing needs. |
| Location tracking (fridge/pantry/shelf) | Skip | Not relevant to price tracking. |
| Meal planning | Import from Mealie | Don't rebuild what Mealie does well. |
| Product groups (hierarchical) | Flat categories | LLM suggests categories during scan. Hierarchy adds complexity without clear benefit for price tracking. |

### What We Do Better Than Grocy

| Feature | Grocy | Our App |
|---------|-------|---------|
| Receipt scanning | Most-requested missing feature (#2831) | Core feature — LLM vision extraction |
| Price comparison across stores | Basic price field per product | Full per-store price history with normalized per-unit comparison |
| Purchase prediction | None — only static min-stock thresholds | Quantity-aware interval prediction from purchase history |
| Matching/normalization | Manual product entry | Three-stage matching engine with auto-rule creation (Actual Budget pattern) |
| Shared lists with pricing | Basic shopping lists | Lists with estimated costs, cheapest-store indicators, real-time collaboration |

---

## Appendix: Open Questions & Decisions (from senior review)

### Resolved in This Plan

**Q1: Table UI complexity (HIGH RISK)**
The receipt review screen AND the product/transaction tables need Actual Budget-style inline editing — click a cell, edit in place, Tab/Enter to navigate, autocomplete dropdowns. Actual built ~3,000 lines of custom table code with `FixedSizeList` virtualization. Our approach: use `@tanstack/react-table` for the headless logic (sorting, filtering, column definitions, virtualization via `@tanstack/react-virtual`) and build custom cell renderers for inline editing. This is the single largest frontend effort — Phase 1 should build the core `<EditableTable>` component that all views reuse. Do NOT underestimate this.

**Q2: Cold start / empty database**
Day one, every item is unmatched. Fix: enhance the LLM prompt to return `suggested_name` and `suggested_category` alongside `raw_name`. First scan seeds the product catalog with LLM-suggested canonical names. User confirms or edits during review. The matching engine still runs for subsequent receipts.

**Q3: Auth for mobile self-hosted**
JWT tokens stored in localStorage, not cookies (phones clear session cookies). Simple flow: household created on first boot, invite link with token for second user. No OAuth, no social login. Session refresh on app open.

**Q4: Multi-image receipts**
Support multiple photos per receipt upload. LLM API accepts multiple images in a single call. Upload UX: "Scan receipt" → camera → "Add more pages?" → submit all. Go backend stitches images into a single LLM call.

**Q5: Bulk price extraction**
When a receipt shows "$10.47" without per-unit breakdown, the LLM should still attempt to extract quantity from context (e.g., "3LB" in the item name). If it can't, the review screen prompts the user to input quantity/unit. The `unit_price` field is nullable for this reason.

**Q6: Category bootstrapping**
LLM prompt returns `suggested_category` per item. Flat categories for v1 ("Meat", "Produce", "Dairy", "Frozen", "Snacks", "Beverages", "Household", "Other"). Seeded in DB, extensible by user. No hierarchy.

**Q7: WebSocket reconnection**
Use `reconnecting-websocket` npm package (auto-reconnect with exponential backoff). On reconnect, React Query's `refetchOnReconnect` refetches all stale queries to catch up on missed changes. No server-side event replay needed — refetch is simpler and sufficient for 2 users.

**Q8: LLM prompt enhancement (updated)**
The extraction prompt now returns both raw and suggested names:
```json
{
  "items": [{
    "raw_name": "BNLS CHKN BRST",
    "suggested_name": "Chicken Breast, Boneless",
    "suggested_category": "Meat",
    "quantity": 3.0,
    "unit": "lb",
    "unit_price": 3.49,
    "total_price": 10.47
  }]
}
```

---

## Appendix A: Key Libraries Reference

| Need | Library | Notes |
|------|---------|-------|
| HTTP framework | echo/v4 | Middleware, groups, binding, validation |
| SQLite | modernc.org/sqlite | Pure Go, no CGO, WAL mode |
| Migrations | golang-migrate/v4 | SQL file-based migrations |
| LLM (Claude) | anthropic-sdk-go | Official, vision support |
| LLM (Gemini) | generative-ai-go | Official, Flash 2.0 for cheap fallback |
| WebSocket | gorilla/websocket | Standard Go WebSocket lib |
| Money math | shopspring/decimal | Arbitrary precision decimals |
| Units | martinlindhe/unit | Has cooking units (cup, tbsp, tsp) |
| Fuzzy match | lithammer/fuzzysearch | Trigram similarity, Levenshtein |
| React table | @tanstack/react-table | Headless, virtualized, sortable |
| Data fetching | @tanstack/react-query | Cache, optimistic updates, WebSocket invalidation |
| Charts | recharts | React-native, composable, good for sparklines |
| CSS | tailwindcss v4 | Design tokens from DESIGN.md |
| Routing | react-router v7 | Standard React routing |

## Appendix B: Receipt Scanning Cost Estimate

| Provider | Cost per Receipt | 500 receipts/month |
|----------|-----------------|---------------------|
| Claude Sonnet (vision) | ~$0.003 | ~$1.50 |
| Gemini Flash 2.0 | ~$0.0002 | ~$0.10 |
| Veryfi (receipt API) | ~$0.08 | ~$40.00 |
| Local PaddleOCR | $0 (CPU cost) | $0 |

**Recommendation:** Start with Claude Sonnet for accuracy. If cost matters at scale, switch to Gemini Flash (nearly as accurate, 15x cheaper). Local OCR is a future escape hatch, not needed for v1.

---

## Phase 1 Work Breakdown

### Verified Assumptions

- `modernc.org/sqlite` + FTS5 — VERIFIED, compiled in by default, trigram tokenizer available
- `anthropic-sdk-go` multi-image — VERIFIED, variadic `ContentBlockParamUnion`, up to 600 images
- `@tanstack/react-table` v8 + `@tanstack/react-virtual` — VERIFIED, Actual Budget proves the pattern
- `golang-jwt/jwt/v5` + `labstack/echo-jwt/v4` — VERIFIED, standard Go auth stack, ~400 lines
- `embed.FS` serves Vite-built React — VERIFIED, standard Go 1.16+ pattern
- Invite links as signed JWTs — no `invite_tokens` table needed

### Approach

Build in strict dependency order across three layers:
1. **Backend scaffold + DB + auth** — everything else depends on this
2. **Frontend scaffold + EditableTable** — the critical-path UI component
3. **Receipt pipeline + review screen + matching** — the core product

The EditableTable is the single largest effort and the critical blocker. It must be built as a standalone, reusable component before any page uses it. It blocks receipt review, product catalog, shopping lists, and rules — all use the same inline-editing table pattern.

**Why not build EditableTable incrementally:** The receipt review screen IS the product. If it feels like a basic form instead of a spreadsheet, the UX fails. The same component is reused in 4+ views, so building it twice wastes effort. Actual Budget's experience proves inline editing + keyboard nav + autocomplete must be designed together, not bolted on.

### Layer 1: Backend Foundation (blocks everything)

**1. Go project scaffold**
- `cmd/server/main.go` — Echo server, config from env vars (PORT, DATA_DIR, ANTHROPIC_API_KEY, LLM_PROVIDER), graceful shutdown via `signal.NotifyContext` + `server.Shutdown`, static file serving via `embed.FS`
- `internal/config/config.go` — Env var parsing with defaults
- `go.mod` — Module init with all dependencies

**2. SQLite connection + migration runner**
- `internal/db/sqlite.go` — Open(), pragma config (WAL, foreign_keys, busy_timeout, cache_size, mmap_size)
- `internal/db/migrate.go` — golang-migrate/migrate/v4 runner, embed migration SQL files

**3. Initial migration — all tables**
- `migrations/001_initial.sql` — All tables from the schema section:
  - `households`, `users`, `stores`, `products`, `product_aliases`, `matching_rules`, `receipts` (`image_paths` as JSON array), `line_items`, `product_prices`, `shopping_lists`, `shopping_list_items`, `unit_conversions`, `product_images`, `product_links`
  - FTS5 virtual tables: `products_fts`, `product_aliases_fts`
  - All indexes including `idx_product_images_product`, `idx_product_links_product`, `idx_product_links_source`
  - FTS5 triggers (INSERT/UPDATE/DELETE on products/aliases sync to FTS tables)
- Single migration for clean initial state. All tables included even if some aren't used until Phase 2 — avoids migration churn.

**4. Domain models**
- `internal/models/models.go` — Go structs for all tables. `shopspring/decimal` for all price fields (stored as TEXT in SQLite, parsed in Go). JSON tags for API responses.

**5. Auth system (JWT)**
- `internal/auth/jwt.go` — JWT creation/validation using `golang-jwt/jwt/v5` (HMAC-SHA256, household_id + user_id claims, 30-day expiry). Invite tokens are also JWTs (household_id claim, 7-day expiry).
- `internal/auth/middleware.go` — `labstack/echo-jwt/v4` middleware. Handles token extraction, validation, context injection.
- `internal/api/auth.go` — Endpoints: `POST /api/v1/setup` (first boot), `POST /api/v1/login`, `POST /api/v1/invite`, `POST /api/v1/join/:token`
- `internal/auth/password.go` — bcrypt hash/verify

**6. API router skeleton**
- `internal/api/router.go` — Echo router with middleware (CORS, auth, request logging). Mount all route groups. Serve embedded static files for `/*` catch-all.

**7. Store CRUD**
- `internal/api/stores.go` — `GET /api/v1/stores`, `POST`, `PUT /:id`, `DELETE /:id`, `PUT /reorder`

**8. Product CRUD + aliases + images + links**
- `internal/api/products.go` — `GET /api/v1/products` (FTS5 search), `POST`, `PUT /:id`, `DELETE /:id`, `POST /:id/images` (multipart), `DELETE /:id/images/:imageId`, `GET /:id/links`
- `internal/api/aliases.go` — `GET /api/v1/products/:id/aliases`, `POST`, `DELETE`

**9. LLM client + receipt extraction**
- `internal/llm/types.go` — Go structs for ReceiptExtraction, ExtractedItem (raw_name, suggested_name, suggested_category, etc.)
- `internal/llm/client.go` — `Client` interface: `ExtractReceipt(images [][]byte) (*ReceiptExtraction, error)`, `Provider() string` (returns "claude", "gemini", "mock" — stored in `receipts.llm_provider`)
- `internal/llm/claude.go` — Claude implementation using `anthropic-sdk-go`. Multi-image support via variadic `NewImageBlockBase64`.
- `internal/llm/mock.go` — Mock implementation returning canned JSON for development/testing. Enabled via `LLM_PROVIDER=mock`. Include `testdata/sample-receipt-*.jpg` + expected JSON.
- `internal/llm/prompt.go` — Prompt template with suggested_name + suggested_category

**10. Matching engine**
- `internal/matcher/normalizer.go` — Normalize: lowercase, strip punctuation, collapse whitespace, trim
- `internal/matcher/rules.go` — matchByRules: query matching_rules by priority DESC, evaluate condition_op
- `internal/matcher/fuzzy.go` — matchByAlias (exact, store-specific first), matchByFuzzy (lithammer/fuzzysearch, >0.7 threshold)
- `internal/matcher/engine.go` — Three-stage pipeline: rules → alias → fuzzy → unmatched

**11. Receipt upload + background processing**
- `internal/api/receipts.go` — `POST /api/v1/receipts/scan` (multipart, 1+ images, max 10MB per image, JPEG/PNG only), `GET /:id`, `GET /`, `PUT /:id/line-items/:itemId`
- `internal/worker/receipt.go` — Goroutine pool, channel-based job queue. Process: save images → LLM → parse → find-or-create store → match each item → save → WebSocket notify

**12. WebSocket hub (minimal)**
- `internal/ws/hub.go` — Hub struct, register/unregister, broadcast by household_id
- `internal/ws/messages.go` — `receipt.processing`, `receipt.complete`
- `internal/api/ws.go` — `GET /api/v1/ws` upgrade (auth via query param token)

**13. Manual match + rule creation**
- `internal/api/matching.go` — `PUT /api/v1/line-items/:id/match`, `GET/POST/DELETE /api/v1/rules`
- On match: update line_items.product_id, create product_alias, insert product_prices, optionally create matching_rule

**14. Docker setup**
- `Dockerfile` — Multi-stage: Node (frontend) → Go (backend) → alpine (runtime). CGO_ENABLED=0.
- `docker-compose.yml` — Single service, `./data:/data` volume, env vars, port 8080
- `.dockerignore`

### Layer 2: Frontend Foundation (blocks all UI)

**15. React scaffold**
- `web/package.json` — react, react-router-dom, @tanstack/react-query, @tanstack/react-table, @tanstack/react-virtual, tailwindcss, @tailwindcss/vite, reconnecting-websocket
- `web/vite.config.ts` — Proxy to localhost:8080 for dev, Tailwind plugin
- `web/tailwind.config.ts` — Full DESIGN.md tokens (brand purple, neutral scale, semantic colors, font sizes, shadows)
- `web/src/styles/tailwind.css` — Tailwind directives + IBM Plex Sans import
- `web/tsconfig.json` — Strict mode, `@/` path alias

**16. TypeScript types**
- `web/src/types/index.ts` — Mirror all Go model structs

**17. API client layer**
- `web/src/api/client.ts` — Fetch wrapper with JWT auth
- `web/src/api/auth.ts`, `stores.ts`, `products.ts`, `receipts.ts` — CRUD calls
- `web/src/api/ws.ts` — `reconnecting-websocket` wrapper with React Query invalidation

**18. Auth pages**
- `web/src/pages/SetupPage.tsx` — First-boot household + user creation
- `web/src/pages/LoginPage.tsx` — Email + password
- `web/src/pages/JoinPage.tsx` — Accept invite, create account
- `web/src/hooks/useAuth.ts` — Auth context

**19. App shell + router + sidebar**
- `web/src/App.tsx` — React Router with auth guard
- `web/src/components/ui/Sidebar.tsx` — Stores (dynamic), Pages (Products, Rules, Receipts). Collapsible on mobile.
- `web/src/components/layout/AppLayout.tsx` — Sidebar + Outlet
- `web/src/pages/DashboardPage.tsx` — Placeholder with onboarding prompt

**20. Design system primitives**
- `Button.tsx` — Primary purple, outlined, subtle, secondary gray per DESIGN.md
- `Input.tsx`, `Badge.tsx`, `Modal.tsx` — Lean, just enough for Phase 1

### Layer 3: EditableTable + Receipt Review (critical path)

**21. EditableTable component** (CRITICAL — largest single effort)
- `web/src/components/ui/EditableTable/EditableTable.tsx` — Generic component with TanStack table + virtual. Props: columns, data, onCellUpdate, getRowClassName, virtualizeRows. Fixed 36px row height.
- `EditableCell.tsx` — View/edit modes. Click to edit, Blur/Enter to commit, Escape to cancel.
- `AutocompleteCell.tsx` — Product matching dropdown. Fuzzy-filtered, keyboard navigable, "Create new..." option. Debounced search. Portal to document.body. **Hardest sub-component.**
- `useTableNavigation.ts` — Active cell coordinates, Tab/Shift-Tab/Enter/Escape/Arrow key navigation.
- Column meta: `meta.editable: boolean`, `meta.cellType: 'text' | 'number' | 'autocomplete'`
- Active cell state in table (single source of truth), not individual cells

**22. Receipt scanner (upload UI)**
- `web/src/components/receipts/ReceiptScanner.tsx` — File picker with `capture="environment"`, multi-image thumbnails, "Add more pages", FormData upload, WebSocket listener for completion

**23. Receipt review screen**
- `web/src/pages/ReceiptReviewPage.tsx` — Two-column: receipt image (scrollable) + EditableTable
- `web/src/components/receipts/ReceiptReview.tsx` — Columns: Status, Raw Name, Match (autocomplete), Quantity, Unit, Price. Status bar: "8 matched, 3 need review". "View Original Receipt" + "View Raw JSON" audit buttons.
- `web/src/components/receipts/CreateRuleModal.tsx` — "Always match X to Y?" with condition_op selector
- "Confirm All" button finalizes receipt

**24. Receipt history page**
- `web/src/pages/ReceiptsPage.tsx` — List of receipts with store, date, total, status

**25. Product catalog page**
- `web/src/pages/ProductsPage.tsx` — EditableTable with Name, Category, Default Unit, Alias Count, Last Price. Inline editing.

### Risks

- ~~FTS5 + modernc.org/sqlite~~ — RESOLVED
- ~~Anthropic Go SDK multi-image~~ — RESOLVED
- **EditableTable scope creep** — Phase 1 scope is strictly: single-cell edit, keyboard nav, autocomplete dropdown. No multi-select, no drag-drop, no batch operations.
- **LLM response parsing fragility** — Validate with strict JSON unmarshaling, log raw_llm_json, retry once on parse failure.
- **WebSocket auth** — JWT as query parameter on connect, validate before upgrade.
- **Autocomplete search performance** — 200ms debounce client-side, FTS5 with LIMIT 20 server-side.

### Implementation Notes

- **Graceful shutdown:** Go server must handle SIGTERM for Docker — use `signal.NotifyContext` + `server.Shutdown(ctx)` to drain WebSocket connections, let in-progress receipt workers finish, and close SQLite WAL cleanly.
- **Mock LLM for development:** Add a `internal/llm/mock.go` implementation of the `Client` interface that returns canned JSON responses. Include 2-3 sample receipt images in `testdata/` for development and integration tests. Enable via `LLM_PROVIDER=mock` env var.
- **CORS configuration:** Production (embedded static files): same-origin, no CORS config needed. Development (Vite dev server on :5173 proxying to :8080): Vite proxy handles this, no Echo CORS middleware needed in dev either. Only add CORS middleware if the frontend is served from a different origin in production.
- **Backup:** SQLite in WAL mode can be safely copied while the app is running. Document a simple backup approach: `sqlite3 /data/cartledger.db ".backup /data/backup.db"` via cron or manual script. The entire data volume (`./data/`) contains both the DB and receipt images — a single volume backup covers everything.
- **Shopping list reorder concurrency:** Two users reordering simultaneously could interleave `sort_order` updates. Known limitation for v1 — acceptable for 2 users with last-write-wins semantics.

### Out of Scope (Phase 2+)

- Gemini fallback provider
- Shopping list live collaboration
- Analytics dashboard (sparklines, price trends, buy-again predictions)
- Mealie import
- Unit conversion + price normalization
- Product merge
- Export/sharing
- PWA manifest + service worker
- Multi-select, batch operations, drag-drop in EditableTable

### Test Coverage

**Backend:** sqlite_test.go (migration + FTS5), jwt_test.go, engine_test.go (all 3 match stages), normalizer_test.go, claude_test.go (mock HTTP, JSON parsing), receipts_test.go (multipart upload)

**Frontend:** EditableTable.test.tsx (render, edit, navigate), AutocompleteCell.test.tsx (dropdown, filter, keyboard, create new), useTableNavigation.test.ts (focus, wrapping)

**Integration:** Full flow: upload → mock LLM → receipt created → review → manual match → alias created → subsequent scan auto-matches
