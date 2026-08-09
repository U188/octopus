package client

import (
	"context"
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

// GetHTTPClientProxyPool resolves a reusable proxy configuration into an HTTP
// client. When perRequestRoundRobin is enabled, every RoundTrip refreshes the
// ordered healthy candidates so a reused client does not stay pinned to the
// same first proxy node.
func GetHTTPClientProxyPool(ctx context.Context, proxyConfigID int, perRequestRoundRobin bool) (*http.Client, error) {
	return GetHTTPClientProxyPoolScoped(ctx, proxyConfigID, perRequestRoundRobin, "")
}

// GetHTTPClientProxyPoolScoped isolates the round-robin cursor for a site or
// channel. When rotation is disabled, newly-created clients do not advance a
// cursor; candidate refreshes or safe failover may still change the node.
func GetHTTPClientProxyPoolScoped(ctx context.Context, proxyConfigID int, perRequestRoundRobin bool, scope string) (*http.Client, error) {
	if proxyConfigID <= 0 {
		return nil, fmt.Errorf("proxy config id must be positive")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	resolveProxyURLs := func(requestCtx context.Context) ([]string, error) {
		if requestCtx == nil {
			requestCtx = ctx
		}
		if !perRequestRoundRobin {
			return op.ProxyURLsForConfigStable(proxyConfigID, requestCtx)
		}
		return op.ProxyURLsForConfigScoped(proxyConfigID, scope, requestCtx)
	}

	// Resolve once at construction to validate the configuration and seed the
	// transport cache, but do not advance a round-robin cursor here. The first
	// cursor step belongs to the first actual RoundTrip below.
	proxyURLs, err := op.ProxyURLsForConfigStable(proxyConfigID, ctx)
	if err != nil {
		return nil, err
	}
	reportFailure := func(proxyURL string, failure error) {
		_ = op.ProxySubscriptionNodeReportFailure(proxyConfigID, proxyURL, failure, ctx)
	}
	if !perRequestRoundRobin {
		return GetHTTPClientCustomProxyPoolWithFailureReporter(proxyURLs, reportFailure)
	}

	return newHTTPClientDynamicProxyPoolWithFailureReporter(
		proxyURLs,
		func(requestCtx context.Context) ([]string, error) {
			return resolveProxyURLs(requestCtx)
		},
		reportFailure,
	)
}

// GetHTTPClientCustomProxyPool retries transport-level failures through the
// remaining proxies when the request body is empty or can be replayed.
func GetHTTPClientCustomProxyPool(proxyURLs []string) (*http.Client, error) {
	return GetHTTPClientCustomProxyPoolWithFailureReporter(proxyURLs, nil)
}

type ProxyFailureReporter func(proxyURL string, failure error)

type proxyURLResolver func(ctx context.Context) ([]string, error)

func GetHTTPClientCustomProxyPoolWithFailureReporter(proxyURLs []string, reportFailure ProxyFailureReporter) (*http.Client, error) {
	endpoints, err := newProxyTransportEndpoints(proxyURLs)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Transport:     &proxyFailoverTransport{endpoints: endpoints, reportFailure: reportFailure},
		CheckRedirect: outboundurl.CheckRedirect,
	}, nil
}

func newHTTPClientDynamicProxyPoolWithFailureReporter(initialProxyURLs []string, resolve proxyURLResolver, reportFailure ProxyFailureReporter) (*http.Client, error) {
	if resolve == nil {
		return nil, fmt.Errorf("proxy url resolver is nil")
	}
	initialEndpoints, err := newProxyTransportEndpoints(initialProxyURLs)
	if err != nil {
		return nil, err
	}
	endpointCache := newProxyEndpointCache(initialEndpoints)
	transport := &proxyFailoverTransport{
		endpoints:     initialEndpoints,
		endpointCache: endpointCache,
		reportFailure: reportFailure,
	}
	transport.resolveEndpoints = func(ctx context.Context) ([]proxyTransportEndpoint, error) {
		proxyURLs, err := resolve(ctx)
		if err != nil {
			return nil, err
		}
		return endpointCache.resolve(proxyURLs)
	}
	return &http.Client{
		Transport:     transport,
		CheckRedirect: outboundurl.CheckRedirect,
	}, nil
}

func newProxyTransportEndpoints(proxyURLs []string) ([]proxyTransportEndpoint, error) {
	proxyURLs, err := uniqueProxyURLs(proxyURLs)
	if err != nil {
		return nil, err
	}
	if len(proxyURLs) == 0 {
		return nil, fmt.Errorf("proxy url list is empty")
	}
	endpoints := make([]proxyTransportEndpoint, 0, len(proxyURLs))
	for _, proxyURL := range proxyURLs {
		endpoint, err := newProxyTransportEndpoint(proxyURL)
		if err != nil {
			for _, createdEndpoint := range endpoints {
				closeProxyTransportEndpoint(createdEndpoint)
			}
			return nil, err
		}
		endpoints = append(endpoints, endpoint)
	}
	return endpoints, nil
}

// uniqueProxyURLs trims and de-duplicates candidate URLs while preserving the
// resolver's order. Subscription rows are already normalized, but custom
// callers and a concurrently refreshed pool can still hand the transport a
// duplicate or whitespace-padded value. Building one transport per URL keeps
// connection pools and failover attempts bounded.
func uniqueProxyURLs(proxyURLs []string) ([]string, error) {
	if len(proxyURLs) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(proxyURLs))
	unique := make([]string, 0, len(proxyURLs))
	for _, raw := range proxyURLs {
		proxyURL := strings.TrimSpace(raw)
		if proxyURL == "" {
			return nil, fmt.Errorf("proxy url is empty")
		}
		if _, ok := seen[proxyURL]; ok {
			continue
		}
		seen[proxyURL] = struct{}{}
		unique = append(unique, proxyURL)
	}
	return unique, nil
}

