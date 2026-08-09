package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	dbpkg "github.com/U188/octopus/internal/db"
	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/op"
	"github.com/U188/octopus/internal/outboundurl"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type closeIdleSpy struct {
	closed atomic.Int64
}

func (s *closeIdleSpy) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
}

func (s *closeIdleSpy) CloseIdleConnections() {
	s.closed.Add(1)
}

func TestProxyFailoverTransportRetriesReplayableRequest(t *testing.T) {
	var firstCalls atomic.Int64
	var secondCalls atomic.Int64
	var reported atomic.Int64
	transport := &proxyFailoverTransport{
		endpoints: []proxyTransportEndpoint{
			{proxyURL: "http://192.0.2.1:8080", primary: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				firstCalls.Add(1)
				_, _ = io.ReadAll(req.Body)
				return nil, &net.OpError{Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 8080}, Err: errors.New("connection refused")}
			})},
			{proxyURL: "http://proxy-2.example:8080", primary: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				secondCalls.Add(1)
				body, _ := io.ReadAll(req.Body)
				if string(body) != "request-body" {
					t.Fatalf("replayed body = %q", body)
				}
				return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody, Request: req}, nil
			})},
		},
		reportFailure: func(proxyURL string, failure error) {
			if proxyURL != "http://192.0.2.1:8080" || failure == nil {
				t.Fatalf("unexpected failure report: proxy=%q error=%v", proxyURL, failure)
			}
			reported.Add(1)
		},
	}
	req, err := http.NewRequest(http.MethodPost, "https://example.com", bytes.NewBufferString("request-body"))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	proxyTrace := NewProxyTrace()
	req = req.WithContext(WithProxyTrace(req.Context(), proxyTrace))
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip with failover: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent || firstCalls.Load() != 1 || secondCalls.Load() != 1 {
		t.Fatalf("unexpected failover result: status=%d first=%d second=%d", resp.StatusCode, firstCalls.Load(), secondCalls.Load())
	}
	if reported.Load() != 1 {
		t.Fatalf("failure reporter called %d times", reported.Load())
	}
	if route := proxyTrace.Snapshot(); route.ProxyNode != "http://proxy-2.example:8080" || route.ProxyIP != "" {
		t.Fatalf("selected proxy route = %#v", route)
	}
}

func TestProxyTraceRemovesCredentialsWithoutInventingConnectedIP(t *testing.T) {
	transport := &proxyFailoverTransport{
		endpoints: []proxyTransportEndpoint{{
			proxyURL: "http://user:secret@192.0.2.44:8080",
			primary: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody, Request: req}, nil
			}),
		}},
	}
	trace := NewProxyTrace()
	req, err := http.NewRequestWithContext(WithProxyTrace(context.Background(), trace), http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	if _, err := transport.RoundTrip(req); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	route := trace.Snapshot()
	if route.ProxyNode != "http://192.0.2.44:8080" || route.ProxyIP != "" {
		t.Fatalf("sanitized proxy route = %#v", route)
	}
	if strings.Contains(route.ProxyNode, "user") || strings.Contains(route.ProxyNode, "secret") {
		t.Fatalf("proxy credentials leaked in route: %#v", route)
	}
	if got := sanitizedProxyNode("HTTP://user:secret@[2001:db8::44]"); got != "http://[2001:db8::44]" {
		t.Fatalf("sanitized IPv6 proxy node = %q", got)
	}
	if got := sanitizedProxyNode("HTTPS://user:secret@Proxy.Example:8443/path?token=hidden"); got != "https://proxy.example:8443" {
		t.Fatalf("sanitized hostname proxy node = %q", got)
	}
}

func TestProxyFailoverErrorRedactsProxyCredentialsButPreservesCause(t *testing.T) {
	cause := errors.New("dial failed for socks5://user:secret@proxy.example:1080")
	transport := &proxyFailoverTransport{
		endpoints: []proxyTransportEndpoint{{
			proxyURL: "socks5://user:secret@proxy.example:1080",
			primary: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, cause
			}),
		}},
	}
	req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	result, err := transport.RoundTrip(req)
	if result != nil {
		result.Body.Close()
	}
	if err == nil {
		t.Fatal("expected proxy failure")
	}
	if !errors.Is(err, cause) {
		t.Fatalf("redacted error lost its cause: %v", err)
	}
	if strings.Contains(err.Error(), "user") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("proxy credentials leaked in error: %v", err)
	}
}

