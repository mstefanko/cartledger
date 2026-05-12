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
  acceptance/rejection, and accepted nutrition persistence; supporting tests
  live in `internal/api/products_enrichment_test.go`.
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
- Rate-limit adapter calls. Open Food Facts currently documents product-query
  limits around 15 requests per minute and asks clients not to crawl the site.

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
        'batch_backfill'
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
    ON product_enrichment_jobs(product_id, trigger, lookup_key)
    WHERE status IN ('queued', 'running');
```

Job idempotency:

- before queueing a job, check for queued/running jobs for the same product and
  lookup key;
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
    ON product_enrichment_jobs(product_id, trigger, lookup_key)
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

- load product, UPC, store codes, latest receipt descriptions, and store refs;
- load `product_identifiers` through `internal/identifiers` rather than
  inventing a second identifier store;
- choose providers based on trigger and available identifiers;
- enforce per-provider rate limits and timeouts;
- store `product_external_metadata`;
- upsert or refresh `product_links` when a canonical source URL exists;
- create field-level suggestions;
- skip suggestions blocked by newer `product_field_edits`;
- update job status and metrics;
- emit WebSocket invalidation events when jobs produce visible changes;
- never overwrite product fields without user acceptance.

Provider order:

1. Manual URL provider, if the trigger includes a URL.
2. UPC providers: Open Food Facts, USDA, paid providers if later configured.
3. Kroger, if configured and a Kroger store/location/product hint is present.
4. Low-confidence name search only for manual requests, not scheduled cron.

### API Surface

Add product enrichment endpoints:

- `POST /api/v1/products/:id/enrichment-jobs`
  - body: `{ "trigger": "manual_lookup", "sources": ["openfoodfacts","usda","kroger"], "upc": "...", "url": "..." }`
  - returns `202` with job response.
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
  shortcut that queues or runs a `manual_lookup` job.
- Keep existing `POST /api/v1/products/:id/links` for manual URL fetch, but
  have it also write an external metadata snapshot.

Add store mapping endpoints:

- `GET /api/v1/stores/:id/external-refs`
- `PUT /api/v1/stores/:id/external-refs/kroger`
- `GET /api/v1/integrations/kroger/locations?zip=...&q=...`

Add settings integration support:

- extend `integrations.type` constants with `kroger`, `usda_fdc`, and optional
  future paid providers;
- Kroger config: `client_id`, `client_secret`, token metadata;
- USDA config: API key. Keep env `USDA_FDC_API_KEY` as fallback.

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
- `PRODUCT_ENRICHMENT_REFRESH_AFTER_DAYS=90`;
- provider-specific timeout and rate-limit constants in code, not user-tuned
  until there is operational evidence.

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

- `Lookup missing info` - queues provider chain using current UPC/name/store
  context.
- `Refresh sources` - re-fetches existing source links/snapshots.
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
- USDA FoodData Central: API key, test button, optional global env fallback
  status.
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
- add runner and manual job endpoint;
- keep existing UPC endpoint as a wrapper;
- add provider rate limiting and provider-specific tests using fixtures;
- add USDA integration row support while preserving `USDA_FDC_API_KEY`.

Frontend:

- settings toggles/API key for Open Food Facts and USDA;
- `Lookup missing info` button on Product Detail;
- job status and provider error display.

Acceptance:

- user enters UPC and clicks lookup;
- OFF/USDA snapshots are stored;
- suggestions appear without overwriting product fields;
- accepting package size can recompute prices;
- failing providers leave visible errors and do not remove old accepted data.

### Phase 3: Automatic Enrichment on Scan and Nightly Sweep

Backend:

- start enrichment worker in `cmd/server/serve.go`;
- mirror the receipt worker's queue depth, shutdown, and stale-running recovery
  patterns;
- queue limited jobs after receipt scan commit;
- add scheduler/sweeper with config gates;
- add metrics for jobs queued, succeeded, failed, provider latency, and
  provider status.

Frontend:

- product list filters for missing metadata and failed lookups;
- receipt review badges for queued/found package/UPC;
- settings toggle for automatic lookup.

Acceptance:

- scanning a receipt with UPCs queues jobs after the receipt completes when the
  household has automatic lookup enabled;
- jobs are capped and idempotent;
- app restart leaves queued jobs recoverable;
- users can disable all automatic external lookups.

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
- browser barcode scanner;
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
- Kroger adapter preserves existing visible-text fixture behavior and adds
  candidate scoring fixtures;
- job idempotency and retry behavior;
- stale running jobs recover on boot;
- suggestion accept conflicts for UPC uniqueness;
- bulk accept returns accepted/skipped/conflicts and does not silently merge UPCs;
- suggestion acceptance records `product_field_edits`;
- suggestion acceptance broadcasts `product.updated`;
- metadata payload allowlist rejects raw provider blobs;
- safe HTTP client remains used for URL/manual fetch paths;
- scheduled sweep caps jobs and skips recent failures.

Frontend tests:

- Product Detail renders grouped suggestions and bulk accept/reject;
- lookup button shows queued/running/error/success states;
- WebSocket `product.updated` invalidates product detail and product list
  caches;
- accepting package-size suggestion offers recompute;
- settings cards store/test provider config;
- settings expose granular provider and automatic lookup toggles;
- product filters for missing metadata and failed lookups.

Manual smoke:

- product with UPC and no package size;
- product with package text only on receipt;
- Kroger product with mapped store;
- product with conflicting UPC;
- provider conflict between Open Food Facts and USDA values;
- photo URL returns 404;
- provider timeout/failure;
- automatic enrichment disabled;
- first-run backfill opt-in with more candidates than the default limit.