func newProxyTransportEndpoint(proxyURL string) (proxyTransportEndpoint, error) {
	primaryClient, err := GetHTTPClientCustomProxy(proxyURL)
	if err != nil {
		return proxyTransportEndpoint{}, err
	}
	http1Client, err := newHTTPClientCustomProxyHTTP1(proxyURL)
	if err != nil {
		closeProxyTransportEndpoint(proxyTransportEndpoint{primary: primaryClient.Transport})
		return proxyTransportEndpoint{}, err
	}
	return proxyTransportEndpoint{
		proxyURL: proxyURL,
		primary:  primaryClient.Transport,
		http1:    http1Client.Transport,
	}, nil
}

type proxyTransportEndpoint struct {
	proxyURL string
	primary  http.RoundTripper
	http1    http.RoundTripper
}

type proxyFailoverTransport struct {
	endpoints        []proxyTransportEndpoint
	resolveEndpoints func(ctx context.Context) ([]proxyTransportEndpoint, error)
	endpointCache    *proxyEndpointCache
	reportFailure    ProxyFailureReporter
}

func (t *proxyFailoverTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("request is nil")
	}
	endpoints, err := t.endpointsForRequest(req.Context())
	if err != nil {
		return nil, err
	}
	var lastErr error
	attempt := 0
	for _, endpoint := range endpoints {
		attemptReq, replayable, err := proxyRequestForAttempt(req, attempt)
		if err != nil {
			return nil, err
		}
		if !replayable {
			break
		}
		resp, rawErr := roundTripWithProxyTrace(endpoint.proxyURL, endpoint.primary, attemptReq)
		if rawErr == nil {
			return resp, nil
		}
		redactedErr := redactProxyError(rawErr, endpoint.proxyURL)
		closeProxyFailureResponse(resp)
		lastErr = redactedErr
		if req.Context().Err() != nil {
			return nil, req.Context().Err()
		}
		if !isSafeProxyFailoverError(rawErr) {
			return nil, redactedErr
		}

		lastProxyFailure := rawErr
		if outboundurl.IsTLSHandshakeFailure(rawErr) && endpoint.http1 != nil {
			attempt++
			fallbackReq, canFallback, replayErr := proxyRequestForAttempt(req, attempt)
			if replayErr != nil {
				return nil, replayErr
			}
			if canFallback {
				fallbackResp, rawFallbackErr := roundTripWithProxyTrace(endpoint.proxyURL, endpoint.http1, fallbackReq)
				if rawFallbackErr == nil {
					return fallbackResp, nil
				}
				fallbackErr := redactProxyError(rawFallbackErr, endpoint.proxyURL)
				closeProxyFailureResponse(fallbackResp)
				lastErr = fallbackErr
				lastProxyFailure = rawFallbackErr
				if req.Context().Err() != nil {
					return nil, req.Context().Err()
				}
				if !isSafeProxyFailoverError(rawFallbackErr) {
					return nil, fallbackErr
				}
			}
		}
		if t.reportFailure != nil && isProxyNodeFailure(lastProxyFailure, endpoint.proxyURL) {
			t.reportFailure(endpoint.proxyURL, lastErr)
		}
		attempt++
	}
	if lastErr == nil {
		lastErr = io.ErrUnexpectedEOF
	}
	return nil, fmt.Errorf("all proxy nodes failed: %w", lastErr)
}

func (t *proxyFailoverTransport) endpointsForRequest(ctx context.Context) ([]proxyTransportEndpoint, error) {
	if t.resolveEndpoints != nil {
		endpoints, err := t.resolveEndpoints(ctx)
		if err != nil {
			return nil, fmt.Errorf("resolve proxy pool candidates: %w", err)
		}
		if len(endpoints) == 0 {
			return nil, fmt.Errorf("proxy url list is empty")
		}
		return endpoints, nil
	}
	if len(t.endpoints) == 0 {
		return nil, fmt.Errorf("proxy url list is empty")
	}
	return t.endpoints, nil
}