func TestProxyFailoverClassificationUsesUnredactedCause(t *testing.T) {
	proxyURL := "http://proxyconnect:secret@proxy.example:8080"
	proxyErr := errors.New("proxyconnect tcp: unexpected status: 429 via " + proxyURL)
	var secondCalls atomic.Int64
	var reported atomic.Int64
	transport := &proxyFailoverTransport{
		endpoints: []proxyTransportEndpoint{
			{
				proxyURL: proxyURL,
				primary: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return nil, proxyErr
				}),
			},
			{
				proxyURL: "http://proxy-2.example:8080",
				primary: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					secondCalls.Add(1)
					return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody, Request: req}, nil
				}),
			},
		},
		reportFailure: func(reportedProxyURL string, failure error) {
			if reportedProxyURL != proxyURL {
				t.Fatalf("reported proxy URL = %q, want %q", reportedProxyURL, proxyURL)
			}
			if strings.Contains(failure.Error(), "secret") || strings.Contains(failure.Error(), "proxyconnect:secret@") || strings.Contains(failure.Error(), proxyURL) {
				t.Fatalf("reported failure leaked proxy credentials: %v", failure)
			}
			reported.Add(1)
		},
	}
	req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip with credential-like username: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent || secondCalls.Load() != 1 || reported.Load() != 1 {
		t.Fatalf("unexpected failover result: status=%d second=%d reported=%d", resp.StatusCode, secondCalls.Load(), reported.Load())
	}
}

func TestEnsureProxyTraceCreatesAndPreservesTrace(t *testing.T) {
	ctx, trace := EnsureProxyTrace(nil)
	if ctx == nil || trace == nil || ProxyTraceFromContext(ctx) != trace {
		t.Fatalf("created proxy trace was not attached: ctx=%v trace=%p", ctx, trace)
	}

	existing := NewProxyTrace()
	existingCtx := WithProxyTrace(context.Background(), existing)
	ensuredCtx, ensured := EnsureProxyTrace(existingCtx)
	if ensuredCtx != existingCtx || ensured != existing {
		t.Fatalf("existing proxy trace was replaced: ctx_equal=%t trace=%p want=%p", ensuredCtx == existingCtx, ensured, existing)
	}
}

func TestProxyTraceKeepsIPAcrossSameNodeFallback(t *testing.T) {
	trace := NewProxyTrace()
	trace.Record(ProxyRoute{ProxyNode: "http://proxy.example:8080", ProxyIP: "198.51.100.10"})
	trace.Record(ProxyRoute{ProxyNode: "http://proxy.example:8080"})
	if got := trace.Snapshot(); got.ProxyNode != "http://proxy.example:8080" || got.ProxyIP != "198.51.100.10" {
		t.Fatalf("same-node fallback discarded route IP: %#v", got)
	}
	trace.Record(ProxyRoute{ProxyNode: "http://other.example:8080"})
	if got := trace.Snapshot(); got.ProxyNode != "http://other.example:8080" || got.ProxyIP != "" {
		t.Fatalf("different-node fallback inherited stale IP: %#v", got)
	}
}

func TestRoundTripWithProxyTraceUsesConnectDoneWhenRemoteAddrIsUnavailable(t *testing.T) {
	trace := NewProxyTrace()
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if current := httptrace.ContextClientTrace(req.Context()); current != nil && current.ConnectDone != nil {
			current.ConnectDone("tcp", "198.51.100.11:8080", nil)
		}
		return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody, Request: req}, nil
	})
	req, err := http.NewRequestWithContext(WithProxyTrace(context.Background(), trace), http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	if _, err := roundTripWithProxyTrace("http://proxy.example:8080", transport, req); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if got := trace.Snapshot(); got.ProxyIP != "198.51.100.11" {
		t.Fatalf("proxy IP from ConnectDone = %q, want 198.51.100.11", got.ProxyIP)
	}
}

