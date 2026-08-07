package client

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
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
