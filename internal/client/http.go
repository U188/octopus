package client

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/op"
	"github.com/U188/octopus/internal/outboundurl"
	"golang.org/x/net/proxy"
)

var (
	systemDirectClient *http.Client
	systemProxyClient  *http.Client
	systemProxyURL     string
	clientLock         sync.RWMutex
)

// GetHTTPClientSystemProxy returns a cached http.Client.
// - useProxy=false: bypass proxy
// - useProxy=true: use proxy settings from system/app settings (setting key: proxy_url)
func GetHTTPClientSystemProxy(useProxy bool) (*http.Client, error) {
	if useProxy {
		currentProxyURL, err := op.SettingGetString(model.SettingKeyProxyURL)
		if err != nil {
			return nil, err
		}
		if currentProxyURL == "" {
			return nil, fmt.Errorf("proxy url is empty")
		}

		clientLock.RLock()
		if systemProxyClient != nil && systemProxyURL == currentProxyURL {
			clientLock.RUnlock()
			return systemProxyClient, nil
		}
		clientLock.RUnlock()

		clientLock.Lock()
		defer clientLock.Unlock()

		// Re-check after acquiring write lock.
		if systemProxyClient != nil && systemProxyURL == currentProxyURL {
			return systemProxyClient, nil
		}

		client, err := newHTTPClientCustomProxy(currentProxyURL)
		if err != nil {
			return nil, err
		}
		systemProxyClient = client
		systemProxyURL = currentProxyURL
		return systemProxyClient, nil
	}

	clientLock.RLock()
	if !useProxy && systemDirectClient != nil {
		clientLock.RUnlock()
		return systemDirectClient, nil
	}
	clientLock.RUnlock()

	clientLock.Lock()
	defer clientLock.Unlock()

	if systemDirectClient != nil {
		return systemDirectClient, nil
	}
	client, err := newHTTPClientNoProxy()
	if err != nil {
		return nil, err
	}
	systemDirectClient = client
	return systemDirectClient, nil
}

// GetHTTPClientCustomProxy returns a NEW http.Client every time (no reuse).
// proxyURL supports: http, https, socks, socks5
func GetHTTPClientCustomProxy(proxyURL string) (*http.Client, error) {
	if proxyURL == "" {
		return nil, fmt.Errorf("proxy url is empty")
	}
	return newHTTPClientCustomProxy(proxyURL)
}

// GetHTTPClientCustomProxyPool retries transport-level failures through the
// remaining proxies when the request body is empty or can be replayed.
func GetHTTPClientCustomProxyPool(proxyURLs []string) (*http.Client, error) {
	return GetHTTPClientCustomProxyPoolWithFailureReporter(proxyURLs, nil)
}

type ProxyFailureReporter func(proxyURL string, failure error)

func GetHTTPClientCustomProxyPoolWithFailureReporter(proxyURLs []string, reportFailure ProxyFailureReporter) (*http.Client, error) {
	if len(proxyURLs) == 0 {
		return nil, fmt.Errorf("proxy url list is empty")
	}
	endpoints := make([]proxyTransportEndpoint, 0, len(proxyURLs))
	for _, proxyURL := range proxyURLs {
		primaryClient, err := GetHTTPClientCustomProxy(proxyURL)
		if err != nil {
			return nil, err
		}
		http1Client, err := newHTTPClientCustomProxyHTTP1(proxyURL)
		if err != nil {
			return nil, err
		}
		endpoints = append(endpoints, proxyTransportEndpoint{
			proxyURL: proxyURL,
			primary:  primaryClient.Transport,
			http1:    http1Client.Transport,
		})
	}
	return &http.Client{
		Transport:     &proxyFailoverTransport{endpoints: endpoints, reportFailure: reportFailure},
		CheckRedirect: outboundurl.CheckRedirect,
	}, nil
}

type proxyTransportEndpoint struct {
	proxyURL string
	primary  http.RoundTripper
	http1    http.RoundTripper
}

type proxyFailoverTransport struct {
	endpoints     []proxyTransportEndpoint
	reportFailure ProxyFailureReporter
}

func (t *proxyFailoverTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("request is nil")
	}
	var lastErr error
	attempt := 0
	for _, endpoint := range t.endpoints {
		attemptReq, replayable, err := proxyRequestForAttempt(req, attempt)
		if err != nil {
			return nil, err
		}
		if !replayable {
			break
		}
		resp, err := endpoint.primary.RoundTrip(attemptReq)
		if err == nil {
			return resp, nil
		}
		closeProxyFailureResponse(resp)
		lastErr = err
		if req.Context().Err() != nil {
			return nil, req.Context().Err()
		}
		if !isSafeProxyFailoverError(err) {
			return nil, err
		}

		if isTLSHandshakeFailure(err) && endpoint.http1 != nil {
			attempt++
			fallbackReq, canFallback, replayErr := proxyRequestForAttempt(req, attempt)
			if replayErr != nil {
				return nil, replayErr
			}
			if canFallback {
				fallbackResp, fallbackErr := endpoint.http1.RoundTrip(fallbackReq)
				if fallbackErr == nil {
					return fallbackResp, nil
				}
				closeProxyFailureResponse(fallbackResp)
				lastErr = fallbackErr
				if req.Context().Err() != nil {
					return nil, req.Context().Err()
				}
				if !isSafeProxyFailoverError(fallbackErr) {
					return nil, fallbackErr
				}
			}
		}
		if t.reportFailure != nil && isProxyNodeFailure(lastErr, endpoint.proxyURL) {
			t.reportFailure(endpoint.proxyURL, lastErr)
		}
		attempt++
	}
	if lastErr == nil {
		lastErr = io.ErrUnexpectedEOF
	}
	return nil, fmt.Errorf("all proxy nodes failed: %w", lastErr)
}

