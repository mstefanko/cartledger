package api

import (
	"errors"
	"net"
	"net/url"

	"github.com/mstefanko/cartledger/internal/httpsafe"
)

var (
	errPrivateAddressBlocked = httpsafe.ErrPrivateAddressBlocked
	errInvalidScheme         = errors.New("base_url scheme must be http or https")
	errInvalidURL            = errors.New("base_url is not a valid URL")
)

// lookupIPsFn is kept for older package-level tests that stubbed integration
// URL resolution. New fetch code should use internal/httpsafe directly.
var lookupIPsFn = net.LookupIP

func validateIntegrationURL(rawURL string, allowPrivate bool) (*url.URL, error) {
	u, err := httpsafe.ValidateURLWithResolver(rawURL, allowPrivate, lookupIPsFn)
	if errors.Is(err, httpsafe.ErrInvalidScheme) {
		return nil, errInvalidScheme
	}
	if errors.Is(err, httpsafe.ErrInvalidURL) {
		return nil, errInvalidURL
	}
	return u, err
}

func isPrivateIP(ip net.IP) bool {
	return httpsafe.IsPrivateIP(ip)
}
