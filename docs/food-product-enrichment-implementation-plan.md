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

## Current CartLedger Stack

Existing pieces to keep and extend:

- `products.upc` and `line_items.upc` already exist, with a household-level
  unique index on product UPC in
  `internal/db/migrations/032_upc_fields.up.sql`.
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

Defer a full PLU database until product identifiers are expanded beyond the
current `products.upc` field. Short term, add lightweight PLU detection only:
normalize 4-5 digit produce codes from receipt text and keep them as line
observations or low-confidence suggestions. Do not scrape the IFPS website.

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
    lookup_key          TEXT,
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
```

Job idempotency:

- before queueing a job, check for queued/running jobs for the same product and
  lookup key;
- provider adapters upsert snapshots by source and source record;
- suggestions upsert through the existing unique constraint;
- failed provider calls update snapshot/job metadata without deleting old good
  snapshots.

### 3. Store External References

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
```

For Kroger, `external_id` is `locationId`. `metadata_json` may hold address,
banner, phone, and geocoordinates from the location lookup.

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

- `openfoodfacts.Provider`: direct UPC lookup.
- `usda.Provider`: direct UPC lookup when API key exists.
- `kroger.Provider`: OAuth, product lookup, product search, and location-aware
  candidate scoring.
- `url.Provider`: wraps current manual URL fetch and Kroger HTML parser.

Keep current `enrichment.Suggestion` as the last-mile field suggestion type.
Provider adapters should return snapshots plus suggestions; handlers store the
snapshot first, then suggestions.

### Enrichment Runner

Create `internal/enrichment/runner`.

Responsibilities:

- load product, UPC, store codes, latest receipt descriptions, and store refs;
- choose providers based on trigger and available identifiers;
- enforce per-provider rate limits and timeouts;
- store `product_external_metadata`;
- upsert or refresh `product_links` when a canonical source URL exists;
- create field-level suggestions;
- update job status and metrics;
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
  - body: `{ "suggestion_ids": ["..."] }`
  - atomic accept where possible; returns accepted, skipped, conflicts.
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

Also add a deterministic parser that scans `raw_name` and
`receipt_description` for explicit package text such as `1GAL`, `16 CT`,
`12 OZ`, `2 x 8 ct`, and `31.7 oz`. If deterministic parser and LLM agree, use
higher confidence. If they disagree, leave as a suggestion.

Worker behavior:

- when package content is explicit and parseable, write line-item package
  overrides so price normalization works immediately for that receipt;
- if the matched/accepted product lacks package size, create pending
  `pack_quantity` and `pack_unit` suggestions from the line evidence;
- if UPC is present on a line and the product lacks UPC, create a pending UPC
  suggestion or carry it into product creation during suggestion acceptance;
- after receipt processing commits, queue enrichment jobs for products with a
  new UPC or missing package size, capped per receipt.

Do not ask the LLM for nutrition, servings, ingredients, allergens, photos, or
web-search facts during receipt scanning.

### Scheduled Backfill

Start with conservative scheduling.

Add config:

- `PRODUCT_ENRICHMENT_ENABLED=false` by default;
- `PRODUCT_ENRICHMENT_AUTO_ON_SCAN=true` only after settings opt-in;
- `PRODUCT_ENRICHMENT_SWEEP_INTERVAL=24h`;
- `PRODUCT_ENRICHMENT_MAX_JOBS_PER_SWEEP=50`;
- `PRODUCT_ENRICHMENT_REFRESH_AFTER_DAYS=90`;
- provider-specific timeout and rate-limit constants in code, not user-tuned
  until there is operational evidence.

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
- products whose missing fields were explicitly rejected recently.

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

## Implementation Phases

### Phase 1: Metadata Backbone and Receipt Package Extraction

Backend:

- add `product_external_metadata` migration;
- add `product_enrichment_jobs` migration;
- add metadata payload Go structs;
- add package-content parser for receipt line text;
- add LLM schema fields for explicit package content;
- populate line item package overrides when evidence is explicit;
- create package-size suggestions from line evidence;
- add tests for parser, worker persistence, and price normalization from line
  overrides.

Frontend:

- add Product Detail grouped suggestions and bulk accept/reject;
- improve UPC lookup success/error feedback;
- show source/snapshot status.

Why first:

- package size is the most useful short-term enrichment;
- it improves price comparison even before external providers are configured;
- it uses data already visible on receipts.

### Phase 2: Open Food Facts and USDA Provider Chain

Backend:

- move current Open Food Facts/USDA logic into provider adapters;
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
- queue limited jobs after receipt scan commit;
- add scheduler/sweeper with config gates;
- add metrics for jobs queued, succeeded, failed, provider latency, and
  provider status.

Frontend:

- product list filters for missing metadata and failed lookups;
- receipt review badges for queued/found package/UPC;
- settings toggle for automatic lookup.

Acceptance:

- scanning a receipt with UPCs queues jobs after the receipt completes;
- jobs are capped and idempotent;
- app restart leaves queued jobs recoverable;
- users can disable all automatic external lookups.

### Phase 4: Kroger Integration

Backend:

- add Kroger integration config and OAuth token handling;
- add `store_external_refs`;
- add location search/mapping endpoints;
- add Kroger product lookup/search adapter;
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
- replacing `products.upc` with a full multi-identifier model in this plan.

The multi-identifier model still belongs in the broader product identity
roadmap. This plan should stay compatible by treating `products.upc` as the
current primary shortcut and storing extra identifiers inside metadata snapshots
until that foundation lands.

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

## Open Questions

- Should Open Food Facts be enabled by default for manual lookup only, while
  automatic lookup remains opt-in?
- Should USDA API key remain environment-only, household integration-only, or
  both? Current code uses `USDA_FDC_API_KEY`; both is the least disruptive.
- What exact threshold should auto-create line package overrides from receipt
  text: deterministic parser only, or parser plus LLM agreement?
- Should accepting a high-confidence UPC automatically queue provider lookup?
- Do we want job status surfaced through WebSocket events, polling, or just
  React Query refetch? Polling is simpler for v1.
- What is the correct retention policy for stale source snapshots and failed
  snapshots?
- How should backups/exports represent external metadata attribution?

## Test Plan

Backend tests:

- package parser for common receipt patterns;
- worker stores package overrides and records normalized price from overrides;
- Open Food Facts adapter maps fixtures to metadata and suggestions;
- USDA adapter filters non-exact UPC matches;
- Kroger adapter candidate scoring with fixtures;
- job idempotency and retry behavior;
- suggestion accept conflicts for UPC uniqueness;
- metadata payload allowlist rejects raw provider blobs;
- safe HTTP client remains used for URL/manual fetch paths;
- scheduled sweep caps jobs and skips recent failures.

Frontend tests:

- Product Detail renders grouped suggestions and bulk accept/reject;
- lookup button shows queued/running/error/success states;
- accepting package-size suggestion offers recompute;
- settings cards store/test provider config;
- product filters for missing metadata and failed lookups.

Manual smoke:

- product with UPC and no package size;
- product with package text only on receipt;
- Kroger product with mapped store;
- product with conflicting UPC;
- provider timeout/failure;
- automatic enrichment disabled.
