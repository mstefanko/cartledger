package api

import (
	"github.com/labstack/echo/v4"
)

// contentSecurityPolicy is the CSP used for all responses served by the API + SPA.
//
// Rationale:
//   - default-src 'self': deny by default, only same-origin.
//   - img-src 'self' data: blob:: receipt thumbnails may be loaded from data: URLs
//     (drag-and-drop previews) or blob: URLs (File API). No remote image hosts are used.
//   - style-src 'self' 'unsafe-inline': Tailwind's generated stylesheet is same-origin,
//     but React/Vite-built apps still emit a few inline style attributes. 'unsafe-inline'
//     on styles is a well-known, low-risk compromise.
//   - script-src 'self': NO 'unsafe-inline' — verified web/index.html has no inline scripts.
//     Vite bundles everything to /assets/*.js which is same-origin.
//   - connect-src 'self' ws: wss:: WebSocket hub uses ws:// in dev, wss:// in prod.
//   - object-src 'none', base-uri 'self', frame-ancestors 'none': standard hardening
//     (frame-ancestors complements X-Frame-Options: DENY for browsers that honor both).
const contentSecurityPolicy = "default-src 'self'; " +
	"img-src 'self' data: blob:; " +
	"style-src 'self' 'unsafe-inline'; " +
	"script-src 'self'; " +
	"connect-src 'self' ws: wss:; " +
	"object-src 'none'; " +
	"base-uri 'self'; " +
	"frame-ancestors 'none'"

// SecurityHeaders returns a middleware that sets conservative HTTP security
// response headers suitable for the CartLedger SPA + JSON API. HSTS is only
// emitted when the request was served over TLS (either directly or via a proxy
// that set X-Forwarded-Proto=https), to avoid poisoning plain-HTTP self-hosted
// deployments.
func SecurityHeaders() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			h := c.Response().Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			h.Set("Content-Security-Policy", contentSecurityPolicy)

			// Only emit HSTS on HTTPS requests. c.IsTLS() alone would miss the
			// reverse-proxy case (proxy terminates TLS and forwards to the app
			// over plain HTTP). RealIP middleware sets "real_proto" based on
			// X-Forwarded-Proto — but only for trusted proxies, so this can't
			// be spoofed by arbitrary clients.
			proto, _ := c.Get("real_proto").(string)
			if c.IsTLS() || proto == "https" {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}

			return next(c)
		}
	}
}
