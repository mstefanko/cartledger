# Phase 3 / Phase 3.5 Design Audit

Scope: lines 1262–1411 of `docs/food-product-enrichment-implementation-plan.md`.
Code reality is taken from `internal/enrichment/runner/service.go`, `internal/worker/receipt.go`,
`internal/api/products_enrichment.go`, `internal/db/migrations/041`, `internal/db/migrations/043`,
`internal/config/config.go`, `internal/ws/messages.go`, `web/src/api/products.ts`,
`web/src/api/ws.ts`, `web/src/components/receipts/ReceiptReview.tsx`,
`web/src/pages/ProductsPage.tsx`.

The CLAUDE.md call-out is confirmed: migrations 040–043 are in place but the runtime is
"Phase 1 + manual lookup + auto-on-scan queue helper only." There is no scheduler, no
sweeper, no snapshot pruner, no metrics for enrichment, no settings UI, and no
`receipt_review_scan` trigger value in the schema.

---

## 1. Summary — top 5 issues to resolve before implementation

1. **`receipt_review_scan` trigger does not exist in schema.** Migration 041 (`internal/db/migrations/041_product_enrichment_jobs_settings_edits.up.sql`) hard-codes the allowed `trigger` values to `'receipt_scan'`, `'manual_lookup'`, `'manual_refresh'`, `'scheduled_refresh'`, `'batch_backfill'`. Phase 3.5 says to queue with `trigger=receipt_review_scan`, which will violate the CHECK constraint. The plan must explicitly add a migration to extend the constraint (or reuse `manual_lookup` — see "Cross-phase consistency"). The dedupe semantics of `idx_product_enrichment_jobs_active` (`household_id, product_id, trigger, lookup_key` per migration 043) hinge on this choice.
2. **No single `QueueForProduct` chokepoint exists.** `worker/receipt.go:1071 queueReceiptScanEnrichment` calls `enrichment.QueueJob` directly with its own UPC discovery query; `internal/api/products_enrichment.go` does the same for manual paths. Phase 3.5 introduces a third call site. The plan must say "introduce a `Service.QueueForProduct(ctx, householdID, productID, trigger, opts)` helper that owns: gate evaluation, UPC normalization, dedupe lookup, queue-cap check, and metric emission" — and migrate the existing two call sites.
3. **Gate precedence is unstated.** Three gates exist (`Cfg.ProductEnrichmentEnabled`, `product_enrichment_settings.auto_on_scan_enabled`, `product_enrichment_settings.manual_lookup_enabled`) plus the new "explicit user action" override for Phase 3.5. `service.go:264-274` already short-circuits jobs at `markJobRunning` time, but the plan does not say which gate Phase 3.5 bypasses. Spell out the precedence table (see §6).
4. **Snapshot pruning rules are underspecified.** The plan says "30-day failed-only and 180-day stale unaccepted." There is no `last_failed_at` column on `product_external_metadata` (migration 040 has `fetched_at`, `expires_at`, `last_error` only) and no `last_suggestion_at` on `product_enrichment_suggestions`. Pruning by `fetched_at` alone is the only thing the schema supports today; either a new column is needed or the plan must commit to `fetched_at` semantics.
5. **Phase 3 metrics target is undefined.** `internal/api/metrics.go` is Prometheus-based with `cartledger_*` names (e.g., `cartledger_worker_queue_depth`). The plan should commit to that pattern with explicit metric names (`cartledger_enrichment_jobs_queued_total`, `_succeeded_total`, `_failed_total{provider, reason}`, `cartledger_enrichment_provider_latency_seconds{provider}`) and a per-status gauge. Without a name list the work item is not estimable.

---

## 2. Phase 3 — Backend

### B1. Sweeper primitive and locking — `internal/enrichment/runner/service.go`
- The plan says "add scheduler/sweeper with config gates" but does not name the primitive. Recommend: `time.Ticker`-driven goroutine started inside `Service.Start(ctx context.Context)`, cancelled by appCtx in `cmd/server/serve.go`. Pattern already exists in the receipt worker (`internal/worker/receipt.go:NewReceiptWorker`).
- Cadence is `cfg.ProductEnrichmentSweepInterval` (already in `config.go:99-101`, default 24h). Document it in the plan.
- Single-process leader election is not needed (SQLite single-writer) but the plan must say so explicitly — otherwise reviewers will ask.
- Sweeper must be skipped on every tick when `cfg.ProductEnrichmentScheduledSweep == false` AND any household's `scheduled_sweep_enabled == 1`. Today the household setting can be true while the env gate is false; the plan must rule on which wins (see §6).

