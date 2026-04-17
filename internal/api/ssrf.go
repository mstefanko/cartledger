package api

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// errPrivateAddressBlocked is returned when a base URL resolves to a private
// range and ALLOW_PRIVATE_INTEGRATIONS is off.
var errPrivateAddressBlocked = errors.New("private/loopback address not allowed")

// errInvalidScheme is returned when a base URL scheme is not http or https.
var errInvalidScheme = errors.New("base_url scheme must be http or https")

// errInvalidURL is returned when a base URL is unparseable or missing a host.
var errInvalidURL = errors.New("base_url is not a valid URL")

// lookupIPsFn is the DNS-resolution function used by validateIntegrationURL.
// It is a package-level var so tests can stub it.
var lookupIPsFn = net.LookupIP

// validateIntegrationURL parses rawURL and, unless allowPrivate is true,
// rejects any URL whose host resolves to a loopback, link-local, RFC1918, or
// IPv6 ULA address. Scheme validation (http/https only) always runs.
//
// Returns (parsedURL, err) — err is non-nil on any violation. Callers should
// treat the error as a 400 response and never echo upstream bodies.
func validateIntegrationURL(rawURL string, allowPrivate bool) (*url.URL, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, errInvalidURL
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, errInvalidURL
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, errInvalidScheme
	}
	host := u.Hostname()
	if host == "" {
		return nil, errInvalidURL
	}

	if allowPrivate {
		return u, nil
	}

	// Catch textual "localhost" up-front — DNS may or may not resolve it to
	// 127.0.0.1 depending on the resolver, and we never want it through.
	if strings.EqualFold(host, "localhost") {
		return nil, errPrivateAddressBlocked
	}

	// If host is already a literal IP, validate directly.
	if ip := net.ParseIP(host); ip != nil {
		if isPrivateIP(ip) {
			return nil, errPrivateAddressBlocked
		}
		return u, nil
	}

	// Otherwise resolve and check every A/AAAA record — any private hit rejects.
	ips, err := lookupIPsFn(host)
	if err != nil {
		return nil, fmt.Errorf("resolve host: %w", err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("resolve host: no addresses")
	}
	for _, ip := range ips {
		if isPrivateIP(ip) {
			return nil, errPrivateAddressBlocked
		}
	}
	return u, nil
}

// isPrivateIP returns true if ip is in a range we must block to prevent SSRF:
// loopback (127/8, ::1), link-local (169.254/16, fe80::/10), RFC1918
// (10/8, 172.16/12, 192.168/16), or IPv6 ULA (fc00::/7).
func isPrivateIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}
	// net.IP.IsPrivate covers RFC1918 + IPv6 ULA (fc00::/7).
	if ip.IsPrivate() {
		return true
	}
	return false
}