func TestProxyEndpointCachePrunesRemovedNodesAndDeduplicatesURLs(t *testing.T) {
	old := &closeIdleSpy{}
	cache := &proxyEndpointCache{endpoints: map[string]proxyTransportEndpoint{
		"http://old.example:8080": {proxyURL: "http://old.example:8080", primary: old},
	}}
	endpoints, err := cache.resolve([]string{
		" http://new.example:8080 ",
		"http://new.example:8080",
	})
	if err != nil {
		t.Fatalf("resolve refreshed endpoint cache: %v", err)
	}
	if len(endpoints) != 1 || endpoints[0].proxyURL != "http://new.example:8080" {
		t.Fatalf("resolved endpoints = %#v, want one new endpoint", endpoints)
	}
	if old.closed.Load() == 0 {
		t.Fatal("removed endpoint did not release idle connections")
	}
	if _, ok := cache.endpoints["http://old.example:8080"]; ok {
		t.Fatal("removed endpoint remained in cache")
	}

	endpoints, err = newProxyTransportEndpoints([]string{
		" http://same.example:8080 ",
		"http://same.example:8080",
	})
	if err != nil {
		t.Fatalf("create deduplicated endpoints: %v", err)
	}
	if len(endpoints) != 1 || endpoints[0].proxyURL != "http://same.example:8080" {
		t.Fatalf("deduplicated endpoints = %#v", endpoints)
	}
}

func TestProxyFailoverTransportRetriesAndReportsProxyDialTimeout(t *testing.T) {
	var secondCalls atomic.Int64
	var reported atomic.Int64
	transport := &proxyFailoverTransport{
		endpoints: []proxyTransportEndpoint{
			{proxyURL: "socks5://proxy-1.example:1080", primary: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, fmt.Errorf("connect SOCKS proxy: %w", outboundurl.ErrProxyDialTimeout)
			})},
			{proxyURL: "socks5://proxy-2.example:1080", primary: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				secondCalls.Add(1)
				return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody, Request: req}, nil
			})},
		},
		reportFailure: func(proxyURL string, failure error) {
			if proxyURL != "socks5://proxy-1.example:1080" || !errors.Is(failure, outboundurl.ErrProxyDialTimeout) {
				t.Fatalf("unexpected failure report: proxy=%q error=%v", proxyURL, failure)
			}
			reported.Add(1)
		},
	}
	req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip with SOCKS timeout failover: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent || secondCalls.Load() != 1 || reported.Load() != 1 {
		t.Fatalf("unexpected timeout failover result: status=%d second=%d reported=%d", resp.StatusCode, secondCalls.Load(), reported.Load())
	}
}

func TestProxyFailoverTransportDoesNotRetryAmbiguousPOSTFailure(t *testing.T) {
	var secondCalls atomic.Int64
	var reported atomic.Int64
	transport := &proxyFailoverTransport{
		endpoints: []proxyTransportEndpoint{
			{proxyURL: "http://proxy-1.example:8080", primary: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, io.ErrUnexpectedEOF
			})},
			{proxyURL: "http://proxy-2.example:8080", primary: roundTripFunc(func(*http.Request) (*http.Response, error) {
				secondCalls.Add(1)
				return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
			})},
		},
		reportFailure: func(string, error) { reported.Add(1) },
	}
	req, err := http.NewRequest(http.MethodPost, "https://example.com", bytes.NewBufferString("request-body"))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	if _, err := transport.RoundTrip(req); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ambiguous request failure = %v, want unexpected EOF", err)
	}
	if secondCalls.Load() != 0 || reported.Load() != 0 {
		t.Fatalf("ambiguous POST was retried or quarantined: second=%d reported=%d", secondCalls.Load(), reported.Load())
	}
}

