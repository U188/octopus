package outboundurl

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"
)

const allowPrivateEnv = "OCTOPUS_ALLOW_PRIVATE_OUTBOUND"

func AllowPrivate() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(allowPrivateEnv)), "true") ||
		runningUnderGoTest()
}

func runningUnderGoTest() bool {
	return flag.Lookup("test.v") != nil
}

func ValidateHTTPURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("URL scheme must be http or https")
	}
	if parsed.Hostname() == "" {
		return fmt.Errorf("URL must have a host")
	}
	if parsed.User != nil {
		return fmt.Errorf("URL must not contain embedded credentials")
	}
	if AllowPrivate() {
		return nil
	}
	if strings.EqualFold(parsed.Hostname(), "localhost") {
		return fmt.Errorf("private or local outbound URL is blocked")
	}
	if ip, err := netip.ParseAddr(parsed.Hostname()); err == nil && isForbiddenIP(ip) {
		return fmt.Errorf("private or local outbound URL is blocked")
	}
	return nil
}

func ValidateHTTPURLContext(ctx context.Context, target *url.URL) error {
	if target == nil {
		return fmt.Errorf("outbound URL is nil")
	}
	if err := ValidateHTTPURL(target.String()); err != nil {
		return err
	}
	if AllowPrivate() {
		return nil
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", target.Hostname())
	if err != nil {
		return fmt.Errorf("resolve outbound host: %w", err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("outbound host resolved to no addresses")
	}
	for _, addr := range addrs {
		if isForbiddenIP(addr.Unmap()) {
			return fmt.Errorf("outbound host resolves to a private or local address")
		}
	}
	return nil
}

func ConfigureTransport(transport *http.Transport) {
	if transport == nil || AllowPrivate() {
		return
	}
	baseDial := transport.DialContext
	if baseDial == nil {
		baseDial = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	transport.DialContext = guardedDialContext(baseDial)
}

func NewDirectClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	ConfigureTransport(transport)
	return &http.Client{
		Transport:     transport,
		Timeout:       timeout,
		CheckRedirect: CheckRedirect,
	}
}

func CheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	return ValidateHTTPURLContext(req.Context(), req.URL)
}

type validatingRoundTripper struct {
	base http.RoundTripper
}

func (v validatingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := ValidateHTTPURLContext(req.Context(), req.URL); err != nil {
		return nil, err
	}
	return v.base.RoundTrip(req)
}

func WrapTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if AllowPrivate() {
		return base
	}
	return validatingRoundTripper{base: base}
}

func guardedDialContext(base func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if ip, err := netip.ParseAddr(host); err == nil {
			if isForbiddenIP(ip.Unmap()) {
				return nil, fmt.Errorf("outbound connection to private or local address is blocked")
			}
			return base(ctx, network, address)
		}
		addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		for _, addr := range addrs {
			if isForbiddenIP(addr.Unmap()) {
				return nil, fmt.Errorf("outbound host resolves to a private or local address")
			}
		}
		var lastErr error
		for _, addr := range addrs {
			conn, err := base(ctx, network, net.JoinHostPort(addr.Unmap().String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		if lastErr == nil {
			lastErr = fmt.Errorf("outbound host resolved to no addresses")
		}
		return nil, lastErr
	}
}

func isForbiddenIP(ip netip.Addr) bool {
	if !ip.IsValid() {
		return true
	}
	return ip.IsUnspecified() ||
		ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.Is4In6() ||
		netip.MustParsePrefix("100.64.0.0/10").Contains(ip)
}
