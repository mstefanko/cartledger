# Implementation Plan: Food Product Enrichment

Date: 2026-05-12

This plan turns product enrichment into a durable CartLedger feature. The goal is
not to build a generic food database. The goal is to make grocery receipt data
more useful for CartLedger's actual workflows:

- better product matching on future receipts;
- reliable package size for normalized price comparison;
- accepted nutrition, ingredients, and allergens when the user cares;
- visible source evidence and reviewable suggestions;
- no fragile retailer scraping loop that quietly breaks.

## Recommendation

Move forward with a staged provider chain:

1. Use the current UPC and product enrichment stack as the base.
2. Add an allowlisted external metadata snapshot table and provider adapters.
3. Make receipt scanning create enrichment work from observed identifiers and
   explicit package text.
4. Implement automatic lookup for Open Food Facts, USDA FoodData Central, and
   Kroger.
5. Keep paid providers, broad web scraping, photos, and global product identity
   expansion deferred until the free/provider-backed flow proves value.

The short-term value is package size and identifiers. Nutrition is useful, but
secondary. Photos are evidence and nice-to-have; they should not drive the
first implementation.

## Review Addendum: Required Corrections Before Implementation

This plan is no longer a greenfield enrichment plan. Implement it as a delta on
the code that already exists in this repository.

Validated existing code:

- `internal/db/migrations/036_product_identifiers.up.sql` already added
  `product_identifiers`, `line_item_identifier_observations`, GTIN/PLU/external
  identifier kinds, and line item `matched='identifier'`.
- `internal/identifiers` already normalizes GTIN, PLU, and external identifiers
  and returns `IdentifierConflictError` / `ErrIdentifierConflict` on household
  identifier collisions.
- `internal/enrichment/types.go`, `internal/enrichment/html.go`, and
  `internal/enrichment/adapters/kroger.go` already exist. The Kroger adapter has
  a substantial HTML/visible-text parser and fixture tests; Phase 4 must extend
  that code, not replace it.
- `internal/api/products_enrichment.go` already supports manual URL fetch,
  Open Food Facts UPC lookup, USDA FoodData Central lookup when
  `USDA_FDC_API_KEY` is configured, source links, field suggestions, suggestion
  acceptance/rejection, bulk accept/reject, and accepted nutrition persistence;
  supporting tests live in `internal/api/products_enrichment_test.go`.
- `internal/llm/guarded.go` and `internal/llm/breaker.go` already provide
  household token budgets, rate-limit detection, retry-after helpers,
  `ErrCircuitOpen`, and `ErrBudgetExceeded`. New LLM enrichment paths must go
  through `GuardedExtractor`; do not create a second budget/breaker path.
- `internal/worker/receipt.go` already has the worker pool, shutdown,
  pending-job recovery, WebSocket completion events, identifier observations,
  and price recording patterns that an enrichment worker should mirror.
- `internal/sqliteutil/errors.go` already exposes `IsUniqueConstraint`; reuse it
  for idempotent inserts and conflict handling.
- `cmd/server/serve.go` is the actual server boot and worker wiring location.
  `cmd/server/main.go` only calls `Execute()`.
- `web/src/pages/ProductDetailPage.tsx` and
  `web/src/components/settings/IntegrationsTab.tsx` already contain the product
  detail and integration-card patterns this work should evolve.

Hard decisions resolved:

- Automatic external lookups are off by default. Manual lookup remains an
  explicit user action. Receipt-triggered lookup and scheduled sweeps require a
  household setting opt-in and the global config gate.
- UPC conflicts stop the accept flow. A single accept returns `409` with the
  existing product details. Bulk accept applies non-conflicting suggestions,
  reports UPC conflicts in the response, and never silently reassigns an
  identifier.
- Manual user edits win over future provider suggestions. Add field-level edit
  tracking so a later fetch does not keep resurfacing values the user already
  corrected.
- Successful products have a provider refresh cooldown. Default to 90 days for
  scheduled refresh, 7 days for repeated failures, and allow an explicit manual
  refresh to bypass the success cooldown while still respecting provider rate
  limits.
- Product enrichment jobs use a durable queue. Phase 2 wires the enrichment
  worker and manual job endpoint; HTTP handlers enqueue or reuse a job row and
  return `202` instead of calling providers inline. Phase 3 adds automatic
  enqueueing and sweeps on top of the same worker.
- Do not run a first-use backfill across the whole product table. At opt-in,
  offer a bounded first-run batch focused on recently purchased/high-value
  products, then let nightly sweeps and user-selected bulk lookup continue from
  there.
- Use WebSockets for cross-tab/client invalidation and terminal job events.
  Use short React Query polling only while a visible page is showing an active
  job.
- Package overrides are written only from explicit, deterministic package text.
  LLM-only package fields create suggestions, not line item overrides, unless
  the deterministic parser agrees.

Schema blockers to fix before provider orchestration:

- `product_enrichment_suggestions` is currently coupled to
  `product_link_id`. The nullable link means snapshot-driven suggestions cannot
  be safely upserted by the existing unique constraint. Add an
  `external_metadata_id` path and unique index, or create a source-link row for
  every snapshot before inserting suggestions. This plan recommends the
  `external_metadata_id` schema change.
- Adding `PackageLabel`, `PackageQuantity`, or `PackageUnit` to
  `llm.ExtractedItem` is not enough. The Claude tool schema in
  `internal/llm/claude.go`, the tolerant unmarshal helper in
  `internal/llm/types_unmarshal.go`, prompts, fixtures, and tests must all be
  updated in the same phase.
- Product mutation paths that accept suggestions must emit
  `ws.EventProductUpdated` with `product_id`, and the frontend WebSocket handler
  must invalidate both `['products']` and `['product-detail', product_id]`.
- Every new migration must include a down migration. Concrete down SQL for the
  proposed 040/041/042 migrations appears in the Data Model section.

## Current CartLedger Stack

Existing pieces to keep and extend:

- `products.upc` and `line_items.upc` already exist, with a household-level
  unique index on product UPC in
  `internal/db/migrations/032_upc_fields.up.sql`.
- `product_identifiers` and `line_item_identifier_observations` already exist
  from migration 036. Treat `products.upc` as the primary GTIN shortcut for
  compatibility, while using `internal/identifiers` as the authoritative
  conflict-aware identifier path.
- `products.pack_quantity` and `products.pack_unit` already drive normalized
  price calculations.
- `line_items.pack_quantity_override` and `line_items.pack_unit_override`
  already exist and are used by `internal/prices.RecordProductPriceFromLineItem`.
  The scan worker does not currently populate them from receipt package text.
- `store_product_codes` already maps store-scoped receipt item codes to
  products. The matcher already uses this for deterministic store-code matches.
- `product_links` already stores product source links, and later migrations add
  fetch metadata such as `fetched_at`, `http_status`, `content_hash`,
  `last_error`, and `source_confidence`.
- `product_enrichment_suggestions` already stores reviewable field-level
  suggestions.
- `product_nutrition` already stores accepted nutrition/ingredient/allergen
  fields.
- `internal/api/products_enrichment.go` already makes live Open Food Facts and
  USDA calls and parses suggestions. The provider-chain work should extract and
  harden this behavior, not create a second implementation.
- `internal/enrichment/adapters/kroger.go` already parses Kroger visible text.
  Use it as the URL/manual evidence parser and extend it for API-backed Kroger
  metadata.
- `ProductDetailPage` already exposes brand, UPC lookup, package size editing,
  source links, suggestions, and accepted nutrition.
- `internal/api/product_enrichment_settings.go` and
  `web/src/components/settings/IntegrationsTab.tsx` already expose the
  household enrichment settings surface; Phase 3 should evolve labels,
  availability messaging, and scheduled/automatic behavior rather than adding a
  second settings API.
- `Settings -> Integrations` already has a credential-storage pattern for
  household-scoped integrations.

That is enough to avoid a large rewrite. The missing layer is orchestration:
provider adapters, status tracking, snapshots, provider settings, store mapping,
and a cleaner user flow around "find missing metadata".

## Source Strategy

### Sources To Implement First

#### Open Food Facts

Use for barcode-first packaged food lookup.

Use when:

- a UPC/GTIN is present on a product or line item;
- the product is packaged food or household grocery;
- CartLedger needs name, brand, quantity, serving size, nutrition, ingredients,
  allergens, and image URLs as source evidence.

Implementation details:

- Query direct product endpoint by barcode.
- Store only allowlisted fields in CartLedger's metadata contract.
- Create suggestions for `upc`, `name`, `brand`, `pack_quantity`, `pack_unit`,
  serving fields, nutrients, ingredients, and allergens.
- Store image URLs as metadata/evidence only in v1. Do not download images.
- Show attribution/source link in the UI.
- Use a CartLedger-specific User-Agent with contact/configurable operator info.
- Rate-limit adapter calls through a shared runner-owned provider limiter. Open
  Food Facts currently caps direct product reads at 15 requests per minute per
  IP and search reads at 10 requests per minute per IP. Phase 2 only uses direct
  product reads; model this as a server-wide per-host limit because the
  CartLedger server IP is the caller.

Sources:

- [Open Food Facts API docs](https://openfoodfacts.github.io/documentation/docs/Product-Opener/api/)
- [Open Food Facts API/data reuse conditions](https://support.openfoodfacts.org/help/en-gb/12-api-data-reuse/94-are-there-conditions-to-use-the-api)

#### USDA FoodData Central

Use for authoritative nutrition fallback and cross-checking branded food data.

Use when:

- a UPC/GTIN is present;
- `USDA_FDC_API_KEY` or a household USDA integration is configured;
- Open Food Facts has no result or weak nutrition data;
- a user explicitly asks to fetch nutrition.

Implementation details:

- Query `/fdc/v1/foods/search` with `query=<upc>`, `dataType=Branded`,
  `pageSize=5`, and configured API key.
- Accept only exact normalized `gtinUpc` matches for automatic suggestions.
- Use name/brand matches without UPC only as manual-search, low-confidence
  results.
- Use USDA nutrients as nutrition suggestions. Do not use USDA to populate
  package size unless a branded record has explicit package text.
- Keep USDA as optional config because it requires an API key.
- Rate-limit adapter calls through the same shared provider limiter. FoodData
  Central currently defaults to 1,000 requests per hour per IP address; model
  this as a server-wide per-host limit. Record whether the effective credential
  came from household settings or the env fallback for observability, but do not
  treat the provider limit itself as per-household.

Source:

- [USDA FoodData Central API guide](https://fdc.nal.usda.gov/api-guide)

#### Kroger

Use for retailer-specific enrichment, especially Kroger receipts and stores.

Use when:

- the receipt store is Kroger or a Kroger banner;
- the user has configured a Kroger integration;
- the product has a UPC, receipt description, or known Kroger product URL;
- a CartLedger store is mapped to a Kroger `locationId`.

Implementation details:

- Add a Kroger integration with `client_id` and `client_secret`; use the
  server-side OAuth flow needed for product/location APIs and store access
  tokens with expiry. Do not implement customer authorization-code/cart flows
  for this feature.
- Add store mapping from CartLedger `stores` to Kroger `locationId`.
- For UPC lookup, call Kroger Products API with UPC/GTIN when possible.
- For receipt-description lookup, search by normalized receipt description and
  location, then score candidates by brand, size, and receipt price.
- If a Kroger product is confidently identified, create a `product_link` and
  source snapshot with Kroger product ID.
- Use Kroger for package size, brand, name, current location-specific price,
  aisle/fulfillment metadata, and images. Treat nutrition as optional and
  source-dependent.
- Do not scrape Kroger product pages as a scheduled workflow. Keep manual URL
  fetch as a fallback/evidence path only.

Sources:

- [Kroger public API docs on Postman](https://www.postman.com/kroger/the-kroger-co-s-public-workspace/documentation/ki6utqb/kroger-public-apis)
- [Kroger API products reference](https://www.postman.com/kroger/the-kroger-co-s-public-workspace/request/ki6utqb/product-search)

### Sources To Defer

#### Paid Nutrition/Product APIs

Keep these behind the same provider interface, but do not implement until the
free and Kroger-backed workflow proves value.

Candidates:

- Nutritionix/Syndigo: strong barcode, restaurant, natural-language, images,
  and nutrition coverage, but caching/licensing terms need review.
- FatSecret Platform: strong commercial database and barcode support.
- Edamam Food Database: UPC, parser, diet/allergen labels, and nutrition.
- Chomp: developer-friendly UPC/nutrition API with paid tiers and caching terms.
- UPCitemdb, Barcode Lookup, Go-UPC, Buycott: useful generic fallback for
  name/brand/image, weaker for normalized nutrition.

Why deferred:

- short-term CartLedger value is package size and matching, not perfect global
  nutrition coverage;
- paid-provider caching rules can conflict with local-first expectations;
- each provider adds credential UI, limits, error handling, and test surface;
- provider-derived nutrition still needs review.

Sources:

- [Nutritionix API guide](https://docx.syndigo.com/developers/docs/nutritionix-api-guide)
- [Nutritionix item lookup endpoint](https://docx.syndigo.com/developers/docs/search-item-endpoint)
- [Nutritionix FAQ](https://docx.syndigo.com/developers/docs/faqs)
- [FatSecret Platform](https://platform.fatsecret.com/platform-api)
- [Edamam Food Database API docs](https://developer.edamam.com/food-database-api-docs)
- [Chomp API](https://chompthis.com/api/)
- [UPCitemdb API](https://upcitemdb.com/api)

#### GS1 and 1WorldSync/Syndigo Product Content

Defer for v1. These are potentially useful for validating GTIN ownership and
brand-sourced product content, but likely require subscriptions or commercial
relationships. They do not solve the immediate CartLedger package-size loop as
quickly as Open Food Facts, USDA, and Kroger.

Sources:

- [Verified by GS1](https://www.gs1.org/services/verified-by-gs1)
- [GS1 US Data Hub](https://www.gs1us.org/tools/gs1-us-data-hub)

#### IFPS PLU Database

Defer a full PLU database. The shipped identifier model can already store PLU
observations; v1 should only add lightweight PLU detection from receipt text and
keep matches as observations or low-confidence suggestions. Do not scrape the
IFPS website.

Source:

- [IFPS PLU codes](https://www.ifpsglobal.com/plu-codes)

## Data Model

### 1. External Metadata Snapshots

Add an allowlisted snapshot table. This preserves source evidence without
committing provider payloads wholesale into CartLedger.

```sql
CREATE TABLE product_external_metadata (
    id                TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    household_id      TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    product_id        TEXT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    product_link_id   TEXT REFERENCES product_links(id) ON DELETE SET NULL,
    source            TEXT NOT NULL,
    source_record_id  TEXT,
    source_url        TEXT,
    lookup_key        TEXT,
    payload_json      TEXT NOT NULL,
    payload_version   INTEGER NOT NULL DEFAULT 1,
    content_hash      TEXT,
    fetched_at        DATETIME,
    expires_at        DATETIME,
    http_status       INTEGER,
    last_error        TEXT,
    confidence        REAL CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_product_external_metadata_product
    ON product_external_metadata(product_id, source, fetched_at);

CREATE UNIQUE INDEX idx_product_external_metadata_source_record
    ON product_external_metadata(product_id, source, source_record_id)
    WHERE source_record_id IS NOT NULL AND source_record_id != '';
```

Rules:

- `payload_json` is a CartLedger contract, not raw provider JSON.
- Store source-separated snapshots. Do not blend Open Food Facts, Kroger, USDA,
  and paid provider payloads into a synthetic "best product database".
- Accepted product fields remain on `products` or `product_nutrition`.
- Rejected suggestions remain rejected unless a later fetch changes evidence or
  source record.

Initial metadata contract:

```json
{
  "version": 1,
  "source": "openfoodfacts",
  "source_record_id": "0034000160006",
  "source_url": "https://world.openfoodfacts.org/product/0034000160006",
  "identifiers": [
    { "type": "gtin", "value": "0034000160006" }
  ],
  "name": "Example Product",
  "brand": "Example Brand",
  "category": "Pantry",
  "tags": ["gluten-free"],
  "package": {
    "label": "12 oz",
    "quantity": 12,
    "unit": "oz"
  },
  "serving": {
    "label": "2 tbsp (32 g)",
    "quantity": 32,
    "unit": "g",
    "servings_per_container": 10
  },
  "nutrients": {
    "calories": 190,
    "total_fat_g": 16,
    "sodium_mg": 120,
    "total_carbohydrate_g": 8,
    "protein_g": 7
  },
  "ingredients": "Peanuts, salt.",
  "allergens": ["peanuts"],
  "image_urls": {
    "front": "https://...",
    "nutrition": "https://..."
  },
  "evidence": [
    { "field": "package", "text": "Quantity: 12 oz" }
  ]
}
```

Concrete Go contract:

```go
type MetadataPayload struct {
    Version        int                  `json:"version"`
    Source         string               `json:"source"`
    SourceRecordID *string              `json:"source_record_id,omitempty"`
    SourceURL      *string              `json:"source_url,omitempty"`
    Identifiers    []PayloadIdentifier  `json:"identifiers,omitempty"`
    Name           *string              `json:"name,omitempty"`
    Brand          *string              `json:"brand,omitempty"`
    Category       *string              `json:"category,omitempty"`
    Tags           []string             `json:"tags,omitempty"`
    Package        *PackagePayload      `json:"package,omitempty"`
    Serving        *ServingPayload      `json:"serving,omitempty"`
    Nutrients      *NutrientPayload     `json:"nutrients,omitempty"`
    Ingredients    *string              `json:"ingredients,omitempty"`
    Allergens      []string             `json:"allergens,omitempty"`
    ImageURLs      map[string]string    `json:"image_urls,omitempty"`
    ProviderMeta   map[string]string    `json:"provider_meta,omitempty"`
    Evidence       []EvidencePayload    `json:"evidence,omitempty"`
}

type PayloadIdentifier struct {
    Type      string  `json:"type"`                // gtin, plu, external_id
    Authority *string `json:"authority,omitempty"` // ifps, kroger, openfoodfacts
    Value     string  `json:"value"`
}

type PackagePayload struct {
    Label    *string  `json:"label,omitempty"`
    Quantity *float64 `json:"quantity,omitempty"`
    Unit     *string  `json:"unit,omitempty"`
}

type ServingPayload struct {
    Label                *string  `json:"label,omitempty"`
    Quantity             *float64 `json:"quantity,omitempty"`
    Unit                 *string  `json:"unit,omitempty"`
    ServingsPerContainer *float64 `json:"servings_per_container,omitempty"`
}

type NutrientPayload struct {
    Calories             *float64 `json:"calories,omitempty"`
    TotalFatG            *float64 `json:"total_fat_g,omitempty"`
    SaturatedFatG        *float64 `json:"saturated_fat_g,omitempty"`
    TransFatG            *float64 `json:"trans_fat_g,omitempty"`
    CholesterolMG        *float64 `json:"cholesterol_mg,omitempty"`
    SodiumMG             *float64 `json:"sodium_mg,omitempty"`
    TotalCarbohydrateG   *float64 `json:"total_carbohydrate_g,omitempty"`
    DietaryFiberG        *float64 `json:"dietary_fiber_g,omitempty"`
    TotalSugarsG         *float64 `json:"total_sugars_g,omitempty"`
    AddedSugarsG         *float64 `json:"added_sugars_g,omitempty"`
    ProteinG             *float64 `json:"protein_g,omitempty"`
}

type EvidencePayload struct {
    Field string  `json:"field"`
    Text  string  `json:"text"`
    URL   *string `json:"url,omitempty"`
}
```

Suggestion linkage fix:

```sql
ALTER TABLE product_enrichment_suggestions
    ADD COLUMN external_metadata_id TEXT
        REFERENCES product_external_metadata(id) ON DELETE SET NULL;

CREATE UNIQUE INDEX idx_product_enrichment_suggestions_snapshot_unique
    ON product_enrichment_suggestions(product_id, external_metadata_id, field, value)
    WHERE external_metadata_id IS NOT NULL;
```

Write rules:

- Provider-backed suggestions must have either `product_link_id` or
  `external_metadata_id`.
- `source_url` remains required by the current suggestions table. Use the
  provider product URL when available, otherwise a deterministic provider search
  URL for the lookup key.
- Snapshot-backed insert/upsert uses
  `ON CONFLICT(product_id, external_metadata_id, field, value)`.
- Existing URL/link-backed insert/upsert can keep using
  `ON CONFLICT(product_id, product_link_id, field, value)`.
- Do not insert new provider suggestions with both link and snapshot unset.

### 2. Enrichment Jobs

Add a small persistent job/status table. This can start as a single-process
worker and later become a richer queue if needed.

```sql
CREATE TABLE product_enrichment_jobs (
    id                  TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    household_id        TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    product_id          TEXT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    requested_by_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    trigger             TEXT NOT NULL CHECK (trigger IN (
        'receipt_scan',
        'manual_lookup',
        'manual_refresh',
        'scheduled_refresh',
        'batch_backfill',
        'receipt_review_scan'
    )),
    lookup_key          TEXT NOT NULL DEFAULT '',
    requested_sources   TEXT,
    status              TEXT NOT NULL DEFAULT 'queued' CHECK (status IN (
        'queued',
        'running',
        'succeeded',
        'partial',
        'failed',
        'cancelled'
    )),
    attempt_count       INTEGER NOT NULL DEFAULT 0,
    next_attempt_at     DATETIME,
    last_error          TEXT,
    queued_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at          DATETIME,
    finished_at         DATETIME,
    updated_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_product_enrichment_jobs_status
    ON product_enrichment_jobs(status, queued_at);

CREATE INDEX idx_product_enrichment_jobs_product
    ON product_enrichment_jobs(product_id, queued_at);

CREATE UNIQUE INDEX idx_product_enrichment_jobs_active
    ON product_enrichment_jobs(household_id, product_id, trigger, lookup_key)
    WHERE status IN ('queued', 'running');
```

Implementation note: migrations 041 and 043 already exist in the repository.
Because migration 041's `trigger` CHECK currently omits
`receipt_review_scan`, add a follow-up migration before Phase 3.5 that recreates
`product_enrichment_jobs` with the expanded trigger set and preserves the
household-scoped active-job unique index from migration 043.

Job idempotency:

- before queueing a job, check for queued/running jobs for the same
  `(household_id, product_id, trigger, lookup_key)`;
- provider adapters upsert snapshots by source and source record;
- suggestions upsert through either the existing link unique constraint or the
  new snapshot unique index;
- failed provider calls update snapshot/job metadata without deleting old good
  snapshots.
- on server boot, reset stale `running` jobs older than 30 minutes to `queued`
  with `attempt_count + 1`, capped by a max-attempt policy.
- provider rate-limit responses set `next_attempt_at` from
  `llm.RateLimitRetryAfter`-style helper logic when available; otherwise use
  exponential backoff with jitter.

### 3. Product Enrichment Settings

Add household-scoped settings for privacy and provider opt-in. These are
separate from credential rows in `integrations` because Open Food Facts has no
secret and because automatic lookup needs its own explicit consent.

```sql
CREATE TABLE product_enrichment_settings (
    household_id                  TEXT PRIMARY KEY REFERENCES households(id) ON DELETE CASCADE,
    manual_lookup_enabled          INTEGER NOT NULL DEFAULT 1,
    auto_on_scan_enabled           INTEGER NOT NULL DEFAULT 0,
    scheduled_sweep_enabled        INTEGER NOT NULL DEFAULT 0,
    provider_openfoodfacts_enabled INTEGER NOT NULL DEFAULT 1,
    provider_usda_fdc_enabled      INTEGER NOT NULL DEFAULT 0,
    provider_kroger_enabled        INTEGER NOT NULL DEFAULT 0,
    first_run_backfill_limit       INTEGER NOT NULL DEFAULT 200,
    created_at                     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at                     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

Defaults:

- Manual lookup is available by default because it is an explicit click.
- Automatic scan lookup and scheduled sweep are disabled until the household
  opts in.
- USDA and Kroger provider toggles remain disabled until credentials or store
  mapping exist.
- The global `PRODUCT_ENRICHMENT_ENABLED=true` feature flag preserves existing
  explicit manual lookup behavior. Operators can set it to `false` to disable
  all provider calls regardless of household settings.

### 4. Manual Field Edit Tracking

Manual user edits should suppress repetitive future suggestions for the same
field. Track the field-level edit instead of trying to infer intent from
`products.updated_at`.

```sql
CREATE TABLE product_field_edits (
    product_id         TEXT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    field              TEXT NOT NULL,
    edited_by_user_id  TEXT REFERENCES users(id) ON DELETE SET NULL,
    edit_source        TEXT NOT NULL DEFAULT 'manual' CHECK (edit_source IN (
        'manual',
        'suggestion_accept',
        'merge',
        'import'
    )),
    edited_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(product_id, field)
);

CREATE INDEX idx_product_field_edits_product
    ON product_field_edits(product_id, edited_at);
```

Rules:

- Product edit endpoints mark `name`, `brand`, `upc`, `pack_quantity`, and
  `pack_unit` as `manual`.
- Suggestion acceptance marks the field as `suggestion_accept`.
- Automatic providers do not create a new pending suggestion for a non-empty
  field if the latest field edit is newer than the snapshot evidence, unless the
  user explicitly runs manual refresh.
- Rejected suggestions stay rejected unless a later snapshot has a different
  `content_hash` or source record.

### 5. Store External References

Kroger needs location mapping. Add a generic table rather than Kroger-specific
columns on `stores`.

```sql
CREATE TABLE store_external_refs (
    id            TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    household_id  TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    store_id      TEXT NOT NULL REFERENCES stores(id) ON DELETE CASCADE,
    source        TEXT NOT NULL,
    external_id   TEXT NOT NULL,
    label         TEXT,
    confidence    REAL CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
    metadata_json TEXT,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(store_id, source)
);

CREATE INDEX idx_store_external_refs_household_source
    ON store_external_refs(household_id, source, external_id);
```

For Kroger, `external_id` is `locationId`. `metadata_json` may hold address,
banner, phone, and geocoordinates from the location lookup.

### 6. Migration Order and Down Migrations

Use the next available migration numbers after 039. The exact filenames should
match the repository convention. The `...` markers below mean "use the full
table definitions from the subsections above"; the down migrations are written
out because they were previously missing.

`040_product_external_metadata.up.sql`:

```sql
CREATE TABLE product_external_metadata (...);
CREATE INDEX idx_product_external_metadata_product
    ON product_external_metadata(product_id, source, fetched_at);
CREATE UNIQUE INDEX idx_product_external_metadata_source_record
    ON product_external_metadata(product_id, source, source_record_id)
    WHERE source_record_id IS NOT NULL AND source_record_id != '';
ALTER TABLE product_enrichment_suggestions
    ADD COLUMN external_metadata_id TEXT
        REFERENCES product_external_metadata(id) ON DELETE SET NULL;
CREATE UNIQUE INDEX idx_product_enrichment_suggestions_snapshot_unique
    ON product_enrichment_suggestions(product_id, external_metadata_id, field, value)
    WHERE external_metadata_id IS NOT NULL;
```

`040_product_external_metadata.down.sql`:

```sql
DROP INDEX IF EXISTS idx_product_enrichment_suggestions_snapshot_unique;
ALTER TABLE product_enrichment_suggestions DROP COLUMN external_metadata_id;
DROP INDEX IF EXISTS idx_product_external_metadata_source_record;
DROP INDEX IF EXISTS idx_product_external_metadata_product;
DROP TABLE IF EXISTS product_external_metadata;
```

`041_product_enrichment_jobs_settings_edits.up.sql`:

```sql
CREATE TABLE product_enrichment_jobs (...);
CREATE INDEX idx_product_enrichment_jobs_status
    ON product_enrichment_jobs(status, queued_at);
CREATE INDEX idx_product_enrichment_jobs_product
    ON product_enrichment_jobs(product_id, queued_at);
CREATE UNIQUE INDEX idx_product_enrichment_jobs_active
    ON product_enrichment_jobs(household_id, product_id, trigger, lookup_key)
    WHERE status IN ('queued', 'running');

CREATE TABLE product_enrichment_settings (...);

CREATE TABLE product_field_edits (...);
CREATE INDEX idx_product_field_edits_product
    ON product_field_edits(product_id, edited_at);
```

`041_product_enrichment_jobs_settings_edits.down.sql`:

```sql
DROP INDEX IF EXISTS idx_product_field_edits_product;
DROP TABLE IF EXISTS product_field_edits;
DROP TABLE IF EXISTS product_enrichment_settings;
DROP INDEX IF EXISTS idx_product_enrichment_jobs_active;
DROP INDEX IF EXISTS idx_product_enrichment_jobs_product;
DROP INDEX IF EXISTS idx_product_enrichment_jobs_status;
DROP TABLE IF EXISTS product_enrichment_jobs;
```

`042_store_external_refs.up.sql`:

```sql
CREATE TABLE store_external_refs (...);
CREATE INDEX idx_store_external_refs_household_source
    ON store_external_refs(household_id, source, external_id);
```

`042_store_external_refs.down.sql`:

```sql
DROP INDEX IF EXISTS idx_store_external_refs_household_source;
DROP TABLE IF EXISTS store_external_refs;
```

## Backend Design

### Provider Interface

Create `internal/enrichment/providers`.

```go
type LookupInput struct {
    HouseholdID        string
    ProductID          string
    ProductName        string
    Brand              *string
    UPC                *string
    Identifiers        []identifiers.ProductIdentifier
    StoreID            *string
    StoreName          *string
    StoreNumber        *string
    StoreExternalRefs  map[string]string
    StoreItemCodes     []string
    ReceiptDescriptions []string
}

type Metadata struct {
    Source         string
    SourceRecordID *string
    SourceURL      string
    LookupKey      string
    Confidence     float64
    Payload        MetadataPayload
    Suggestions    []enrichment.Suggestion
}

type Provider interface {
    Name() string
    Enabled(ctx context.Context, householdID string) bool
    Lookup(ctx context.Context, input LookupInput) ([]Metadata, error)
}
```

Adapters:

- `openfoodfacts.Provider`: extract current Open Food Facts logic from
  `internal/api/products_enrichment.go` into a fixture-tested adapter.
- `usda.Provider`: extract current USDA FoodData Central logic from
  `internal/api/products_enrichment.go`; use env `USDA_FDC_API_KEY` as fallback
  after household settings.
- `kroger.Provider`: add OAuth, product lookup, product search, and
  location-aware candidate scoring while reusing the existing Kroger
  visible-text parser in `internal/enrichment/adapters/kroger.go`.
- `url.Provider`: wraps current manual URL fetch and existing Kroger HTML
  parser.

Keep current `enrichment.Suggestion` as the last-mile field suggestion type.
Provider adapters should return snapshots plus suggestions; handlers store the
snapshot first, then suggestions.

HTTP-backed adapters must use `internal/httpsafe` and provider-specific
allowlists/timeouts. LLM-backed enrichment, if added later, must use
`internal/llm.GuardedExtractor`; receipt scanning should remain the only LLM
caller in v1.

### Enrichment Runner

Create `internal/enrichment/runner`.

Responsibilities:

- expose a single queue chokepoint,
  `Service.QueueForProduct(ctx, householdID, productID, trigger, opts)`, and
  migrate the existing manual lookup and receipt-scan call sites onto it before
  adding receipt-review barcode assist;
- own queue-time gate checks, UPC normalization, lookup-key construction,
  active-job dedupe, queue caps, and metrics emission in that helper; adapters
  should not have to know which UI or worker requested the job;
- load product, UPC, store codes, latest receipt descriptions, and store refs;
- load `product_identifiers` through `internal/identifiers` rather than
  inventing a second identifier store;
- load household `product_enrichment_settings` and provider integration
  credentials;
- enforce `PRODUCT_ENRICHMENT_ENABLED`, `manual_lookup_enabled`, automatic
  trigger flags when relevant, per-provider enabled flags, and credential
  availability before calling adapters;
- choose providers based on trigger and available identifiers;
- enforce per-provider rate limits and timeouts;
- store `product_external_metadata`;
- upsert or refresh `product_links` when a canonical source URL exists;
- create field-level suggestions;
- skip suggestions blocked by newer `product_field_edits`;
- update job status and metrics;
- emit WebSocket invalidation events when jobs produce visible changes;
- never overwrite product fields without user acceptance.

Gate precedence:

| Gate | Applies to | Behavior |
| --- | --- | --- |
| `PRODUCT_ENRICHMENT_ENABLED=false` | all provider lookups | Do not enqueue provider work. Explicit receipt-review barcode apply may still store the UPC/match the row and returns `lookup_skipped_reason="env_disabled"`. |
| `manual_lookup_enabled=false` | `manual_lookup`, `manual_refresh`, `receipt_review_scan` | Do not enqueue provider work for explicit lookup actions. Receipt-review barcode apply still stores the identifier and returns `lookup_skipped_reason="household_manual_lookup_disabled"`. |
| `auto_on_scan_enabled=false` | `receipt_scan` | Receipt scan completion does not queue enrichment jobs; eligible products may still be found by a later scheduled sweep if enabled. |
| `PRODUCT_ENRICHMENT_SCHEDULED_SWEEP=false` | `scheduled_refresh`, `batch_backfill` | Sweeper does not run at all, regardless of household settings. |
| `scheduled_sweep_enabled=false` | `scheduled_refresh`, `batch_backfill` | Sweeper skips that household. |

Metric names:

- `cartledger_enrichment_jobs_queued_total{trigger}`;
- `cartledger_enrichment_jobs_finished_total{trigger,status,provider}`;
- `cartledger_enrichment_provider_latency_seconds{provider}`;
- `cartledger_enrichment_provider_requests_total{provider,http_status}`;
- `cartledger_enrichment_queue_depth{status}`.

Provider order:

1. Manual URL provider, if the trigger includes a URL.
2. UPC providers: Open Food Facts, USDA, paid providers if later configured.
3. Kroger, if configured and a Kroger store/location/product hint is present.
4. Low-confidence name search only for manual requests, not scheduled cron.

### API Surface

Add product enrichment endpoints:

- `POST /api/v1/products/:id/enrichment-jobs`
  - body:
    `{ "trigger": "manual_lookup", "sources": ["openfoodfacts","usda","kroger"], "upc": "...", "url": "..." }`
  - creates or reuses a durable job row and returns before provider calls
    finish; handlers do not call providers inline.
  - returns `202` with job response:
    `{ "job": { "id": "...", "status": "queued", "product_id": "...", "trigger": "manual_lookup", "requested_sources": ["openfoodfacts"], "lookup_key": "...", "next_attempt_at": null } }`.
- `GET /api/v1/products/:id/enrichment-jobs`
  - returns recent jobs for status display.
- `POST /api/v1/products/:id/enrichment-suggestions/bulk-accept`
  - body: `{ "suggestion_ids": ["..."], "recompute_prices": false }`
  - accepts each suggestion independently inside one request; non-conflicting
    suggestions are applied, conflicted suggestions are reported and left
    pending.
- `POST /api/v1/products/:id/enrichment-suggestions/bulk-reject`
  - body: `{ "suggestion_ids": ["..."] }`.
- Keep existing `POST /api/v1/products/:id/enrich/upc` as a compatibility
  shortcut that queues the same `manual_lookup` job through the new endpoint
  code path.
- Keep existing `POST /api/v1/products/:id/links` for manual URL fetch, but
  have it also write an external metadata snapshot.

Add product enrichment settings endpoints:

- `GET /api/v1/product-enrichment/settings`
  - returns the household `product_enrichment_settings` row plus effective
    provider availability derived from integrations and env fallback config.
- `PUT /api/v1/product-enrichment/settings`
  - updates manual lookup, automatic lookup, scheduled sweep, per-provider flags,
    and `first_run_backfill_limit`.

Add store mapping endpoints:

- `GET /api/v1/stores/:id/external-refs`
- `PUT /api/v1/stores/:id/external-refs/kroger`
- `GET /api/v1/integrations/kroger/locations?zip=...&q=...`

Add settings integration support:

- extend `integrations.type` constants with `kroger`, `usda_fdc`, and optional
  future paid providers;
- Kroger config: `client_id`, `client_secret`, token metadata;
- USDA config: API key. Keep env `USDA_FDC_API_KEY` as fallback. API responses
  may expose that an operator fallback is configured, but must never reveal the
  env key.

Bulk accept response:

```json
{
  "accepted": [
    { "suggestion_id": "s1", "field": "pack_quantity", "value": "12" }
  ],
  "skipped": [
    { "suggestion_id": "s2", "field": "brand", "reason": "already_current" }
  ],
  "conflicts": [
    {
      "suggestion_id": "s3",
      "field": "upc",
      "code": "identifier_conflict",
      "message": "UPC already belongs to another product",
      "existing_product_id": "p_existing",
      "existing_product_name": "Existing Product",
      "suggested_merge": true
    }
  ]
}
```

Single-suggestion accept may keep returning `409` for conflicts, but should use
the same conflict shape. Bulk accept should return `200` when some suggestions
were accepted and some conflicted, and `409` only when nothing could be applied
because every selected mutation conflicted.

WebSocket contract:

- `ProductHandler` needs a `Hub *ws.Hub` dependency, wired in
  `internal/api/router.go`.
- Suggestion acceptance, bulk acceptance, product update, product merge, and
  package-size recompute should broadcast `ws.EventProductUpdated`.
- Payload: `{ "product_id": "...", "changed_fields": ["pack_quantity"] }`.
- Enrichment worker should broadcast `ws.EventProductEnrichmentJobUpdated`
  (`product.enrichment_job.updated`) when a job reaches a terminal `succeeded`,
  `partial`, `failed`, or `cancelled` state.
- Job event payload:
  `{ "product_id": "...", "job_id": "...", "status": "...", "provider_status": { "openfoodfacts": "succeeded" }, "error": null }`.
- Product Detail and settings batch pages use the `202` response plus short
  polling for queued/running jobs; terminal WebSocket events stop polling and
  invalidate visible data.
- `web/src/api/ws.ts` must invalidate `['products']`,
  `['product-detail', product_id]`, and field-specific queries such as
  `['product-usage', product_id]` when relevant.

### Receipt Scan Flow

Keep the LLM as an extraction source, not a product database.

What the LLM should do during receipt scanning:

- extract UPC/GTIN only when printed;
- extract store item code only when printed;
- extract receipt description with store code removed;
- suggest brand/category/tags from visible text;
- capture explicit package content when visible.

Add optional fields to `llm.ExtractedItem`:

```go
PackageLabel    *string `json:"package_label,omitempty"`
PackageQuantity *string `json:"package_quantity,omitempty"`
PackageUnit     *string `json:"package_unit,omitempty"`
```

Required companion changes:

- Add the same fields to the tolerant item aux struct in
  `internal/llm/types_unmarshal.go`.
- Update `internal/llm/prompt.go` and receipt repair prompt text to ask for
  package content only when printed on the receipt.
- Update `internal/llm/claude.go` `receiptTool.InputSchema.Properties` under
  `items.items.properties`:

```go
"package_label": map[string]any{
    "type": []any{"string", "null"},
    "description": "explicit package content printed on the receipt line, such as 12 OZ, 1GAL, 16 CT, or 2 x 8 ct; null if not printed",
},
"package_quantity": map[string]any{
    "type": []any{"string", "null"},
    "description": "numeric package quantity from explicit package text only; preserve decimal text when present",
},
"package_unit": map[string]any{
    "type": []any{"string", "null"},
    "description": "package unit from explicit package text only, such as oz, fl_oz, lb, g, ml, l, gal, each, or ct",
},
```

- Add `package_label`, `package_quantity`, and `package_unit` to the item
  schema `required` list with nullable types, matching the existing nullable
  `store_item_code`, `receipt_description`, and `upc` pattern.
- Update `internal/llm/testdata/sample-receipt.json`, mock client fixtures, and
  unmarshal tests.

Also add a deterministic parser that scans `raw_name` and
`receipt_description` for explicit package text such as `1GAL`, `16 CT`,
`12 OZ`, `2 x 8 ct`, and `31.7 oz`. If deterministic parser and LLM agree, use
higher confidence. If they disagree, leave as a suggestion.

Worker behavior:

- when deterministic package content is explicit and parseable, write line-item
  package overrides so price normalization works immediately for that receipt;
- when deterministic parser and LLM package fields agree after canonicalization,
  mark the override `pack_override_source='receipt_explicit'` and use higher
  confidence for product suggestions;
- when LLM package fields appear without deterministic evidence, create
  suggestions only and do not write line overrides;
- if the matched/accepted product lacks package size, create pending
  `pack_quantity` and `pack_unit` suggestions from the line evidence;
- if UPC is present on a line and the product lacks UPC, create a pending UPC
  suggestion using `internal/identifiers` conflict checks, or carry it into
  product creation during suggestion acceptance;
- after receipt processing commits, queue enrichment jobs for products with a
  new UPC or missing package size only when household auto-on-scan is enabled,
  capped per receipt.

Do not ask the LLM for nutrition, servings, ingredients, allergens, photos, or
web-search facts during receipt scanning.

### Scheduled Backfill

Start with conservative scheduling.

Add config:

- `PRODUCT_ENRICHMENT_ENABLED=true` by default to preserve existing explicit
  manual URL/UPC lookup behavior;
- `PRODUCT_ENRICHMENT_AUTO_ON_SCAN=false` by default; the household setting
  must also be enabled before scan-triggered jobs are queued;
- `PRODUCT_ENRICHMENT_SCHEDULED_SWEEP=false` by default; the household setting
  must also be enabled before sweeps run;
- `PRODUCT_ENRICHMENT_SWEEP_INTERVAL=24h`;
- `PRODUCT_ENRICHMENT_MAX_JOBS_PER_SWEEP=50`;
- `PRODUCT_ENRICHMENT_MAX_JOBS_PER_RECEIPT=20`;
- `PRODUCT_ENRICHMENT_REFRESH_AFTER_DAYS=90`;
- provider-specific timeout and rate-limit constants in code, not user-tuned
  until there is operational evidence.

Scheduling model:

- use a `time.NewTicker` goroutine owned by the enrichment service and started
  from `cmd/server/serve.go`; no cron dependency or external scheduler is needed
  for this single-process SQLite deployment;
- the ticker must be context-cancellable and stopped in tests/shutdown;
- skip the entire sweeper when the global scheduled-sweep env gate is disabled;
- for each household with `scheduled_sweep_enabled`, query eligible products
  from current product/job/snapshot state rather than maintaining a separate
  worklist table;
- order sweep candidates by `last_purchased_at DESC NULLS LAST`, then purchase
  count/value signals, and limit to `PRODUCT_ENRICHMENT_MAX_JOBS_PER_SWEEP`;
- first-run backfill uses `first_run_backfill_limit` once per household opt-in,
  then normal sweeps return to `PRODUCT_ENRICHMENT_MAX_JOBS_PER_SWEEP`;
- batch reads outside write transactions and insert jobs in small chunks to
  respect SQLite's single-writer model.

First-run backfill:

- do not enqueue every historical product after opt-in;
- show an estimated candidate count and default to the top
  `first_run_backfill_limit` products, ordered by recent purchase date,
  purchase count, missing package size, and available UPC;
- default first-run limit is 200 products per household;
- user-selected bulk lookup can exceed the default limit, but still respects
  provider rate limits and job queue caps;
- nightly sweep continues at `PRODUCT_ENRICHMENT_MAX_JOBS_PER_SWEEP` for the
  remaining eligible recent products.

Sweep candidates:

1. products purchased in the last 180 days;
2. products with UPC but missing package size;
3. products with UPC but no successful Open Food Facts/USDA snapshot;
4. Kroger-linked products missing Kroger source metadata;
5. products with stale snapshots and pending missing fields.

Skip:

- non-products;
- products with no useful identifier unless the user manually requested lookup;
- products with repeated provider failures in the last 7 days;
- products whose missing fields were explicitly rejected recently;
- products whose last successful snapshot for all requested providers is newer
  than `PRODUCT_ENRICHMENT_REFRESH_AFTER_DAYS`, unless the trigger is
  `manual_refresh`.

Retention maintenance:

- implement scheduled pruning with the Phase 3 sweeper, not Phase 2 manual
  lookup. Phase 2 must store enough timestamps/status to make pruning safe.
- prune failed-only snapshots by `product_external_metadata.fetched_at` after
  30 days when `last_error IS NOT NULL`;
- prune unaccepted stale snapshots by `fetched_at` after 180 days when no
  pending or accepted suggestion references the snapshot through
  `product_enrichment_suggestions.external_metadata_id`;
- keep snapshots referenced by accepted suggestions, source links, or current
  product evidence while the product exists;
- delete in bounded transactions, e.g. 1000 snapshot rows per sweep tick, so
  pruning does not block receipt writes.

## UI/UX Plan

### Product Detail

Replace the current scattered enrichment controls with one `Enrichment` section
near Product Info.

Display:

- UPC field and `Lookup` action;
- package size status;
- latest enrichment job status;
- source badges with last fetched date and errors;
- pending suggestions grouped by field category:
  - Identity: name, brand, UPC;
  - Package: package quantity/unit;
  - Nutrition: serving, calories, nutrients;
  - Ingredients/allergens.

Actions:

- `Lookup missing info` - calls
  `POST /api/v1/products/:id/enrichment-jobs` with trigger `manual_lookup` and
  current UPC/name/store context.
- `Refresh sources` - calls the same job endpoint with trigger
  `manual_refresh` to re-fetch existing source links/snapshots.
- `Add URL` - current manual URL fetch.
- `Accept selected` - bulk accept checked suggestions.
- `Dismiss selected`.
- `Recompute prices` remains tied to package size changes.

Important UX details:

- show current value, suggested value, source, evidence, and confidence;
- make package-size suggestions visually prominent because they affect price
  comparison;
- show provider failures inline, but do not block manual editing;
- when accepting package size, offer `Save and recompute price history`;
- when accepting UPC, handle conflicts with a product-merge prompt or a clear
  conflict message.

### Receipt Review

After a scan, users should see the receipt first, not an enrichment dashboard.
Add lightweight signals:

- line row badge: `UPC found`, `package found`, `lookup queued`, `lookup failed`;
- package override indicator when the receipt line itself supplied pack size;
- action on unmatched/high-value items: `Find product info`.

Avoid showing every nutrition suggestion in receipt review. Send the user to
Product Detail for detailed enrichment review.

### Products Page

Add filters:

- `Missing UPC`;
- `Missing package size`;
- `Pending enrichment`;
- `Lookup failed`;
- `Recently enriched`.

Add bulk action:

- `Lookup missing info` for selected products.

### Settings

Add a `Product metadata` area in Integrations.

Sections:

- Open Food Facts: enabled toggle, attribution note, no credentials.
- USDA FoodData Central: API key, test button, and read-only
  `USDA_FDC_API_KEY` fallback status shown as configured by operator when
  present. Never reveal the env key; a household key overrides it.
- Kroger: OAuth credentials, test button, store mapping helper.
- Paid providers: hidden/deferred or disabled "coming later", not a prominent
  promise.

Store mapping helper for Kroger:

- list CartLedger stores whose names look like Kroger banners;
- search Kroger locations by zip/store number;
- let user attach `locationId`;
- show mapped location label and confidence.

Privacy copy:

- automatic lookup sends UPCs, product names, and possibly store context to
  providers;
- automatic enrichment is opt-in;
- snapshots can be deleted/refetched;
- accepted user edits remain household data.

Workflow edge cases:

- Provider conflicts: group competing suggestions by field and show source,
  evidence, and current value. Do not auto-rank into a hidden winner; default
  selected value should be the current value when user-edited.
- Rate limits mid-batch: pause the provider, set affected jobs to queued with
  `next_attempt_at`, and show a "paused until" provider status. Do not mark the
  whole batch failed.
- Photo URL 404s: store image URLs as evidence only. Lazy-check failures in the
  UI should mark the image URL unavailable on the snapshot without deleting the
  snapshot or failing the product lookup.
- Granular provider opt-in: users can enable manual Open Food Facts lookup
  while leaving automatic Open Food Facts, USDA, and Kroger disabled.
- Undo accept: v1 should offer "Restore previous value" immediately after
  accepting name/brand/package fields by using the old value returned in the
  accept response. Full multi-step history can remain deferred.
- Post-merge metadata: when products merge, move snapshots, links, identifiers,
  and pending suggestions to the survivor when they do not conflict; leave
  duplicate suggestions rejected with evidence text instead of adding new status
  values in v1.
- Running-state recovery: on boot, stale `running` enrichment jobs become
  `queued` with an incremented attempt count, matching the receipt worker's
  recovery style.
- Integration test buttons: tests validate credentials and permissions without
  writing snapshots or suggestions. A successful test may update only the
  integration row's token/status metadata.

## Implementation Phases

### Phase 1: Metadata Backbone and Receipt Package Extraction

Backend:

- add `product_external_metadata` migration and
  `product_enrichment_suggestions.external_metadata_id`;
- add `product_enrichment_jobs`, `product_enrichment_settings`, and
  `product_field_edits` migrations;
- include down migrations for each new migration file;
- add metadata payload Go structs;
- add package-content parser for receipt line text;
- add LLM schema fields for explicit package content in `types.go`,
  `types_unmarshal.go`, `claude.go`, prompts, fixtures, and tests;
- populate line item package overrides when evidence is explicit;
- create package-size suggestions from line evidence;
- update suggestion acceptance to record field edits and broadcast
  `product.updated`;
- add or update bulk accept/reject endpoints so Product Detail bulk UI can ship
  in the same phase; bulk accept records field edits for every applied
  suggestion and returns accepted/skipped/conflict detail;
- add tests for parser, worker persistence, and price normalization from line
  overrides.

Frontend:

- add Product Detail grouped suggestions and bulk accept/reject;
- update WebSocket handling to invalidate product detail on `product.updated`;
- improve UPC lookup success/error feedback;
- show source/snapshot status.

Why first:

- package size is the most useful short-term enrichment;
- it improves price comparison even before external providers are configured;
- it uses data already visible on receipts.

### Phase 2: Open Food Facts and USDA Provider Chain

Backend:

- move current Open Food Facts/USDA logic from
  `internal/api/products_enrichment.go` into provider adapters;
- store metadata snapshots before creating suggestions;
- update the existing manual URL/link enrichment path to write snapshots and use
  the snapshot-aware suggestion store;
- add runner, worker wiring in `cmd/server/serve.go`, receipt-worker-style
  queue depth/shutdown/stale-running recovery, and a manual job endpoint that
  queues durable jobs and returns `202`;
- keep existing UPC endpoint as a wrapper around the same queueing path;
- enforce `PRODUCT_ENRICHMENT_ENABLED`, `manual_lookup_enabled`, per-provider
  enabled flags, and credential availability before adapter calls;
- enforce `product_field_edits` suppression for provider-created suggestions,
  except when the user explicitly runs `manual_refresh`;
- add shared per-host provider rate limiting and provider-specific tests using
  fixtures;
- add job status list endpoint and terminal job WebSocket event;
- harden the existing product enrichment settings API for household
  provider/manual/automatic toggles and provider availability messaging;
- add USDA integration row support while preserving `USDA_FDC_API_KEY`.

Frontend:

- evolve settings provider toggles for Open Food Facts and USDA plus USDA API
  key, including the operator-configured env fallback display for USDA;
- `Lookup missing info` button on Product Detail routed to
  `POST /api/v1/products/:id/enrichment-jobs`;
- job status and provider error display.

Acceptance:

- user enters UPC and clicks lookup;
- lookup endpoint returns `202` with a durable job response;
- the Phase 2 worker processes the queued manual job without waiting for Phase 3;
- OFF/USDA snapshots are stored;
- suggestions appear without overwriting product fields;
- disabled providers are skipped with visible status instead of being called;
- terminal job events are emitted for succeeded/partial/failed/cancelled jobs;
- accepting package size can recompute prices;
- failing providers leave visible errors and do not remove old accepted data.

### Phase 3: Automatic Enrichment on Scan and Nightly Sweep

Backend:

- reuse the Phase 2 enrichment worker for automatic scan and sweep jobs, but
  first introduce `Service.QueueForProduct` so manual lookup, receipt-scan
  auto-enqueue, scheduled sweeps, and Phase 3.5 barcode apply all share the same
  gate, dedupe, lookup-key, cap, and metrics logic;
- keep the existing receipt worker's post-commit `queueReceiptScanEnrichment`
  shape: it remains best-effort, runs after the receipt transaction commits, and
  logs then moves on if queueing fails because the sweeper can catch eligible
  products later;
- cap receipt-scan auto jobs per receipt with
  `PRODUCT_ENRICHMENT_MAX_JOBS_PER_RECEIPT`, default 20, prioritizing highest
  `total_price` products first;
- only queue auto-on-scan products with a normalized UPC/GTIN from `products.upc`
  or `line_items.upc`; products without a UPC remain manual/sweep candidates only
  when a later provider supports non-UPC lookup;
- apply queued/running recovery on startup by resetting stale `running` jobs back
  to `queued` with incremented attempts until the max-attempt policy is reached;
- add a context-cancellable `time.NewTicker` scheduler in the enrichment service
  for scheduled sweeps and snapshot pruning; no new cron dependency is needed;
- enforce gate precedence from the Enrichment Runner section at queue time and
  again before adapter execution so disabled jobs do not surprise users after
  sitting in the queue;
- add sweeper candidate selection from current product state: missing package
  metadata, UPC present, no recent successful snapshot, or failed job whose
  `next_attempt_at` has elapsed; order by recent purchase signals and cap by
  `PRODUCT_ENRICHMENT_MAX_JOBS_PER_SWEEP`;
- add scheduled snapshot pruning using `fetched_at` semantics: 30-day failed-only
  rows, 180-day stale unaccepted rows, and never delete snapshots referenced by
  accepted suggestions/current evidence;
- expose the explicit Prometheus metrics listed in Enrichment Runner;
- include `receipt_id` in `product.enrichment_job.updated` payloads for
  `receipt_scan` and `receipt_review_scan` jobs so Receipt Review can invalidate
  the visible receipt without a full product-list refresh.

Frontend:

- product list filters for `missing_metadata` and `failed_lookups`; extend
  `web/src/api/products.ts:listProducts` and `internal/api/products.go` rather
  than filtering all products client-side. Define `missing_metadata` as missing
  brand, missing package size, or absent accepted nutrition/source metadata;
- receipt review row badges using one badge slot beside the product/suggestion:
  `queued`/`running` -> `Looking up...`, `succeeded` with suggestions ->
  `Package data found`, `succeeded` without useful data -> `No package data`,
  terminal `failed` -> `Lookup failed`, and hide `cancelled`/uninformative
  `partial` states;
- extend `GET /api/v1/receipts/:id` to include per-line latest enrichment job
  status and suggestion count so badges render without a Product Detail fetch;
- update `web/src/api/ws.ts` handling for `product.enrichment_job.updated` to
  invalidate `['receipt', receipt_id]` when `receipt_id` is present, while
  preserving existing product invalidation;
- settings use the existing Product Enrichment settings API/UI surface in
  `IntegrationsTab`; label the receipt toggle as
  `Automatic lookup after receipt scan` and show the global env-disabled banner.

Acceptance:

- scanning a receipt with UPCs queues jobs after the receipt completes when the
  household has automatic lookup enabled;
- receipt auto-enqueue is capped per receipt and idempotent against
  `(household_id, product_id, trigger, lookup_key)` for active jobs;
- scheduled sweeps are capped per household sweep, respect first-run backfill
  limits, and skip recent failures until `next_attempt_at`;
- app restart recovers queued jobs and reconciles stale running jobs;
- users can disable all automatic external lookups from Settings and the next
  receipt scan queues zero auto-on-scan jobs;
- receipt review badges update after terminal job WebSocket events without a
  page refresh;
- metrics for queued/finished jobs, provider requests, provider latency, and
  queue depth are visible on `/metrics`;
- snapshot pruning preserves snapshots referenced by accepted suggestions and
  deletes only eligible failed-only or stale unaccepted snapshots.

### Phase 3.5: Receipt Review Barcode Assist

This is a narrow scanner assist, not a standalone grocery scanner or inventory
workflow. Its primary use is receipt review when the user still has a newly
purchased packaged item in hand and the receipt row is unmatched or would create
a new product. Do not make receipt review the place to correct UPCs on already
matched household products; keep that cleanup on Product Detail, where the user
has source links, existing suggestions, merge context, and field history. The
same scanner component may add a Product Detail UPC-field camera shortcut so
existing-product corrections are still faster than typing.

Why it belongs after Phase 3:

- scanning a package barcode is faster and less error-prone than typing a UPC,
  but only once the durable enrichment worker, provider settings, UPC conflict
  handling, and receipt-review badges already exist;
- the flow should reuse the same manual lookup gates as Product Detail, because
  the user explicitly initiated the scan and automatic lookup opt-in should not
  be required;
- the feature improves the "new product on this receipt" loop without turning
  CartLedger into a pantry/inventory scanner.

Backend:

- add a follow-up migration before implementation that extends CHECK
  constraints for `product_enrichment_jobs.trigger`,
  `product_identifiers.source`, `line_item_identifier_observations.source`, and
  `product_aliases.source` to allow `receipt_review_scan`; SQLite table
  recreation is expected for the job/source CHECK changes;
- add `TriggerReceiptReviewScan = "receipt_review_scan"` and treat it as a
  manual trigger for provider-gate purposes: the global env kill switch still
  blocks lookup, and `manual_lookup_enabled=false` stores the UPC/match but
  skips provider work;
- add dedicated receipt-review barcode endpoints instead of stretching generic
  line-item update semantics:
  - `POST /api/v1/receipts/:id/line-items/:itemId/barcode/preview`
    with `{ "upc": "..." }`;
  - `POST /api/v1/receipts/:id/line-items/:itemId/barcode`
    with `{ "upc": "...", "create_product": false }`;
- normalize UPC/GTIN through `internal/identifiers` / `internal/upc` and reject
  invalid codes before any provider call;
- limit persisted-line eligibility to editable receipts and line items that are
  not reviewed, not actively processing, and either unmatched/no `product_id` or
  suggested as `new_product`; pre-commit Add Row/manual grid scanning uses the
  modal's `fill` mode and the existing create-line-item `upc` field instead of
  this apply endpoint;
- on preview, return local household matches from `product_identifiers` and
  `products.upc`, provider/manual-lookup availability, and any conflict details;
- reuse the existing UPC conflict response shape from product enrichment
  (`existing_product_id`, `existing_product_name`, `suggested_merge`) so the
  frontend has one conflict contract;
- on apply with a local match, update the line item directly to
  `product_id=<matched product>`, `matched='identifier'`, `confidence=1.0`, and
  store the line UPC/identifier observation; do not overwrite product fields;
- upsert the receipt text alias using a new `receipt_review_scan` alias source
  and upsert the product identifier using `receipt_review_scan` with
  `SetPrimaryProduct=true`;
- on apply with `create_product=true`, create a product from the receipt text
  plus scanned UPC, attach the primary GTIN using the existing conflict checks,
  match the line item, and queue a Phase 2 enrichment job with trigger
  `receipt_review_scan`;
- product creation, identifier attach, line-item update, alias upsert, and job
  queue insert must commit atomically or roll back together, except provider
  queue skip responses caused by disabled gates;
- if providers are disabled or unavailable, still store the scanned identifier
  and return `lookup_skipped_reason` with one of `env_disabled`,
  `household_manual_lookup_disabled`, or `no_provider_configured`;
- make apply idempotent by DB state: a second identical apply for a line already
  matched to the same product returns the same success shape and does not create
  another product or job;
- for Product Detail correction, reuse the existing product update and
  enrichment-job endpoints; do not create a second product-UPC write path;
- do not auto-merge products, auto-accept provider suggestions, or update
  already matched existing products from receipt review.

Frontend:

- create a reusable `BarcodeScannerModal` that owns camera lifecycle, manual
  fallback, duplicate debounce, and stream cleanup;
- add a row-level scan affordance only for unmatched/new-product rows, plus an
  optional toolbar action `Scan next new item` when any eligible rows remain;
- open a `BarcodeScannerModal` that is locked to one receipt line and shows the
  receipt text, price, and current suggestion context before the camera starts;
- add a camera icon beside the Product Detail UPC input; on scan success, fill
  the UPC field and use the existing save/lookup controls rather than adding a
  separate correction flow;
- add UPC entry/scan fill support to `ManualLineItemGrid` and Add Row so new
  rows can carry a UPC through existing create-line-item APIs before they have a
  line-item ID;
- support two modal modes: `apply-to-line` for Receipt Review and `fill` for
  Product Detail / simple UPC fields;
- implement a live camera scanner with a custom CartLedger modal using
  `@zxing/browser` or a similarly small wrapper; do not depend solely on the
  native `BarcodeDetector` API because it is still limited/experimental across
  major browsers;
- restrict camera decoding to grocery 1D formats (`UPC_A`, `UPC_E`, `EAN_13`,
  `EAN_8`, `ITF`, `CODE_128`) to reduce false positives and CPU work;
- optionally feature-detect native `BarcodeDetector` by supported formats as a
  fast path, but keep ZXing as the default/fallback implementation;
- lazy-load the scanner code so desktop receipt review does not pay the camera
  bundle cost until the user opens the modal;
- include manual UPC entry in the same modal for denied camera permission,
  unsupported browsers, damaged labels, and physical USB scanners that type into
  the focused field; use a text input, not `type=number`, so leading zeroes are
  preserved;
- after a successful scan, stop the camera stream immediately unless the user
  enables `Keep scanner open`; the default should be scan, confirm, then
  auto-advance to the next eligible row;
- cleanup must call `controls.stop()`, stop every track on `video.srcObject`,
  clear `video.srcObject`, and run on modal unmount and `visibilitychange` hidden;
- auto-advance only focus/cursor after a successful apply; do not auto-open the
  scanner or auto-commit the next row;
- prefer the rear/environment camera, remember the last selected camera when the
  browser exposes stable device IDs, and keep camera selection secondary to the
  main scan/confirm task;
- show an HTTPS/self-hosting warning when `getUserMedia` is unavailable because
  the app is served over non-localhost HTTP;
- result states:
  - existing household product found: show product name and `Match row`;
  - no local product: show `Create product from receipt row` and queue lookup;
  - lookup queued/running: show the same receipt row badge language as Phase 3;
  - invalid/duplicate/conflicting UPC: show a blocking inline error with a
    Product Detail or merge path when relevant;
  - camera blocked/unsupported: keep manual entry available without dead-ending
    the user;
- basic keyboard-wedge scanners are supported through the focused manual UPC
  input and Enter-to-submit behavior; a global hidden-input/HID scanner mode is
  deferred;
- keep detailed nutrition, ingredient, allergen, and source-snapshot review on
  Product Detail. Receipt review should show only enough metadata to confirm that
  the row is matched correctly.

Acceptance:

- reviewing a receipt on a phone, the user can tap scan on an unmatched packaged
  item, scan the barcode, and match the row to an existing household product when
  the UPC is already known on iOS Safari 16+ and Android Chrome 110+;
- scanning a UPC for a new product can create and match a product from the
  receipt row, store the UPC, and queue provider lookup without typing the UPC;
- manual UPC entry hits the same preview and apply endpoints as camera scanning;
- camera permission denial, unsupported camera APIs, and failed decodes do not
  block manual review;
- invalid GTIN/check-digit input shows an inline error and does not call apply;
- repeated scans of the same barcode do not create duplicate products or jobs;
  the modal debounces duplicate decodes in-session and the backend enforces
  idempotency through DB state plus active-job uniqueness;
- existing matched products are not silently corrected from receipt review; if a
  scanned UPC resolves to a different existing product, the modal blocks with
  `Switch match` or `Cancel`;
- when `PRODUCT_ENRICHMENT_ENABLED=false`, apply can still create/match/store the
  UPC but returns `lookup_skipped_reason="env_disabled"` and inserts no job row;
- on Product Detail, scanning fills the UPC input and follows the existing
  product save/conflict/lookup behavior;
- the scanner chunk is lazy-loaded and Receipt Review's initial desktop bundle
  does not include ZXing when the scanner is never opened;
- the camera stream is stopped when the modal closes, the route changes, the tab
  hides, or the scan is applied.

References:

- MyFitnessPal opens barcode scan from the food logging flow, then asks the user
  to confirm the scanned item before adding it.
- Open Food Facts and Yuka use barcode scanning as the product lookup front
  door, but their broad health-analysis model is intentionally larger than this
  CartLedger receipt-review assist.
- Grocy and Barcode Buddy show the useful self-hosted pattern: barcode-ready
  forms, field-adjacent camera buttons, optional browser/device-camera scanning,
  physical scanner/manual entry fallback, and Open Food Facts lookup for unknown
  products.
- Technical references: [`@zxing/browser`](https://github.com/zxing-js/browser),
  [`html5-qrcode`](https://github.com/mebjas/html5-qrcode),
  [`zxing-wasm`](https://github.com/Sec-ant/zxing-wasm) as a future migration
  target if `@zxing/browser` lifecycle issues become blocking,
  [Grocy camera scanner component](https://github.com/grocy/grocy/blob/master/public/viewjs/components/camerabarcodescanner.js),
  [MDN `BarcodeDetector`](https://developer.mozilla.org/en-US/docs/Web/API/BarcodeDetector/detect),
  [MDN `getUserMedia`](https://developer.mozilla.org/en-US/docs/Web/API/MediaDevices/getUserMedia),
  [Grocy](https://grocy.info/), and
  [Barcode Buddy](https://barcodebuddy-documentation.readthedocs.io/en/latest/).

### Phase 4: Kroger Integration

Backend:

- add Kroger integration config and OAuth token handling;
- add `store_external_refs`;
- add location search/mapping endpoints;
- extend existing `internal/enrichment/adapters/kroger.go` fixtures/parser for
  URL evidence and add Kroger product lookup/search adapter logic around it;
- score candidates using UPC, product name, brand, package size, location, and
  receipt price when available.

Frontend:

- Kroger credential card in settings;
- store mapping UI;
- Product Detail source card for Kroger metadata;
- manual `Fetch from Kroger` action when a mapped Kroger store exists.

Acceptance:

- user maps a Kroger store to a Kroger location;
- Product Detail can fetch Kroger metadata for a product with UPC or receipt
  description;
- Kroger price/aisle/image data appears as source metadata, not canonical facts;
- package/name/brand suggestions behave like other suggestions.

### Phase 5: Evaluation of Paid Providers

Do not implement until after Phases 1-4 have been used with real receipts.

Evaluation criteria:

- improves package-size hit rate by at least 15 percentage points over
  OFF/USDA/Kroger for frequently purchased products;
- has caching/storage terms compatible with self-hosted local data;
- has clear pricing for household-scale usage;
- adapter can fit the same metadata contract;
- provider can be disabled and data deleted cleanly.

Likely first paid provider to test: Chomp or FatSecret, depending on licensing
fit. Nutritionix is attractive for data quality but needs extra caution around
caching and active-user pricing.

## Deferred Work

Deferred because value is uncertain or maintenance cost is high:

- scheduled web scraping of retailer product pages;
- automatic download/storage of product photos;
- standalone pantry/inventory barcode scanning, bulk shelf scanning, and
  barcode-first product correction flows outside Product Detail;
- global USB/HID scanner mode with hidden always-focused input, prefix/suffix
  detection, and app-wide scan routing;
- full PLU database;
- GS1/1WorldSync commercial catalog integration;
- paid provider implementation;
- natural-language nutrition search without UPC;
- AI web search during receipt processing;
- source-based automatic product overwrites without review;
- global product identity expansion beyond the shipped household-scoped
  `product_identifiers` model.

## Risks

- Licensing and attribution: Open Food Facts uses open-data terms that require
  attribution and care when reusing data. Keep source provenance and UI
  attribution.
- Provider terms and caching: commercial APIs can restrict storage. Do not add
  paid providers without a terms review.
- Data quality: crowdsourced nutrition and retailer pages can be wrong. Use
  confidence and review.
- UPC conflicts: accepting a UPC can collide with an existing product. Surface
  conflict/merge instead of silently reassigning.
- Kroger location ambiguity: store number on a receipt may not be enough to map
  `locationId`. Require user confirmation.
- Rate limits: scheduled jobs must be capped and back off per provider.
- Privacy: automatic lookups disclose household purchase identifiers to third
  parties. Make automatic lookup opt-in.
- Camera privacy: receipt-review barcode assist must request camera access only
  after an explicit user action, stop streams on modal close/unmount, and keep
  manual UPC entry available for users who decline permission.
- Scanner ambiguity: camera scanning can decode the wrong barcode on crowded
  packaging. Always show the scanned UPC and product candidate before applying.
- Old price history: accepting package size changes can rewrite normalized
  analytics. Keep the current recompute confirmation.
- Worker complexity: do not mix enrichment calls inside the receipt LLM worker's
  critical path. Queue after receipt commit.
- Schema drift: provider payloads change. Adapter tests must use fixtures and
  the stored payload must remain allowlisted.
- Manual edit churn: without `product_field_edits`, providers can keep
  resurfacing values the user already fixed. Implement edit tracking before
  scheduled lookup.

## Resolved Questions and Recommendations

- Open Food Facts default: enable manual lookup by default because it requires a
  click. Keep automatic Open Food Facts lookup disabled until the household opts
  in.
- USDA API key: support both household integration and `USDA_FDC_API_KEY`.
  Household credentials win. The env key remains a server-wide fallback and
  should be shown in settings as configured by operator, not revealed.
- Package override threshold: write line item overrides only when a
  deterministic parser finds explicit package text. If the LLM agrees, raise
  confidence. If only the LLM sees package content, create suggestions only.
- UPC accept follow-up: accepting a UPC should queue provider lookup only when
  the household has automatic lookup enabled or the user clicked a lookup action
  in the same flow. Otherwise show a `Lookup missing info` action.
- Manual lookup routing: new UI calls
  `POST /api/v1/products/:id/enrichment-jobs`. The legacy
  `POST /api/v1/products/:id/enrich/upc` endpoint remains a compatibility
  wrapper around the same queueing path.
- Bulk suggestion endpoints: Phase 1 owns the backend and frontend together
  because grouped Product Detail review depends on bulk accept/reject. Existing
  bulk handlers should be updated rather than replaced where present.
- Job status transport: use WebSocket events for invalidation and terminal
  state; use short polling while a Product Detail or settings batch page is
  visibly showing active jobs.
- Snapshot retention: keep snapshots tied to accepted suggestions, source
  links, or current product evidence while the product exists. Prune failed-only
  snapshots after 30 days and unaccepted stale snapshots after 180 days unless a
  pending suggestion still references them.
- Backups/exports: include `product_external_metadata`,
  `product_enrichment_suggestions.external_metadata_id`, `store_external_refs`,
  and provider attribution in backups. User-facing exports should include
  accepted product/nutrition values plus source attribution, not raw provider
  payloads beyond the allowlisted metadata contract.
- Provider conflict UI: show conflicts as review choices grouped by field.
  Never auto-merge UPC conflicts; route to the existing ProductMerge flow.
- Rate limits mid-batch: pause only the affected provider and reschedule jobs
  with `next_attempt_at`. Continue other providers when configured.
- Photo URLs: treat image URLs as evidence. Failed image loads mark that image
  unavailable but do not fail the product metadata snapshot.
- Granular opt-in: expose provider toggles separately from automatic lookup
  toggles.
- Undo accept: v1 supports immediate restore using previous value returned by
  accept responses. Full audit/history UI remains deferred.
- Post-merge metadata: move non-conflicting links, identifiers, snapshots, and
  pending suggestions to the survivor. Conflicting identifiers stay blocked and
  are shown in the merge result.
- Running recovery: reset stale `running` jobs on boot, increment attempts, and
  cap retries with backoff.
- Integration test semantics: test credentials and permissions only; do not
  create product snapshots or suggestions from a test button.

## Test Plan

Backend tests:

- migrations 040/041/042 apply and down-migrate cleanly;
- package parser for common receipt patterns;
- LLM schema, prompt fixtures, and tolerant unmarshal include package fields;
- worker stores package overrides and records normalized price from overrides;
- Open Food Facts adapter maps fixtures to metadata and suggestions;
- USDA adapter filters non-exact UPC matches;
- runner honors global, manual, and per-provider settings before adapter calls;
- `Service.QueueForProduct` centralizes gate checks, lookup-key construction,
  active-job dedupe, queue caps, and metric emission for manual lookup,
  receipt-scan enqueue, scheduled sweep, and receipt-review barcode apply;
- provider rate limiter enforces Open Food Facts and USDA budgets and
  reschedules on provider throttle responses;
- Kroger adapter preserves existing visible-text fixture behavior and adds
  candidate scoring fixtures;
- job idempotency and retry behavior;
- stale running jobs recover on boot;
- terminal enrichment job WebSocket event is emitted on
  succeeded/partial/failed/cancelled;
- suggestion accept conflicts for UPC uniqueness;
- bulk accept returns accepted/skipped/conflicts and does not silently merge UPCs;
- suggestion acceptance records `product_field_edits`;
- runner suppresses suggestions blocked by newer `product_field_edits`;
- suggestion acceptance broadcasts `product.updated`;
- metadata payload allowlist rejects raw provider blobs;
- safe HTTP client remains used for URL/manual fetch paths;
- scheduled sweep caps jobs, orders by recent purchase signals, and skips recent
  failures until `next_attempt_at`;
- receipt auto-enrichment caps jobs per receipt and prioritizes high-value lines;
- snapshot prune keeps referenced snapshots and deletes only eligible failed-only
  or stale unaccepted snapshots.
- metrics expose enrichment queued/finished counters, provider request/latency
  series, and queue depth on `/metrics`;
- Phase 3.5 migration expands `receipt_review_scan` trigger/source CHECK values
  and down-migrates cleanly;
- barcode preview returns local match/conflict/provider-availability state
  without writes;
- barcode apply with existing product updates the line to `matched='identifier'`,
  stores observation/product identifier, and upserts alias with confidence 1.0;
- barcode apply with `create_product=true` commits product creation, UPC attach,
  line match, alias, and job enqueue atomically;
- barcode apply with disabled provider gates stores the identifier/match and
  returns `lookup_skipped_reason` without inserting a job.

Frontend tests:

- Product Detail renders grouped suggestions and bulk accept/reject;
- lookup button shows queued/running/error/success states;
- WebSocket `product.updated` invalidates product detail and product list
  caches;
- accepting package-size suggestion offers recompute;
- settings cards store/test provider config;
- USDA settings show env fallback configured status without revealing the key;
- settings expose granular provider and automatic lookup toggles;
- product filters for missing metadata and failed lookups;
- receipt review enrichment badges map queued/running/succeeded/failed states and
  update from `product.enrichment_job.updated` without a page refresh;
- BarcodeScannerModal supports camera denied/unsupported states, manual UPC
  entry, duplicate scan debounce, invalid GTIN error, and Product Detail `fill`
  mode;
- ManualLineItemGrid/Add Row can fill and submit UPC values without stripping
  leading zeroes;
- scanner cleanup stops ZXing controls and all media tracks on close, tab hide,
  and route unmount.

Manual smoke:

- product with UPC and no package size;
- product with package text only on receipt;
- Kroger product with mapped store;
- product with conflicting UPC;
- provider conflict between Open Food Facts and USDA values;
- photo URL returns 404;
- provider timeout/failure;
- automatic enrichment disabled;
- first-run backfill opt-in with more candidates than the default limit;
- receipt review barcode scan for an existing product, a new product, a
  conflicting product, and a disabled-provider lookup.