func TestProxyFailoverTransportDoesNotQuarantineTargetTLSFailure(t *testing.T) {
	var secondCalls atomic.Int64
	var reported atomic.Int64
	tlsFailure := errors.New("remote error: tls: handshake failure")
	transport := &proxyFailoverTransport{
		endpoints: []proxyTransportEndpoint{
			{
				proxyURL: "http://proxy-1.example:8080",
				primary:  roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, tlsFailure }),
				http1:    roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, tlsFailure }),
			},
			{proxyURL: "http://proxy-2.example:8080", primary: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				secondCalls.Add(1)
				return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody, Request: req}, nil
			})},
		},
		reportFailure: func(string, error) { reported.Add(1) },
	}
	req, err := http.NewRequest(http.MethodPost, "https://example.com", bytes.NewBufferString("request-body"))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	if _, err := transport.RoundTrip(req); err != nil {
		t.Fatalf("retry target TLS failure: %v", err)
	}
	if secondCalls.Load() != 1 || reported.Load() != 0 {
		t.Fatalf("target TLS failure retry result: second=%d reported=%d", secondCalls.Load(), reported.Load())
	}
}

func TestProxyFailoverTransportRetriesTLSHandshakeWithHTTP1(t *testing.T) {
	var fallbackCalls atomic.Int64
	var reported atomic.Int64
	transport := &proxyFailoverTransport{
		endpoints: []proxyTransportEndpoint{{
			proxyURL: "http://proxy.example:8080",
			primary: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("remote error: tls: handshake failure")
			}),
			http1: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				fallbackCalls.Add(1)
				body, _ := io.ReadAll(req.Body)
				if string(body) != "request-body" {
					t.Fatalf("HTTP/1 fallback body = %q", body)
				}
				return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody, Request: req}, nil
			}),
		}},
		reportFailure: func(string, error) { reported.Add(1) },
	}
	req, err := http.NewRequest(http.MethodPost, "https://example.com", bytes.NewBufferString("request-body"))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("HTTP/1 fallback failed: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent || fallbackCalls.Load() != 1 || reported.Load() != 0 {
		t.Fatalf("unexpected HTTP/1 fallback result: status=%d fallback=%d reported=%d", resp.StatusCode, fallbackCalls.Load(), reported.Load())
	}
}

func TestProxyFailoverTransportDoesNotRetryNonReplayableBody(t *testing.T) {
	var secondCalls atomic.Int64
	transport := &proxyFailoverTransport{endpoints: []proxyTransportEndpoint{
		{proxyURL: "http://proxy-1.example:8080", primary: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
		})},
		{proxyURL: "http://proxy-2.example:8080", primary: roundTripFunc(func(*http.Request) (*http.Response, error) {
			secondCalls.Add(1)
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
		})},
	}}
	req, err := http.NewRequest(http.MethodPost, "https://example.com", struct{ io.Reader }{Reader: strings.NewReader("stream")})
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	if req.GetBody != nil {
		t.Fatal("test request unexpectedly has a replay function")
	}
	if _, err := transport.RoundTrip(req); err == nil || !strings.Contains(err.Error(), "all proxy nodes failed") {
		t.Fatalf("expected first proxy failure, got %v", err)
	}
	if secondCalls.Load() != 0 {
		t.Fatalf("non-replayable request retried %d times", secondCalls.Load())
	}
}

func TestProxyFailoverTransportResolvesCandidatesForEveryRequest(t *testing.T) {
	endpoint := func(name string) proxyTransportEndpoint {
		return proxyTransportEndpoint{
			proxyURL: "http://" + name + ".example:8080",
			primary: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusNoContent,
					Header:     http.Header{"X-Test-Proxy": []string{name}},
					Body:       http.NoBody,
					Request:    req,
				}, nil
			}),
		}
	}
	first := endpoint("proxy-1")
	second := endpoint("proxy-2")
	var resolverCalls atomic.Int64
	transport := &proxyFailoverTransport{
		resolveEndpoints: func(context.Context) ([]proxyTransportEndpoint, error) {
			if resolverCalls.Add(1)%2 == 1 {
				return []proxyTransportEndpoint{first, second}, nil
			}
			return []proxyTransportEndpoint{second, first}, nil
		},
	}

	for index, want := range []string{"proxy-1", "proxy-2", "proxy-1", "proxy-2"} {
		req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
		if err != nil {
			t.Fatalf("create request %d: %v", index, err)
		}
		resp, err := transport.RoundTrip(req)
		if err != nil {
			t.Fatalf("round trip %d: %v", index, err)
		}
		if got := resp.Header.Get("X-Test-Proxy"); got != want {
			t.Fatalf("request %d used proxy %q, want %q", index, got, want)
		}
	}
	if resolverCalls.Load() != 4 {
		t.Fatalf("candidate resolver called %d times, want 4", resolverCalls.Load())
	}
}

