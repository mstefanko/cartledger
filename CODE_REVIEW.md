# Code Review: CartLedger

### Verdict: NEEDS_CHANGES

---

## Scope Reviewed

All 44 Go source files and 52 TypeScript/React source files read in full across the 5 implementation phases.

**Backend (all read fully):**
- `internal/api/*.go` (auth, products, receipts, lists, analytics, stores, matching, aliases, conversions, export, import, ws, router)
- `internal/auth/*.go` (jwt, middleware, password)
- `internal/config/config.go`
- `internal/db/*.go` (sqlite, migrate, migrations/001_initial.up.sql)
- `internal/llm/client.go`
- `internal/matcher/*.go` (engine, rules, fuzzy, normalizer)
- `internal/mealie/client.go`
- `internal/units/*.go` (converter, parser, price)
- `internal/worker/receipt.go`
- `internal/ws/*.go` (hub, messages)
- `cmd/server/main.go`

**Frontend (all critical paths read fully):**
- `web/src/api/*.ts` (client, ws, auth, lists, products, receipts, matching, analytics, stores, conversions, import)
- `web/src/hooks/*.ts` (useAuth, useWebSocket)
- `web/src/pages/*.tsx` (ShoppingListPage, DashboardPage, ReceiptReviewPage)
- `web/src/components/receipts/ReceiptReview.tsx`
- `web/src/types/index.ts`

---

## Critical Issues

### 1. [CRITICAL] `internal/matcher/rules.go:56` -- ReDoS via user-controlled regex

The `"matches"` condition_op compiles a user-provided string as a regular expression via `regexp.Compile(conditionVal)`. Any authenticated user can create a matching rule with `condition_op: "matches"` and supply a catastrophic backtracking pattern (e.g., `(a+)+$`). This regex is evaluated on every receipt scan for every line item against every rule.

**Impact:** Denial of service -- a single malicious or accidentally bad regex pattern can hang the receipt processing worker indefinitely.

**Suggested fix:** Use `regexp.CompilePOSIX` (which disallows backtracking) or set a compile/match timeout. Alternatively, validate the regex complexity at rule creation time in `matching.go:250` and reject patterns with known pathological constructs. At minimum, wrap the match in a goroutine with a timeout.

### 2. [CRITICAL] `internal/api/conversions.go:51,96,144` -- Missing household scoping on unit_conversions

The `unit_conversions` table has no `household_id` column (confirmed in `migrations/001_initial.up.sql:148-155`). The `ConversionHandler.List()` at line 51 calls `auth.HouseholdIDFrom(c)` but discards it (`_ = auth.HouseholdIDFrom(c)`). The `Create()` at line 96 and `Delete()` at line 144 do the same.

**Impact:** Any authenticated user from any household can read, create, and delete unit conversions belonging to other households. A user in Household A can delete product-specific conversions owned by products in Household B.

**Suggested fix:** Add `household_id` to `unit_conversions` table (migration), or at minimum JOIN through `products` table on `product_id` to verify household ownership on all CRUD operations. For generic conversions (`product_id IS NULL`), consider making them system-wide by design and documenting that decision.

### 3. [CRITICAL] `internal/api/products.go:235-248` -- Product image deletion leaks files across households / path traversal potential

At line 235, `product_images` are queried by `product_id` alone (no household scoping) before the product ownership check at line 250. More critically, the `image_path` stored in the database is joined with `DataDir` at line 241: `fullPath := filepath.Join(h.Cfg.DataDir, imagePath)`. If a malicious image_path were stored (e.g., via SQL injection into `product_images`), it could lead to arbitrary file deletion. While this requires a prior compromise, the real bug is that image file cleanup at lines 235-248 executes **before** the household ownership check at line 250-253.

**Impact:** If the product does not belong to the household but images exist, the files are deleted from disk but the DB row deletion fails. This creates an inconsistent state (orphaned DB rows pointing to deleted files). The `os.RemoveAll(productDir)` at line 248 also runs unconditionally.

