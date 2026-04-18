package api

import (
	"net"
	"net/netip"
	"strings"

	"github.com/labstack/echo/v4"
)

// RealIP returns middleware that resolves the true client IP + protocol when
// the request arrives via a trusted reverse proxy (Caddy/Nginx/Traefik).
//
// Behavior:
//   - Reads the direct TCP peer from c.Request().RemoteAddr.
//   - If the peer address matches any CIDR in trustedProxies, walks the
//     X-Forwarded-For header from right to left and picks the first entry
//     that is NOT itself a trusted proxy — that's the real client. Also
//     honors X-Forwarded-Proto / X-Forwarded-Host.
//   - If the peer is NOT trusted, X-Forwarded-* headers are ignored entirely
//     (spoofing defense — any HTTP client can inject those headers).
//
// The resolved values are stashed on the echo.Context via c.Set for
// downstream middleware, and the request's RemoteAddr is rewritten so that
// c.RealIP() returns the correct IP automatically.
func RealIP(trustedProxies []netip.Prefix) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			req := c.Request()
			peerIP := parsePeerIP(req.RemoteAddr)
			realIP := peerIP
			realProto := "http"
			if req.TLS != nil {
				realProto = "https"
			}
			realHost := req.Host

			if peerIP.IsValid() && ipInPrefixes(peerIP, trustedProxies) {
				// Walk X-Forwarded-For right-to-left; first untrusted entry wins.
				if xff := req.Header.Get("X-Forwarded-For"); xff != "" {
					parts := strings.Split(xff, ",")
					for i := len(parts) - 1; i >= 0; i-- {
						candidate, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
						if err != nil {
							continue // skip malformed entries silently
						}
						if !ipInPrefixes(candidate, trustedProxies) {
							realIP = candidate
							break
						}
						// entry itself is a trusted proxy — keep walking left
						realIP = candidate
					}
				}
				if proto := req.Header.Get("X-Forwarded-Proto"); proto != "" {
					realProto = strings.ToLower(strings.TrimSpace(proto))
				}
				if host := req.Header.Get("X-Forwarded-Host"); host != "" {
					realHost = strings.TrimSpace(host)
				}
			}

			if realIP.IsValid() {
				c.Set("real_ip", realIP.String())
				// Rewrite RemoteAddr so Echo's built-in c.RealIP() returns the
				// correct value. Preserve the original port if we can parse it;
				// otherwise just use the IP.
				if _, port, err := net.SplitHostPort(req.RemoteAddr); err == nil {
					req.RemoteAddr = net.JoinHostPort(realIP.String(), port)
				} else {
					req.RemoteAddr = realIP.String()
				}
			}
			c.Set("real_proto", realProto)
			c.Set("real_host", realHost)
			return next(c)
		}
	}
}

// parsePeerIP extracts the IP portion from a Go net RemoteAddr string.
// Handles IPv4 ("1.2.3.4:5678"), IPv6 ("[::1]:5678"), and bare addresses.
// Returns the zero netip.Addr if parsing fails.
func parsePeerIP(remoteAddr string) netip.Addr {
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	// Strip IPv6 zone identifier (e.g. "fe80::1%eth0") — ParseAddr rejects zones.
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return addr.Unmap() // normalize 4in6 so CIDR matches behave as expected
}

// ipInPrefixes reports whether ip is contained in any of the given prefixes.
func ipInPrefixes(ip netip.Addr, prefixes []netip.Prefix) bool {
	ip = ip.Unmap()
	for _, p := range prefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}