func TestProxyFailoverTransportDynamicCandidatesAreConcurrentSafe(t *testing.T) {
	var firstCalls atomic.Int64
	var secondCalls atomic.Int64
	first := proxyTransportEndpoint{
		proxyURL: "http://proxy-1.example:8080",
		primary: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			firstCalls.Add(1)
			return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody, Request: req}, nil
		}),
	}
	second := proxyTransportEndpoint{
		proxyURL: "http://proxy-2.example:8080",
		primary: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			secondCalls.Add(1)
			return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody, Request: req}, nil
		}),
	}
	var resolverCalls atomic.Int64
	transport := &proxyFailoverTransport{
		resolveEndpoints: func(context.Context) ([]proxyTransportEndpoint, error) {
			if resolverCalls.Add(1)%2 == 1 {
				return []proxyTransportEndpoint{first, second}, nil
			}
			return []proxyTransportEndpoint{second, first}, nil
		},
	}

	const requestCount = 64
	var wg sync.WaitGroup
	errs := make(chan error, requestCount)
	for index := 0; index < requestCount; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
			if err != nil {
				errs <- err
				return
			}
			resp, err := transport.RoundTrip(req)
			if err != nil {
				errs <- err
				return
			}
			_ = resp.Body.Close()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent round trip: %v", err)
		}
	}
	if resolverCalls.Load() != requestCount {
		t.Fatalf("candidate resolver called %d times, want %d", resolverCalls.Load(), requestCount)
	}
	if firstCalls.Load() != requestCount/2 || secondCalls.Load() != requestCount/2 {
		t.Fatalf("unexpected proxy distribution: first=%d second=%d", firstCalls.Load(), secondCalls.Load())
	}
}

func TestDynamicProxyPoolResolvesCandidatesOnEveryRoundTrip(t *testing.T) {
	var resolverCalls atomic.Int64
	httpClient, err := newHTTPClientDynamicProxyPoolWithFailureReporter(
		[]string{"http://proxy-1.example:8080"},
		func(context.Context) ([]string, error) {
			resolverCalls.Add(1)
			return []string{"http://proxy-2.example:8080"}, nil
		},
		nil,
	)
	if err != nil {
		t.Fatalf("create dynamic proxy pool: %v", err)
	}
	transport, ok := httpClient.Transport.(*proxyFailoverTransport)
	if !ok {
		t.Fatalf("dynamic transport type = %T", httpClient.Transport)
	}
	first, err := transport.endpointsForRequest(context.Background())
	if err != nil {
		t.Fatalf("resolve first candidates: %v", err)
	}
	if resolverCalls.Load() != 1 || len(first) != 1 || first[0].proxyURL != "http://proxy-2.example:8080" {
		t.Fatalf("unexpected first candidates: calls=%d endpoints=%#v", resolverCalls.Load(), first)
	}
	second, err := transport.endpointsForRequest(context.Background())
	if err != nil {
		t.Fatalf("resolve second candidates: %v", err)
	}
	if resolverCalls.Load() != 2 || len(second) != 1 || second[0].proxyURL != "http://proxy-2.example:8080" {
		t.Fatalf("unexpected second candidates: calls=%d endpoints=%#v", resolverCalls.Load(), second)
	}
}

func TestGetHTTPClientProxyPoolHonorsPerRequestOption(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "proxy-round-robin.db")
	if err := dbpkg.InitDB("sqlite", dbPath, false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() { _ = dbpkg.Close() })
	if err := op.InitCache(); err != nil {
		t.Fatalf("InitCache failed: %v", err)
	}
	proxyConfig := model.ProxyConfiguration{
		Name:    "round-robin-option-test",
		URL:     "http://proxy.example:8080",
		Enabled: true,
	}
	if err := op.ProxyConfigurationCreate(&proxyConfig, context.Background()); err != nil {
		t.Fatalf("create proxy configuration: %v", err)
	}

	dynamicClient, err := GetHTTPClientProxyPool(context.Background(), proxyConfig.ID, true)
	if err != nil {
		t.Fatalf("create dynamic proxy pool client: %v", err)
	}
	dynamicTransport, ok := dynamicClient.Transport.(*proxyFailoverTransport)
	if !ok || dynamicTransport.resolveEndpoints == nil {
		t.Fatalf("expected dynamic proxy transport, got %T", dynamicClient.Transport)
	}

	staticClient, err := GetHTTPClientProxyPool(context.Background(), proxyConfig.ID, false)
	if err != nil {
		t.Fatalf("create static proxy pool client: %v", err)
	}
	staticTransport, ok := staticClient.Transport.(*proxyFailoverTransport)
	if !ok || staticTransport.resolveEndpoints != nil {
		t.Fatalf("expected static proxy transport, got %T", staticClient.Transport)
	}
}