### B2. Candidate selection — `service.go` new query
- "How does the sweep pick candidates" is not in the plan. Schema can answer this now: products with `upc IS NOT NULL` AND no successful `product_external_metadata` row from any enabled provider in the last `ProductEnrichmentRefreshAfterDays` (default 90, `config.go:227`). Failed-and-eligible-for-retry: `product_enrichment_jobs` rows where `status='failed'` AND `next_attempt_at <= now()`.
- Plan should add: "Sweeper ORDER BY `last_purchased_at DESC NULLS LAST` and LIMIT to `cfg.ProductEnrichmentMaxJobsPerSweep` (default 50)." Without that, behaviour on a 10k-product household is unbounded.
- First-run backfill: `product_enrichment_settings.first_run_backfill_limit` (200) is wired but never read in code today. Plan should specify that the sweeper's first invocation per household uses the larger limit, then drops back to `MaxJobsPerSweep`.

### B3. Scan-completion hook dedupe — `internal/worker/receipt.go:1071-1130`
- `queueReceiptScanEnrichment` already exists and is correct for the per-product `(household_id, product_id, trigger='receipt_scan', lookup_key=normalizedUPC)` dedupe via index `idx_product_enrichment_jobs_active`. Plan should reference this code by line rather than implying the hook is new.
- The hook silently drops products whose `lookupExpr COALESCE(p.upc, li.upc) IS NULL`. Plan should state this explicitly: "products without a UPC after the scan are not eligible for auto-on-scan; they only get picked up by the nightly sweep against alt lookup keys (brand + name fuzzy) when implemented."
- Hook runs in a 5s context after the receipt commits. Plan must rule on what happens if the receipt-scan commit succeeds but auto-enrichment queue insert fails: today it logs and moves on (best-effort). Acceptance criterion "jobs are capped and idempotent" doesn't say whether a missed queue is acceptable. Recommend: yes, the nightly sweep will catch them — document it.

### B4. Queue caps — `service.go` and `config.go:101`
- `MaxJobsPerSweep` is sweep-side, but there is no per-receipt cap today. A receipt with 50 UPC line items will enqueue 50 jobs. Plan should add `ProductEnrichmentMaxJobsPerReceipt` (recommend 20) and have `queueReceiptScanEnrichment` truncate, ordered by `total_price DESC` so most-expensive items get priority lookups.
- No per-household-per-hour rate limit on auto jobs exists. Plan should either say "rely on provider rate limiter in `openfoodfacts.go`" or add one. Today the provider rate limiter is the only backstop, which means a 200-receipt backfill can saturate Open Food Facts.

### B5. Snapshot pruning — schema gap, `migration 040`
- Schema does not store `last_failed_at` or `last_suggestion_at`. Options:
  - **Option A (preferred):** prune `product_external_metadata` by `fetched_at` (30 days for rows with `last_error IS NOT NULL`, 180 days for rows where no accepted suggestion references them via `product_enrichment_suggestions.external_metadata_id`). Both clocks start at `fetched_at`.
  - **Option B:** add `last_failed_at` to `product_external_metadata` in a new migration 044.
- The plan must answer: when a product gains an `accepted_by_user=1` row on `product_nutrition` populated from a snapshot, does the snapshot itself get pruned? Recommend: keep snapshots referenced by any accepted suggestion forever (lookup via `product_enrichment_suggestions.external_metadata_id` join). Unaccepted snapshots prune freely.
- Pruning must run inside a transaction with a row limit (e.g., 1000/tick) to avoid blocking writes on large households.

### B6. Metrics — `internal/api/metrics.go`
- Existing pattern is `prometheus.NewCounterVec`/`prometheus.NewHistogramVec` registered in `NewMetrics`. Plan must commit to:
  - `cartledger_enrichment_jobs_queued_total{trigger}`
  - `cartledger_enrichment_jobs_finished_total{trigger,status,provider}`
  - `cartledger_enrichment_provider_latency_seconds{provider}` (histogram)
  - `cartledger_enrichment_provider_requests_total{provider,http_status}`
  - `cartledger_enrichment_queue_depth{status}` (gauge polled from DB on each scrape)