type proxyEndpointCache struct {
	mu        sync.RWMutex
	endpoints map[string]proxyTransportEndpoint
}

func newProxyEndpointCache(initial []proxyTransportEndpoint) *proxyEndpointCache {
	cache := &proxyEndpointCache{endpoints: make(map[string]proxyTransportEndpoint, len(initial))}
	for _, endpoint := range initial {
		cache.endpoints[endpoint.proxyURL] = endpoint
	}
	return cache
}

func (c *proxyEndpointCache) resolve(proxyURLs []string) ([]proxyTransportEndpoint, error) {
	proxyURLs, err := uniqueProxyURLs(proxyURLs)
	if err != nil {
		return nil, err
	}
	if len(proxyURLs) == 0 {
		return nil, fmt.Errorf("proxy url list is empty")
	}

	// Transport construction clones TLS/proxy state and can allocate internal
	// dialers. Do it outside the cache lock so a refreshed subscription does
	// not block concurrent requests that only need an already-cached endpoint.
	missing := make([]string, 0, len(proxyURLs))
	c.mu.RLock()
	for _, proxyURL := range proxyURLs {
		if _, ok := c.endpoints[proxyURL]; !ok {
			missing = append(missing, proxyURL)
		}
	}
	c.mu.RUnlock()

	created := make(map[string]proxyTransportEndpoint, len(missing))
	for _, proxyURL := range missing {
		endpoint, createErr := newProxyTransportEndpoint(proxyURL)
		if createErr != nil {
			for _, createdEndpoint := range created {
				closeProxyTransportEndpoint(createdEndpoint)
			}
			return nil, createErr
		}
		created[proxyURL] = endpoint
	}

	active := make(map[string]struct{}, len(proxyURLs))
	for _, proxyURL := range proxyURLs {
		active[proxyURL] = struct{}{}
	}
	var toClose []proxyTransportEndpoint
	c.mu.Lock()
	for proxyURL, endpoint := range created {
		if _, ok := c.endpoints[proxyURL]; ok {
			// Another concurrent resolver won the race to install this URL.
			// Its endpoint is equivalent, so discard the duplicate we built.
			toClose = append(toClose, endpoint)
			continue
		}
		c.endpoints[proxyURL] = endpoint
	}
	for proxyURL, endpoint := range c.endpoints {
		if _, ok := active[proxyURL]; ok {
			continue
		}
		delete(c.endpoints, proxyURL)
		// A stale URL must not keep idle sockets or a full Transport alive for
		// the lifetime of a long-lived dynamic client.
		toClose = append(toClose, endpoint)
	}
	endpoints := make([]proxyTransportEndpoint, 0, len(proxyURLs))
	for _, proxyURL := range proxyURLs {
		if endpoint, ok := c.endpoints[proxyURL]; ok {
			endpoints = append(endpoints, endpoint)
		}
	}
	c.mu.Unlock()
	for _, endpoint := range toClose {
		closeProxyTransportEndpoint(endpoint)
	}
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("proxy url list is empty")
	}
	return endpoints, nil
}

func closeProxyTransportEndpoint(endpoint proxyTransportEndpoint) {
	for _, transport := range []http.RoundTripper{endpoint.primary, endpoint.http1} {
		if closer, ok := transport.(interface{ CloseIdleConnections() }); ok {
			closer.CloseIdleConnections()
		}
	}
}

func (c *proxyEndpointCache) all() []proxyTransportEndpoint {
	c.mu.RLock()
	defer c.mu.RUnlock()
	endpoints := make([]proxyTransportEndpoint, 0, len(c.endpoints))
	for _, endpoint := range c.endpoints {
		endpoints = append(endpoints, endpoint)
	}
	return endpoints
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

// Safe failover errors happen before the upstream HTTP request can be
// processed. Retrying ambiguous read/write failures can duplicate billable
// model requests.
func isSafeProxyFailoverError(err error) bool {
	if outboundurl.IsTLSHandshakeFailure(err) || errors.Is(err, outboundurl.ErrProxyDialTimeout) || hasDialFailure(err) {
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
	if errors.Is(err, outboundurl.ErrProxyDialTimeout) {
		return true
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
	endpoints := t.endpoints
	if t.endpointCache != nil {
		endpoints = t.endpointCache.all()
	}
	for _, endpoint := range endpoints {
		closeProxyTransportEndpoint(endpoint)
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

	if err := outboundurl.ConfigureProxyTransport(cloned, proxyURL); err != nil {
		return nil, err
	}
	if http1Only {
		outboundurl.ConfigureHTTP1Transport(cloned)
	}

	return &http.Client{
		Transport:     outboundurl.WrapTransport(cloned),
		CheckRedirect: outboundurl.CheckRedirect,
	}, nil
}
