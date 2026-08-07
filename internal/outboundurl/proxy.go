package outboundurl

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	xproxy "golang.org/x/net/proxy"
)

var socksProxyDialTimeout = 10 * time.Second

var ErrProxyDialTimeout = errors.New("proxy dial timeout")

// ConfigureProxyTransport applies an HTTP or SOCKS proxy to transport. SOCKS
// dialing must retain the request context so canceled requests also terminate
// an in-progress proxy handshake.
func ConfigureProxyTransport(transport *http.Transport, proxyURL *url.URL) error {
	if transport == nil {
		return fmt.Errorf("proxy transport is nil")
	}
	if proxyURL == nil {
		return fmt.Errorf("proxy url is nil")
	}

	switch strings.ToLower(strings.TrimSpace(proxyURL.Scheme)) {
	case "http", "https":
		httpProxyURL := *proxyURL
		httpProxyURL.Scheme = strings.ToLower(strings.TrimSpace(httpProxyURL.Scheme))
		transport.Proxy = http.ProxyURL(&httpProxyURL)
		return nil
	case "socks", "socks5":
		socksURL := *proxyURL
		socksURL.Scheme = strings.ToLower(strings.TrimSpace(socksURL.Scheme))
		if socksURL.Scheme == "socks" {
			socksURL.Scheme = "socks5"
		}
		forward := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
		socksDialer, err := xproxy.FromURL(&socksURL, forward)
		if err != nil {
			return fmt.Errorf("invalid socks proxy: %w", err)
		}
		contextDialer, ok := socksDialer.(xproxy.ContextDialer)
		if !ok {
			return fmt.Errorf("socks proxy does not support context-aware dialing")
		}
		transport.Proxy = nil
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			dialCtx, cancel := context.WithTimeout(ctx, socksProxyDialTimeout)
			defer cancel()
			conn, err := contextDialer.DialContext(dialCtx, network, address)
			if err != nil {
				return nil, normalizeProxyDialError(ctx, dialCtx, err)
			}
			return conn, err
		}
		// Resolve and validate the destination locally before handing an IP to
		// the SOCKS proxy, so proxy-side DNS cannot redirect a public hostname
		// to a private address.
		ConfigureTransport(transport)
		return nil
	default:
		return fmt.Errorf("unsupported proxy scheme: %s", proxyURL.Scheme)
	}
}

// normalizeProxyDialError keeps the original SOCKS error for diagnostics while
// exposing stable sentinel/context errors to callers. The x/net SOCKS client
// enforces a context deadline with net.Conn.SetDeadline; depending on the
// scheduler that path can surface as a plain "i/o timeout" before the context
// timer records its error. Treat all such deadline failures consistently.
func normalizeProxyDialError(parent context.Context, dialCtx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctxErr := parent.Err(); ctxErr != nil {
		return fmt.Errorf("%w: %w: %v", ErrProxyDialTimeout, ctxErr, err)
	}
	if ctxErr := dialCtx.Err(); ctxErr != nil {
		return fmt.Errorf("%w: %w: %v", ErrProxyDialTimeout, ctxErr, err)
	}
	var netErr net.Error
	if errors.Is(err, os.ErrDeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return fmt.Errorf("%w: %w: %v", ErrProxyDialTimeout, context.DeadlineExceeded, err)
	}
	return err
}
