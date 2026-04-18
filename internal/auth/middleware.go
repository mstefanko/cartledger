package auth

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

// Context keys for extracting user info from echo.Context.
const (
	ContextKeyUserID      = "user_id"
	ContextKeyHouseholdID = "household_id"

	// SessionCookieName is the cookie set for same-site session auth when the
	// request is served over plain HTTP (dev / loopback).
	SessionCookieName = "cartledger_session"
	// SessionCookieHostName is the cookie set when the request is served over
	// HTTPS. The __Host- prefix instructs browsers to reject the cookie unless
	// it's Secure, has Path=/, and has NO Domain attribute — which hardens
	// against subdomain takeover and cross-origin cookie shadowing.
	SessionCookieHostName = "__Host-cartledger_session"

	// SessionCookieMaxAge matches the JWT auth-token expiry (30 days, see
	// CreateAuthToken in jwt.go). Keep these in sync.
	SessionCookieMaxAge = 30 * 24 * time.Hour
)

// isTLSRequest reports whether the request should be treated as HTTPS for the
// purpose of cookie Secure flag + __Host- prefix selection. Prefers the
// "real_proto" context key (set by the RealIP middleware in internal/api/realip.go),
// which respects a trusted reverse proxy's X-Forwarded-Proto. Falls back to
// c.IsTLS() if the key is missing (shouldn't happen in practice, but keeps the
// helper safe to call from any handler).
func isTLSRequest(c echo.Context) bool {
	if proto, ok := c.Get("real_proto").(string); ok && proto != "" {
		return strings.EqualFold(proto, "https")
	}
	return c.IsTLS()
}

// SetAuthCookie writes the session cookie on the response. Uses the __Host-
// prefix + Secure on HTTPS; falls back to the plain name over HTTP (dev).
// SameSite=Strict since the frontend and API share an origin — we never need
// the cookie to ride along on a cross-site navigation.
func SetAuthCookie(c echo.Context, token string) {
	secure := isTLSRequest(c)
	name := SessionCookieName
	if secure {
		name = SessionCookieHostName
	}
	http.SetCookie(c.Response(), &http.Cookie{
		Name:     name,
		Value:    token,
		Path:     "/",
		MaxAge:   int(SessionCookieMaxAge.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// ClearAuthCookie issues a Max-Age=0 cookie to remove the session cookie from
// the browser. We clear BOTH cookie names so that switching between HTTP/HTTPS
// during deploy or testing doesn't leave a stale cookie behind.
func ClearAuthCookie(c echo.Context) {
	secure := isTLSRequest(c)
	http.SetCookie(c.Response(), &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
	http.SetCookie(c.Response(), &http.Cookie{
		Name:     SessionCookieHostName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true, // __Host- cookies MUST be Secure even to clear
		SameSite: http.SameSiteStrictMode,
	})
}

// extractToken pulls a JWT from the request using a fixed priority order:
//  1. Session cookie (__Host-cartledger_session, then cartledger_session)
//  2. Authorization: Bearer <jwt>
//  3. X-API-Key: <jwt>
//  4. ?token=<jwt> query string — ONLY if allowQueryToken is true
//
// Returns the raw token string and the source (for logging). Empty string on
// none-found. The query-string path is intentionally restricted to the handful
// of endpoints that historically needed it (/files/* and /ws) to discourage
// token leakage via Referer / server access logs.
func extractToken(c echo.Context, allowQueryToken bool) (token, source string) {
	// 1. Cookies (prefer __Host- prefix)
	if ck, err := c.Cookie(SessionCookieHostName); err == nil && ck.Value != "" {
		return ck.Value, "cookie_host"
	}
	if ck, err := c.Cookie(SessionCookieName); err == nil && ck.Value != "" {
		return ck.Value, "cookie"
	}
	// 2. Authorization: Bearer
	if h := c.Request().Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer "), "bearer"
	}
	// 3. X-API-Key
	if h := c.Request().Header.Get("X-API-Key"); h != "" {
		return h, "api_key"
	}
	// 4. Query string (opt-in per-route)
	if allowQueryToken {
		if t := c.QueryParam("token"); t != "" {
			return t, "query"
		}
	}
	return "", ""
}

// JWTMiddleware returns an Echo middleware that authenticates requests via any
// of: session cookie, Authorization: Bearer, or X-API-Key. Query-string ?token=
// is NOT honored by the middleware — it's only accepted by the specific
// handlers that still need it (see AuthenticateWithQueryToken).
//
// On success the middleware sets user_id and household_id on the echo context.
// On failure it returns 401 with {"error":"invalid or expired token"}.
func JWTMiddleware(secret string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			tok, _ := extractToken(c, false)
			if tok == "" {
				return c.JSON(http.StatusUnauthorized, map[string]string{
					"error": "missing or invalid token",
				})
			}
			claims, err := ValidateAuthToken(secret, tok)
			if err != nil {
				return c.JSON(http.StatusUnauthorized, map[string]string{
					"error": "invalid or expired token",
				})
			}
			c.Set(ContextKeyUserID, claims.UserID)
			c.Set(ContextKeyHouseholdID, claims.HouseholdID)
			return next(c)
		}
	}
}

// AuthenticateWithQueryToken validates an auth JWT allowing the ?token= query
// fallback. Returns the claims on success, or an *echo.HTTPError on failure
// that the caller should return directly.
//
// This is the path used by /files/* (for <img src=> over cookie-less legacy
// iOS Shortcuts) and /ws (for edge cases where the WS client can't send a
// Cookie header). A deprecation warning is logged whenever the query fallback
// is actually used so operators can track whether it's still needed before
// removing it in a future release.
func AuthenticateWithQueryToken(c echo.Context, secret string) (*Claims, error) {
	tok, src := extractToken(c, true)
	if tok == "" {
		return nil, echo.NewHTTPError(http.StatusUnauthorized, "missing token")
	}
	if src == "query" {
		slog.Warn("auth: deprecated ?token= query-string auth used",
			"path", c.Request().URL.Path,
			"ip", c.RealIP(),
		)
	}
	claims, err := ValidateAuthToken(secret, tok)
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusUnauthorized, "invalid token")
	}
	return claims, nil
}

// UserIDFrom extracts the user_id from the echo context.
func UserIDFrom(c echo.Context) string {
	v, _ := c.Get(ContextKeyUserID).(string)
	return v
}

// HouseholdIDFrom extracts the household_id from the echo context.
func HouseholdIDFrom(c echo.Context) string {
	v, _ := c.Get(ContextKeyHouseholdID).(string)
	return v
}