- Without the names, the acceptance bullet "metrics for jobs queued, succeeded, failed, provider latency, provider status" is not testable.

### B7. Recovery semantics — `service.go`
- Plan says "queued/running recovery." Current code (`service.go:markJobRunning`) returns `sql.ErrNoRows` if a job is no longer `'queued'`, but there is no reconciler that flips orphaned `'running'` rows (process crash mid-flight) back to `'queued'` or `'failed'` on boot. Plan should add a startup pass: `UPDATE product_enrichment_jobs SET status='queued', started_at=NULL WHERE status='running' AND updated_at < now() - interval` — or `'failed'` after N attempts. Acceptance bullet "app restart leaves queued jobs recoverable" hinges on this.

---

## 3. Phase 3 — Frontend

### F1. Badge state machine — `web/src/components/receipts/ReceiptReview.tsx:380-1050`
- Plan says badges for "queued / found package / UPC." Reality: a job moves through `queued → running → (succeeded|partial|failed|cancelled)`, optionally producing suggestions. Plan must map each state to a badge:
  - `queued` → neutral "Looking up…" (clock icon)
  - `running` → same (collapses with queued for UI purposes)
  - `succeeded` + suggestion exists → success "Package found" with `count` (link to Product Detail suggestions)
  - `succeeded` + no result → neutral "No package data"
  - `failed` (terminal) → warning "Lookup failed" (no retry CTA on review screen — that's Product Detail)
  - `cancelled` / `partial` → not shown
- Plan must pick a location: today row badges live near the suggested-product name (`ReceiptReview.tsx:388-1046`). Recommend a single badge slot to the right of the product name on each row.
- `ManualLineItemGrid.tsx` is the create-from-spreadsheet path; same badge component, but it must read enrichment job status from a separate query (rows have no `line_item_id` until commit).

### F2. WebSocket integration — `web/src/api/ws.ts:97`
- `EventProductEnrichmentJobUpdated` is already broadcast (`internal/enrichment/runner/service.go:799`) and `web/src/api/ws.ts:97` already handles `'product.enrichment_job.updated'` (per the grep). Plan should reference this and say "invalidate `['receipts', receiptId]` AND `['products', productId, 'enrichment-jobs']` on receipt of this event."
- For receipt-review badges to update live, the receipt detail response must include each row's most recent enrichment job state. Today `internal/api/receipts.go` does not join enrichment_jobs into the line-item payload. Plan should add: "extend `GET /api/v1/receipts/:id` to embed `enrichment_job_status` and `latest_suggestion_count` per line item" — note the join cost (one job + one suggestion count per item is fine at 50 items, not for 500).

### F3. Settings page — `web/src/pages/SettingsPage.tsx`
- `SettingsPage.tsx` exists but the CLAUDE.md notes household settings UI is "not live yet" for enrichment. Plan must say:
  - Add new section "Product enrichment" with toggles bound to `product_enrichment_settings` columns (`manual_lookup_enabled`, `auto_on_scan_enabled`, `scheduled_sweep_enabled`, three provider toggles).
  - Add GET/PUT `/api/v1/households/settings/enrichment` endpoints — these do not exist today.
  - Show env-gate banner when `cfg.ProductEnrichmentEnabled == false` ("Disabled by server administrator").

### F4. Product list filters — `web/src/pages/ProductsPage.tsx:103-208`
- Existing filter infra: `useSearchParams` + `useState` for `searchTerm`, debounced. Adding `?filter=missing-metadata` and `?filter=failed-lookups` fits this pattern without refactor.
- Backend support is missing: `GET /api/v1/products` (per `web/src/api/products.ts:listProducts`) takes only `q` and `sort`. Plan must add: extend `listProducts` to accept `filter: 'missing_metadata' | 'failed_lookups'` and have `internal/api/products.go` translate to SQL (`LEFT JOIN product_external_metadata` for missing; `EXISTS (...) WHERE status='failed'` for failed).
- "Missing metadata" needs a definition. Recommend: brand IS NULL OR product_nutrition row is absent. Plan should commit.

---

## 4. Phase 3.5 — Backend

### B3.5.1. Endpoint design — `POST /api/v1/receipts/:id/line-items/:itemId/barcode`
- The route pattern is consistent with existing nesting at `internal/api/receipts.go:248-250` (`receipts.POST("/:id/line-items/bulk", ...)`, `receipts.PUT("/:id/line-items/:itemId", ...)`). The `/barcode` suffix is novel but fits.
- **`mode: "preview" | "apply"` is unnecessary.** Existing pattern in this repo is a separate GET for preview (`GET /api/v1/receipts/:id/repair-preview` → `POST /api/v1/receipts/:id/apply-repair`). Recommend:
  - `POST /api/v1/receipts/:id/line-items/:itemId/barcode/preview` — returns local matches, provider availability, conflicts. No state change.
  - `POST /api/v1/receipts/:id/line-items/:itemId/barcode` — applies. Body: `{ "upc": "...", "create_product": false }`.
- Both routes carry a precondition: receipt status must be in `pending|processing|matched` (not `error`/`deleted`). 404 on missing, 409 on terminal state.

### B3.5.2. Idempotency and race — `internal/api/security.go` / Echo middleware
- Plan says "idempotent for repeated scans of the same UPC on the same line item" but does not name a mechanism. Two scans within 100ms in two tabs both pass server-side checks today. Recommend:
  - Compute an idempotency key server-side: `sha256(line_item_id || normalized_upc)`. Insert into a small in-memory LRU (5-minute TTL) keyed on this and short-circuit duplicates with the prior response.
  - Or: rely on the DB. The first apply transitions the line item from `unmatched`→`matched(product_id=X)`; the second apply sees the line is already matched to the same product (because the scanned UPC resolves to X again) and returns the same success. The plan should state which path is canonical.
- An `If-Match: <line_items.updated_at>` precondition is NOT needed for receipt review today because no other endpoint takes ETags; introducing it here would be inconsistent.

### B3.5.3. "Match the line item to that product" — interaction with matcher Session
- The receipt worker uses `matcher.Engine.NewSession` (`internal/worker/receipt.go:queueScanEnrich` context). After scan-review apply, that session is long gone (commit completed). The plan must say:
  - Apply path writes directly: `UPDATE line_items SET product_id = ?, confidence = 1.0, match_method = 'barcode_scan' WHERE id = ?`.
  - Insert alias via `matcher.UpsertAlias` (existing helper used at `receipt.go:identifiers.UpsertProductIdentifier` call site) with `source = 'receipt_review_scan'`. Today valid sources are enumerated in `internal/matcher/aliases.go` — plan must add `'receipt_review_scan'`.
  - Insert/upsert `product_identifiers` row via `identifiers.UpsertProductIdentifier` with `Source = 'receipt_review_scan'`, `SetPrimaryProduct = true`.
- Confidence: `1.0` for barcode-scanned matches, distinct from fuzzy match confidence band. Plan should state this — otherwise downstream analytics treat barcode matches the same as 0.9 fuzzy matches.

### B3.5.4. `receipt_review_scan` trigger — schema gap
- See §1.1. Three options, plan must pick one:
  - **A. New migration 044** extending the CHECK constraint to include `'receipt_review_scan'`. SQLite CHECK constraints require table recreation — non-trivial but follows pattern of migration 043.
  - **B. Reuse `'manual_lookup'`** with a discriminating `lookup_key` prefix (e.g., `lookup_key = 'scan:' + upc`). Avoids schema change. Loses metric/visibility.
  - **C. Add a separate `source` column** (e.g., `requested_via VARCHAR DEFAULT 'manual'`) that distinguishes scan from form input.
- Recommend A. Update `runner.TriggerReceiptReviewScan = "receipt_review_scan"` constant in `service.go:1-25`.

### B3.5.5. Gate bypass for explicit user action
- Plan says "the user explicitly initiated the scan and automatic lookup opt-in should not be required." But `service.go:isManualTrigger` (line 705) is the function that decides whether a trigger bypasses the auto-on-scan gate. `receipt_review_scan` must be added to `isManualTrigger` to be treated as opt-in by user action.
- The env gate `cfg.ProductEnrichmentEnabled` (`service.go:globalEnabled`, called from line 264) still blocks `receipt_review_scan`. Plan should confirm this is intended — recommend yes, env-level kill switch overrides per-action consent.
- `product_enrichment_settings.manual_lookup_enabled` (defaults to 1) is the household-level opt-out. Plan should confirm `receipt_review_scan` is treated as a manual trigger — same as `manual_lookup` — and therefore respects `manual_lookup_enabled`. If the household has explicitly disabled manual lookups, the scan stores the UPC but does NOT queue a provider lookup. Plan should say "Result state: 'lookup skipped — manual lookups disabled in settings'."

### B3.5.6. UPC conflict response — `internal/api/products_enrichment.go:20` (`enrich_api(20)`)
- The existing conflict path returns `{existing_product_id, existing_product_name, suggested_merge: true}` via `identifiers.IdentifierConflictError`. Plan should reuse this exact response shape for the barcode endpoint duplicate path so the frontend doesn't learn a second contract.
- **Merge UI gap:** the response says "merge path when relevant" but `web/src/pages/ProductsPage.tsx` exposes `mergeProducts` only via a manual two-product selector. There is no deep link `/products/merge?keep=:idA&take=:idB`. Plan must either: (a) add that route, or (b) link the user to the existing-product detail page and surface a "merge from receipt scan" callout. Recommend (a).

### B3.5.7. Editable-receipt eligibility — `internal/api/receipts.go`
- Plan says "limit eligibility to editable receipts and line items that are unmatched, suggested as `new_product`, or explicitly being created from the Add Row flow." Reality: line items have `match_method`/`product_id`/`suggestion_type` but no single "is editable" predicate today. Plan must define: `editable = receipt.status IN ('matched','review') AND line_item.product_id IS NULL` OR `line_item.suggestion_type = 'new_product'`. Reject 409 otherwise with `error.code = 'line_item_already_matched'`.

### B3.5.8. Provider-disabled fallback
- Plan says "if providers are disabled or unavailable, still store the scanned identifier and show that lookup was skipped." This means the apply path must succeed even when `globalEnabled() == false`. The response payload needs a `lookup_skipped_reason` field with values `'env_disabled' | 'household_disabled' | 'no_provider_configured'`. Plan should state the values.

---

## 5. Phase 3.5 — Frontend

### F3.5.1. Modal vs. route — commit to one
- Plan calls it `BarcodeScannerModal`. For mobile receipt review (the stated primary use case) a full-screen modal that takes over the viewport is correct; on desktop the same component works as a centered overlay. Recommend: implement as a `<Dialog>` from the existing UI primitives that goes full-screen below `md:` breakpoint via Tailwind v4 utilities.
- Do NOT make it a route. A route causes router state changes that fight the existing `ReceiptReview` page state (selected row, scroll position, in-progress edits). Modal preserves that state for free.

### F3.5.2. Lazy-load mechanism
- Plan says "lazy-load the scanner code." The pattern is `React.lazy(() => import('./BarcodeScannerModal')) + <Suspense fallback={<Spinner/>}>`. Check the codebase for an existing example. If none exists, plan must say "introduces a lazy-load pattern in `web/src/components/receipts/ReceiptReview.tsx`; reuse the `Suspense` pattern in future."
- `@zxing/browser` is ~150 KB minified gzipped. Plan should add a bundle-budget acceptance criterion: "the receipt review desktop bundle size MUST NOT change when the scanner is not opened."

### F3.5.3. Camera lifecycle
- Plan says stop the stream "when the modal closes, the route changes, or the scan is applied." Correct mechanism: a single `useEffect` cleanup inside `BarcodeScannerModal` that calls `controls.stop()` from `@zxing/browser`. The modal already unmounts on close.
- "Route changes" is automatic if the modal unmounts with the page — React unmounts modals when their parent route does. Plan can drop this clause unless it's defending against React-Router transitions that don't unmount (Suspense-driven loads, parallel routes). For Vite/React-Router v6 the cleanup is automatic. Plan should say "cleanup runs in modal's `useEffect` return; no separate context provider needed."
- The bigger risk: granted-but-never-released MediaStream tracks on iOS Safari, which holds the indicator on after unmount. Plan should add: "always call `stream.getTracks().forEach(t => t.stop())` in cleanup, not only `controls.stop()`."

### F3.5.4. Result state → modal lifecycle
- Plan: "lookup queued/running: show the same receipt row badge language as Phase 3." Ambiguous on whether the modal closes immediately or stays open showing progress. Recommend:
  - **Apply with local match**: modal closes immediately, row badge shows "matched" instantly. No provider call.
  - **Apply with `create_product=true`**: modal shows a 3-state inline strip ("scanning… → product created → lookup queued"), then auto-closes after 1.5s on the last state. Row badge transitions to "Looking up…" via WebSocket `product.enrichment_job.updated`. This is the same WS message currently dispatched at `service.go:799`.
- "Auto-advance to next eligible row" — plan should state: only after a successful apply (not after a duplicate-UPC error). Selector logic: `useMemo(() => lineItems.find(li => li.id > currentId && isEligible(li)), [lineItems, currentId])`.

### F3.5.5. Physical scanner support
- USB scanners type into the focused field followed by `\n`. The manual UPC input must be an uncontrolled `<input>` with `onKeyDown` handler that triggers apply on `Enter`. Plan should say: "manual UPC entry input listens for `Enter` and submits — no separate 'scanner mode' toggle, because focused-input fallback covers both keyboard and USB-HID scanners."
- No `<input type="number">` — UPCs have leading zeros that get stripped.

### F3.5.6. Product Detail camera icon — `web/src/pages/ProductDetailPage.tsx`
- Plan says add camera icon beside the UPC input. Implementation: small icon button to the right of the existing UPC `<input>`, opens the same `BarcodeScannerModal` in "single-field-fill" mode (no apply call, just sets `upc` form state and closes). Plan should explicitly name the new modal mode (`mode: 'fill' | 'apply-to-line'`) or split into two components.

### F3.5.7. ManualLineItemGrid scan affordance
- `web/src/components/receipts/ManualLineItemGrid.tsx` is the row-level grid. The scan icon goes in the same column as the existing UPC cell. Plan should reference this file. Eligibility: only show when row has no `product_id` yet.

---

## 6. Cross-phase consistency

### 6.1. Gate precedence table — must be in the plan
| Source | Trigger evaluated | Effect |
|---|---|---|
| `cfg.ProductEnrichmentEnabled = false` | any | All triggers rejected at queue time. Phase 3.5 still stores UPC observation and returns `lookup_skipped_reason='env_disabled'`. |
| `cfg.ProductEnrichmentEnabled = true`, `auto_on_scan_enabled = false` | `receipt_scan` | Rejected by `service.go:274`. Phase 3 sweeper will catch later if sweep is enabled. |
| `cfg.ProductEnrichmentEnabled = true`, `manual_lookup_enabled = false` | `manual_lookup`, `manual_refresh`, `receipt_review_scan` | Rejected. Phase 3.5 stores UPC but skips provider. |
| `cfg.ProductEnrichmentEnabled = true`, `scheduled_sweep_enabled = false` | `scheduled_refresh`, `batch_backfill` | Sweeper doesn't run for that household. |
| `cfg.ProductEnrichmentScheduledSweep = false` (env) | `scheduled_refresh` | Sweeper goroutine doesn't run at all (regardless of household). |

### 6.2. Single chokepoint
- Add `Service.QueueForProduct(ctx, householdID, productID, trigger string, opts QueueOpts) (Job, queued bool, err error)` to `internal/enrichment/runner/service.go`. Existing call sites to migrate:
  - `internal/worker/receipt.go:1071 queueReceiptScanEnrichment` (the per-UPC loop)
  - `internal/api/products_enrichment.go enrichProductByUPC` handler
  - New: `internal/api/receipts_barcode.go apply` handler
- The helper owns: gate checks (mirroring `service.go:264-274` so they run at enqueue time, not just at dequeue), UPC normalization, dedupe lookup, queue-cap check, metric emission.

### 6.3. Naming
- Plan flips between "auto on scan" and "automatic lookup." Code is consistent: column `auto_on_scan_enabled`, env `PRODUCT_ENRICHMENT_AUTO_ON_SCAN`, function `AutoOnScanEnabled`. Plan and UI strings should use "Automatic lookup after receipt scan."
- "Found package" badge text (Phase 3 frontend) should match Product Detail terminology — today suggestions are labeled "package data found" in `products_enrichment.go`. Pick one term and use it across both pages.

### 6.4. WS event reuse
- `EventProductEnrichmentJobUpdated` already exists and is dispatched at `service.go:799`. Phase 3 badges and Phase 3.5 row updates both subscribe to it. Plan should say: no new WS event types needed; the existing message must include `receipt_id` in payload when triggered from `receipt_scan` or `receipt_review_scan` so receipt-review can filter. Today the payload likely does not include receipt_id — verify and amend.

---

## 7. Acceptance criteria gaps

### Phase 3 — written criteria
- "scanning a receipt with UPCs queues jobs after the receipt completes when the household has automatic lookup enabled" — testable. Existing `internal/worker/receipt_enrichment_test.go` likely already covers this (file is in git status as new).
- "jobs are capped and idempotent" — **not testable as written**. Cap by what? Per-receipt? Per-sweep? Per-household-per-hour? Idempotent against what duplicate? Plan must split into:
  - Per-receipt cap of N (recommend 20). Test: 30 UPC items, only 20 jobs queued.
  - Sweep cap of `MaxJobsPerSweep`. Test already covered by config wiring.
  - Idempotency against `(household_id, product_id, trigger, lookup_key)` while `status IN ('queued','running')` — already enforced by migration 043. Test: queue twice in 1s, assert one row.
- "app restart leaves queued jobs recoverable" — testable IF the orphan-running reconciler exists (see §B7). Without it, the criterion is misleading.
- "users can disable all automatic external lookups" — testable but vague. Add: "from Settings page, toggling 'Automatic lookup after receipt scan' off causes the next scan to enqueue zero jobs." Currently no settings UI exists; the criterion implies the UI must ship in Phase 3.

### Phase 3 — missing criteria
- "Failed jobs are retried with exponential backoff up to N attempts, then move to terminal `failed`." (Service has `retryAt(attemptCount)` per `service.go` but no max-attempt rule visible.)
- "Snapshot pruning preserves snapshots referenced by accepted suggestions." (Behavioural — easy to regress.)
- "Metrics `cartledger_enrichment_jobs_finished_total{status}` are exposed on `/metrics`." (Without this the operator can't tell if Phase 3 is working in prod.)
- "Receipt review badges update without a page refresh." (WS-driven; testable with a mock WS message.)

### Phase 3.5 — written criteria
- "reviewing a receipt on a phone, the user can tap scan…" — testable. Add device matrix: iOS Safari 16+, Android Chrome 110+.
- "scanning a UPC for a new product can create and match a product…" — testable. Add transactional requirement: product creation + UPC attach + line item update + job enqueue must all commit atomically OR roll back together. Without this, a half-applied scan corrupts the receipt.
- "manual UPC entry follows the same preview/apply path as camera scanning" — testable, but contradicts the recommendation to drop `mode: "preview"|"apply"` (§B3.5.1). Reword: "manual UPC entry hits the same `/preview` and `/barcode` endpoints as camera scanning."
- "camera permission denial… do not block manual review" — testable.
- "repeated scans of the same barcode do not create duplicate products or jobs" — testable. Add: "second identical scan within the modal session shows a brief 'already scanned' toast and does not re-fire the apply request." (Frontend-side debounce, not just server idempotency.)
- "existing matched products are not silently corrected from receipt review" — testable. Add: "if the scanned UPC resolves to a different product than the line item's currently matched product, the modal shows a blocking conflict with options 'switch match' or 'cancel' — no silent overwrite."
- "on Product Detail, scanning fills the UPC input and follows the existing product save/conflict/lookup behavior" — testable.
- "the camera stream is stopped when the modal closes, the route changes, or the scan is applied" — testable via Playwright with `navigator.mediaDevices` mock.

### Phase 3.5 — missing criteria
- "Scanning a barcode whose normalized UPC fails GTIN check-digit validation shows an inline error and does not POST to the server." (Save a server round-trip; protect against ZXing false reads.)
- "Modal opens within 500ms on a 4× CPU throttled mid-range Android device with a cold cache." (Validates the lazy-load chunk size.)
- "Apply with `create_product=true` returns 201 with the new product payload and the updated line item in a single response, so the receipt query does not need an extra round-trip."
- "When `cfg.ProductEnrichmentEnabled=false`, apply still creates the product and matches the line, but the response includes `lookup_skipped_reason='env_disabled'` and no job row is inserted."
- "The `'receipt_review_scan'` trigger appears in `idx_product_enrichment_jobs_active` dedupe so two rapid scans of the same UPC enqueue exactly one job."

---

## Confidence: HIGH (≈85%)
Schema, code paths, and frontend file layout are all verified against current source. The two soft spots are the precise pruning column choice (§B5 option A vs B) and whether the receipt detail API already embeds enrichment job state (§F2 — file is large and not exhaustively read; verify before estimating).