**Suggested fix:** Move the image cleanup after the ownership verification. Check `household_id` on the product before doing any file operations:
```go
// First verify ownership
result, err := h.DB.Exec("DELETE FROM products WHERE id = ? AND household_id = ?", productID, householdID)
// Then clean up files only if the delete succeeded
```

### 4. [CRITICAL] `internal/api/auth.go:98-103` -- Setup race condition allows duplicate household creation

The `Setup` handler uses `h.DB.Begin()` (line 99) which starts a DEFERRED transaction in SQLite, not an IMMEDIATE one. The comment at line 105 acknowledges this limitation. Between the `SELECT COUNT(*)` at line 108 and the `INSERT` at line 124, another concurrent request could also see `count == 0` and both would create households.

**Impact:** Two concurrent setup requests can both succeed, creating two households with two users. The first user would then be orphaned in a household they cannot invite others to (since the frontend would redirect based on the second setup's token).

**Suggested fix:** Use `tx.Exec("BEGIN IMMEDIATE")` before any reads, or use a mutex in the handler for this one-time operation. Since `modernc.org/sqlite` supports `BEGIN IMMEDIATE` via DSN parameters, configure the connection string to use `_txlock=immediate`.

### 5. [CRITICAL] `internal/worker/receipt.go:263` -- NormalizePrice called with `w.db` (main connection) inside a transaction

At line 263, `units.NormalizePrice(totalPrice, quantity, unit, *productID, w.db)` is called with the main database handle `w.db`, but the code is inside a transaction (`tx`) that holds a write lock. The `NormalizePrice` function internally calls `Convert()` which queries `unit_conversions` using the passed `db` handle. Under SQLite's WAL mode, reads can proceed concurrently with a write transaction, so this is not a deadlock. However, it reads potentially stale data -- the product-specific conversions might have been modified within the transaction.

**Impact:** Normalized prices could be calculated with stale conversion factors. This is a data integrity issue, not a crash.

**Suggested fix:** Pass `tx` instead of `w.db` to `units.NormalizePrice()`, or accept this as a minor consistency trade-off and document it.

---

## Warnings

### 6. [WARNING] `internal/api/lists.go:257-281` -- N+1 query for cheapest store prices

The `Get()` handler fetches list items in one query (line 217-228), then for EACH item with a `product_id`, executes an individual query to find the cheapest store (lines 263-275). With 50 items on a shopping list, this is 51 queries.

**Suggested fix:** Use a single query with a CTE or subquery that joins `shopping_list_items` to `product_prices` to get cheapest store in one pass. Or use a LEFT JOIN LATERAL equivalent in SQLite.

### 7. [WARNING] `internal/api/analytics.go:131-132` -- Incorrect month arithmetic for January

At line 132, `time.Date(now.Year(), now.Month()-1, 1, ...)` when `now.Month()` is January (1) computes `Month(0)` which Go normalizes to December of the **previous year**. This actually works correctly in Go. However, the query at line 148 uses `receipt_date >= ? AND receipt_date < ?` which correctly bounds last month. No bug here after verification.

**[Retracted after verification]**

### 8. [WARNING] `internal/api/analytics.go:137-142` -- Unchecked error on analytics Overview queries

Lines 137-142: `h.DB.QueryRow(...).Scan(...)` -- the error return is completely ignored. If the query fails, `resp.TotalSpentThisMonth` and `resp.TripCountThisMonth` silently remain zero. Same pattern at lines 145-149, 163-169.

**Impact:** Silent data loss in analytics. User sees $0 spent with no error indication.

**Suggested fix:** Check errors and return 500 if the query fails, or at minimum log them.

### 9. [WARNING] `internal/api/analytics.go:282-300` -- SQL injection via sort field

At line 292-300, `orderClause` is constructed from user input `sortField` via a switch statement, which is safe because it maps to known values. However, the `order` variable at line 283-285 is only validated as not-"asc" (defaulting to "desc"), and is then interpolated directly into SQL at line 292: `fmt.Sprintf("percent_change %s", order)`. Since `order` can only be "asc" or "desc" (line 284 defaults anything non-"asc" to "desc"), this is technically safe. However, if the validation logic were ever changed, it would become injectable.

**Impact:** Currently safe but fragile. One-character change to the validation would create SQL injection.

**Suggested fix:** Whitelist both values explicitly: `if order != "asc" && order != "desc" { order = "desc" }`.

### 10. [WARNING] `internal/api/products.go:99-109` -- FTS5 search query passes user input directly to MATCH

At line 107, user-provided query parameter `q` is passed directly to `products_fts MATCH ?`. SQLite FTS5 MATCH syntax supports special operators like `NOT`, `OR`, `AND`, `*`, `NEAR()`, column filters, etc. A user can craft queries like `* NOT something` or use prefix queries that scan the entire index.

**Impact:** Not SQL injection (it's parameterized), but a user can cause expensive FTS5 operations or unexpected search results. A query like `"*"` would match all rows.

**Suggested fix:** Sanitize FTS5 query input by escaping special characters, or wrap the user input in double quotes to force phrase matching: `'"' + escapedQ + '"'`.

### 11. [WARNING] `internal/api/receipts.go:100-103` -- No limit on number of uploaded receipt images

The `Scan()` handler at line 100 reads `form.File["images"]` with no upper bound on the number of files. A malicious user could upload hundreds of images in a single request.

**Impact:** Memory exhaustion, disk space exhaustion, and LLM API cost amplification.

**Suggested fix:** Add a maximum file count check (e.g., `if len(files) > 10 { return error }`).

### 12. [WARNING] `internal/worker/receipt.go:56-58` -- Blocking Submit on full job channel

At line 57, `w.jobs <- job` is a blocking send. The channel has a buffer of 100 (line 42). If 100 receipt jobs are already queued, the HTTP handler thread calling `Submit()` will block indefinitely.

**Impact:** Under load, the `/receipts/scan` endpoint could hang, eventually exhausting all HTTP handler goroutines.

**Suggested fix:** Use a `select` with a `default` case to return a "server busy" error, or use a larger buffer with monitoring.

### 13. [WARNING] `internal/api/ws.go:14-22` -- WebSocket CheckOrigin allows all origins

At line 19-21, `CheckOrigin` returns `true` for all requests. Combined with the JWT token passed as a query parameter, this means any website the user visits could initiate a WebSocket connection to CartLedger if the token is known (e.g., from XSS).

**Impact:** WebSocket hijacking if token is leaked. The comment says "reverse proxy handles origin validation" but this is not enforced.

**Suggested fix:** In production (when `JWTSecret != "change-me-in-production"`), validate the Origin header against the application's own origin.

### 14. [WARNING] `internal/api/analytics.go:462-496` -- Trips endpoint has no LIMIT, returns all receipts

The `Trips()` handler queries all receipts for the household with no pagination. Over time, this grows unboundedly.

**Impact:** Slow responses, high memory usage, large JSON payloads as receipt history grows.

**Suggested fix:** Add pagination parameters like `ProductsWithTrends` does, or at minimum a `LIMIT 100` default.

### 15. [WARNING] `internal/api/analytics.go:500-549` -- Deals endpoint with no LIMIT

Same issue as Trips -- the Deals query returns all products with prices below 85% of average, with no limit.

### 16. [WARNING] `web/src/api/ws.ts:10-11` -- JWT token exposed in WebSocket URL

At line 11, the JWT token is placed in the WebSocket URL as a query parameter: `?token=${encodeURIComponent(token ?? '')}`. This is a known limitation of the WebSocket API (it cannot send custom headers). However, the token will appear in server access logs, browser history, and any intermediary proxy logs.

**Impact:** Token leakage via logs. In a Docker Compose self-hosted setup, this is low risk but worth noting.

**Suggested fix:** Document this trade-off. Consider using a short-lived WebSocket ticket instead of the long-lived JWT.

### 17. [WARNING] `internal/config/config.go:28` -- Default JWT secret is a known string

At line 28, the default JWT secret is `"change-me-in-production"`. If a user deploys without setting `JWT_SECRET`, all tokens are signed with this public value, allowing anyone to forge tokens.

**Impact:** Complete authentication bypass if deployed with defaults.

**Suggested fix:** On startup, if `JWT_SECRET` is the default value, either (a) log a prominent warning, (b) refuse to start, or (c) auto-generate a random secret and persist it to the data directory.

### 18. [WARNING] `internal/api/auth.go` -- No rate limiting on login/setup/join endpoints

The `Login`, `Setup`, and `Join` endpoints have no rate limiting. An attacker can brute-force passwords without throttling.

**Impact:** Password brute-force attacks on the login endpoint.

**Suggested fix:** Add rate limiting middleware (e.g., `echo-contrib/rate` or a simple token bucket) on the auth routes. Even a simple per-IP limit of 10 requests/minute would help.

### 19. [WARNING] `web/src/components/receipts/ReceiptReview.tsx:384-386` -- Unsafe JSON.parse of raw LLM JSON

At line 385, `JSON.parse(receipt.raw_llm_json)` is called inside a `<pre>` tag. If parsing fails (malformed JSON from LLM), the entire component will throw an unhandled error and crash the React tree.

**Suggested fix:** Wrap in try/catch: `try { JSON.stringify(JSON.parse(receipt.raw_llm_json), null, 2) } catch { receipt.raw_llm_json }`.

### 20. [WARNING] `internal/api/products.go:99-116` -- Product list returns ALL products with no pagination

The `List()` handler returns all products for the household when no search query is provided (line 111-116). No LIMIT or pagination.

**Impact:** As the product catalog grows (hundreds of products), response sizes and query times increase unboundedly.

---

## Info

### 21. [INFO] `internal/matcher/fuzzy.go:57-83` -- Fuzzy matching loads ALL aliases and products into memory

The `matchByFuzzy` function queries all product aliases and all product names for the household, loads them into a Go slice, then iterates over all of them computing Levenshtein distances. For a household with thousands of products, this is O(n * m) where n is candidates and m is the string length.

**Suggested fix (future):** Consider SQLite FTS5 or a trigram index for fuzzy search instead of loading everything into memory.

### 22. [INFO] `internal/units/parser.go:106-112` -- parseUint panics on invalid input

The `parseUint` function at line 106 has no bounds checking and will panic on non-digit characters. The comment says it's only called with regex-validated strings, which is true, but a future refactor could introduce a bug.

### 23. [INFO] `internal/ws/hub.go:167-173` -- WebSocket write pump concatenates multiple messages with newlines

At lines 169-172, queued messages are concatenated with `\n` separators in a single WebSocket frame. The client at `web/src/api/ws.ts:27-31` parses `event.data` as a single JSON object. If multiple messages are batched, only the first will parse correctly; the rest will be silently dropped.

**Impact:** Under high message throughput, real-time updates could be lost.

**Suggested fix:** Send each message as a separate WebSocket frame, or have the client split on newlines before parsing.

### 24. [INFO] `internal/api/analytics.go:558-589` -- BuyAgain query uses LEAD window function correctly

The Buy Again algorithm uses `LEAD(pp.receipt_date) OVER w` and `LAST_VALUE` with proper window framing (`ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING`). The `HAVING COUNT(*) >= 2` ensures sufficient data points. The query is correct.

### 25. [INFO] `web/src/hooks/useAuth.ts:85` -- isAuthenticated based solely on token presence

At line 85, `isAuthenticated: token !== null` does not verify the token is valid or unexpired. An expired token in localStorage will show the user as authenticated until the first API call returns 401.

**Impact:** Brief flash of authenticated UI before redirect to login when token expires.

### 26. [INFO] Multiple files -- Consistent use of parameterized queries throughout

All SQL queries across the entire codebase use parameterized queries (`?` placeholders). No string concatenation with user input in SQL was found. This is commendable.

### 27. [INFO] `internal/api/matching.go:56` -- "matches" regex condition_op creates user-supplied regex

This is documented in Critical #1 above as a ReDoS vector, but also note that the `regexp.Compile` at `rules.go:56` creates a new compiled regex on every evaluation of every rule for every line item. No caching of compiled regexes.

---

## Positive Findings

- **`internal/auth/jwt.go:68-86`** -- Excellent JWT security: algorithm is explicitly checked (`*jwt.SigningMethodHMAC`), token type is verified, and separate claim types prevent token confusion between auth and invite tokens.

- **`internal/db/sqlite.go:27-34`** -- Well-chosen SQLite pragmas: WAL mode, NORMAL synchronous, foreign keys ON, generous cache and mmap settings. This is production-ready SQLite configuration.

- **`internal/api/products.go:475-623`** -- The product merge operation is thorough and correct: it runs in a single transaction, covers all 8 FK tables (aliases, line_items, prices, shopping list items, rules, images, links), aggregates purchase stats properly, and uses `MAX(last_purchased_at)` correctly.

- **`web/src/pages/ShoppingListPage.tsx:93-142`** -- Optimistic updates with rollback on error for the toggle-check mutation. This is well-implemented with `onMutate`/`onError`/`onSettled` following React Query best practices.

- **`internal/db/migrations/001_initial.up.sql:183-217`** -- FTS5 sync triggers are complete and correct, covering INSERT, UPDATE, and DELETE for both products and aliases tables. The UPDATE trigger correctly deletes the old entry before inserting the new one.

- **Consistent household scoping** -- The vast majority of queries correctly filter by `household_id`. The only exception found is the `unit_conversions` table (Critical #2).

- **`web/src/api/client.ts`** -- Clean, minimal fetch wrapper with proper 401 handling, token management, and correct Content-Type handling for multipart uploads (omitting Content-Type to let the browser set the boundary).

---

## Production Risk

1. **WebSocket message batching** (Info #23): Under concurrent list collaboration (the primary real-time use case), rapidly firing events could get batched into a single frame. The frontend parser would drop all but the first, causing stale UI for the other user. This is hard to reproduce in testing but would surface with two users actively checking items simultaneously.

2. **Receipt worker blocking** (Warning #12): If the LLM API is slow or down, the 100-job buffer fills quickly. Subsequent scan requests from the HTTP handler would block indefinitely, eventually causing the entire server to become unresponsive. There is no timeout on the `Submit()` call.

3. **Default JWT secret** (Warning #17): The most likely production issue. A user following the Docker Compose setup who forgets to set `JWT_SECRET` has a completely compromised deployment. The CORS configuration does check for the default secret (router.go:32) but only for CORS policy, not authentication.

4. **Unit conversion cross-household access** (Critical #2): If this app is ever deployed in a multi-household scenario (the plan mentions "single household per deployment for v1"), any user can manipulate another household's unit conversions, corrupting their price normalization.

---

## Out of Scope / Not Reviewed

- **`internal/llm/claude.go`, `internal/llm/prompt.go`, `internal/llm/types.go`**: LLM prompt engineering and API integration. Not reviewed for prompt injection or output validation. The LLM client is behind an interface, so the mock is sufficient for structural review.
- **`web/src/components/ui/EditableTable/*.tsx`**: Complex table component internals. Read the interface (`index.ts`) but did not deep-review the cell editing logic for correctness.
- **CSS/Tailwind classes**: Not reviewed for accessibility or responsive design correctness.
- **`internal/llm/mock.go`, `internal/llm/mock_test.go`**: Test fixtures, not production code.
- **Node modules and build configuration** (`vite.config.ts`, `package.json`): Not reviewed.

---

## Confidence: HIGH
## Status: COMPLETE
