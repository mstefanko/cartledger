# Changelog

All notable changes to CartLedger follow [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased] — self-hosting v1.0

### Breaking — action required on deploy

**All existing logged-in users are signed out on first boot after upgrade.**
Session JWTs previously stored in the browser's `localStorage` are no longer
sent by the SPA. Users open the app and are redirected to `/login`, where
re-authentication reissues the session as a cookie. No data is affected; only
the session format changed.

**New required environment variable in production:**
- `ALLOWED_ORIGINS` — comma-separated list of exact origins the API trusts
  for CORS and WebSocket upgrades, e.g. `https://cartledger.example.com`.
  The server refuses to boot if unset with `CARTLEDGER_ENV=production`.
  Dev (`CARTLEDGER_ENV≠production`) falls back to localhost variants.

**New recommended environment variables:**
- `TRUST_PROXY` — comma-separated CIDRs (or `loopback` / `private` macros)
  whose `X-Forwarded-*` headers the server honors. Required behind Caddy /
  Nginx / Traefik for correct client IP and `Secure`-cookie detection.
  If unset behind a proxy, rate limits key on `127.0.0.1` and cookies
  lose the `Secure` flag.
- `JWT_SECRET` — in prod (`CARTLEDGER_ENV=production` or `PROD=true`) the
  server refuses to boot with an empty, default, or < 16-char secret.
  In dev, a random 32-byte hex secret is generated per boot and logged
  with a warning ("sessions will invalidate on restart").
- `LOG_LEVEL` / `LOG_FORMAT` — `debug|info|warn|error` and `json|text`
  (default `info` + `json`).
- `RATE_LIMIT_ENABLED` — default `true`. Set to `false` only for local
  testing.

**New first-run bootstrap flow:**
When the `users` table is empty at boot, the server prints a one-time
URL to stderr:

    http://localhost:8079/setup?bootstrap=<random-token>

The `/setup` endpoint now **requires** the `?bootstrap=` token (or the
`X-Bootstrap-Token` header). The token is regenerated on first boot only;
if the server restarts before setup completes, the same URL keeps working.
Once setup succeeds, the token is marked consumed and `/setup` rejects
every further call with 401.

### Added
- Session auth via `__Host-cartledger_session` cookie over HTTPS (plain
  `cartledger_session` over HTTP), `HttpOnly`, `SameSite=Strict`, `Path=/`.
  Shell / iOS-Shortcut users can still authenticate via `Authorization:
  Bearer <jwt>` or `X-API-Key: <jwt>` — priority is cookie → Bearer →
  X-API-Key.
- `POST /api/v1/auth/logout` — clears session cookies.
- `TRUST_PROXY` middleware (`internal/api/realip.go`): spoofing-safe
  `X-Forwarded-*` handling, walks XFF right-to-left, handles IPv6 zones
  and 4-in-6 mapping.
- Security headers middleware: strict CSP for the SPA, `X-Frame-Options:
  DENY`, `Referrer-Policy`, `Permissions-Policy`, conditional HSTS.
- `/readyz` (DB ping + worker presence) and `/livez` (always 200) for
  orchestrators. `/health` now does a 2s DB ping.
- Tiered in-memory rate limits: auth (5/s/IP), read (20/s/household),
  write (10/s/household), **worker-submit (3/s/household)** — hard cap on
  LLM cost surface — and global (50/s/household) fallback.
- `log/slog` throughout: JSON handler by default, text handler for dev.
- Graceful worker drain on SIGTERM/SIGINT: HTTP shuts down first (10s),
  receipt worker drains the buffered queue (30s). Buffered jobs not yet
  started are marked `status='pending'` so the next boot re-queues them.
- EXIF strip on receipt upload (`imaging.StripMetadata`): prevents GPS
  leak across household members viewing shared receipts.
- 50M `BodyLimit` on `POST /receipts/scan` — OOM guard for pathological
  multipart uploads.
- Config validation at startup: `config.Validate()` aggregates errors via
  `errors.Join` and `Load()` refuses to return a partial config.
- Docker: non-root uid 10001, read-only rootfs, tmpfs `/tmp`, cap_drop
  ALL, no-new-privileges, healthcheck `/livez`, mem/cpu defaults.
- CI (`.github/workflows/ci.yml`): `go build`, `go vet`, `go test -race
  -cover`, frontend build, coverage ratchet against
  `.github/coverage-baseline`.
- Release (`.github/workflows/release.yml` + `.goreleaser.yml`):
  multi-arch binaries (linux/darwin × amd64/arm64) and GHCR multi-arch
  Docker image on `v*.*.*` tag push.
- Matcher golden-file regression tests (`internal/matcher/
  matcher_golden_test.go`) — 20 subtests covering scoring primitives
  and ranking.

### Changed
- SQLite PRAGMAs: `temp_store=MEMORY`, `busy_timeout=10000` (was 5000).
- Login / Setup / Join responses no longer return the JWT in the body
  (the `"token"` field is now always the empty string — do not read it).
- `/files/*` now prefers cookie auth; the legacy `?token=` query-string
  fallback still works but logs a deprecation warning per hit.
- WebSocket `CheckOrigin` now validates the `Origin` header against
  `ALLOWED_ORIGINS`. Previously it accepted every origin.

### Fixed
- Migration 016: four indexes silently dropped by migrations 005 and 006
  (SQLite DROP TABLE cascaded the index loss; neither migration
  recreated them). Affects `line_items(receipt_id)`,
  `line_items(product_id)`, `product_prices(product_id, receipt_date)`,
  `product_prices(store_id, receipt_date)`. Deployed databases that ran
  005/006 were doing full scans on every receipt/price lookup.
