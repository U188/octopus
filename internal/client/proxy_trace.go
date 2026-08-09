package client

import (
	"context"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
)

type proxyTraceContextKey struct{}

// ProxyRoute is the credential-free proxy endpoint selected for an outbound
// request plus the concrete proxy server IP observed by the TCP connection.
// It deliberately does not call either value an egress IP: only the upstream
// service (or a dedicated IP echo endpoint) can observe the public exit IP.
type ProxyRoute struct {
	ProxyNode string
	ProxyIP   string
}

// ProxyTrace stores request-scoped proxy routing information. A trace may be
// updated more than once when safe proxy failover tries multiple nodes; the
// final snapshot therefore identifies the node that ultimately responded, or
// the last node attempted when every candidate failed.
type ProxyTrace struct {
	mu    sync.RWMutex
	route ProxyRoute
}

func NewProxyTrace() *ProxyTrace {
	return &ProxyTrace{}
}

// EnsureProxyTrace returns a context carrying a proxy trace and the trace
// itself. Existing traces are preserved so nested transports and WebSocket
// connection pools keep writing to the request-level snapshot; callers such
// as connection warmups still receive a private trace that can be persisted
// with the reusable connection.
func EnsureProxyTrace(ctx context.Context) (context.Context, *ProxyTrace) {
	if ctx == nil {
		ctx = context.Background()
	}
	if trace := ProxyTraceFromContext(ctx); trace != nil {
		return ctx, trace
	}
	trace := NewProxyTrace()
	return WithProxyTrace(ctx, trace), trace
}

func WithProxyTrace(ctx context.Context, trace *ProxyTrace) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if trace == nil {
		return ctx
	}
	return context.WithValue(ctx, proxyTraceContextKey{}, trace)
}

func ProxyTraceFromContext(ctx context.Context) *ProxyTrace {
	if ctx == nil {
		return nil
	}
	trace, _ := ctx.Value(proxyTraceContextKey{}).(*ProxyTrace)
	return trace
}

func (t *ProxyTrace) Snapshot() ProxyRoute {
	if t == nil {
		return ProxyRoute{}
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.route
}

func (t *ProxyTrace) Record(route ProxyRoute) {
	if t == nil {
		return
	}
	route.ProxyNode = strings.TrimSpace(route.ProxyNode)
	route.ProxyIP = strings.TrimSpace(route.ProxyIP)
	if route.ProxyNode == "" && route.ProxyIP == "" {
		return
	}
	t.mu.Lock()
	// A transport fallback can report the same node twice. If the fallback
	// fails before obtaining a connection, keep the IP captured by the first
	// attempt instead of replacing it with an empty value. Do not carry an IP
	// across different nodes, because that would make a failed final attempt
	// look as if it used the previous node's address.
	if route.ProxyNode == "" {
		route.ProxyNode = t.route.ProxyNode
	}
	if route.ProxyIP == "" && (t.route.ProxyNode == "" || route.ProxyNode == t.route.ProxyNode) {
		route.ProxyIP = t.route.ProxyIP
	}
	t.route = route
	t.mu.Unlock()
}

func RecordProxyRoute(ctx context.Context, route ProxyRoute) {
	if trace := ProxyTraceFromContext(ctx); trace != nil {
		trace.Record(route)
	}
}

func roundTripWithProxyTrace(proxyURL string, transport http.RoundTripper, req *http.Request) (*http.Response, error) {
	if transport == nil {
		return nil, http.ErrNotSupported
	}
	if req == nil {
		return transport.RoundTrip(req)
	}
	trace := ProxyTraceFromContext(req.Context())
	if trace == nil {
		return transport.RoundTrip(req)
	}

	route := ProxyRoute{
		ProxyNode: sanitizedProxyNode(proxyURL),
	}
	trace.Record(route)

	var remoteMu sync.Mutex
	remoteAddress := ""
	clientTrace := &httptrace.ClientTrace{
		ConnectDone: func(_ string, address string, err error) {
			if err != nil {
				return
			}
			remoteMu.Lock()
			// GotConn below is preferred because it exposes the concrete
			// net.Conn address. ConnectDone is a useful fallback for custom
			// transports that do not populate RemoteAddr consistently.
			if remoteAddress == "" {
				remoteAddress = address
			}
			remoteMu.Unlock()
		},
		GotConn: func(info httptrace.GotConnInfo) {
			if info.Conn == nil || info.Conn.RemoteAddr() == nil {
				return
			}
			remoteMu.Lock()
			remoteAddress = info.Conn.RemoteAddr().String()
			remoteMu.Unlock()
		},
	}
	tracedReq := req.Clone(httptrace.WithClientTrace(req.Context(), clientTrace))
	resp, err := transport.RoundTrip(tracedReq)

	remoteMu.Lock()
	if ip := addressIP(remoteAddress); ip != "" {
		route.ProxyIP = ip
	}
	remoteMu.Unlock()
	trace.Record(route)
	return resp, err
}

// proxyCredentialRedactedError keeps the original error available to
// errors.Is/errors.As while exposing a credential-free Error string to relay
// logs and API responses.
type proxyCredentialRedactedError struct {
	err     error
	message string
}

func (e *proxyCredentialRedactedError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func (e *proxyCredentialRedactedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func redactProxyError(err error, proxyURL string) error {
	if err == nil {
		return nil
	}
	parsed, parseErr := url.Parse(strings.TrimSpace(proxyURL))
	if parseErr != nil || parsed.User == nil {
		return err
	}

	message := err.Error()
	for _, secret := range []string{
		parsed.User.String(),
		parsed.User.Username(),
	} {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[credentials]")
		}
	}
	if password, ok := parsed.User.Password(); ok && password != "" {
		message = strings.ReplaceAll(message, password, "[credentials]")
	}
	if sanitized := sanitizedProxyNode(proxyURL); sanitized != "" {
		message = strings.ReplaceAll(message, strings.TrimSpace(proxyURL), sanitized)
	}
	if message == err.Error() {
		return err
	}
	return &proxyCredentialRedactedError{err: err, message: message}
}

func sanitizedProxyNode(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	host := parsed.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	} else {
		host = strings.ToLower(host)
	}
	if port := parsed.Port(); port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		// Hostname strips IPv6 brackets; restore them when no port is present
		// so the credential-free display value remains a valid URL.
		host = "[" + host + "]"
	}
	if parsed.Scheme == "" {
		return host
	}
	return strings.ToLower(parsed.Scheme) + "://" + host
}

func addressIP(address string) string {
	address = strings.TrimSpace(address)
	if address == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = strings.Trim(address, "[]")
	}
	if zoneIndex := strings.LastIndex(host, "%"); zoneIndex >= 0 {
		host = host[:zoneIndex]
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return ""
}