func TestGetHTTPClientProxyPoolScopedAdvancesCursorOnRoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "proxy-round-trip-cursor.db")
	if err := dbpkg.InitDB("sqlite", dbPath, false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() { _ = dbpkg.Close() })
	if err := op.InitCache(); err != nil {
		t.Fatalf("InitCache failed: %v", err)
	}
	ctx := context.Background()
	proxyConfig := model.ProxyConfiguration{
		Name:                   "round-trip-cursor-test",
		URL:                    "https://example.com/round-trip-cursor.txt",
		Type:                   model.ProxyConfigurationTypeSubscription,
		Enabled:                true,
		RefreshIntervalMinutes: 30,
	}
	if err := op.ProxyConfigurationCreate(&proxyConfig, ctx); err != nil {
		t.Fatalf("create proxy configuration: %v", err)
	}
	nodes := []model.ProxySubscriptionNode{
		{ProxyConfigurationID: proxyConfig.ID, URL: "http://proxy-1.example:8080", Active: true, HealthStatus: model.ProxyTestHealthHealthy, LatencyMS: 10},
		{ProxyConfigurationID: proxyConfig.ID, URL: "http://proxy-2.example:8080", Active: true, HealthStatus: model.ProxyTestHealthHealthy, LatencyMS: 20},
	}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&nodes).Error; err != nil {
		t.Fatalf("create proxy nodes: %v", err)
	}

	httpClient, err := GetHTTPClientProxyPoolScoped(ctx, proxyConfig.ID, true, "site:17")
	if err != nil {
		t.Fatalf("create scoped proxy client: %v", err)
	}
	transport, ok := httpClient.Transport.(*proxyFailoverTransport)
	if !ok {
		t.Fatalf("scoped transport type = %T", httpClient.Transport)
	}
	first, err := transport.endpointsForRequest(ctx)
	if err != nil {
		t.Fatalf("resolve first request candidates: %v", err)
	}
	second, err := transport.endpointsForRequest(ctx)
	if err != nil {
		t.Fatalf("resolve second request candidates: %v", err)
	}
	if len(first) != 2 || len(second) != 2 || first[0].proxyURL != nodes[0].URL || second[0].proxyURL != nodes[1].URL {
		t.Fatalf("request-scoped rotation = %#v then %#v", first, second)
	}
}

func TestNewHTTPClientCustomProxyHTTP1DisablesHTTP2ALPN(t *testing.T) {
	client, err := newHTTPClientCustomProxyHTTP1("http://127.0.0.1:8080")
	if err != nil {
		t.Fatalf("create HTTP/1 proxy client: %v", err)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("HTTP/1 client transport type = %T", client.Transport)
	}
	if transport.ForceAttemptHTTP2 {
		t.Fatal("HTTP/1 transport still forces HTTP/2")
	}
	if transport.Protocols == nil || !transport.Protocols.HTTP1() || transport.Protocols.HTTP2() {
		t.Fatalf("unexpected transport protocols: %+v", transport.Protocols)
	}
	if transport.TLSClientConfig == nil || len(transport.TLSClientConfig.NextProtos) != 1 || transport.TLSClientConfig.NextProtos[0] != "http/1.1" {
		t.Fatalf("unexpected TLS ALPN protocols: %+v", transport.TLSClientConfig)
	}
	if transport.TLSNextProto == nil || len(transport.TLSNextProto) != 0 {
		t.Fatalf("HTTP/2 TLS handlers were not disabled: %+v", transport.TLSNextProto)
	}
}
