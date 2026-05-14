# Phase 3 & 3.5 External Research

External patterns and pitfalls collected to strengthen Phase 3 (Automatic Enrichment on Scan and Nightly Sweep) and Phase 3.5 (Receipt Review Barcode Assist) of `/Users/mstefanko/cartledger/docs/food-product-enrichment-implementation-plan.md`.

Each section ends with **Implications for CartLedger**.

---

## 1. Background jobs + scheduled sweeps in Go/SQLite single-process apps

### How comparable projects schedule periodic work

- **Miniflux (Go, SQLite/Postgres, single-process)** uses two plain in-process goroutines, each driven by `time.Tick(frequency)`, kicked off by `runScheduler` at daemon start. One ticker (`feedScheduler`) emits "next-check-expired" batches into a buffered worker pool channel; another (`cleanupScheduler`) calls a top-level `runCleanupTasks(store)` on its own period. No external library. See `internal/cli/scheduler.go` and `internal/cli/daemon.go` in the [miniflux/v2 repo](https://github.com/miniflux/v2/blob/main/internal/cli/scheduler.go).
- The worker pool itself is a simple `chan model.Job` + `sync.WaitGroup`. `Pool.Push(jobs)` blocks if the queue is full (unbuffered channel), so the scheduler naturally backpressures itself. See `internal/worker/pool.go` in [miniflux/v2](https://github.com/miniflux/v2/blob/main/internal/worker/pool.go).
- **Linkding (Python + Django + SQLite)** uses Huey's `@huey.periodic_task(crontab(minute="*"))` plus a global `@huey.lock_task("schedule-html-snapshots-lock")` to ensure only one instance of the sweep runs at a time, even if the previous one is still going. It then *paginates the workload* — pulls 5 pending assets per tick rather than enqueueing them all. See `bookmarks/services/tasks.py` in [sissbruecker/linkding](https://github.com/sissbruecker/linkding/blob/master/bookmarks/services/tasks.py).
- **Library options for Go**:
  - `robfig/cron` v3 — cron-expression scheduler; mature (14k stars). [Repo](https://github.com/robfig/cron).
  - `go-co-op/gocron` v2 — fluent API with first-class `WithSingletonMode` (reschedule-on-overlap or queue-on-overlap) and per-scheduler concurrency limits via `WithLimitConcurrentJobs`. Also offers `WithIntervalFromCompletion` so the next tick measures from when the job finishes — useful when work duration is variable. See [go-co-op/gocron](https://github.com/go-co-op/gocron).

### Dedupe / idempotency keys for enqueued jobs

- **Stripe's idempotency-key approach** is the dominant industry pattern: client picks a v4 UUID (or any 255-char random key), server stores first-response by key, retries with the same key return the cached result rather than running again. Keys are kept for ~24 hours. ([Stripe docs](https://docs.stripe.com/api/idempotent_requests))
- **IETF draft `draft-ietf-httpapi-idempotency-key-header`** (Oct 2025) formalizes this: a unique `Idempotency-Key` header plus an optional **idempotency fingerprint** (checksum of selected payload fields) to detect "same key, different body" errors. Standardized handling:
  - First time (key + fingerprint unseen) → process normally.
  - Retry (key + fingerprint match, original done) → replay stored result.
  - Concurrent retry (key + fingerprint match, original still running) → `409` resource conflict.
  - Key reused with different body → `422 Unprocessable Content`.
  - Missing key on documented idempotent op → `400` with link to docs.
  ([IETF draft](https://datatracker.ietf.org/doc/html/draft-ietf-httpapi-idempotency-key-header))
- **Miniflux dedupe** is simpler — done at the *batch builder* level: `WithNextCheckExpired()` filters out feeds whose `next_check_at > now`, so a feed can't appear in two batches. Combined with the unbuffered channel, you literally can't enqueue two jobs for the same row in the same tick.
- **Linkding** combines a status enum (`STATUS_PENDING`) with a Huey task lock per task type — the dedupe key is implicit in the row state, not a separate token.

### Snapshot / data pruning patterns

- Linkding paginates pending work (5 at a time) rather than fixed-age expiry — the workload itself acts as the cap. ([`bookmarks/services/tasks.py`](https://github.com/sissbruecker/linkding/blob/master/bookmarks/services/tasks.py))
- Miniflux's `cleanupScheduler` is invoked on its own ticker independent of the worker schedule. Cleanup tasks run sequentially in a single goroutine (no contention with feed workers).
- Hybrid is common: "keep most recent N successes per product **and** drop anything older than X days **and** clear failures retried > Y times". CartLedger's plan already proposes a per-product snapshot cap; that matches established practice.

### Implications for CartLedger

- **Prefer the Miniflux pattern over `robfig/cron` for the nightly sweep** — a single `time.Tick(frequency)` goroutine started in `cmd/server/main.go` is sufficient, idiomatic, has zero dependencies, and matches the existing single-process architecture. Reserve `gocron` only if you later need per-job singleton/overlap semantics (e.g., manual "Run sweep now" buttons).
- **Add a dedupe primitive at the row level**, not as a separate idempotency-key table. Suggestion: `enrichment_job` row with `(product_id, provider, status)` plus a partial unique index where `status IN ('queued','running')`. The receipt-complete hook, the sweep, and a manual click all `INSERT … ON CONFLICT (product_id, provider) WHERE status IN ('queued','running') DO NOTHING` — three callers, one row.
- **Use `WithNextCheckExpired`-style filtering** in the sweep query rather than maintaining a worklist table. Filter `WHERE missing_metadata AND (last_attempt_at IS NULL OR last_attempt_at < now - cooldown)` — the row's own state is the dedupe primitive.
- **Backpressure via channel capacity, not unbounded enqueue**: copy Miniflux — bounded `chan job` + `Pool.Push` blocks. Beats trying to compute "is queue too full?" in the scheduler.

---

## 2. Barcode scanning in web apps: `@zxing/browser` vs alternatives

### Library comparison (2025–2026)

| Library | Formats | Bundle | Activity | Notes |
| --- | --- | --- | --- | --- |
| `@zxing/browser` | All major 1D + 2D (UPC-A, UPC-E, EAN-13, EAN-8, Code 128/39/93, ITF, QR, Data Matrix, PDF417, Aztec, Codabar) via `BrowserMultiFormatReader`; single-format readers (`BrowserQRCodeReader`, etc.) are smaller. | ~280 stars, 47 forks, master last touched 2024. | Maintenance is slow but still functional. | Reader class hierarchy is documented in [its README](https://github.com/zxing-js/browser). |
| `html5-qrcode` | Same superset (QR_CODE, AZTEC, CODABAR, CODE_39/93/128, DATA_MATRIX, MAXICODE, ITF, EAN_13/8, PDF_417, RSS_14, RSS_EXPANDED, **UPC_A, UPC_E**, UPC_EAN_EXTENSION). Built on ZXing internally. | 6.1k stars, more active issue tracker. | Ships an opinionated `Html5QrcodeScanner` UI plus a lower-level `Html5Qrcode` for custom UIs. ([README](https://github.com/mebjas/html5-qrcode)) | Recommends *narrowing the format list* for performance — same advice applies to ZXing. |
| `quagga2` (fork of QuaggaJS) | 1D only (EAN, UPC, Code 128/39, Codabar, ITF) — no QR. | Active fork; original `serratus/quaggaJS` archived (5.2k stars). | Recommends webrtc-adapter shim; documents `facingMode: 'environment'` + `deviceId` constraints. ([README](https://github.com/serratus/quaggaJS)) | Heavier on CPU than ZXing; less accurate on noisy/curved receipts. |
| Native `BarcodeDetector` | Aztec, Code 128/39/93, Codabar, Data Matrix, EAN-13/8, ITF, PDF417, QR, UPC-A, UPC-E. | Zero bundle. | **Chrome/Edge Android & desktop, Opera. NOT in Firefox. Safari shipped a partial implementation but coverage is uneven — many UPC-A/EAN combos are missing on macOS, e.g. macOS Chrome `getSupportedFormats()` returns no `code_128` or `upc_a` in some builds per [Chrome Developers article](https://developer.chrome.com/articles/shape-detection).** | Available as a polyfill (`barcode-detector` package wraps ZXing-WASM). Must feature-detect *and* call `BarcodeDetector.getSupportedFormats()` to find out what the host actually supports ([MDN](https://developer.mozilla.org/en-US/docs/Web/API/Barcode_Detection_API)). |
| `zxing-wasm` | All ZXing formats. | Actively developed (the ZXing JS team itself points users to it: [zxing-js/browser#127](https://github.com/zxing-js/browser/issues/127)). | Modern WASM build, much faster than `@zxing/browser`. | Worth considering as the engine behind a `barcode-detector` polyfill — best of both worlds. |

### Camera lifecycle bugs from real projects

- **ZXing camera-light-stays-on**: `controls.stop()` is required after `decodeFromVideoDevice` — see the README example which explicitly calls `setTimeout(() => controls.stop(), 20000)`. Grocy's wrapper calls `Scanner.reset()` plus resets a "TorchIsOn"/"LiveVideoSizeAdjusted" set of flags on modal `onHide`; otherwise the next open leaks state. See `Grocy.Components.CameraBarcodeScanner.StopScanning` in [`grocy/public/viewjs/components/camerabarcodescanner.js`](https://github.com/grocy/grocy/blob/master/public/viewjs/components/camerabarcodescanner.js).
- **`stop()` is not enough on its own** — per [MDN](https://developer.mozilla.org/en-US/docs/Web/API/MediaStreamTrack/stop): also iterate every track on `videoElem.srcObject.getTracks()`, call `track.stop()` on each, then set `videoElem.srcObject = null` to actually release the source. Just nulling out the reference without calling `stop()` leaves the source live until GC.
- **Open issues on `zxing-js/browser` that affect production**:
  - [#128 "NextJS - stop() not stopping decodeFromConstraints"](https://github.com/zxing-js/browser/issues/128) — stop is unreliable without manually releasing the stream.
  - [#131 "InvalidAccessError: Track has ended at applyConstraints"](https://github.com/zxing-js/browser/issues/131) — torch toggle after the track ended throws.
  - [#97 "listVideoInputDevices() doesn't ask for Camera permission on MacOS and iOS"](https://github.com/zxing-js/browser/issues/97) — on macOS/iOS, `listVideoInputDevices` does **not** prompt for permission, so you get an empty list and the scanner silently fails. Fix: call `getUserMedia({ video: true })` first to trigger the prompt, then enumerate.
  - [#140 "infinite loop"](https://github.com/zxing-js/browser/issues/140), [#137 "Add Play, Pause, Stop, Destroy methods"](https://github.com/zxing-js/browser/issues/137) — controls lifecycle gaps.
- **Torch handling**: Grocy detects via `track.getCapabilities().torch`; if false, the torch button is hidden. Toggle is `track.applyConstraints({ advanced: [{ torch: true }] })`. The button must be hidden, not just disabled, when unsupported, because Safari throws on `getCapabilities` if undefined.
- **`facingMode` quirks**: per [MDN getUserMedia examples](https://developer.mozilla.org/en-US/docs/Web/API/MediaDevices/getUserMedia#front_and_back_camera) — "it may be necessary to release the current camera facing mode before you can switch to a different one… invoke `stop()` on the track before requesting a different facing mode". This is the source of the classic "switching cameras hangs" bug.
- **Constraint failure modes** (from MDN): `NotAllowedError` (user denied), `NotFoundError` (no matching device), `NotReadableError` (HW busy / OS-level error), `OverconstrainedError` (e.g. `facingMode: { exact: 'environment' }` on a laptop with no rear camera), `AbortError`, `InvalidStateError` (document not active — e.g. tab hidden when call fires).

### Implications for CartLedger

- **Keep `@zxing/browser` for now** — its API is well-known and the Grocy/OFF reference flows use it. But add an internal escape hatch: feature-detect `'BarcodeDetector' in window` and use it as a fast path when supported; fall back to ZXing. Also pin a migration target: `zxing-wasm` via the `barcode-detector` polyfill, since the ZXing team is steering users there ([#127](https://github.com/zxing-js/browser/issues/127)).
- **Lock formats to grocery reality** — restrict the decoder to `UPC_A, UPC_E, EAN_13, EAN_8, ITF (GTIN-14), CODE_128`. Drop QR/Aztec/Data Matrix/PDF417. This cuts decoder work, false positives, and decoder bundle weight (use `BrowserMultiFormatOneDReader` instead of `BrowserMultiFormatReader`).
- **Cleanup order in the modal**: `controls.stop()` → iterate `videoElem.srcObject.getTracks()` and call `track.stop()` on each → `videoElem.srcObject = null` → null out the reader. Wire this to React's `useEffect` cleanup *and* a Page Visibility listener (`document.visibilitychange` → stop on `hidden`) — fixes the "camera light stays on after browser tab loses focus" class of bug.
- **For camera selection, use the layered fallback**: `getUserMedia({ video: { facingMode: { ideal: 'environment' } } })` first (broadest compatibility), not `{ exact: 'environment' }` which throws `OverconstrainedError` on laptops. On success, remember the resulting `deviceId` and prefer it next time. Skip `enumerateDevices` until *after* the first `getUserMedia` succeeds — otherwise device labels are empty and you can't distinguish front/rear (MDN: privacy-mode behavior pre-permission).
- **Hide torch by default**; only show after `track.getCapabilities().torch === true`.

---

## 3. UX patterns for "scan → confirm → next" workflows

### Established patterns from comparable apps

- **MyFitnessPal** (food log): scan → barcode is decoded → result card replaces the scanner with the product (or "Not Found, add manually") → user explicitly taps "Add" or chooses a meal slot. Auto-add is reserved for the "rapid log" mode that the user must enable. The pattern is **scan-then-confirm**, never scan-then-commit.
- **Open Food Facts mobile (smooth-app)**: continuous scanner that overlays detected matches as cards that slide up; the user has to tap a card to drill in. Decoded barcodes that aren't in the DB show an "Add this product" CTA rather than failing silently. ([openfoodfacts/smooth-app](https://github.com/openfoodfacts/smooth-app))
- **Yuka**: scan-then-confirm — even after a successful decode, the full product page must be loaded and the user explicitly confirms an action ("Add to history"). No silent add.
- **Grocy**: barcode scan **dismisses the modal on first decode** and emits a custom event with the result and target field — `$(document).trigger("Grocy.BarcodeScanned", [result.getText(), Grocy.Components.CameraBarcodeScanner.CurrentTarget])`. The receiving form then populates and lets the user inspect *before* submission. Auto-advance is event-driven, not auto-commit. ([`camerabarcodescanner.js`](https://github.com/grocy/grocy/blob/master/public/viewjs/components/camerabarcodescanner.js))
- **Barcode Buddy** (the Grocy companion that handles HID scanners): the *whole product* is the confirmation layer — it sits in front of Grocy and lets the user tag the barcode (`consume`, `purchase`, etc.) before forwarding. The lesson: even with a hardware scanner, projects insert an explicit semantic step rather than committing on raw scan. ([Forceu/barcodebuddy](https://github.com/Forceu/barcodebuddy))

### Consensus on auto-advance vs manual confirm

- Auto-advance is acceptable **when the scan only populates a field**, not when it creates/links data. Grocy auto-closes the scanner modal on first decode, then sits the value in a form for review. None of the surveyed apps both decode *and* commit a row from a single tap.
- Result-state UI converges on four states: **scanning** (live preview, no result yet), **found** (product card, primary action = confirm), **not-found-but-decoded** (barcode value visible, primary action = "create with this UPC"), **error/permission** (camera failure or denied, primary action = manual entry).
- Manual entry should be **always visible**, not revealed on failure. Grocy puts the camera select dropdown *inside* the bootbox dialog; Barcode Buddy keeps the manual text input as the primary mode and treats hardware scanning as enrichment.

### Implications for CartLedger

- **Phase 3.5's preview/apply split aligns with industry consensus** — a single decode should not silently link a row to a product. Preview = "found state", apply = explicit confirm. Keep this even though it adds a round-trip.
- **Auto-advance only the *cursor*, not the *commit***. After confirm, dismiss the modal and move focus to the next eligible row's barcode button, but do not auto-open the camera on the next row. Re-opening the camera is a permission/UX hit the user should re-invoke explicitly (and it lets them stop, look at the receipt, then continue).
- **Show manual UPC input below the live preview**, always. Match Grocy's pattern — manual entry is not a fallback path, it's a coequal input mode.
- **Use four distinct UI states** as above; the plan's draft already names them — keep this granularity rather than collapsing into found/not-found.

---

## 4. USB / hardware barcode scanner support in browsers

- **Most USB scanners enumerate as HID keyboards** — they "type" the decoded value into the focused input, optionally with a configurable suffix (CR, LF, Tab) and prefix.
- **Classic pitfall**: the suffix is `Enter`, which fires form `submit` *before* the application has parsed the barcode. Standard mitigations:
  - **Hidden input that always has focus**, with a debounce window (e.g. 50 ms) — characters that arrive faster than human typing are treated as a scan; the buffer is flushed on a configurable suffix (typically `Enter`) or after a timeout, *not* by letting the keypress event bubble to a form. Barcode Buddy implements this as its scanner-mode — see [Forceu/barcodebuddy](https://github.com/Forceu/barcodebuddy).
  - **Prefix-based mode switching**: configure scanners to emit a prefix (e.g. `*`) before each scan; the page maintains a "in-scan" state and ignores normal keyboard input until the suffix arrives.
  - **WebHID** (Chromium-only) lets you treat the scanner as a non-keyboard HID device and read raw report data — avoids keyboard-event pollution entirely. Limited browser support and prompts for explicit device authorization, so usually a power-user feature.
- **Grocy + Barcode Buddy** is the canonical OSS reference architecture for HID scanner support in a grocery app. Barcode Buddy is essentially a long-running input collector that proxies into Grocy's REST API. ([Forceu/barcodebuddy](https://github.com/Forceu/barcodebuddy))

### Implications for CartLedger

- **Out of scope for Phase 3.5 — say so explicitly.** Plan only covers camera scanning. Defer HID/USB support to a future phase.
- **If/when added: implement as a "scanner mode" toggle in user settings**, which adds a hidden always-focused input + debounce/suffix detection. Do NOT just rely on a regular input field — the form-submit-on-Enter bug is a guaranteed regression vector.
- **Add a barcode-input shape check** at the API layer regardless of input source: reject anything that isn't 8/12/13/14 digits with a valid GS1 check digit. This makes hardware-scanner integration drop-in later because the validator already exists.

---

## 5. Preview/apply vs straight-apply API design

### Industry precedents

- **Stripe's PaymentIntent / SetupIntent**: create-then-confirm is a deliberate two-step. Create sets up the resource and returns a `client_secret`; confirm advances state and may yield `requires_action`, `processing`, or `succeeded`. The two-step exists because the *side effects* (charging, saving a card) need a moment of explicit consent and may need a `next_action` step (3DS, etc.). ([Stripe Setup Intents](https://docs.stripe.com/payments/setup-intents), [Payment status updates](https://docs.stripe.com/payments/payment-intents/verifying-status))
- **GitHub merge preview** (`GET /repos/:owner/:repo/pulls/:n` returns `mergeable`/`mergeable_state` flags) — preview is a *read* not a separate POST. Apply is the existing merge endpoint.
- **IETF idempotency draft** explicitly says: for non-idempotent POST/PATCH, use `Idempotency-Key` and let the server distinguish first-time vs retry vs concurrent vs different-payload-same-key (`409`/`422`). The preview/apply split is *one way* to model this; an idempotent POST with a key is another. ([draft](https://datatracker.ietf.org/doc/html/draft-ietf-httpapi-idempotency-key-header))

### Judgment of the plan's preview/apply split

- **Justified** when (a) the apply side has irreversible side effects (creates a product row, links a line item, schedules a job) and (b) the response surface is rich enough that the UI needs to render confirmation state before committing.
- **Overengineered** when the same idempotency property could be achieved with a single POST + `Idempotency-Key` and a structured response that includes `dry_run: true` flag.
- For CartLedger's case the preview/apply split *is* defensible because:
  - Apply creates a new `product` row in a write-heavy SQLite single-writer model — running it twice produces two products with the same UPC.
  - Preview lets the frontend show the "found in DB" vs "create new" disambiguation without committing.
  - But it should be **augmented with an `Idempotency-Key` on the apply call**: even with the split, a user can double-tap "Confirm".

### Implications for CartLedger

- **Keep the preview/apply split as designed** — the irreversible-side-effect criterion is met.
- **Add an `Idempotency-Key` header** (UUID generated client-side per confirm click, scoped to receipt-id + line-item-id + barcode) to the apply endpoint. On replay return the original response. Store keys for ~24h.
- **Use `409` for concurrent retries (still running), `422` for mismatched body (same key, different payload), `200` with cached body for completed retries** — match the IETF draft so future clients (e.g. a CLI) get standardized behavior.
- **Consider a partial unique index on `products(household_id, upc)`** so the DB enforces the no-duplicate invariant even if the idempotency layer is bypassed.

---

## 6. Camera permission UX

- **Secure context required**: `getUserMedia` only works on HTTPS. On HTTP it doesn't fail with a clear error — the API may simply be `undefined`. Grocy surfaces this explicitly in its error toast: *"Camera access is only possible when supported and allowed by your browser and when Grocy is served via a secure (https://) connection"*. ([camerabarcodescanner.js](https://github.com/grocy/grocy/blob/master/public/viewjs/components/camerabarcodescanner.js); [MDN getUserMedia secure context](https://developer.mozilla.org/en-US/docs/Web/API/MediaDevices/getUserMedia))
- **Permission revocation is not programmatic** — per [MDN Permissions API](https://developer.mozilla.org/en-US/docs/Web/API/Permissions_API): `Permissions.revoke()` was removed from browsers that briefly implemented it. Once a user denies camera, the only recovery is to navigate to the browser's per-site settings. Apps cannot re-prompt programmatically.
- **Pre-permission state**: `enumerateDevices` returns devices with empty `label` until permission is granted. `BrowserCodeReader.listVideoInputDevices()` on macOS/iOS does not trigger the prompt itself; the page must call `getUserMedia` first. ([zxing-js/browser#97](https://github.com/zxing-js/browser/issues/97))
- **Best practices for permission copy** (synthesized from MDN guidance and the Grocy/OFF flows):
  - Explain *why* before the prompt fires (a one-line caption next to the "Scan" button).
  - Trigger the prompt from a user gesture (button click) — never on page load or on focus.
  - Detect `state: 'denied'` via `navigator.permissions.query({ name: 'camera' })` and show recovery copy with a link to browser docs.
  - Don't repeatedly retry — browsers will mark you as abusive and auto-deny.

### Implications for CartLedger

- **Pre-flight check on scan button click**: `navigator.permissions.query({ name: 'camera' as PermissionName })` → if `denied`, show inline copy ("Camera is blocked. To re-enable: tap the address bar lock icon → Site settings → Camera → Allow.") rather than calling `getUserMedia` and getting a silent `NotAllowedError`.
- **HTTPS requirement is real for self-hosted**: surface a one-time setup warning if `location.protocol === 'http:'` and `location.hostname !== 'localhost'`. This is a common new-user trap for CartLedger's audience.
- **One-sentence permission rationale** next to the scan button: *"Allow camera access to scan barcodes from packages. CartLedger never uploads or stores the camera feed."* Sets expectations and shifts perceived trust before the OS prompt fires.
- **Never auto-open the camera on page load**; require an explicit user click each session.

---

## 7. Things you'd wish you knew before building this

- **`time.Tick` leaks in tests** — `time.Tick` returns a channel that's never garbage-collected. Use `time.NewTicker` + explicit `Stop()` in production (Miniflux's `time.Tick` is fine because the goroutine lives the daemon's lifetime). If you accept a context, drive the loop via `select { case <-ticker.C: ...; case <-ctx.Done(): return }` so tests can cancel.
- **SQLite single-writer model bites batch sweeps**. The nightly sweep + a user receipt scan are both writers — even short-lived jobs serialize. Mitigate by (a) batching reads outside the write transaction, (b) using SQLite WAL mode if not already, (c) deliberately spacing sweep batches (Linkding does 5-at-a-time per minute rather than 1000-at-once nightly).
- **Camera light staying on is the visible-trust bug**. Users notice the light/indicator more than they notice CPU usage. Test specifically: open scanner → close modal → check OS-level indicator → switch browser tab → return → check again. Per [MDN MediaStreamTrack.stop](https://developer.mozilla.org/en-US/docs/Web/API/MediaStreamTrack/stop), if any other tab still references the same source, it stays alive — so even calling `stop()` correctly doesn't guarantee the light goes off if a second scanner instance leaked earlier.
- **iOS Safari `getUserMedia` regressions are silent**. Versions 15.x had a streak of issues where the prompt fired but the resolved MediaStream had `readyState: 'ended'` immediately. Defensive: after `getUserMedia` resolves, check `track.readyState === 'live'` before passing to ZXing.
- **`BarcodeDetector` is misleadingly named "supported"**. `'BarcodeDetector' in window` is true on macOS Chrome, but `getSupportedFormats()` may not return UPC-A. Always feature-detect by format, not by interface. ([Chrome dev article](https://developer.chrome.com/articles/shape-detection), [MDN](https://developer.mozilla.org/en-US/docs/Web/API/Barcode_Detection_API))
- **Receipt UPCs are usually truncated**. Many grocery receipts print 11-digit "internal" SKU codes, not the full 12-digit UPC-A. Even when the package has a barcode, the receipt won't necessarily match. Plan must treat receipt-line UPC and package barcode as two distinct identifiers, not collapse them.
- **Open Food Facts coverage skews EU**. US store-brand products (Kroger, Trader Joe's, Costco Kirkland) often miss; expect a substantial "decoded but not in DB" rate. The "create new product with this UPC" path is *the common path*, not the edge case. ([openfoodfacts-server issues](https://github.com/openfoodfacts/openfoodfacts-server/issues))
- **`@zxing/browser` is in soft maintenance**. The maintainers themselves point at `zxing-wasm` for new users ([zxing-js/browser#127](https://github.com/zxing-js/browser/issues/127)). Don't fork ZXing locally; if you hit a blocker, migrate.
- **Lazy-loading the scanner module is mandatory for bundle size**. ZXing's multi-format reader is ~150-200 KB minified+gzipped. A Vite `() => import('@zxing/browser')` dynamic import inside `BarcodeScannerModal` keeps it out of the main bundle for users who never scan.
- **Concurrency limit your provider calls**. Open Food Facts has soft rate limits (~100 req/min per IP) and USDA FoodData Central is 1000 req/hour by API key. A burst sweep at 50 products in parallel will hit them. Use `gocron`'s `WithLimitConcurrentJobs` or a manual semaphore — and per-provider, not global.
- **Don't queue enrichment jobs from inside the receipt-write transaction.** Defer until the transaction commits (use `db.AfterCommit` style pattern or push to an in-memory channel). Otherwise a failed worker DB call rolls back the receipt write.
- **State machine for snapshots**: `pending` → `running` → `succeeded` / `failed (transient)` / `failed (permanent)`. Transient failures retry with exponential backoff (Linkding: 60s, 240s, 960s, 3840s, 15360s — `retry_delay *= retry_backoff` with backoff=4); permanent failures (404 from provider) skip retry. Keep them separately countable in metrics.

---

## Source map

| Source label | URL / path |
| --- | --- |
| `miniflux-scheduler-real` | https://github.com/miniflux/v2/blob/main/internal/cli/scheduler.go |
| `miniflux-daemon` | https://github.com/miniflux/v2/blob/main/internal/cli/daemon.go |
| `miniflux-pool` | https://github.com/miniflux/v2/blob/main/internal/worker/pool.go |
| `miniflux-worker` | https://github.com/miniflux/v2/blob/main/internal/worker/worker.go |
| `miniflux-refresh-feeds` | https://github.com/miniflux/v2/blob/main/internal/cli/refresh_feeds.go |
| `linkding-tasks` | https://github.com/sissbruecker/linkding/blob/master/bookmarks/services/tasks.py |
| `robfig-cron-readme` | https://github.com/robfig/cron |
| `gocron-readme` | https://github.com/go-co-op/gocron |
| `grocy-camerabarcodescanner` | https://github.com/grocy/grocy/blob/master/public/viewjs/components/camerabarcodescanner.js |
| `zxing-browser-readme` | https://github.com/zxing-js/browser |
| `zxing-issues` (#97, #128, #131, #137, #140, #127) | https://github.com/zxing-js/browser/issues |
| `html5-qrcode-readme` | https://github.com/mebjas/html5-qrcode |
| `quaggajs-readme` | https://github.com/serratus/quaggaJS |
| `chrome-shape-detection` | https://developer.chrome.com/articles/shape-detection |
| `mdn-barcode-detection` | https://developer.mozilla.org/en-US/docs/Web/API/Barcode_Detection_API |
| `mdn-getusermedia` | https://developer.mozilla.org/en-US/docs/Web/API/MediaDevices/getUserMedia |
| `mdn-mediastream-stop` | https://developer.mozilla.org/en-US/docs/Web/API/MediaStreamTrack/stop |
| `mdn-permissions-api` | https://developer.mozilla.org/en-US/docs/Web/API/Permissions_API |
| `stripe-idempotency` | https://docs.stripe.com/api/idempotent_requests |
| `stripe-setup-intents-2` | https://docs.stripe.com/payments/setup-intents |
| `stripe-confirm` | https://docs.stripe.com/payments/payment-intents/verifying-status |
| `rfc-idempotency-key` | https://datatracker.ietf.org/doc/html/draft-ietf-httpapi-idempotency-key-header |
| `barcode-buddy-correct` | https://github.com/Forceu/barcodebuddy |
| `openfoodfacts-smooth-app` | https://github.com/openfoodfacts/smooth-app |
| `off-server-readme` | https://github.com/openfoodfacts/openfoodfacts-server |