func proxyRequestForAttempt(req *http.Request, attempt int) (*http.Request, bool, error) {
	if attempt == 0 {
		return req, true, nil
	}
	if req.Body != nil && req.Body != http.NoBody && req.GetBody == nil {
		return nil, false, nil
	}
	attemptReq := req.Clone(req.Context())
	if req.Body != nil && req.Body != http.NoBody {
		body, err := req.GetBody()
		if err != nil {
			return nil, false, fmt.Errorf("replay proxy request body: %w", err)
		}
		attemptReq.Body = body
	} else if req.Body == http.NoBody {
		attemptReq.Body = http.NoBody
	}
	return attemptReq, true, nil
}

func closeProxyFailureResponse(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
}

func isTLSHandshakeFailure(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "tls") && strings.Contains(message, "handshake")
}

// Safe failover errors happen before the upstream HTTP request can be
// processed. Retrying ambiguous read/write failures can duplicate billable
// model requests.
func isSafeProxyFailoverError(err error) bool {
	if isTLSHandshakeFailure(err) || hasDialFailure(err) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "proxyconnect tcp:") ||
		strings.Contains(message, "socks connect tcp") ||
		strings.Contains(message, "socks5 connect tcp")
}

// Only proxy-specific connection failures may quarantine a node. A target TLS
// handshake failure is safe to retry through another node, but is not proof
// that the shared proxy node itself is unhealthy.
func isProxyNodeFailure(err error, proxyURL string) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "proxyconnect tcp:") &&
		(strings.Contains(message, "status: 407") || strings.Contains(message, "status: 429") || strings.Contains(message, "not enough bandwidth")) {
		return true
	}
	parsedProxy, parseErr := url.Parse(proxyURL)
	if parseErr != nil {
		return false
	}
	proxyAddress := parsedProxy.Host
	if parsedProxy.Port() == "" {
		switch parsedProxy.Scheme {
		case "http":
			proxyAddress = net.JoinHostPort(parsedProxy.Hostname(), "80")
		case "https":
			proxyAddress = net.JoinHostPort(parsedProxy.Hostname(), "443")
		}
	}
	for current := err; current != nil; current = errors.Unwrap(current) {
		opErr, ok := current.(*net.OpError)
		if !ok || !strings.EqualFold(strings.TrimSpace(opErr.Op), "dial") || opErr.Addr == nil {
			continue
		}
		if strings.EqualFold(opErr.Addr.String(), proxyAddress) {
			return true
		}
	}
	return false
}

func hasDialFailure(err error) bool {
	for current := err; current != nil; current = errors.Unwrap(current) {
		if opErr, ok := current.(*net.OpError); ok && strings.EqualFold(strings.TrimSpace(opErr.Op), "dial") {
			return true
		}
	}
	return false
}

func (t *proxyFailoverTransport) CloseIdleConnections() {
	for _, endpoint := range t.endpoints {
		for _, transport := range []http.RoundTripper{endpoint.primary, endpoint.http1} {
			if closer, ok := transport.(interface{ CloseIdleConnections() }); ok {
				closer.CloseIdleConnections()
			}
		}
	}
}

func clonedDefaultTransport() (*http.Transport, error) {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("default transport is not *http.Transport")
	}
	cloned := transport.Clone()
	return cloned, nil
}

func newHTTPClientNoProxy() (*http.Client, error) {
	cloned, err := clonedDefaultTransport()
	if err != nil {
		return nil, err
	}
	cloned.Proxy = nil
	outboundurl.ConfigureTransport(cloned)
	return &http.Client{Transport: cloned, CheckRedirect: outboundurl.CheckRedirect}, nil
}

func newHTTPClientCustomProxy(proxyURLStr string) (*http.Client, error) {
	return newHTTPClientCustomProxyWithMode(proxyURLStr, false)
}

func newHTTPClientCustomProxyHTTP1(proxyURLStr string) (*http.Client, error) {
	return newHTTPClientCustomProxyWithMode(proxyURLStr, true)
}

func newHTTPClientCustomProxyWithMode(proxyURLStr string, http1Only bool) (*http.Client, error) {
	cloned, err := clonedDefaultTransport()
	if err != nil {
		return nil, err
	}

	proxyURL, err := url.Parse(proxyURLStr)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy url: %w", err)
	}

	switch proxyURL.Scheme {
	case "http", "https":
		cloned.Proxy = http.ProxyURL(proxyURL)
	case "socks", "socks5":
		socksDialer, err := proxy.FromURL(proxyURL, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("invalid socks proxy: %w", err)
		}
		cloned.Proxy = nil
		cloned.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return socksDialer.Dial(network, addr)
		}
		// Resolve and validate the destination locally before handing an IP to
		// the SOCKS proxy, so proxy-side DNS cannot redirect a public hostname
		// to a private address.
		outboundurl.ConfigureTransport(cloned)
	default:
		return nil, fmt.Errorf("unsupported proxy scheme: %s", proxyURL.Scheme)
	}
	if http1Only {
		protocols := new(http.Protocols)
		protocols.SetHTTP1(true)
		cloned.Protocols = protocols
		cloned.ForceAttemptHTTP2 = false
		tlsConfig := cloned.TLSClientConfig
		if tlsConfig == nil {
			tlsConfig = &tls.Config{}
		} else {
			tlsConfig = tlsConfig.Clone()
		}
		tlsConfig.NextProtos = []string{"http/1.1"}
		cloned.TLSClientConfig = tlsConfig
		cloned.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	}

	return &http.Client{
		Transport:     outboundurl.WrapTransport(cloned),
		CheckRedirect: outboundurl.CheckRedirect,
	}, nil
}
