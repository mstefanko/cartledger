package httpsafe

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestValidateURLBlocksPrivateAddresses(t *testing.T) {
	cases := []string{
		"http://localhost",
		"http://127.0.0.1",
		"http://10.0.0.5",
		"http://172.16.0.5",
		"http://192.168.1.5",
		"http://169.254.1.5",
		"http://[::1]",
		"http://[fc00::1]",
	}

	for _, rawURL := range cases {
		if _, err := ValidateURL(rawURL, false); !errors.Is(err, ErrPrivateAddressBlocked) {
			t.Fatalf("ValidateURL(%q) err = %v, want ErrPrivateAddressBlocked", rawURL, err)
		}
	}
}

func TestValidateURLBlocksPrivateDNSAnswer(t *testing.T) {
	restore := SetLookupIPsForTest(func(host string) ([]net.IP, error) {
		if host != "example.test" {
			t.Fatalf("unexpected host %q", host)
		}
		return []net.IP{net.ParseIP("192.168.1.25")}, nil
	})
	defer restore()

	if _, err := ValidateURL("https://example.test/product", false); !errors.Is(err, ErrPrivateAddressBlocked) {
		t.Fatalf("ValidateURL private DNS err = %v, want ErrPrivateAddressBlocked", err)
	}
}

func TestSafeHTTPClientCapsResponseBytes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("0123456789"))
	}))
	defer server.Close()

	client := NewSafeHTTPClient(2*time.Second, 4, true)
	_, err := client.Fetch(context.Background(), server.URL)
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("Fetch err = %v, want ErrResponseTooLarge", err)
	}
}

func TestSafeHTTPClientBlocksPrivateRedirectTarget(t *testing.T) {
	var serverPort string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://rebind.test:"+serverPort+"/private", http.StatusFound)
	}))
	defer server.Close()

	serverHost, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatalf("split server URL: %v", err)
	}
	serverPort = port
	restoreLookup := SetLookupIPsForTest(func(host string) ([]net.IP, error) {
		switch host {
		case "public.test":
			return []net.IP{net.ParseIP("203.0.113.10")}, nil
		case "rebind.test":
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		default:
			return net.LookupIP(host)
		}
	})
	defer restoreLookup()

	restoreDial := SetDialContextForTest(func(ctx context.Context, network, address string) (net.Conn, error) {
		_, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if port == serverPort {
			return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(serverHost, serverPort))
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	})
	defer restoreDial()

	client := NewSafeHTTPClient(2*time.Second, 1024, false)
	_, err = client.Fetch(context.Background(), "http://public.test:"+serverPort+"/redirect")
	if !errors.Is(err, ErrPrivateAddressBlocked) {
		t.Fatalf("Fetch redirect err = %v, want ErrPrivateAddressBlocked", err)
	}
}
