package httpsafe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrPrivateAddressBlocked is returned when a URL resolves to a private
	// range and private network fetches are disabled.
	ErrPrivateAddressBlocked = errors.New("private/loopback address not allowed")
	ErrInvalidScheme         = errors.New("url scheme must be http or https")
	ErrInvalidURL            = errors.New("url is not a valid URL")
	ErrResponseTooLarge      = errors.New("response exceeded byte limit")
)

var lookupIPsFn = net.LookupIP

// SetLookupIPsForTest swaps the resolver used by ValidateURL and SafeHTTPClient.
func SetLookupIPsForTest(fn func(string) ([]net.IP, error)) func() {
	old := lookupIPsFn
	lookupIPsFn = fn
	return func() { lookupIPsFn = old }
}

// ValidateURL parses rawURL and, unless allowPrivate is true, rejects any URL
// whose host resolves to a loopback, link-local, RFC1918, or IPv6 ULA address.
func ValidateURL(rawURL string, allowPrivate bool) (*url.URL, error) {
	return ValidateURLWithResolver(rawURL, allowPrivate, lookupIPsFn)
}

func ValidateURLWithResolver(rawURL string, allowPrivate bool, lookup func(string) ([]net.IP, error)) (*url.URL, error) {
	if lookup == nil {
		lookup = net.LookupIP
	}
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, ErrInvalidURL
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, ErrInvalidURL
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, ErrInvalidScheme
	}
	host := u.Hostname()
	if host == "" {
		return nil, ErrInvalidURL
	}
	if allowPrivate {
		return u, nil
	}
	if err := validatePublicHost(host, lookup); err != nil {
		return nil, err
	}
	return u, nil
}

func validatePublicHost(host string, lookup func(string) ([]net.IP, error)) error {
	if strings.EqualFold(host, "localhost") {
		return ErrPrivateAddressBlocked
	}
	if ip := net.ParseIP(host); ip != nil {
		if IsPrivateIP(ip) {
			return ErrPrivateAddressBlocked
		}
		return nil
	}

	ips, err := lookup(host)
	if err != nil {
		return fmt.Errorf("resolve host: %w", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("resolve host: no addresses")
	}
	for _, ip := range ips {
		if IsPrivateIP(ip) {
			return ErrPrivateAddressBlocked
		}
	}
	return nil
}

// IsPrivateIP returns true for address ranges blocked to prevent SSRF:
// loopback, link-local, unspecified, RFC1918, and IPv6 ULA.
func IsPrivateIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}
	return ip.IsPrivate()
}

type SafeHTTPClient struct {
	client       *http.Client
	maxBytes     int64
	allowPrivate bool
	userAgent    string
}

type FetchResult struct {
	URL         string
	StatusCode  int
	ContentType string
	Body        []byte
	FetchedAt   time.Time
}

func NewSafeHTTPClient(timeout time.Duration, maxBytes int64, allowPrivate bool) *SafeHTTPClient {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if maxBytes <= 0 {
		maxBytes = 512 * 1024
	}
	c := &SafeHTTPClient{
		maxBytes:     maxBytes,
		allowPrivate: allowPrivate,
		userAgent:    "CartLedger/1.0",
	}
	transport := &http.Transport{
		Proxy:       nil,
		DialContext: c.safeDialContext,
	}
	c.client = &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			_, err := ValidateURL(req.URL.String(), allowPrivate)
			return err
		},
	}
	return c
}

func (c *SafeHTTPClient) Fetch(ctx context.Context, rawURL string) (FetchResult, error) {
	u, err := ValidateURL(rawURL, c.allowPrivate)
	if err != nil {
		return FetchResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return FetchResult{}, ErrInvalidURL
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json;q=0.9,text/plain;q=0.8,*/*;q=0.5")
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.client.Do(req)
	if err != nil {
		return FetchResult{}, err
	}
	defer resp.Body.Close()

	body, err := readCapped(resp.Body, c.maxBytes)
	if err != nil {
		return FetchResult{}, err
	}
	return FetchResult{
		URL:         resp.Request.URL.String(),
		StatusCode:  resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Body:        body,
		FetchedAt:   time.Now().UTC(),
	}, nil
}

func (c *SafeHTTPClient) safeDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := resolveDialIPs(host)
	if err != nil {
		return nil, err
	}
	if !c.allowPrivate {
		for _, ip := range ips {
			if IsPrivateIP(ip) {
				return nil, ErrPrivateAddressBlocked
			}
		}
	}
	dialer := &net.Dialer{}
	return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
}

func resolveDialIPs(host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	ips, err := lookupIPsFn(host)
	if err != nil {
		return nil, fmt.Errorf("resolve host: %w", err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("resolve host: no addresses")
	}
	return ips, nil
}

func readCapped(r io.Reader, maxBytes int64) ([]byte, error) {
	limited := io.LimitReader(r, maxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("%w: %s bytes", ErrResponseTooLarge, strconv.FormatInt(maxBytes, 10))
	}
	return body, nil
}
